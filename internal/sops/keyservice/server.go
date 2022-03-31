// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package keyservice

import (
	"fmt"

	"go.mozilla.org/sops/v3/keyservice"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fluxcd/kustomize-controller/internal/sops/age"
	"github.com/fluxcd/kustomize-controller/internal/sops/azkv"
	"github.com/fluxcd/kustomize-controller/internal/sops/hcvault"
	"github.com/fluxcd/kustomize-controller/internal/sops/pgp"
)

// Server is a key service server that uses SOPS MasterKeys to fulfill
// requests. It intercepts Encrypt and Decrypt requests made for key types
// that need to run in a contained environment, instead of the default
// implementation which heavily utilizes environment variables or the runtime
// environment. Any request not handled by the Server is forwarded to the
// embedded default server.
type Server struct {
	// homeDir is the contained "home directory" used for the Encrypt and
	// Decrypt operations for certain key types, e.g. PGP.
	homeDir string

	// agePrivateKeys holds the private keys used for Encrypt and Decrypt
	// operations of age requests.
	agePrivateKeys []string

	// vaultToken is the token used for Encrypt and Decrypt operations of
	// Hashicorp Vault requests.
	// When empty, the request will be handled by defaultServer.
	vaultToken string

	// azureAADConfig is the configuration used for Encrypt and Decrypt
	// operations of Azure Key Vault requests.
	// When nil, the request will be handled by defaultServer.
	azureAADConfig *azkv.AADConfig

	// defaultServer is the fallback server, used to handle any request that
	// is not eligible to be handled by this Server.
	defaultServer keyservice.KeyServiceServer
}

// NewServer constructs a new Server, configuring it with the provided options
// before returning the result.
// When WithDefaultServer is not provided as an option, the SOPS server
// implementation is configured as default.
func NewServer(options ...ServerOption) keyservice.KeyServiceServer {
	s := &Server{}
	for _, opt := range options {
		opt.ApplyToServer(s)
	}
	if s.defaultServer == nil {
		s.defaultServer = &keyservice.Server{
			Prompt: false,
		}
	}
	return s
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
		if ks.azureAADConfig != nil {
			ciphertext, err := ks.encryptWithAzureKeyvault(k.AzureKeyvaultKey, req.Plaintext)
			if err != nil {
				return nil, err
			}
			return &keyservice.EncryptResponse{
				Ciphertext: ciphertext,
			}, nil
		}
	case nil:
		return nil, status.Errorf(codes.NotFound, "must provide a key")
	}
	// Fallback to default server for any other request.
	return ks.defaultServer.Encrypt(ctx, req)
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
		if ks.vaultToken != "" {
			plaintext, err := ks.decryptWithVault(k.VaultKey, req.Ciphertext)
			if err != nil {
				return nil, err
			}
			return &keyservice.DecryptResponse{
				Plaintext: plaintext,
			}, nil
		}
	case *keyservice.Key_AzureKeyvaultKey:
		if ks.azureAADConfig != nil {
			plaintext, err := ks.decryptWithAzureKeyvault(k.AzureKeyvaultKey, req.Ciphertext)
			if err != nil {
				return nil, err
			}
			return &keyservice.DecryptResponse{
				Plaintext: plaintext,
			}, nil
		}
	case nil:
		return nil, status.Errorf(codes.NotFound, "must provide a key")
	}
	// Fallback to default server for any other request.
	return ks.defaultServer.Decrypt(ctx, req)
}

func (ks *Server) encryptWithPgp(key *keyservice.PgpKey, plaintext []byte) ([]byte, error) {
	if ks.homeDir == "" {
		return nil, status.Errorf(codes.Unimplemented, "PGP encrypt service unavailable: missing home dir configuration")
	}

	pgpKey := pgp.NewMasterKeyFromFingerprint(key.Fingerprint, ks.homeDir)
	err := pgpKey.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}
	return []byte(pgpKey.EncryptedKey), nil
}

func (ks *Server) decryptWithPgp(key *keyservice.PgpKey, ciphertext []byte) ([]byte, error) {
	if ks.homeDir == "" {
		return nil, status.Errorf(codes.Unimplemented, "PGP decrypt service unavailable: missing home dir configuration")
	}

	pgpKey := pgp.NewMasterKeyFromFingerprint(key.Fingerprint, ks.homeDir)
	pgpKey.EncryptedKey = string(ciphertext)
	plaintext, err := pgpKey.Decrypt()
	return plaintext, err
}

func (ks *Server) encryptWithAge(key *keyservice.AgeKey, plaintext []byte) ([]byte, error) {
	// Unlike the other encrypt and decrypt methods, validation of configuration
	// is not required here. As the encryption happens purely based on the
	// Recipient from the key.

	ageKey := age.MasterKey{
		Recipient: key.Recipient,
	}
	if err := ageKey.Encrypt(plaintext); err != nil {
		return nil, err
	}
	return []byte(ageKey.EncryptedKey), nil
}

func (ks *Server) decryptWithAge(key *keyservice.AgeKey, ciphertext []byte) ([]byte, error) {
	if len(ks.agePrivateKeys) == 0 {
		return nil, status.Errorf(codes.Unimplemented, "age decrypt service unavailable: no private keys present")
	}

	ageKey := age.MasterKey{
		Recipient:  key.Recipient,
		Identities: ks.agePrivateKeys,
	}
	ageKey.EncryptedKey = string(ciphertext)
	plaintext, err := ageKey.Decrypt()
	return plaintext, err
}

func (ks *Server) decryptWithVault(key *keyservice.VaultKey, ciphertext []byte) ([]byte, error) {
	if ks.vaultToken == "" {
		return nil, status.Errorf(codes.Unimplemented, "Hashicorp Vault decrypt service unavailable: no token found")
	}

	vaultKey := hcvault.MasterKey{
		VaultAddress: key.VaultAddress,
		EnginePath:   key.EnginePath,
		KeyName:      key.KeyName,
		VaultToken:   ks.vaultToken,
	}
	vaultKey.EncryptedKey = string(ciphertext)
	plaintext, err := vaultKey.Decrypt()
	return plaintext, err
}

func (ks *Server) encryptWithAzureKeyvault(key *keyservice.AzureKeyVaultKey, plaintext []byte) ([]byte, error) {
	if ks.azureAADConfig == nil {
		return nil, status.Errorf(codes.Unimplemented, "Azure Key Vault encrypt service unavailable: no authentication config present")
	}

	azureKey := azkv.MasterKey{
		VaultURL: key.VaultUrl,
		Name:     key.Name,
		Version:  key.Version,
	}
	if err := ks.azureAADConfig.SetToken(&azureKey); err != nil {
		return nil, fmt.Errorf("failed to set token for Azure encryption request: %w", err)
	}
	if err := azureKey.Encrypt(plaintext); err != nil {
		return nil, err
	}
	return []byte(azureKey.EncryptedKey), nil
}

func (ks *Server) decryptWithAzureKeyvault(key *keyservice.AzureKeyVaultKey, ciphertext []byte) ([]byte, error) {
	if ks.azureAADConfig == nil {
		return nil, status.Errorf(codes.Unimplemented, "Azure Key Vault decrypt service unavailable: no authentication config present")
	}

	azureKey := azkv.MasterKey{
		VaultURL: key.VaultUrl,
		Name:     key.Name,
		Version:  key.Version,
	}
	if err := ks.azureAADConfig.SetToken(&azureKey); err != nil {
		return nil, fmt.Errorf("failed to set token for Azure decryption request: %w", err)
	}
	azureKey.EncryptedKey = string(ciphertext)
	plaintext, err := azureKey.Decrypt()
	return plaintext, err
}
