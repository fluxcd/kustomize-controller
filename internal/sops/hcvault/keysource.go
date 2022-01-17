// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package hcvault

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/vault/api"
)

// MasterKey is a Vault Transit backend path used to encrypt and decrypt sops' data key.
//
// Adapted from https://github.com/mozilla/sops/blob/v3.7.1/hcvault/keysource.go
// to be able to have fine-grain control over the used decryption keys
// without relying on the existence of environment variable or file.
type MasterKey struct {
	EncryptedKey string
	KeyName      string
	EnginePath   string
	VaultAddress string
	VaultToken   string
	CreationDate time.Time
}

// NewMasterKeysFromURIs gets lots of keys from lots of URIs
func NewMasterKeysFromURIs(uris string) ([]*MasterKey, error) {
	var keys []*MasterKey
	if uris == "" {
		return keys, nil
	}
	uriList := strings.Split(uris, ",")
	for _, uri := range uriList {
		if uri == "" {
			continue
		}
		key, err := NewMasterKeyFromURI(uri)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// NewMasterKeyFromURI obtains the vaultAddress the transit backend path and the key name from the full URI of the key
func NewMasterKeyFromURI(uri string) (*MasterKey, error) {
	var key *MasterKey
	if uri == "" {
		return key, nil
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("missing scheme in vault URL (should be like this: https://vault.example.com:8200/v1/transit/keys/keyName), got: %v", uri)
	}
	enginePath, keyName, err := getBackendAndKeyFromPath(u.RequestURI())
	if err != nil {
		return nil, err
	}
	u.Path = ""
	return NewMasterKey(u.String(), enginePath, keyName), nil

}

func getBackendAndKeyFromPath(fullPath string) (enginePath, keyName string, err error) {
	// Running vault behind a reverse proxy with longer urls seems not to be supported
	// by the vault client api so we have a separate Error for that here.
	if re := regexp.MustCompile(`/[^/]+/v[\d]+/[^/]+/[^/]+/[^/]+`); re.Match([]byte(fullPath)) {
		return "", "", fmt.Errorf("running Vault with a prefixed url is not supported! (Format has to be like https://vault.example.com:8200/v1/transit/keys/keyName)")
	} else if re := regexp.MustCompile(`/v[\d]+/[^/]+/[^/]+/[^/]+`); re.Match([]byte(fullPath)) == false {
		return "", "", fmt.Errorf("vault path does not seem to be formatted correctly: (eg. https://vault.example.com:8200/v1/transit/keys/keyName)")
	}
	fullPath = strings.TrimPrefix(fullPath, "/")
	fullPath = strings.TrimSuffix(fullPath, "/")

	dirs := strings.Split(fullPath, "/")

	keyName = dirs[len(dirs)-1]
	enginePath = path.Join(dirs[1 : len(dirs)-2]...)
	err = nil
	return
}

// NewMasterKey creates a new MasterKey from a vault address, transit backend path and a key name and setting the creation date to the current date
func NewMasterKey(addess, enginePath, keyName string) *MasterKey {
	mk := &MasterKey{
		VaultAddress: addess,
		EnginePath:   enginePath,
		KeyName:      keyName,
		CreationDate: time.Now().UTC(),
	}
	return mk
}

// EncryptedDataKey returns the encrypted data key this master key holds
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

func vaultClient(address, token string) (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = address
	cli, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot create Vault client: %w", err)
	}
	cli.SetToken(token)
	return cli, nil
}

// Encrypt takes a sops data key, encrypts it with Vault Transit and stores the result in the EncryptedKey field
func (key *MasterKey) Encrypt(dataKey []byte) error {
	fullPath := path.Join(key.EnginePath, "encrypt", key.KeyName)
	cli, err := vaultClient(key.VaultAddress, key.VaultToken)
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(dataKey)
	payload := make(map[string]interface{})
	payload["plaintext"] = encoded
	raw, err := cli.Logical().Write(fullPath, payload)
	if err != nil {
		return fmt.Errorf("the encryption to %s has failed: %w", fullPath, err)
	}
	if raw == nil || raw.Data == nil {
		return fmt.Errorf("the transit backend %s is empty", fullPath)
	}
	encrypted, ok := raw.Data["ciphertext"]
	if !ok {
		return fmt.Errorf("there's no encrypted data")
	}
	encryptedKey, ok := encrypted.(string)
	if !ok {
		return fmt.Errorf("the ciphertext cannot be cast to string")
	}
	key.EncryptedKey = encryptedKey
	return nil
}

// EncryptIfNeeded encrypts the provided sops' data key and encrypts it if it hasn't been encrypted yet
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// Decrypt decrypts the EncryptedKey field with Vault Transit and returns the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	fullPath := path.Join(key.EnginePath, "decrypt", key.KeyName)
	cli, err := vaultClient(key.VaultAddress, key.VaultToken)
	if err != nil {
		return nil, err
	}
	payload := make(map[string]interface{})
	payload["ciphertext"] = key.EncryptedKey
	raw, err := cli.Logical().Write(fullPath, payload)
	if err != nil {
		return nil, fmt.Errorf("the decryption from %s has failed: %w", fullPath, err)
	}
	if raw == nil || raw.Data == nil {
		return nil, fmt.Errorf("the transit backend %s is empty", fullPath)
	}
	decrypted, ok := raw.Data["plaintext"]
	if !ok {
		return nil, fmt.Errorf("there's no decrypted data")
	}
	dataKey, ok := decrypted.(string)
	if !ok {
		return nil, fmt.Errorf("the plaintest cannot be cast to string")
	}
	result, err := base64.StdEncoding.DecodeString(dataKey)
	if err != nil {
		return nil, fmt.Errorf("couldn't decode base64 plaintext")
	}
	return result, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
// This is simply copied from GCPKMS
// TODO: handle key rotation on vault side
func (key *MasterKey) NeedsRotation() bool {
	//TODO: manage rewrapping https://www.vaultproject.io/api/secret/transit/index.html#rewrap-data
	return time.Since(key.CreationDate) > (time.Hour * 24 * 30 * 6)
}

// ToString converts the key to a string representation
func (key *MasterKey) ToString() string {
	return fmt.Sprintf("%s/v1/%s/keys/%s", key.VaultAddress, key.EnginePath, key.KeyName)
}

func (key *MasterKey) createVaultTransitAndKey() error {
	cli, err := vaultClient(key.VaultAddress, key.VaultToken)
	if err != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("cannot create Vault Client: %w", err)
	}
	err = cli.Sys().Mount(key.EnginePath, &api.MountInput{
		Type:        "transit",
		Description: "backend transit used by SOPS",
	})
	if err != nil {
		return err
	}
	path := path.Join(key.EnginePath, "keys", key.KeyName)
	payload := make(map[string]interface{})
	payload["type"] = "rsa-4096"
	_, err = cli.Logical().Write(path, payload)
	if err != nil {
		return err
	}
	_, err = cli.Logical().Read(path)
	if err != nil {
		return err
	}
	return nil
}

// ToMap converts the MasterKey to a map for serialization purposes
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["vault_address"] = key.VaultAddress
	out["key_name"] = key.KeyName
	out["engine_path"] = key.EnginePath
	out["enc"] = key.EncryptedKey
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	return out
}
