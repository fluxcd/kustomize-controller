// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

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
