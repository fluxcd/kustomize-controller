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
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestToken_ApplyToMasterKey(t *testing.T) {
	g := NewWithT(t)

	token, err := TokenFromAADConfig(
		AADConfig{TenantID: "tenant", ClientID: "client", ClientSecret: "secret"},
	)
	g.Expect(err).ToNot(HaveOccurred())

	key := &MasterKey{}
	token.ApplyToMasterKey(key)
	g.Expect(key.token).To(Equal(token.token))
}

func TestMasterKey_EncryptedDataKey(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{EncryptedKey: "some key"}
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(key.EncryptedKey))
}

func TestMasterKey_SetEncryptedDataKey(t *testing.T) {
	g := NewWithT(t)

	encryptedKey := []byte("encrypted")
	key := &MasterKey{}
	key.SetEncryptedDataKey(encryptedKey)
	g.Expect(key.EncryptedKey).To(BeEquivalentTo(encryptedKey))
}

func TestMasterKey_NeedsRotation(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromURL("", "", "")
	g.Expect(key.NeedsRotation()).To(BeFalse())

	key.CreationDate = key.CreationDate.Add(-(azkvTTL + time.Second))
	g.Expect(key.NeedsRotation()).To(BeTrue())
}

func TestMasterKey_ToString(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromURL("https://myvault.vault.azure.net", "key-name", "key-version")
	g.Expect(key.ToString()).To(Equal("https://myvault.vault.azure.net/keys/key-name/key-version"))
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)

	key := MasterKeyFromURL("https://myvault.vault.azure.net", "key-name", "key-version")
	key.EncryptedKey = "data"
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"vaultUrl":   key.VaultURL,
		"key":        key.Name,
		"version":    key.Version,
		"created_at": key.CreationDate.UTC().Format(time.RFC3339),
		"enc":        key.EncryptedKey,
	}))
}
