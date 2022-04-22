// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package gcpkms

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"google.golang.org/api/option"
	kmspb "google.golang.org/genproto/googleapis/cloud/kms/v1"
	"google.golang.org/grpc"
)

var (
	// gcpkmsTTL is the duration after which a MasterKey requires rotation.
	gcpkmsTTL = time.Hour * 24 * 30 * 6
)

// CredentialJSON is the service account keys used for authentication towards
// GCP KMS.
type CredentialJSON []byte

// ApplyToMasterKey configures the CredentialJSON on the provided key.
func (c CredentialJSON) ApplyToMasterKey(key *MasterKey) {
	key.credentialJSON = c
}

// MasterKey is a GCP KMS key used to encrypt and decrypt the SOPS
// data key.
// Adapted from https://github.com/mozilla/sops/blob/v3.7.2/gcpkms/keysource.go
// to be able to have fine-grain control over the credentials used to authenticate
// towards GCP KMS.
type MasterKey struct {
	// ResourceID is the resource id used to refer to the gcp kms key.
	// It can be retrieved using the `gcloud` command.
	ResourceID string
	// EncryptedKey is the string returned after encrypting with GCP KMS.
	EncryptedKey string
	// CreationDate is the creation timestamp of the MasterKey. Used
	// for NeedsRotation.
	CreationDate time.Time

	// credentialJSON are the service account keys used to authenticate
	// towards GCP KMS.
	credentialJSON []byte
	// grpcConn can be used to inject a custom GCP client connection.
	// Mostly useful for testing at present, to wire the client to a mock
	// server.
	grpcConn *grpc.ClientConn
}

// MasterKeyFromResourceID creates a new MasterKey with the provided resource
// ID.
func MasterKeyFromResourceID(resourceID string) *MasterKey {
	return &MasterKey{
		ResourceID:   resourceID,
		CreationDate: time.Now().UTC(),
	}
}

// Encrypt takes a SOPS data key, encrypts it with GCP KMS, and stores the
// result in the EncryptedKey field.
func (key *MasterKey) Encrypt(datakey []byte) error {
	cloudkmsService, err := key.newKMSClient()
	if err != nil {
		return err
	}
	defer cloudkmsService.Close()

	req := &kmspb.EncryptRequest{
		Name:      key.ResourceID,
		Plaintext: datakey,
	}
	ctx := context.Background()
	resp, err := cloudkmsService.Encrypt(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to encrypt sops data key with GCP KMS: %w", err)
	}
	key.EncryptedKey = base64.StdEncoding.EncodeToString(resp.Ciphertext)
	return nil
}

// SetEncryptedDataKey sets the encrypted data key for this master key.
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// EncryptedDataKey returns the encrypted data key this master key holds.
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// EncryptIfNeeded encrypts the provided SOPS data key, if it has not been
// encrypted yet.
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if key.EncryptedKey == "" {
		return key.Encrypt(dataKey)
	}
	return nil
}

// Decrypt decrypts the EncryptedKey field with GCP KMS and returns
// the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	service, err := key.newKMSClient()
	if err != nil {
		return nil, err
	}
	defer service.Close()

	decodedCipher, err := base64.StdEncoding.DecodeString(string(key.EncryptedDataKey()))
	if err != nil {
		return nil, err
	}
	req := &kmspb.DecryptRequest{
		Name:       key.ResourceID,
		Ciphertext: decodedCipher,
	}
	ctx := context.Background()
	resp, err := service.Decrypt(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt sops data key with GCP KMS Key: %w", err)
	}

	return resp.Plaintext, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	return time.Since(key.CreationDate) > (gcpkmsTTL)
}

// ToString converts the key to a string representation.
func (key *MasterKey) ToString() string {
	return key.ResourceID
}

// ToMap converts the MasterKey to a map for serialization purposes.
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["resource_id"] = key.ResourceID
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	out["enc"] = key.EncryptedKey
	return out
}

// newKMSClient returns a GCP KMS client configured with the credentialJSON
// and/or grpcConn, falling back to environmental defaults.
// It returns an error if the ResourceID is invalid, or if the client setup
// fails.
func (key *MasterKey) newKMSClient() (*kms.KeyManagementClient, error) {
	re := regexp.MustCompile(`^projects/[^/]+/locations/[^/]+/keyRings/[^/]+/cryptoKeys/[^/]+$`)
	matches := re.FindStringSubmatch(key.ResourceID)
	if matches == nil {
		return nil, fmt.Errorf("no valid resourceId found in %q", key.ResourceID)
	}

	var opts []option.ClientOption
	if key.credentialJSON != nil {
		opts = append(opts, option.WithCredentialsJSON(key.credentialJSON))
	}
	if key.grpcConn != nil {
		opts = append(opts, option.WithGRPCConn(key.grpcConn))
	}

	ctx := context.Background()
	client, err := kms.NewKeyManagementClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return client, nil
}
