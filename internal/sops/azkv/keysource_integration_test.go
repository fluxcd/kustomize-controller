// +tag integration

/*
Copyright 2022 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package azkv

import (
	"os"
	"testing"
	"time"

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

	key := &MasterKey{
		VaultURL:     testVaultURL,
		Name:         testVaultKeyName,
		Version:      testVaultKeyVersion,
		CreationDate: time.Now(),
	}

	g.Expect(testAADConfig.SetToken(key)).To(Succeed())

	g.Expect(key.Encrypt([]byte("foo"))).To(Succeed())
	g.Expect(key.EncryptedDataKey()).ToNot(BeEmpty())
}

func TestMasterKey_Decrypt(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{
		VaultURL: testVaultURL,
		Name:     testVaultKeyName,
		Version:  testVaultKeyVersion,
		// EncryptedKey equals "foo" in bytes
		EncryptedKey: "AdvS9HGJG7thHiUAisVJ8XqZiKfTjjbMETl5pIUBcOiHhMS6nLJpqeHcoKFUX6T4HFNT5o9tUXJsVprkkXzaL0Fyd01gef-eF4lTKsKl3EAn2hPAbfT-HTiuOnXzm4Zmvb4S-Ef3loOgLuoIH8Ks7SzSGhy6U9qvRk4Y4IZjzHCtUHaGE5utuTTy9lff8h4HCzgCp92ots2PPXD4dGHN_yXs-EpARXGPR2RbWWnj4P3Pu8xeMBk7hDCa51ZweJ_xQBRvXHmSy0PkauDUbr4dlUf6QQa8RxSPsOSaVT8dtVIURZ9YP1p69ajSo98aHXqSBAouZGWkrWgmQsleNrSGcg",
		CreationDate: time.Now(),
	}

	g.Expect(testAADConfig.SetToken(key)).To(Succeed())

	got, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(Equal([]byte("foo")))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{
		VaultURL:     testVaultURL,
		Name:         testVaultKeyName,
		Version:      testVaultKeyVersion,
		CreationDate: time.Now(),
	}

	g.Expect(testAADConfig.SetToken(key)).To(Succeed())

	dataKey := []byte("some-data-that-should-be-secret")

	g.Expect(key.Encrypt(dataKey)).To(Succeed())
	g.Expect(key.EncryptedDataKey()).ToNot(BeEmpty())

	dec, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	encryptKey := &MasterKey{
		VaultURL:     testVaultURL,
		Name:         testVaultKeyName,
		Version:      testVaultKeyVersion,
		CreationDate: time.Now(),
	}
	g.Expect(testAADConfig.SetToken(encryptKey)).To(Succeed())

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

	decryptKey := &MasterKey{
		VaultURL:     testVaultURL,
		Name:         testVaultKeyName,
		Version:      testVaultKeyVersion,
		EncryptedKey: encryptKey.EncryptedKey,
		CreationDate: time.Now(),
	}
	g.Expect(testAADConfig.SetToken(decryptKey)).To(Succeed())

	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}
