// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pgp

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// MasterKey is a PGP key used to securely store sops' data key by
// encrypting it and decrypting it.
//
// Adapted from https://github.com/mozilla/sops/blob/v3.7.0/pgp/keysource.go
// to be able to control the GPG home directory and have a "contained"
// environment.
//
// We are unable to drop the dependency on the GPG binary (although we
// wish!) because the builtin GPG support in Go is limited, it does for
// example not offer support for FIPS:
// * https://github.com/golang/go/issues/11658#issuecomment-120448974
// * https://github.com/golang/go/issues/45188
type MasterKey struct {
	Fingerprint  string
	EncryptedKey string
	CreationDate time.Time
	homeDir      string
}

// EncryptedDataKey returns the encrypted data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

func gpgBinary() string {
	binary := "gpg"
	if envBinary := os.Getenv("SOPS_GPG_EXEC"); envBinary != "" {
		binary = envBinary
	}
	return binary
}

func (key *MasterKey) encryptWithGPGBinary(dataKey []byte) error {
	fingerprint := key.Fingerprint
	if offset := len(fingerprint) - 16; offset > 0 {
		fingerprint = fingerprint[offset:]
	}
	args := []string{
		"--no-default-recipient",
		"--yes",
		"--encrypt",
		"-a",
		"-r",
		key.Fingerprint,
		"--trusted-key",
		fingerprint,
		"--no-encrypt-to",
	}
	if key.homeDir != "" {
		args = append([]string{"--homedir", key.homeDir}, args...)
	}
	cmd := exec.Command(gpgBinary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(dataKey)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return err
	}
	key.EncryptedKey = stdout.String()
	return nil
}

func getKeyFromKeyServer(keyserver string, fingerprint string) (openpgp.Entity, error) {
	url := fmt.Sprintf("https://%s/pks/lookup?op=get&options=mr&search=0x%s", keyserver, fingerprint)
	resp, err := http.Get(url)
	if err != nil {
		return openpgp.Entity{}, fmt.Errorf("error getting key from keyserver: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return openpgp.Entity{}, fmt.Errorf("keyserver returned non-200 status code %s", resp.Status)
	}
	ents, err := openpgp.ReadArmoredKeyRing(resp.Body)
	if err != nil {
		return openpgp.Entity{}, fmt.Errorf("could not read entities: %s", err)
	}
	return *ents[0], nil
}

func gpgKeyServer() string {
	keyServer := "gpg.mozilla.org"
	if envKeyServer := os.Getenv("SOPS_GPG_KEYSERVER"); envKeyServer != "" {
		keyServer = envKeyServer
	}
	return keyServer
}

func (key *MasterKey) getPubKey() (openpgp.Entity, error) {
	ring, err := key.pubRing()
	if err == nil {
		fingerprints := key.fingerprintMap(ring)
		entity, ok := fingerprints[key.Fingerprint]
		if ok {
			return entity, nil
		}
	}
	keyServer := gpgKeyServer()
	entity, err := getKeyFromKeyServer(keyServer, key.Fingerprint)
	if err != nil {
		return openpgp.Entity{},
			fmt.Errorf("key with fingerprint %s is not available "+
				"in keyring and could not be retrieved from keyserver", key.Fingerprint)
	}
	return entity, nil
}

func (key *MasterKey) encryptWithCryptoOpenPGP(dataKey []byte) error {
	entity, err := key.getPubKey()
	if err != nil {
		return err
	}
	encbuf := new(bytes.Buffer)
	armorbuf, err := armor.Encode(encbuf, "PGP MESSAGE", nil)
	if err != nil {
		return err
	}
	plaintextbuf, err := openpgp.Encrypt(armorbuf, []*openpgp.Entity{&entity}, nil, &openpgp.FileHints{IsBinary: true}, nil)
	if err != nil {
		return err
	}
	_, err = plaintextbuf.Write(dataKey)
	if err != nil {
		return err
	}
	err = plaintextbuf.Close()
	if err != nil {
		return err
	}
	err = armorbuf.Close()
	if err != nil {
		return err
	}
	b, err := io.ReadAll(encbuf)
	if err != nil {
		return err
	}
	key.EncryptedKey = string(b)
	return nil
}

// Encrypt encrypts the data key with the PGP key with the same
// fingerprint as the MasterKey. It first looks for PGP public
// keys in MasterKey.homeDir, and falls back to $GNUPGHOME/pubring.gpg.
func (key *MasterKey) Encrypt(dataKey []byte) error {
	openpgpErr := key.encryptWithCryptoOpenPGP(dataKey)
	if openpgpErr == nil {
		return nil
	}
	binaryErr := key.encryptWithGPGBinary(dataKey)
	if binaryErr == nil {
		return nil
	}
	return fmt.Errorf(
		`could not encrypt data key with PGP key: golang.org/x/crypto/openpgp error: %v; GPG binary error: %v`,
		openpgpErr, binaryErr)
}

// EncryptIfNeeded encrypts the data key with PGP only if it's needed,
// that is, if it hasn't been encrypted already.
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

func (key *MasterKey) decryptWithGPGBinary() ([]byte, error) {
	args := []string{
		"--use-agent",
		"-d",
	}
	if key.homeDir != "" {
		args = append([]string{"--homedir", key.homeDir}, args...)
	}
	cmd := exec.Command(gpgBinary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(key.EncryptedKey)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func (key *MasterKey) decryptWithCryptoOpenpgp() ([]byte, error) {
	ring, err := key.secRing()
	if err != nil {
		return nil, fmt.Errorf("could not load secring: %s", err)
	}
	block, err := armor.Decode(strings.NewReader(key.EncryptedKey))
	if err != nil {
		return nil, fmt.Errorf("armor decoding failed: %s", err)
	}
	// No support for encrypted private keys
	noPrompt := func(keys []openpgp.Key, symmetric bool) ([]byte, error) {
		return nil, fmt.Errorf("PGP keys encrypted with a passphrase are not supported")
	}
	md, err := openpgp.ReadMessage(block.Body, ring, noPrompt, nil)
	if err != nil {
		return nil, fmt.Errorf("reading PGP message failed: %s", err)
	}
	if b, err := io.ReadAll(md.UnverifiedBody); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("the key could not be decrypted with any of the PGP entries")
}

// Decrypt uses PGP to obtain the data key from the EncryptedKey store
// in the MasterKey and returns it.
func (key *MasterKey) Decrypt() ([]byte, error) {
	dataKey, openpgpErr := key.decryptWithCryptoOpenpgp()
	if openpgpErr == nil {
		return dataKey, nil
	}
	dataKey, binaryErr := key.decryptWithGPGBinary()
	if binaryErr == nil {
		return dataKey, nil
	}
	return nil, fmt.Errorf(
		`could not decrypt data key with PGP key: golang.org/x/crypto/openpgp error: %v; GPG binary error: %v`,
		openpgpErr, binaryErr)
}

// NeedsRotation returns whether the data key needs to be rotated
// or not.
func (key *MasterKey) NeedsRotation() bool {
	return time.Since(key.CreationDate).Hours() > 24*30*6
}

// ToString returns the string representation of the key, i.e. its
// fingerprint.
func (key *MasterKey) ToString() string {
	return key.Fingerprint
}

func (key *MasterKey) gpgHome() string {
	if key.homeDir != "" {
		return key.homeDir
	}
	dir := os.Getenv("GNUPGHOME")
	if dir == "" {
		usr, err := user.Current()
		if err != nil {
			return path.Join(os.Getenv("HOME"), "/.gnupg")
		}
		return path.Join(usr.HomeDir, ".gnupg")
	}
	return dir
}

// NewMasterKeyFromFingerprint takes a PGP fingerprint and returns a
// new MasterKey with that fingerprint.
func NewMasterKeyFromFingerprint(fingerprint, homeDir string) *MasterKey {
	return &MasterKey{
		Fingerprint:  strings.Replace(fingerprint, " ", "", -1),
		CreationDate: time.Now().UTC(),
		homeDir:      homeDir,
	}
}

func (key *MasterKey) loadRing(path string) (openpgp.EntityList, error) {
	f, err := os.Open(path)
	if err != nil {
		return openpgp.EntityList{}, err
	}
	defer f.Close()
	keyring, err := openpgp.ReadKeyRing(f)
	if err != nil {
		return keyring, err
	}
	return keyring, nil
}

func (key *MasterKey) secRing() (openpgp.EntityList, error) {
	return key.loadRing(key.gpgHome() + "/secring.gpg")
}

func (key *MasterKey) pubRing() (openpgp.EntityList, error) {
	return key.loadRing(key.gpgHome() + "/pubring.gpg")
}

func (key *MasterKey) fingerprintMap(ring openpgp.EntityList) map[string]openpgp.Entity {
	fps := make(map[string]openpgp.Entity)
	for _, entity := range ring {
		fp := strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint[:]))
		if entity != nil {
			fps[fp] = *entity
		}
	}
	return fps
}

// ToMap converts the MasterKey into a map for serialization purposes
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["fp"] = key.Fingerprint
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	out["enc"] = key.EncryptedKey
	return out
}
