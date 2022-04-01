// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package pgp

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const (
	// SopsGpgExecEnv can be set as an environment variable to overwrite the
	// GnuPG binary used.
	SopsGpgExecEnv = "FLUX_SOPS_GPG_EXEC"
)

var (
	// pgpTTL is the duration after which a MasterKey requires rotation.
	pgpTTL = time.Hour * 24 * 30 * 6
)

// MasterKey is a PGP key used to securely store SOPS' data key by
// encrypting it and decrypting it.
//
// Adapted from https://github.com/mozilla/sops/blob/v3.7.2/pgp/keysource.go
// to be able to control the GPG home directory and have a "contained"
// environment.
//
// We are unable to drop the dependency on the GPG binary (although we
// wish!) because the builtin GPG support in Go is limited, it does for
// example not offer support for FIPS:
// * https://github.com/golang/go/issues/11658#issuecomment-120448974
// * https://github.com/golang/go/issues/45188
type MasterKey struct {
	// Fingerprint contains the fingerprint of the PGP key used to Encrypt
	// or Decrypt the data key with.
	Fingerprint string
	// EncryptedKey contains the SOPS data key encrypted with PGP.
	EncryptedKey string
	// CreationDate of the MasterKey, used to determine if the EncryptedKey
	// needs rotation.
	CreationDate time.Time

	homeDir string
}

// MasterKeyFromFingerprint takes a PGP fingerprint and returns a
// new MasterKey with that fingerprint.
func MasterKeyFromFingerprint(fingerprint, homeDir string) *MasterKey {
	return &MasterKey{
		Fingerprint:  strings.Replace(fingerprint, " ", "", -1),
		CreationDate: time.Now().UTC(),
		homeDir:      homeDir,
	}
}

// Encrypt encrypts the data key with the PGP key with the same
// fingerprint as the MasterKey.
func (key *MasterKey) Encrypt(dataKey []byte) error {
	fingerprint := shortenFingerprint(key.Fingerprint)

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
	err, stdout, stderr := gpgExec(key.gpgHome(), args, bytes.NewReader(dataKey))
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}

	key.SetEncryptedDataKey(bytes.TrimSpace(stdout.Bytes()))
	return nil
}

// EncryptIfNeeded encrypts the data key with PGP only if it's needed,
// that is, if it hasn't been encrypted already.
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// EncryptedDataKey returns the encrypted data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Decrypt uses PGP to obtain the data key from the EncryptedKey store
// in the MasterKey and returns it.
func (key *MasterKey) Decrypt() ([]byte, error) {
	args := []string{
		"-d",
	}
	err, stdout, stderr := gpgExec(key.gpgHome(), args, strings.NewReader(key.EncryptedKey))
	if err != nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// NeedsRotation returns whether the data key needs to be rotated
// or not.
func (key *MasterKey) NeedsRotation() bool {
	return time.Since(key.CreationDate) > (pgpTTL)
}

// ToString returns the string representation of the key, i.e. its
// fingerprint.
func (key *MasterKey) ToString() string {
	return key.Fingerprint
}

// ToMap converts the MasterKey into a map for serialization purposes.
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["fp"] = key.Fingerprint
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	out["enc"] = key.EncryptedKey
	return out
}

// gpgHome determines the GnuPG home directory for the MasterKey, and returns
// its path. In order of preference:
// 1. MasterKey.homeDir
// 2. $GNUPGHOME
// 3. user.Current().HomeDir/.gnupg
// 4. $HOME/.gnupg
func (key *MasterKey) gpgHome() string {
	if key.homeDir == "" {
		dir := os.Getenv("GNUPGHOME")
		if dir == "" {
			usr, err := user.Current()
			if err != nil {
				return filepath.Join(os.Getenv("HOME"), ".gnupg")
			}
			return filepath.Join(usr.HomeDir, ".gnupg")
		}
		return dir
	}
	return key.homeDir
}

// gpgExec runs the provided args with the gpgBinary, while restricting it to
// gpgHome. Stdout and stderr can be read from the returned buffers.
// When the command fails, an error is returned.
func gpgExec(gpgHome string, args []string, stdin io.Reader) (err error, stdout bytes.Buffer, stderr bytes.Buffer) {
	if gpgHome != "" {
		args = append([]string{"--no-default-keyring", "--homedir", gpgHome}, args...)
	}

	cmd := exec.Command(gpgBinary(), args...)
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return
}

// gpgBinary returns the GNuPG binary which must be used.
// It allows for runtime modifications by setting the environment variable
// SopsGpgExecEnv to the absolute path of the replacement binary.
func gpgBinary() string {
	binary := "gpg"
	if envBinary := os.Getenv(SopsGpgExecEnv); envBinary != "" && filepath.IsAbs(envBinary) {
		binary = envBinary
	}
	return binary
}

// shortenFingerprint returns the short ID of the given fingerprint.
// This is mostly used for compatability reasons, as older versions of GnuPG
// do not always like long IDs.
func shortenFingerprint(fingerprint string) string {
	if offset := len(fingerprint) - 16; offset > 0 {
		fingerprint = fingerprint[offset:]
	}
	return fingerprint
}
