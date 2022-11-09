// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package azkv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"time"
	"unicode/utf16"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azkeys"
	"github.com/dimchansky/utfbom"
)

var (
	// azkvTTL is the duration after which a MasterKey requires rotation.
	azkvTTL = time.Hour * 24 * 30 * 6
)

// MasterKey is an Azure Key Vault Key used to Encrypt and Decrypt SOPS'
// data key.
//
// The underlying authentication token can be configured using TokenFromAADConfig
// and Token.ApplyToMasterKey().
type MasterKey struct {
	VaultURL string
	Name     string
	Version  string

	EncryptedKey string
	CreationDate time.Time

	token azcore.TokenCredential
}

// MasterKeyFromURL creates a new MasterKey from a Vault URL, key name, and key
// version.
func MasterKeyFromURL(url, name, version string) *MasterKey {
	key := &MasterKey{
		VaultURL:     url,
		Name:         name,
		Version:      version,
		CreationDate: time.Now().UTC(),
	}
	return key
}

// Token is an azcore.TokenCredential used for authenticating towards Azure Key
// Vault.
type Token struct {
	token azcore.TokenCredential
}

// NewToken creates a new Token with the provided azcore.TokenCredential.
func NewToken(token azcore.TokenCredential) *Token {
	return &Token{token: token}
}

// ApplyToMasterKey configures the Token on the provided key.
func (t Token) ApplyToMasterKey(key *MasterKey) {
	key.token = t.token
}

// Encrypt takes a SOPS data key, encrypts it with Azure Key Vault, and stores
// the result in the EncryptedKey field.
func (key *MasterKey) Encrypt(dataKey []byte) error {
	c, err := azkeys.NewClient(key.VaultURL, key.token, nil)
	if err != nil {
		return fmt.Errorf("failed to construct Azure Key Vault crypto client to encrypt data: %w", err)
	}
	resp, err := c.Encrypt(context.Background(), key.Name, key.Version, azkeys.KeyOperationsParameters{
		Algorithm: to.Ptr(azkeys.JSONWebKeyEncryptionAlgorithmRSAOAEP256),
		Value:     dataKey,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to encrypt sops data key with Azure Key Vault key '%s': %w", key.ToString(), err)
	}
	// This is for compatibility between the SOPS upstream which uses
	// a much older Azure SDK, and our implementation which is up-to-date
	// with the latest.
	encodedEncryptedKey := base64.RawURLEncoding.EncodeToString(resp.Result)
	key.SetEncryptedDataKey([]byte(encodedEncryptedKey))
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

// EncryptIfNeeded encrypts the provided SOPS data key, if it has not been
// encrypted yet.
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// Decrypt decrypts the EncryptedKey field with Azure Key Vault and returns
// the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	c, err := azkeys.NewClient(key.VaultURL, key.token, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct Azure Key Vault crypto client to decrypt data: %w", err)
	}
	// This is for compatibility between the SOPS upstream which uses
	// a much older Azure SDK, and our implementation which is up-to-date
	// with the latest.
	rawEncryptedKey, err := base64.RawURLEncoding.DecodeString(key.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to base64 decode Azure Key Vault encrypted key: %w", err)
	}
	resp, err := c.Decrypt(context.Background(), key.Name, key.Version, azkeys.KeyOperationsParameters{
		Algorithm: to.Ptr(azkeys.JSONWebKeyEncryptionAlgorithmRSAOAEP256),
		Value:     rawEncryptedKey,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt sops data key with Azure Key Vault key '%s': %w", key.ToString(), err)
	}
	return resp.Result, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	return time.Since(key.CreationDate) > (azkvTTL)
}

// ToString converts the key to a string representation.
func (key *MasterKey) ToString() string {
	return fmt.Sprintf("%s/keys/%s/%s", key.VaultURL, key.Name, key.Version)
}

// ToMap converts the MasterKey to a map for serialization purposes.
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["vaultUrl"] = key.VaultURL
	out["key"] = key.Name
	out["version"] = key.Version
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	out["enc"] = key.EncryptedKey
	return out
}

func decode(b []byte) ([]byte, error) {
	reader, enc := utfbom.Skip(bytes.NewReader(b))
	switch enc {
	case utfbom.UTF16LittleEndian:
		u16 := make([]uint16, (len(b)/2)-1)
		err := binary.Read(reader, binary.LittleEndian, &u16)
		if err != nil {
			return nil, err
		}
		return []byte(string(utf16.Decode(u16))), nil
	case utfbom.UTF16BigEndian:
		u16 := make([]uint16, (len(b)/2)-1)
		err := binary.Read(reader, binary.BigEndian, &u16)
		if err != nil {
			return nil, err
		}
		return []byte(string(utf16.Decode(u16))), nil
	}
	return ioutil.ReadAll(reader)
}
