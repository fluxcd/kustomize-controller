package azkv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"unicode/utf16"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/keyvault/keyvault"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/dimchansky/utfbom"
)

type Key struct {
	VaultUrl string
	Name     string
	Version  string

	keyvaultAuthorizer autorest.Authorizer
}

func (key *Key) LoadCredentialsFromFile(fileLocation string) error {
	s := auth.FileSettings{}
	s.Values = map[string]string{}

	contents, err := ioutil.ReadFile(fileLocation)
	if err != nil {
		return err
	}

	// Auth file might be encoded
	var decoded []byte
	reader, enc := utfbom.Skip(bytes.NewReader(contents))
	switch enc {
	case utfbom.UTF16LittleEndian:
		u16 := make([]uint16, (len(contents)/2)-1)
		err := binary.Read(reader, binary.LittleEndian, &u16)
		if err != nil {
			return err
		}
		decoded = []byte(string(utf16.Decode(u16)))
	case utfbom.UTF16BigEndian:
		u16 := make([]uint16, (len(contents)/2)-1)
		err := binary.Read(reader, binary.BigEndian, &u16)
		if err != nil {
			return err
		}
		decoded = []byte(string(utf16.Decode(u16)))
	default:
		decoded, err = ioutil.ReadAll(reader)
		if err != nil {
			return err
		}
	}

	// unmarshal file
	authFile := map[string]interface{}{}
	err = json.Unmarshal(decoded, &authFile)
	if err != nil {
		return err
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

	key.keyvaultAuthorizer, err = s.ClientCredentialsAuthorizerWithResource(azure.PublicCloud.ResourceIdentifiers.KeyVault)
	if err != nil {
		return fmt.Errorf("failed to load azure credentials file: %w", err)
	}

	return nil
}

func (key *Key) Encrypt(plaintext []byte) ([]byte, error) {
	plainstring := string(plaintext)

	kv := keyvault.New()
	kv.Authorizer = key.keyvaultAuthorizer

	result, err := kv.Encrypt(context.Background(), key.VaultUrl, key.Name, "", keyvault.KeyOperationsParameters{Algorithm: keyvault.RSAOAEP256, Value: &plainstring})
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data: %w", err)
	}

	ciphertext, err := base64.RawURLEncoding.DecodeString(*result.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data: %w", err)
	}

	return ciphertext, nil
}

func (key *Key) Decrypt(ciphertext []byte) ([]byte, error) {
	cipherstring := string(ciphertext)

	kv := keyvault.New()
	kv.Authorizer = key.keyvaultAuthorizer

	result, err := kv.Decrypt(context.Background(), key.VaultUrl, key.Name, key.Version, keyvault.KeyOperationsParameters{Algorithm: keyvault.RSAOAEP256, Value: &cipherstring})
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	plaintext, err := base64.RawURLEncoding.DecodeString(*result.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	return plaintext, nil
}
