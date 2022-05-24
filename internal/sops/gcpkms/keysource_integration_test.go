//go:build integration && disabled
// +build integration,disabled

// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package gcpkms

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	"go.mozilla.org/sops/v3/gcpkms"

	"google.golang.org/api/option"
	kmspb "google.golang.org/genproto/googleapis/cloud/kms/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kms "cloud.google.com/go/kms/apiv1"
)

var (
	project       = os.Getenv("TEST_PROJECT")
	testKeyring   = os.Getenv("TEST_KEYRING")
	testKey       = os.Getenv("TEST_CRYPTO_KEY")
	testCredsJSON = os.Getenv("TEST_CRED_JSON")
	resourceID    = fmt.Sprintf("projects/%s/locations/global/keyRings/%s/cryptoKeys/%s",
		project, testKeyring, testKey)
)

func TestMasterKey_Decrypt_SOPS_Compat(t *testing.T) {
	g := NewWithT(t)
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", testCredsJSON)

	g.Expect(createKMSKeyIfNotExists(resourceID)).To(Succeed())

	dataKey := []byte("blue golden light")
	encryptedKey := gcpkms.NewMasterKeyFromResourceID(resourceID)
	g.Expect(encryptedKey.Encrypt(dataKey)).To(Succeed())

	decryptionKey := MasterKeyFromResourceID(resourceID)
	creds, err := ioutil.ReadFile(testCredsJSON)
	g.Expect(err).ToNot(HaveOccurred())
	decryptionKey.EncryptedKey = encryptedKey.EncryptedKey
	decryptionKey.credentialJSON = creds
	dec, err := decryptionKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_Encrypt_SOPS_Compat(t *testing.T) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", testCredsJSON)
	g := NewWithT(t)

	g.Expect(createKMSKeyIfNotExists(resourceID)).To(Succeed())

	dataKey := []byte("silver golden lights")

	encryptionKey := MasterKeyFromResourceID(resourceID)
	creds, err := ioutil.ReadFile(testCredsJSON)
	g.Expect(err).ToNot(HaveOccurred())
	encryptionKey.credentialJSON = creds
	err = encryptionKey.Encrypt(dataKey)
	g.Expect(err).ToNot(HaveOccurred())

	decryptionKey := gcpkms.NewMasterKeyFromResourceID(resourceID)
	decryptionKey.EncryptedKey = encryptionKey.EncryptedKey
	dec, err := decryptionKey.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(dataKey))
}

func TestMasterKey_EncryptDecrypt_RoundTrip(t *testing.T) {
	g := NewWithT(t)

	g.Expect(createKMSKeyIfNotExists(resourceID)).To(Succeed())

	key := MasterKeyFromResourceID(resourceID)
	creds, err := ioutil.ReadFile(testCredsJSON)
	g.Expect(err).ToNot(HaveOccurred())
	key.credentialJSON = creds

	datakey := []byte("a thousand splendid sons")
	g.Expect(key.Encrypt(datakey)).To(Succeed())
	g.Expect(key.EncryptedKey).ToNot(BeEmpty())

	dec, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(dec).To(Equal(datakey))
}

func createKMSKeyIfNotExists(resourceID string) error {
	ctx := context.Background()
	// check if crypto key exists if not create it
	c, err := kms.NewKeyManagementClient(ctx, option.WithCredentialsFile(testCredsJSON))
	if err != nil {
		return fmt.Errorf("err creating client: %q", err)
	}

	getCryptoKeyReq := &kmspb.GetCryptoKeyRequest{
		Name: resourceID,
	}
	_, err = c.GetCryptoKey(ctx, getCryptoKeyReq)
	if err == nil {
		return nil
	}

	e, ok := status.FromError(err)
	if !ok || (ok && e.Code() != codes.NotFound) {
		return fmt.Errorf("err getting crypto key: %q", err)
	}

	projectID := fmt.Sprintf("projects/%s/locations/global", project)
	createKeyRingReq := &kmspb.CreateKeyRingRequest{
		Parent:    projectID,
		KeyRingId: testKeyring,
	}

	_, err = c.CreateKeyRing(ctx, createKeyRingReq)
	e, ok = status.FromError(err)
	if err != nil && !(ok && e.Code() == codes.AlreadyExists) {
		return fmt.Errorf("err creating key ring: %q", err)
	}

	keyRingName := fmt.Sprintf("%s/keyRings/%s", projectID, testKeyring)
	keyReq := &kmspb.CreateCryptoKeyRequest{
		Parent:      keyRingName,
		CryptoKeyId: testKey,
		CryptoKey: &kmspb.CryptoKey{
			Purpose: kmspb.CryptoKey_ENCRYPT_DECRYPT,
		},
	}
	_, err = c.CreateCryptoKey(ctx, keyReq)
	e, ok = status.FromError(err)
	if err != nil && !(ok && e.Code() == codes.AlreadyExists) {
		return fmt.Errorf("err creating crypto key: %q", err)
	}

	return nil
}
