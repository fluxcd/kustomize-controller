// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package keyservice

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"unicode/utf16"

	"go.mozilla.org/sops/v3/keyservice"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/fluxcd/kustomize-controller/internal/sops/age"
	"github.com/fluxcd/kustomize-controller/internal/sops/pgp"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/keyvault/keyvault"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/dimchansky/utfbom"
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

	// DefaultServer is the server used for any other request than a PGP
	// or age encryption/decryption.
	DefaultServer keyservice.KeyServiceServer
}

func NewServer(prompt bool, homeDir string, agePrivateKeys []string) keyservice.KeyServiceServer {
	server := &Server{
		Prompt:         prompt,
		HomeDir:        homeDir,
		AgePrivateKeys: agePrivateKeys,
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

func (ks *Server) newKeyvaultAuthorizerFromFile(fileLocation string) (autorest.Authorizer, error) {
	s := auth.FileSettings{}
	s.Values = map[string]string{}

	contents, err := ioutil.ReadFile(fileLocation)
	if err != nil {
		return nil, err
	}

	// Auth file might be encoded
	var decoded []byte
	reader, enc := utfbom.Skip(bytes.NewReader(contents))
	switch enc {
	case utfbom.UTF16LittleEndian:
		u16 := make([]uint16, (len(contents)/2)-1)
		err := binary.Read(reader, binary.LittleEndian, &u16)
		if err != nil {
			return nil, err
		}
		decoded = []byte(string(utf16.Decode(u16)))
	case utfbom.UTF16BigEndian:
		u16 := make([]uint16, (len(contents)/2)-1)
		err := binary.Read(reader, binary.BigEndian, &u16)
		if err != nil {
			return nil, err
		}
		decoded = []byte(string(utf16.Decode(u16)))
	default:
		decoded, err = ioutil.ReadAll(reader)
		if err != nil {
			return nil, err
		}
	}

	// unmarshal file
	authFile := map[string]interface{}{}
	err = json.Unmarshal(decoded, &authFile)
	if err != nil {
		return nil, err
	}

	if val, ok := authFile["clientId"]; ok {
		s.Values["AZURE_CLIENT_ID"] = val.(string)
	}
	if val, ok := authFile["clientSecret"]; ok {
		s.Values["AZURE_CLIENT_SECRET"] = val.(string)
	}
	if val, ok := authFile["clientCertificate"]; ok {
		s.Values["AZURE_CERTIFICATE_PATH"] = val.(string)
	}
	if val, ok := authFile["clientCertificatePassword"]; ok {
		s.Values["AZURE_CERTIFICATE_PASSWORD"] = val.(string)
	}
	if val, ok := authFile["subscriptionId"]; ok {
		s.Values["AZURE_SUBSCRIPTION_ID"] = val.(string)
	}
	if val, ok := authFile["tenantId"]; ok {
		s.Values["AZURE_TENANT_ID"] = val.(string)
	}
	if val, ok := authFile["activeDirectoryEndpointUrl"]; ok {
		s.Values["ActiveDirectoryEndpoint"] = val.(string)
	}
	if val, ok := authFile["resourceManagerEndpointUrl"]; ok {
		s.Values["ResourceManagerEndpoint"] = val.(string)
	}
	if val, ok := authFile["activeDirectoryGraphResourceId"]; ok {
		s.Values["GraphResourceID"] = val.(string)
	}
	if val, ok := authFile["sqlManagementEndpointUrl"]; ok {
		s.Values["SQLManagementEndpoint"] = val.(string)
	}
	if val, ok := authFile["galleryEndpointUrl"]; ok {
		s.Values["GalleryEndpoint"] = val.(string)
	}
	if val, ok := authFile["managementEndpointUrl"]; ok {
		s.Values["ManagementEndpoint"] = val.(string)
	}

	resource := azure.PublicCloud.ResourceIdentifiers.KeyVault
	if a, err := s.ClientCredentialsAuthorizerWithResource(resource); err == nil {
		return a, err
	}
	if a, err := s.ClientCertificateAuthorizerWithResource(resource); err == nil {
		return a, err
	}
	return nil, errors.New("auth file missing client and certificate credentials")
}

func (ks *Server) encryptWithAzureKeyvault(key *keyservice.AzureKeyVaultKey, plaintext []byte) ([]byte, error) {
	var err error

	kv := keyvault.New()
	if kv.Authorizer, err = ks.newKeyvaultAuthorizerFromFile(filepath.Join(ks.HomeDir, "azure_kv.json")); err != nil {
		return nil, err
	}

	plainstring := string(plaintext)
	result, err := kv.Encrypt(context.Background(), key.VaultUrl, key.Name, "", keyvault.KeyOperationsParameters{Algorithm: keyvault.RSAOAEP256, Value: &plainstring})
	if err != nil {
		return nil, fmt.Errorf("Failed to encrypt data: %w", err)
	}

	ciphertext, err := base64.RawURLEncoding.DecodeString(*result.Result)
	if err != nil {
		return nil, fmt.Errorf("Failed to encrypt data: %w", err)
	}

	return ciphertext, nil
}

func (ks *Server) decryptWithAzureKeyvault(key *keyservice.AzureKeyVaultKey, ciphertext []byte) ([]byte, error) {
	var err error

	kv := keyvault.New()
	if kv.Authorizer, err = ks.newKeyvaultAuthorizerFromFile(filepath.Join(ks.HomeDir, "azure_kv.json")); err != nil {
		return nil, err
	}

	cipherstring := string(ciphertext)
	result, err := kv.Decrypt(context.Background(), key.VaultUrl, key.Name, key.Version, keyvault.KeyOperationsParameters{Algorithm: keyvault.RSAOAEP256, Value: &cipherstring})
	if err != nil {
		return nil, fmt.Errorf("Failed to decrypt data: %w", err)
	}

	plaintext, err := base64.RawURLEncoding.DecodeString(*result.Result)
	if err != nil {
		return nil, fmt.Errorf("Failed to decrypt data: %w", err)
	}

	return plaintext, nil
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
	case *keyservice.Key_AzureKeyvaultKey:
		if _, err := os.Stat(filepath.Join(ks.HomeDir, "azure_kv.json")); os.IsNotExist(err) {
			return ks.Encrypt(ctx, req)
		} else {
			ciphertext, err := ks.encryptWithAzureKeyvault(k.AzureKeyvaultKey, req.Plaintext)
			if err != nil {
				return nil, err
			}
			response = &keyservice.EncryptResponse{
				Ciphertext: ciphertext,
			}
		}
	default:
		return ks.Encrypt(ctx, req)
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
		return fmt.Sprintf("Unknown key type")
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
		return grpc.Errorf(codes.PermissionDenied, "Request rejected by user")
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
	case *keyservice.Key_AzureKeyvaultKey:
		if _, err := os.Stat(filepath.Join(ks.HomeDir, "azure_kv.json")); os.IsNotExist(err) {
			return ks.DefaultServer.Decrypt(ctx, req)
		} else {
			plaintext, err := ks.decryptWithAzureKeyvault(k.AzureKeyvaultKey, req.Ciphertext)
			if err != nil {
				return nil, err
			}
			response = &keyservice.DecryptResponse{
				Plaintext: plaintext,
			}
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
