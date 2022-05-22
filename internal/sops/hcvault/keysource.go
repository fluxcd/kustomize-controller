// Copyright (C) 2020 The Mozilla SOPS authors
// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package hcvault

import (
	"encoding/base64"
	"fmt"
	"path"
	"time"

	"github.com/hashicorp/vault/api"
)

var (
	// vaultTTL is the duration after which a MasterKey requires rotation.
	vaultTTL = time.Hour * 24 * 30 * 6
)

// VaultToken used for authenticating towards a Vault server.
type VaultToken string

// ApplyToMasterKey configures the token on the provided key.
func (t VaultToken) ApplyToMasterKey(key *MasterKey) {
	key.vaultToken = string(t)
}

// MasterKey is a Vault Transit backend path used to Encrypt and Decrypt
// SOPS' data key.
//
// Adapted from https://github.com/mozilla/sops/blob/v3.7.1/hcvault/keysource.go
// to be able to have fine-grain control over the used decryption keys
// without relying on the existence of environment variable or file.
type MasterKey struct {
	KeyName      string
	EnginePath   string
	VaultAddress string

	EncryptedKey string
	CreationDate time.Time

	vaultToken string
}

// MasterKeyFromAddress creates a new MasterKey from a Vault address, Transit
// backend path and a key name.
func MasterKeyFromAddress(address, enginePath, keyName string) *MasterKey {
	key := &MasterKey{
		VaultAddress: address,
		EnginePath:   enginePath,
		KeyName:      keyName,
		CreationDate: time.Now().UTC(),
	}
	return key
}

// Encrypt takes a SOPS data key, encrypts it with Vault Transit, and stores
// the result in the EncryptedKey field.
func (key *MasterKey) Encrypt(dataKey []byte) error {
	client, err := vaultClient(key.VaultAddress, key.vaultToken)
	if err != nil {
		return err
	}

	fullPath := key.encryptPath()
	secret, err := client.Logical().Write(fullPath, encryptPayload(dataKey))
	if err != nil {
		return fmt.Errorf("failed to encrypt sops data key to Vault transit backend '%s': %w", fullPath, err)
	}
	encryptedKey, err := encryptedKeyFromSecret(secret)
	if err != nil {
		return fmt.Errorf("failed to encrypt sops data key to Vault transit backend '%s': %w", fullPath, err)
	}
	key.EncryptedKey = encryptedKey
	return nil
}

// EncryptIfNeeded encrypts the provided SOPS data key, if it has not been
// encrypted yet.
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

// Decrypt decrypts the EncryptedKey field with Vault Transit and returns the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	client, err := vaultClient(key.VaultAddress, key.vaultToken)
	if err != nil {
		return nil, err
	}

	fullPath := key.decryptPath()
	secret, err := client.Logical().Write(fullPath, decryptPayload(key.EncryptedKey))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt sops data key from Vault transit backend '%s': %w", fullPath, err)
	}
	dataKey, err := dataKeyFromSecret(secret)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt sops data key from Vault transit backend '%s': %w", fullPath, err)
	}
	return dataKey, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	// TODO: manage rewrapping https://www.vaultproject.io/api/secret/transit/index.html#rewrap-data
	return time.Since(key.CreationDate) > (vaultTTL)
}

// ToString converts the key to a string representation.
func (key *MasterKey) ToString() string {
	return fmt.Sprintf("%s/v1/%s/keys/%s", key.VaultAddress, key.EnginePath, key.KeyName)
}

// ToMap converts the MasterKey to a map for serialization purposes.
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["vault_address"] = key.VaultAddress
	out["key_name"] = key.KeyName
	out["engine_path"] = key.EnginePath
	out["enc"] = key.EncryptedKey
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	return out
}

// encryptPath returns the path for Encrypt requests.
func (key *MasterKey) encryptPath() string {
	return path.Join(key.EnginePath, "encrypt", key.KeyName)
}

// decryptPath returns the path for Decrypt requests.
func (key *MasterKey) decryptPath() string {
	return path.Join(key.EnginePath, "decrypt", key.KeyName)
}

// encryptPayload returns the payload for an encrypt request of the dataKey.
func encryptPayload(dataKey []byte) map[string]interface{} {
	encoded := base64.StdEncoding.EncodeToString(dataKey)
	return map[string]interface{}{
		"plaintext": encoded,
	}
}

// encryptedKeyFromSecret attempts to extract the encrypted key from the data
// of the provided secret.
func encryptedKeyFromSecret(secret *api.Secret) (string, error) {
	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("transit backend is empty")
	}
	encrypted, ok := secret.Data["ciphertext"]
	if !ok {
		return "", fmt.Errorf("no encrypted data")
	}
	encryptedKey, ok := encrypted.(string)
	if !ok {
		return "", fmt.Errorf("encrypted ciphertext cannot be cast to string")
	}
	return encryptedKey, nil
}

// decryptPayload returns the payload for a decrypt request of the
// encryptedKey.
func decryptPayload(encryptedKey string) map[string]interface{} {
	return map[string]interface{}{
		"ciphertext": encryptedKey,
	}
}

// dataKeyFromSecret attempts to extract the data key from the data of the
// provided secret.
func dataKeyFromSecret(secret *api.Secret) ([]byte, error) {
	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("transit backend is empty")
	}
	decrypted, ok := secret.Data["plaintext"]
	if !ok {
		return nil, fmt.Errorf("no decrypted data")
	}
	plaintext, ok := decrypted.(string)
	if !ok {
		return nil, fmt.Errorf("decrypted plaintext data cannot be cast to string")
	}
	dataKey, err := base64.StdEncoding.DecodeString(plaintext)
	if err != nil {
		return nil, fmt.Errorf("cannot decode base64 plaintext into data key bytes")
	}
	return dataKey, nil
}

// vaultClient returns a new Vault client, configured with the given address
// and token.
func vaultClient(address, token string) (*api.Client, error) {
	cfg := api.DefaultConfig()
	cfg.Address = address
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot create Vault client: %w", err)
	}
	client.SetToken(token)
	return client, nil
}
