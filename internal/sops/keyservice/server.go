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
	// Prompt indicates whether the server should prompt before decrypting
	// or encrypting data.
	Prompt bool

	// HomeDir configures the home directory used for PGP operations.
	HomeDir string

	// AgePrivateKeys configures the age private keys known by the server.
	AgePrivateKeys []string

	// VaultToken configures the Vault token used by the server.
	VaultToken string

	// DefaultServer is the server used for any other request than a PGP
	// or age encryption/decryption.
	DefaultServer keyservice.KeyServiceServer
}

func NewServer(prompt bool, homeDir, vaultToken string, agePrivateKeys []string) keyservice.KeyServiceServer {
	server := &Server{
		Prompt:         prompt,
		HomeDir:        homeDir,
		AgePrivateKeys: agePrivateKeys,
		VaultToken:     vaultToken,
		DefaultServer: &keyservice.Server{
			Prompt: prompt,
		},
	}
	return server
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
	return []byte(plaintext), err
}

// Encrypt takes an encrypt request and encrypts the provided plaintext with the provided key,
// returning the encrypted result.
func (ks Server) Encrypt(ctx context.Context,
	req *keyservice.EncryptRequest) (*keyservice.EncryptResponse, error) {
	key := req.Key
	var response *keyservice.EncryptResponse
	switch k := key.KeyType.(type) {
	case *keyservice.Key_PgpKey:
		ciphertext, err := ks.encryptWithPgp(k.PgpKey, req.Plaintext)
		if err != nil {
			return nil, err
		}
		response = &keyservice.EncryptResponse{
			Ciphertext: ciphertext,
		}
	case *keyservice.Key_AgeKey:
		ciphertext, err := ks.encryptWithAge(k.AgeKey, req.Plaintext)
		if err != nil {
			return nil, err
		}
		response = &keyservice.EncryptResponse{
			Ciphertext: ciphertext,
		}
	default:
		return ks.DefaultServer.Encrypt(ctx, req)
	}
	if ks.Prompt {
		err := ks.prompt(key, "encrypt")
		if err != nil {
			return nil, err
		}
	}
	return response, nil
}

func keyToString(key *keyservice.Key) string {
	switch k := key.KeyType.(type) {
	case *keyservice.Key_PgpKey:
		return fmt.Sprintf("PGP key with fingerprint %s", k.PgpKey.Fingerprint)
	default:
		return "Unknown key type"
	}
}

func (ks Server) prompt(key *keyservice.Key, requestType string) error {
	keyString := keyToString(key)
	var response string
	for response != "y" && response != "n" {
		fmt.Printf("\nReceived %s request using %s. Respond to request? (y/n): ", requestType, keyString)
		_, err := fmt.Scanln(&response)
		if err != nil {
			return err
		}
	}
	if response == "n" {
		return status.Errorf(codes.PermissionDenied, "Request rejected by user")
	}
	return nil
}

// Decrypt takes a decrypt request and decrypts the provided ciphertext with the provided key,
// returning the decrypted result.
func (ks Server) Decrypt(ctx context.Context,
	req *keyservice.DecryptRequest) (*keyservice.DecryptResponse, error) {
	key := req.Key
	var response *keyservice.DecryptResponse
	switch k := key.KeyType.(type) {
	case *keyservice.Key_PgpKey:
		plaintext, err := ks.decryptWithPgp(k.PgpKey, req.Ciphertext)
		if err != nil {
			return nil, err
		}
		response = &keyservice.DecryptResponse{
			Plaintext: plaintext,
		}
	case *keyservice.Key_AgeKey:
		plaintext, err := ks.decryptWithAge(k.AgeKey, req.Ciphertext)
		if err != nil {
			return nil, err
		}
		response = &keyservice.DecryptResponse{
			Plaintext: plaintext,
		}
	case *keyservice.Key_VaultKey:
		// FallBack to DefaultServer if vaulToken is not present
		// to ensure backward compatibility (VAULT_TOKEN env var is set)
		if ks.VaultToken == "" {
			return ks.DefaultServer.Decrypt(ctx, req)
		}

		plaintext, err := ks.decryptWithVault(k.VaultKey, req.Ciphertext)
		if err != nil {
			return nil, err
		}
		response = &keyservice.DecryptResponse{
			Plaintext: plaintext,
		}
	default:
		return ks.DefaultServer.Decrypt(ctx, req)
	}
	if ks.Prompt {
		err := ks.prompt(key, "decrypt")
		if err != nil {
			return nil, err
		}
	}
	return response, nil
}
