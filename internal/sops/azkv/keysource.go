// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package azkv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"
	"unicode/utf16"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/keyvault/keyvault"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/dimchansky/utfbom"
)

// MasterKey is an Azure Key Vault key used to encrypt and decrypt SOPS' data key.
// The underlying authentication token can be configured using AADSettings.
type MasterKey struct {
	VaultURL string
	Name     string
	Version  string

	EncryptedKey string

	token *adal.ServicePrincipalToken
}

// LoadAADSettingsFromBytes attempts to load the given bytes into the given AADSettings.
// By first decoding it if UTF-16, and then unmarshalling it into the given struct.
// It returns an error for any failure.
func LoadAADSettingsFromBytes(b []byte, s *AADSettings) error {
	b, err := decode(b)
	if err != nil {
		return fmt.Errorf("failed to decode Azure authentication file bytes: %w", err)
	}
	if err = json.Unmarshal(b, s); err != nil {
		err = fmt.Errorf("failed to unmarshal Azure authentication file: %w", err)
	}
	return err
}

// AADSettings contains the selection of fields from an Azure authentication file
// required for Active Directory authentication.
//
// It is based on the unpublished contract in
// https://github.com/Azure/go-autorest/blob/c7f947c0610de1bc279f76e6d453353f95cd1bfa/autorest/azure/auth/auth.go#L331-L342,
// which seems to be due to an assumption of configuration through environment
// variables over file based configuration.
type AADSettings struct {
	ClientID                string `json:"clientId"`
	ClientSecret            string `json:"clientSecret"`
	TenantID                string `json:"tenantId"`
	ActiveDirectoryEndpoint string `json:"activeDirectoryEndpointUrl"`
}

// SetToken configures the token on the given MasterKey using the AADSettings.
func (s *AADSettings) SetToken(key *MasterKey) error {
	if s == nil {
		return nil
	}
	config, err := adal.NewOAuthConfig(s.GetAADEndpoint(), s.TenantID)
	if err != nil {
		return err
	}
	if key.token, err = adal.NewServicePrincipalToken(*config, s.ClientID, s.ClientSecret,
		azure.PublicCloud.ResourceIdentifiers.KeyVault); err != nil {
		return err
	}
	return nil
}

// GetAADEndpoint returns the ActiveDirectoryEndpoint, or the Azure Public Cloud
// default.
func (s *AADSettings) GetAADEndpoint() string {
	if s.ActiveDirectoryEndpoint != "" {
		return s.ActiveDirectoryEndpoint
	}
	return azure.PublicCloud.ActiveDirectoryEndpoint
}

// EncryptedDataKey returns the encrypted data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Encrypt takes a SOPS data key, encrypts it with Key Vault and stores the result in the EncryptedKey field.
func (key *MasterKey) Encrypt(plaintext []byte) error {
	c := newThrottledKeyvaultClient(key.authorizer())

	data := base64.RawURLEncoding.EncodeToString(plaintext)
	p := keyvault.KeyOperationsParameters{Value: &data, Algorithm: keyvault.RSAOAEP256}
	res, err := c.Encrypt(context.Background(), key.VaultURL, key.Name, key.Version, p)
	if err != nil {
		return fmt.Errorf("failed to encrypt data: %w", err)
	}

	key.EncryptedKey = *res.Result
	if err != nil {
		return fmt.Errorf("failed to encrypt data: %w", err)
	}
	return nil
}

// EncryptIfNeeded encrypts the provided SOPS' data key and encrypts it if it hasn't been encrypted yet.
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// Decrypt decrypts the EncryptedKey field with Azure Key Vault and returns the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	c := newThrottledKeyvaultClient(key.authorizer())

	result, err := c.Decrypt(context.Background(), key.VaultURL, key.Name, key.Version, keyvault.KeyOperationsParameters{
		Algorithm: keyvault.RSAOAEP256, Value: &key.EncryptedKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	plaintext, err := base64.RawURLEncoding.DecodeString(*result.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}
	return plaintext, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	return key.token.Token().IsExpired()
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
	out["enc"] = key.EncryptedKey
	return out
}

func (key *MasterKey) authorizer() autorest.Authorizer {
	if key.token == nil {
		return &autorest.NullAuthorizer{}
	}
	return autorest.NewBearerAuthorizer(key.token)
}

// newThrottledKeyvaultClient returns a client configured to retry requests.
//
// Ref: https://docs.microsoft.com/en-us/azure/key-vault/general/overview-throttling
func newThrottledKeyvaultClient(authorizer autorest.Authorizer) keyvault.BaseClient {
	const (
		// Number of times the client will attempt to make an HTTP request
		retryAttempts = 6
		// Duration between HTTP request retries
		retryDuration = 5 * time.Second
	)

	c := keyvault.New()
	c.Authorizer = authorizer
	c.RetryAttempts = retryAttempts
	c.RetryDuration = retryDuration
	return c
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
