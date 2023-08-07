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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
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
	creds, err := key.getTokenCredential()
	if err != nil {
		return fmt.Errorf("failed to get Azure token credential to encrypt: %w", err)
	}
	c, err := azkeys.NewClient(key.VaultURL, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to construct Azure Key Vault crypto client to encrypt data: %w", err)
	}
	resp, err := c.Encrypt(context.Background(), key.Name, key.Version, azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
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
	creds, err := key.getTokenCredential()
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure token credential to decrypt: %w", err)
	}
	c, err := azkeys.NewClient(key.VaultURL, creds, nil)
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
	resp, err := c.Decrypt(context.Background(), key.Name, key.Version, azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
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

// getTokenCredential returns the tokenCredential of the MasterKey, or
// azidentity.NewDefaultAzureCredential.
func (key *MasterKey) getTokenCredential() (azcore.TokenCredential, error) {
	if key.token == nil {
		return getDefaultAzureCredential()
	}
	return key.token, nil
}

// getDefaultAzureCredentials is a modification of
// azidentity.NewDefaultAzureCredential, specifically adapted to not shell out
// to the Azure CLI.
//
// It attemps to return an azcore.TokenCredential based on the following order:
//
//   - azidentity.NewEnvironmentCredential if environment variables AZURE_CLIENT_ID,
//     AZURE_CLIENT_ID is set with either one of the following: (AZURE_CLIENT_SECRET)
//     or (AZURE_CLIENT_CERTIFICATE_PATH and AZURE_CLIENT_CERTIFICATE_PATH) or
//     (AZURE_USERNAME, AZURE_PASSWORD)
//   - azidentity.WorkloadIdentity if environment variable configuration
//     (AZURE_AUTHORITY_HOST, AZURE_CLIENT_ID, AZURE_FEDERATED_TOKEN_FILE, AZURE_TENANT_ID)
//     is set by the Azure workload identity webhook.
//   - azidentity.ManagedIdentity if only AZURE_CLIENT_ID env variable is set.
func getDefaultAzureCredential() (azcore.TokenCredential, error) {
	var (
		azureClientID           = "AZURE_CLIENT_ID"
		azureFederatedTokenFile = "AZURE_FEDERATED_TOKEN_FILE"
		azureAuthorityHost      = "AZURE_AUTHORITY_HOST"
		azureTenantID           = "AZURE_TENANT_ID"
	)

	var errorMessages []string
	options := &azidentity.DefaultAzureCredentialOptions{}

	envCred, err := azidentity.NewEnvironmentCredential(&azidentity.EnvironmentCredentialOptions{
		ClientOptions: options.ClientOptions, DisableInstanceDiscovery: options.DisableInstanceDiscovery},
	)
	if err == nil {
		return envCred, nil
	} else {
		errorMessages = append(errorMessages, "EnvironmentCredential: "+err.Error())
	}

	// workload identity requires values for AZURE_AUTHORITY_HOST, AZURE_CLIENT_ID, AZURE_FEDERATED_TOKEN_FILE, AZURE_TENANT_ID
	haveWorkloadConfig := false
	clientID, haveClientID := os.LookupEnv(azureClientID)
	if haveClientID {
		if file, ok := os.LookupEnv(azureFederatedTokenFile); ok {
			if _, ok := os.LookupEnv(azureAuthorityHost); ok {
				if tenantID, ok := os.LookupEnv(azureTenantID); ok {
					haveWorkloadConfig = true
					workloadCred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
						ClientID:                 clientID,
						TenantID:                 tenantID,
						TokenFilePath:            file,
						ClientOptions:            options.ClientOptions,
						DisableInstanceDiscovery: options.DisableInstanceDiscovery,
					})
					if err == nil {
						return workloadCred, nil
					} else {
						errorMessages = append(errorMessages, "Workload Identity"+": "+err.Error())
					}
				}
			}
		}
	}
	if !haveWorkloadConfig {
		err := errors.New("missing environment variables for workload identity. Check webhook and pod configuration")
		errorMessages = append(errorMessages, fmt.Sprintf("Workload Identity: %s", err))
	}

	o := &azidentity.ManagedIdentityCredentialOptions{ClientOptions: options.ClientOptions}
	if haveClientID {
		o.ID = azidentity.ClientID(clientID)
	}
	miCred, err := azidentity.NewManagedIdentityCredential(o)
	if err == nil {
		return miCred, nil
	} else {
		errorMessages = append(errorMessages, "ManagedIdentity"+": "+err.Error())
	}

	return nil, errors.New(strings.Join(errorMessages, "\n"))
}
