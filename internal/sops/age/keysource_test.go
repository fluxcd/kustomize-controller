// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package age

import (
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	. "github.com/onsi/gomega"
	"go.mozilla.org/sops/v3/age"
)

const (
	mockRecipient string = "age1lzd99uklcjnc0e7d860axevet2cz99ce9pq6tzuzd05l5nr28ams36nvun"
	mockIdentity  string = "AGE-SECRET-KEY-1G0Q5K9TV4REQ3ZSQRMTMG8NSWQGYT0T7TZ33RAZEE0GZYVZN0APSU24RK7"

	// mockUnrelatedIdentity is not actually utilized in tests, but confirms we
	// iterate over all available identities.
	mockUnrelatedIdentity string = "AGE-SECRET-KEY-1432K5YRNSC44GC4986NXMX6GVZ52WTMT9C79CLUVWYY4DKDHD5JSNDP4MC"

	// mockEncryptedKey equals to "data" when decrypted with mockIdentity.
	mockEncryptedKey string = `-----BEGIN AGE ENCRYPTED FILE-----
YWdlLWVuY3J5cHRpb24ub3JnL3YxCi0+IFgyNTUxOSBvY2t2NkdLUGRvY3l2OGNy
MVJWcUhCOEZrUG8yeCtnRnhxL0I5NFk4YjJFCmE4SVQ3MEdyZkFqRWpSa2F0NVhF
VDUybzBxdS9nSGpHSVRVMUI0UEVqZkkKLS0tIGJjeGhNQ0Y5L2VZRVVYSm90djFF
bzdnQ3UwTGljMmtrbWNMV1MxYkFzUFUK4xjOZOTGdcbzuwUY/zeBXhcF+Md3e5PQ
EylloI7MNGbadPGb
-----END AGE ENCRYPTED FILE-----`
)

var (
	mockIdentities = []string{
		mockUnrelatedIdentity,
		mockIdentity,
	}
)

func TestMasterKeyFromRecipient(t *testing.T) {
	g := NewWithT(t)

	got, err := MasterKeyFromRecipient(mockRecipient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).ToNot(BeNil())
	g.Expect(got.Recipient).To(Equal(mockRecipient))
	g.Expect(got.parsedRecipient).ToNot(BeNil())
	g.Expect(got.parsedIdentities).To(BeEmpty())

	got, err = MasterKeyFromRecipient("invalid")
	g.Expect(err).To(HaveOccurred())
	g.Expect(got).To(BeNil())
}

func TestMasterKeyFromIdentities(t *testing.T) {
	g := NewWithT(t)

	got, err := MasterKeyFromIdentities(mockIdentities...)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).ToNot(BeNil())
	g.Expect(got.Identities).To(HaveLen(2))
	g.Expect(got.Identities).To(ContainElements(mockIdentities))
	g.Expect(got.parsedIdentities).To(HaveLen(2))

	got, err = MasterKeyFromIdentities("invalid")
	g.Expect(err).To(HaveOccurred())
	g.Expect(got).To(BeNil())
}

func TestParsedIdentities_Import(t *testing.T) {
	g := NewWithT(t)

	i := make(ParsedIdentities, 0)
	g.Expect(i.Import(mockIdentities...)).To(Succeed())
	g.Expect(i).To(HaveLen(2))

	g.Expect(i.Import("invalid")).To(HaveOccurred())
	g.Expect(i).To(HaveLen(2))
}

func TestParsedIdentities_ApplyToMasterKey(t *testing.T) {
	g := NewWithT(t)

	i := make(ParsedIdentities, 0)
	g.Expect(i.Import(mockIdentities...)).To(Succeed())

	key := &MasterKey{}
	i.ApplyToMasterKey(key)
	g.Expect(key.parsedIdentities).To(BeEquivalentTo(i))
}

func TestMasterKey_Encrypt(t *testing.T) {
	mockParsedRecipient, _ := parseRecipient(mockRecipient)

	tests := []struct {
		name    string
		key     *MasterKey
		wantErr bool
	}{
		{
			name: "recipient",
			key: &MasterKey{
				Recipient: mockRecipient,
			},
		},
		{
			name: "parsed recipient",
			key: &MasterKey{
				parsedRecipient: mockParsedRecipient,
			},
		},
		{
			name: "invalid recipient",
			key: &MasterKey{
				Recipient: "invalid",
			},
			wantErr: true,
		},
		{
			name: "parsed recipient and invalid recipient",
			key: &MasterKey{
				Recipient:       "invalid",
				parsedRecipient: mockParsedRecipient,
			},
			// This should pass, confirming parsedRecipient > Recipient
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := tt.key.Encrypt([]byte("data"))
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(tt.key.EncryptedKey).To(BeEmpty())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tt.key.EncryptedKey).ToNot(BeEmpty())
			g.Expect(tt.key.EncryptedKey).To(ContainSubstring("AGE ENCRYPTED FILE"))
		})
	}
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	dataKey := []byte("foo")

	encryptKey := &MasterKey{
		Recipient: mockRecipient,
	}
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	t.Setenv(age.SopsAgeKeyEnv, mockIdentity)
	decryptKey := &age.MasterKey{
		EncryptedKey: encryptKey.EncryptedKey,
	}
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptIfNeeded(t *testing.T) {
	g := NewWithT(t)

	key, err := MasterKeyFromRecipient(mockRecipient)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(key.EncryptIfNeeded([]byte("data"))).To(Succeed())

	encryptedKey := key.EncryptedKey
	g.Expect(encryptedKey).To(ContainSubstring("AGE ENCRYPTED FILE"))

	g.Expect(key.EncryptIfNeeded([]byte("some other data"))).To(Succeed())
	g.Expect(key.EncryptedKey).To(Equal(encryptedKey))
}

func TestMasterKey_EncryptedDataKey(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{EncryptedKey: "some key"}
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(key.EncryptedKey))
}

func TestMasterKey_Decrypt(t *testing.T) {
	parsedIdentities, _ := parseIdentities(mockIdentities...)

	tests := []struct {
		name    string
		key     *MasterKey
		wantErr bool
	}{
		{
			name: "identities",
			key: &MasterKey{
				Identities:   mockIdentities,
				EncryptedKey: mockEncryptedKey,
			},
		},
		{
			name: "parsed identities",
			key: &MasterKey{
				EncryptedKey:     mockEncryptedKey,
				parsedIdentities: parsedIdentities,
			},
		},
		{
			name: "no identities",
			key: &MasterKey{
				Identities:   []string{},
				EncryptedKey: mockEncryptedKey,
			},
			wantErr: true,
		},
		{
			name: "invalid identity",
			key: &MasterKey{
				Identities:   []string{"invalid"},
				EncryptedKey: mockEncryptedKey,
			},
			wantErr: true,
		},
		{
			name: "invalid encrypted key",
			key: &MasterKey{
				Identities:   mockIdentities,
				EncryptedKey: "invalid",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			data, err := tt.key.Decrypt()
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(data).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(data).To(Equal([]byte("data")))
		})
	}
}

func TestMasterKey_Decrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)

	dataKey := []byte("foo")

	encryptKey := &age.MasterKey{
		Recipient: mockRecipient,
	}
	g.Expect(encryptKey.Encrypt(dataKey)).To(Succeed())

	decryptKey := &MasterKey{
		Identities:   []string{mockIdentity},
		EncryptedKey: encryptKey.EncryptedKey,
	}
	dec, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	encryptKey, err := MasterKeyFromRecipient(mockRecipient)
	g.Expect(err).ToNot(HaveOccurred())

	data := []byte("some secret data")
	g.Expect(encryptKey.Encrypt(data)).To(Succeed())
	g.Expect(encryptKey.EncryptedKey).ToNot(BeEmpty())

	decryptKey, err := MasterKeyFromIdentities(mockIdentity)
	g.Expect(err).ToNot(HaveOccurred())

	decryptKey.EncryptedKey = encryptKey.EncryptedKey
	decryptedData, err := decryptKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(decryptedData).To(Equal(data))
}

func TestMasterKey_ToString(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{Recipient: mockRecipient}
	g.Expect(key.ToString()).To(Equal(key.Recipient))
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)

	key := &MasterKey{
		Recipient:    mockRecipient,
		Identities:   mockIdentities, // must never be included
		EncryptedKey: "some-encrypted-key",
	}
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"recipient": mockRecipient,
		"enc":       key.EncryptedKey,
	}))
}

func Fuzz_Age(f *testing.F) {
	f.Fuzz(func(t *testing.T, receipt, identities string, seed, data []byte) {
		fc := fuzz.NewConsumer(seed)
		masterKey := MasterKey{}

		if err := fc.GenerateStruct(&masterKey); err != nil {
			return
		}

		_ = masterKey.Encrypt(data)
		_ = masterKey.EncryptIfNeeded(data)
		_, _ = MasterKeyFromRecipient(receipt)
		_, _ = MasterKeyFromIdentities(identities)
	})
}
