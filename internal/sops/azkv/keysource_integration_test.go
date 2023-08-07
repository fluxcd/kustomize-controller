//go:build integration
// +build integration

// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package azkv

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	. "github.com/onsi/gomega"
	"go.mozilla.org/sops/v3/azkv"
)

// The following values should be created based on the instructions in:
// https://github.com/mozilla/sops#encrypting-using-azure-key-vault
var (
	testVaultURL        = os.Getenv("TEST_AZURE_VAULT_URL")
	testVaultKeyName    = os.Getenv("TEST_AZURE_VAULT_KEY_NAME")
	testVaultKeyVersion = os.Getenv("TEST_AZURE_VAULT_KEY_VERSION")
	testAADConfig       = AADConfig{
		TenantID:     os.Getenv("TEST_AZURE_TENANT_ID"),
		ClientID:     os.Getenv("TEST_AZURE_CLIENT_ID"),
		ClientSecret: os.Getenv("TEST_AZURE_CLIENT_SECRET"),
	}
)

func TestMasterKey_Encrypt(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromURL(testVaultURL, testVaultKeyName, testVaultKeyVersion)
	token, err := TokenFromAADConfig(testAADConfig)
	g.Expect(err).ToNot(HaveOccurred())
	token.ApplyToMasterKey(key)

	g.Expect(key.Encrypt([]byte("foo"))).To(Succeed())
	g.Expect(key.EncryptedDataKey()).ToNot(BeEmpty())
}

func TestMasterKey_Decrypt(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromURL(testVaultURL, testVaultKeyName, testVaultKeyVersion)
	token, err := TokenFromAADConfig(testAADConfig)
	g.Expect(err).ToNot(HaveOccurred())
	token.ApplyToMasterKey(key)

	dataKey := []byte("this is super secret data")
	c, err := azkeys.NewClient(key.VaultURL, key.token, nil)
	g.Expect(err).ToNot(HaveOccurred())
	resp, err := c.Encrypt(context.Background(), key.Name, key.Version, azkeys.KeyOperationParameters{
		Algorithm: to.Ptr(azkeys.EncryptionAlgorithmRSAOAEP256),
		Value:     dataKey,
	}, nil)
	g.Expect(err).ToNot(HaveOccurred())
	key.EncryptedKey = base64.RawURLEncoding.EncodeToString(resp.Result)
	g.Expect(key.EncryptedKey).ToNot(BeEmpty())
	g.Expect(key.EncryptedKey).ToNot(Equal(dataKey))

	got, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromURL(testVaultURL, testVaultKeyName, testVaultKeyVersion)
	token, err := TokenFromAADConfig(testAADConfig)
	g.Expect(err).ToNot(HaveOccurred())
	token.ApplyToMasterKey(key)

	dataKey := []byte("some-data-that-should-be-secret")

	g.Expect(key.Encrypt(dataKey)).To(Succeed())
	g.Expect(key.EncryptedDataKey()).ToNot(BeEmpty())

	dec, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	encryptKey := MasterKeyFromURL(testVaultURL, testVaultKeyName, testVaultKeyVersion)
	token, err := TokenFromAADConfig(testAADConfig)
	g.Expect(err).ToNot(HaveOccurred())
	token.ApplyToMasterKey(encryptKey)

	dataKey := []byte("foo")
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	t.Setenv("AZURE_CLIENT_ID", testAADConfig.ClientID)
	t.Setenv("AZURE_TENANT_ID", testAADConfig.TenantID)
	t.Setenv("AZURE_CLIENT_SECRET", testAADConfig.ClientSecret)

	decryptKey := &azkv.MasterKey{
		VaultURL:     testVaultURL,
		Name:         testVaultKeyName,
		Version:      testVaultKeyVersion,
		EncryptedKey: encryptKey.EncryptedKey,
		CreationDate: time.Now(),
	}

	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_Decrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("AZURE_CLIENT_ID", testAADConfig.ClientID)
	t.Setenv("AZURE_TENANT_ID", testAADConfig.TenantID)
	t.Setenv("AZURE_CLIENT_SECRET", testAADConfig.ClientSecret)

	dataKey := []byte("foo")

	encryptKey := &azkv.MasterKey{
		VaultURL:     testVaultURL,
		Name:         testVaultKeyName,
		Version:      testVaultKeyVersion,
		CreationDate: time.Now(),
	}
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	decryptKey := MasterKeyFromURL(testVaultURL, testVaultKeyName, testVaultKeyVersion)
	token, err := TokenFromAADConfig(testAADConfig)
	g.Expect(err).ToNot(HaveOccurred())
	token.ApplyToMasterKey(decryptKey)

	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}
