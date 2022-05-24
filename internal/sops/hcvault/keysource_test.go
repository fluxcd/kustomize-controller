// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package hcvault

import (
	"fmt"
	logger "log"
	"os"
	"path"
	"testing"
	"time"

	"github.com/hashicorp/vault/api"
	vaulttransit "github.com/hashicorp/vault/builtin/logical/transit"
	vaulthttp "github.com/hashicorp/vault/http"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/hashicorp/vault/vault"
	. "github.com/onsi/gomega"
	"go.mozilla.org/sops/v3/hcvault"
)

var (
	// testEnginePath is the path to mount the Vault Transit on.
	testEnginePath = "sops"
	// testVaultAddress is the HTTP/S address of the Vault server, it is set
	// by TestMain after booting it.
	testVaultAddress string
	// testVaultToken is the token of the Vault server.
	testVaultToken string
)

// TestMain initializes a Vault server using Docker, writes the HTTP address to
// testVaultAddress, waits for it to become ready to serve requests, and enables
// Vault Transit on the testEnginePath. It then runs all the tests, which can
// make use of the various `test*` variables.
func TestMain(m *testing.M) {
	// this is set to prevent "certificate signed by unknown authority" errors
	os.Setenv("VAULT_SKIP_VERIFY", "true")
	os.Setenv("VAULT_INSECURE", "true")
	t := &testing.T{}
	coreConfig := &vault.CoreConfig{
		LogicalBackends: map[string]logical.Factory{
			"transit": vaulttransit.Factory,
		},
	}
	cluster := vault.NewTestCluster(t, coreConfig, &vault.TestClusterOptions{
		HandlerFunc: vaulthttp.Handler,
		NumCores:    1,
	})
	cluster.Start()

	if err := vault.TestWaitActiveWithError(cluster.Cores[0].Core); err != nil {
		logger.Fatalf("test core not active: %s", err)
	}

	testClient := cluster.Cores[0].Client

	status, err := testClient.Sys().InitStatus()
	if err != nil {
		logger.Fatalf("cannot checking Vault client status: %s", err)
	}
	if status != true {
		logger.Fatal("waiting on Vault server to become ready")
	}

	testVaultToken = testClient.Token()
	testVaultAddress = testClient.Address()

	if err := enableVaultTransit(testVaultAddress, testVaultToken, testEnginePath); err != nil {
		logger.Fatalf("could not enable Vault transit: %s", err)
	}

	// Run the tests, but only if we succeeded in setting up the Vault server
	var code int
	code = m.Run()

	cluster.Cleanup()
	os.Exit(code)
}

func TestMasterKey_Encrypt(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromAddress(testVaultAddress, testEnginePath, "encrypt")
	(VaultToken(testVaultToken)).ApplyToMasterKey(key)
	g.Expect(createVaultKey(key)).To(Succeed())

	dataKey := []byte("the majority of your brain is fat")
	g.Expect(key.Encrypt(dataKey)).To(Succeed())
	g.Expect(key.EncryptedKey).ToNot(BeEmpty())

	client, err := vaultClient(key.VaultAddress, key.vaultToken)
	g.Expect(err).ToNot(HaveOccurred())

	payload := decryptPayload(key.EncryptedKey)
	secret, err := client.Logical().Write(key.decryptPath(), payload)
	g.Expect(err).ToNot(HaveOccurred())

	decryptedData, err := dataKeyFromSecret(secret)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decryptedData).To(Equal(dataKey))

	key.EnginePath = "invalid"
	g.Expect(key.Encrypt(dataKey)).To(HaveOccurred())

	key.EnginePath = testEnginePath
	key.vaultToken = ""
	g.Expect(key.Encrypt(dataKey)).To(HaveOccurred())
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	encryptKey := MasterKeyFromAddress(testVaultAddress, testEnginePath, "encrypt-compat")
	(VaultToken(testVaultToken)).ApplyToMasterKey(encryptKey)
	g.Expect(createVaultKey(encryptKey)).To(Succeed())

	dataKey := []byte("foo")
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	t.Setenv("VAULT_ADDR", testVaultAddress)
	t.Setenv("VAULT_TOKEN", testVaultToken)
	decryptKey := hcvault.NewMasterKey(testVaultAddress, testEnginePath, "encrypt-compat")
	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptIfNeeded(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromAddress(testVaultAddress, testEnginePath, "encrypt-if-needed")
	(VaultToken(testVaultToken)).ApplyToMasterKey(key)
	g.Expect(createVaultKey(key)).To(Succeed())

	g.Expect(key.EncryptIfNeeded([]byte("data"))).To(Succeed())

	encryptedKey := key.EncryptedKey
	g.Expect(encryptedKey).ToNot(BeEmpty())

	g.Expect(key.EncryptIfNeeded([]byte("some other data"))).To(Succeed())
	g.Expect(key.EncryptedKey).To(Equal(encryptedKey))
}

func TestMasterKey_EncryptedDataKey(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{EncryptedKey: "some key"}
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(key.EncryptedKey))
}

func TestMasterKey_Decrypt(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromAddress(testVaultAddress, testEnginePath, "decrypt")
	(VaultToken(testVaultToken)).ApplyToMasterKey(key)
	g.Expect(createVaultKey(key)).To(Succeed())

	client, err := vaultClient(key.VaultAddress, key.vaultToken)
	g.Expect(err).ToNot(HaveOccurred())

	dataKey := []byte("the heart of a shrimp is located in its head")
	secret, err := client.Logical().Write(key.encryptPath(), encryptPayload(dataKey))
	g.Expect(err).ToNot(HaveOccurred())

	encryptedKey, err := encryptedKeyFromSecret(secret)
	g.Expect(err).NotTo(HaveOccurred())

	key.EncryptedKey = encryptedKey
	got, err := key.Decrypt()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(dataKey))

	key.EnginePath = "invalid"
	g.Expect(key.Encrypt(dataKey)).To(HaveOccurred())

	key.EnginePath = testEnginePath
	key.vaultToken = ""
	g.Expect(key.Encrypt(dataKey)).To(HaveOccurred())
}

func TestMasterKey_Decrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	decryptKey := MasterKeyFromAddress(testVaultAddress, testEnginePath, "decrypt-compat")
	(VaultToken(testVaultToken)).ApplyToMasterKey(decryptKey)
	g.Expect(createVaultKey(decryptKey)).To(Succeed())

	dataKey := []byte("foo")

	t.Setenv("VAULT_ADDR", testVaultAddress)
	t.Setenv("VAULT_TOKEN", testVaultToken)
	encryptKey := hcvault.NewMasterKey(testVaultAddress, testEnginePath, "decrypt-compat")
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	token := VaultToken(testVaultToken)

	encryptKey := MasterKeyFromAddress(testVaultAddress, testEnginePath, "roundtrip")
	token.ApplyToMasterKey(encryptKey)
	g.Expect(createVaultKey(encryptKey)).To(Succeed())

	dataKey := []byte("some people have an extra bone in their knee")
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())
	g.Expect(encryptKey.EncryptedKey).ToNot(BeEmpty())

	decryptKey := MasterKeyFromAddress(testVaultAddress, testEnginePath, "roundtrip")
	token.ApplyToMasterKey(decryptKey)
	decryptKey.EncryptedKey = encryptKey.EncryptedKey

	decryptedData, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decryptedData).To(Equal(dataKey))
}

func TestMasterKey_NeedsRotation(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromAddress("", "", "")
	g.Expect(key.NeedsRotation()).To(BeFalse())

	key.CreationDate = key.CreationDate.Add(-(vaultTTL + time.Second))
	g.Expect(key.NeedsRotation()).To(BeTrue())
}

func TestMasterKey_ToString(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromAddress("https://example.com", "engine", "key-name")
	g.Expect(key.ToString()).To(Equal("https://example.com/v1/engine/keys/key-name"))
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{
		KeyName:      "test-key",
		EnginePath:   "engine",
		VaultAddress: testVaultAddress,
		EncryptedKey: "some-encrypted-key",
	}
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"vault_address": key.VaultAddress,
		"key_name":      key.KeyName,
		"engine_path":   key.EnginePath,
		"enc":           key.EncryptedKey,
		"created_at":    "0001-01-01T00:00:00Z",
	}))
}

func Test_encryptedKeyFromSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *api.Secret
		want    string
		wantErr bool
	}{
		{name: "nil secret", secret: nil, wantErr: true},
		{name: "secret with nil data", secret: &api.Secret{Data: nil}, wantErr: true},
		{name: "secret without ciphertext data", secret: &api.Secret{Data: map[string]interface{}{"other": true}}, wantErr: true},
		{name: "ciphertext non string", secret: &api.Secret{Data: map[string]interface{}{"ciphertext": 123}}, wantErr: true},
		{name: "ciphertext data", secret: &api.Secret{Data: map[string]interface{}{"ciphertext": "secret string"}}, want: "secret string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := encryptedKeyFromSecret(tt.secret)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(got).To(BeEmpty())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func Test_dataKeyFromSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *api.Secret
		want    []byte
		wantErr bool
	}{
		{name: "nil secret", secret: nil, wantErr: true},
		{name: "secret with nil data", secret: &api.Secret{Data: nil}, wantErr: true},
		{name: "secret without plaintext data", secret: &api.Secret{Data: map[string]interface{}{"other": true}}, wantErr: true},
		{name: "plaintext non string", secret: &api.Secret{Data: map[string]interface{}{"plaintext": 123}}, wantErr: true},
		{name: "plaintext non base64", secret: &api.Secret{Data: map[string]interface{}{"plaintext": "notbase64"}}, wantErr: true},
		{name: "plaintext base64 data", secret: &api.Secret{Data: map[string]interface{}{"plaintext": "Zm9v"}}, want: []byte("foo")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := dataKeyFromSecret(tt.secret)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(got).To(BeNil())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

// enableVaultTransit enables the Vault Transit backend on the given enginePath.
func enableVaultTransit(address, token, enginePath string) error {
	client, err := vaultClient(address, token)
	if err != nil {
		return fmt.Errorf("cannot create Vault client: %w", err)
	}

	if err = client.Sys().Mount(enginePath, &api.MountInput{
		Type:        "transit",
		Description: "backend transit used by SOPS",
	}); err != nil {
		return fmt.Errorf("failed to mount transit on engine path '%s': %w", enginePath, err)
	}
	return nil
}

// createVaultKey creates a new RSA-4096 Vault key using the data from the
// provided MasterKey.
func createVaultKey(key *MasterKey) error {
	client, err := vaultClient(key.VaultAddress, key.vaultToken)
	if err != nil {
		return fmt.Errorf("cannot create Vault client: %w", err)
	}

	p := path.Join(key.EnginePath, "keys", key.KeyName)
	payload := make(map[string]interface{})
	payload["type"] = "rsa-4096"
	if _, err = client.Logical().Write(p, payload); err != nil {
		return err
	}

	_, err = client.Logical().Read(p)
	return err
}
