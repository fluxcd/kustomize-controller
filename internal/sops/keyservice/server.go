// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package keyservice

import (
	"fmt"

	"go.mozilla.org/sops/v3/keyservice"
	"golang.org/x/net/context"

	"github.com/fluxcd/kustomize-controller/internal/sops/age"
	"github.com/fluxcd/kustomize-controller/internal/sops/azkv"
	"github.com/fluxcd/kustomize-controller/internal/sops/hcvault"
	"github.com/fluxcd/kustomize-controller/internal/sops/pgp"
)

// Server is a key service server that uses SOPS MasterKeys to fulfill
// requests. It intercepts encryption and decryption requests made for
// PGP and Age keys, so that they can be run in a contained environment
// instead of the default implementation which heavily utilizes
// environmental variables. Any other request is forwarded to
// the embedded DefaultServer.
type Server struct {
	// HomeDir configures the home directory used for PGP operations.
	HomeDir string

	// AgePrivateKeys configures the age private keys known by the server.
	AgePrivateKeys []string

	// VaultToken configures the Vault token used by the server.
	VaultToken string

	// AzureAADConfig configures the Azure Active Directory settings used
	// by the server.
	AzureAADConfig *azkv.AADConfig

	// DefaultServer is the server used for any other request than a PGP
	// or age encryption/decryption.
	DefaultServer keyservice.KeyServiceServer
}

func NewServer(homeDir, vaultToken string, agePrivateKeys []string, azureCfg *azkv.AADConfig) keyservice.KeyServiceServer {
	server := &Server{
		HomeDir:        homeDir,
		AgePrivateKeys: agePrivateKeys,
		VaultToken:     vaultToken,
		AzureAADConfig: azureCfg,
		DefaultServer: &keyservice.Server{
			Prompt: false,
		},
	}
	return server
}

// Encrypt takes an encrypt request and encrypts the provided plaintext with
// the provided key, returning the encrypted result.
func (ks Server) Encrypt(ctx context.Context, req *keyservice.EncryptRequest) (*keyservice.EncryptResponse, error) {
	key := req.Key
	switch k := key.KeyType.(type) {
	case *keyservice.Key_PgpKey:
		ciphertext, err := ks.encryptWithPgp(k.PgpKey, req.Plaintext)
		if err != nil {
			return nil, err
		}
		return &keyservice.EncryptResponse{
			Ciphertext: ciphertext,
		}, nil
	case *keyservice.Key_AgeKey:
		ciphertext, err := ks.encryptWithAge(k.AgeKey, req.Plaintext)
		if err != nil {
			return nil, err
		}
		return &keyservice.EncryptResponse{
			Ciphertext: ciphertext,
		}, nil
	case *keyservice.Key_AzureKeyvaultKey:
		if ks.AzureAADConfig != nil {
			ciphertext, err := ks.encryptWithAzureKeyvault(k.AzureKeyvaultKey, req.Plaintext)
			if err != nil {
				return nil, err
			}
			return &keyservice.EncryptResponse{
				Ciphertext: ciphertext,
			}, nil
		}
	}
	// Fallback to default server for any other request.
	return ks.DefaultServer.Encrypt(ctx, req)
}

// Decrypt takes a decrypt request and decrypts the provided ciphertext with
// the provided key, returning the decrypted result.
func (ks Server) Decrypt(ctx context.Context, req *keyservice.DecryptRequest) (*keyservice.DecryptResponse, error) {
	key := req.Key
	switch k := key.KeyType.(type) {
	case *keyservice.Key_PgpKey:
		plaintext, err := ks.decryptWithPgp(k.PgpKey, req.Ciphertext)
		if err != nil {
			return nil, err
		}
		return &keyservice.DecryptResponse{
			Plaintext: plaintext,
		}, nil
	case *keyservice.Key_AgeKey:
		plaintext, err := ks.decryptWithAge(k.AgeKey, req.Ciphertext)
		if err != nil {
			return nil, err
		}
		return &keyservice.DecryptResponse{
			Plaintext: plaintext,
		}, nil
	case *keyservice.Key_VaultKey:
		if ks.VaultToken != "" {
			plaintext, err := ks.decryptWithVault(k.VaultKey, req.Ciphertext)
			if err != nil {
				return nil, err
			}
			return &keyservice.DecryptResponse{
				Plaintext: plaintext,
			}, nil
		}
	case *keyservice.Key_AzureKeyvaultKey:
		if ks.AzureAADConfig != nil {
			plaintext, err := ks.decryptWithAzureKeyvault(k.AzureKeyvaultKey, req.Ciphertext)
			if err != nil {
				return nil, err
			}
			return &keyservice.DecryptResponse{
				Plaintext: plaintext,
			}, nil
		}
	}
	// Fallback to default server for any other request.
	return ks.DefaultServer.Decrypt(ctx, req)
}

func (ks *Server) encryptWithPgp(key *keyservice.PgpKey, plaintext []byte) ([]byte, error) {
	pgpKey := pgp.NewMasterKeyFromFingerprint(key.Fingerprint, ks.HomeDir)
	err := pgpKey.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}
	return []byte(pgpKey.EncryptedKey), nil
}

func (ks *Server) decryptWithPgp(key *keyservice.PgpKey, ciphertext []byte) ([]byte, error) {
	pgpKey := pgp.NewMasterKeyFromFingerprint(key.Fingerprint, ks.HomeDir)
	pgpKey.EncryptedKey = string(ciphertext)
	plaintext, err := pgpKey.Decrypt()
	return plaintext, err
}

func (ks *Server) encryptWithAge(key *keyservice.AgeKey, plaintext []byte) ([]byte, error) {
	ageKey := age.MasterKey{
		Recipient: key.Recipient,
	}
	if err := ageKey.Encrypt(plaintext); err != nil {
		return nil, err
	}
	return []byte(ageKey.EncryptedKey), nil
}

func (ks *Server) decryptWithAge(key *keyservice.AgeKey, ciphertext []byte) ([]byte, error) {
	ageKey := age.MasterKey{
		Recipient:  key.Recipient,
		Identities: ks.AgePrivateKeys,
	}
	ageKey.EncryptedKey = string(ciphertext)
	plaintext, err := ageKey.Decrypt()
	return plaintext, err
}

func (ks *Server) decryptWithVault(key *keyservice.VaultKey, ciphertext []byte) ([]byte, error) {
	vaultKey := hcvault.MasterKey{
		VaultAddress: key.VaultAddress,
		EnginePath:   key.EnginePath,
		KeyName:      key.KeyName,
		VaultToken:   ks.VaultToken,
	}
	vaultKey.EncryptedKey = string(ciphertext)
	plaintext, err := vaultKey.Decrypt()
	return plaintext, err
}

func (ks *Server) encryptWithAzureKeyvault(key *keyservice.AzureKeyVaultKey, plaintext []byte) ([]byte, error) {
	azureKey := azkv.MasterKey{
		VaultURL: key.VaultUrl,
		Name:     key.Name,
		Version:  key.Version,
	}
	if err := ks.AzureAADConfig.SetToken(&azureKey); err != nil {
		return nil, fmt.Errorf("failed to set token for Azure encryption request: %w", err)
	}
	if err := azureKey.Encrypt(plaintext); err != nil {
		return nil, err
	}
	return []byte(azureKey.EncryptedKey), nil
}

func (ks *Server) decryptWithAzureKeyvault(key *keyservice.AzureKeyVaultKey, ciphertext []byte) ([]byte, error) {
	azureKey := azkv.MasterKey{
		VaultURL: key.VaultUrl,
		Name:     key.Name,
		Version:  key.Version,
	}
	if err := ks.AzureAADConfig.SetToken(&azureKey); err != nil {
		return nil, fmt.Errorf("failed to set token for Azure decryption request: %w", err)
	}
	azureKey.EncryptedKey = string(ciphertext)
	plaintext, err := azureKey.Decrypt()
	return plaintext, err
}
