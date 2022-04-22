// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package gcpkms

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	kmspb "google.golang.org/genproto/googleapis/cloud/kms/v1"
	"google.golang.org/grpc"
)

var (
	testResourceID = "projects/test-flux/locations/global/keyRings/test-flux/cryptoKeys/sops"
	decryptedData  = "decrypted data"
	encryptedData  = "encrypted data"
)

func TestMasterKey_EncryptedDataKey(t *testing.T) {
	g := NewWithT(t)
	key := MasterKey{EncryptedKey: encryptedData}
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(encryptedData))
}

func TestMasterKey_SetEncryptedDataKey(t *testing.T) {
	g := NewWithT(t)
	enc := "encrypted key"
	key := &MasterKey{}
	key.SetEncryptedDataKey([]byte(enc))
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(enc))
}

func TestMasterKey_EncryptIfNeeded(t *testing.T) {
	g := NewWithT(t)
	key := MasterKey{EncryptedKey: "encrypted key"}
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(key.EncryptedKey))

	err := key.EncryptIfNeeded([]byte("sops data key"))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(key.EncryptedKey))
}

func TestMasterKey_ToString(t *testing.T) {
	rsrcId := testResourceID
	g := NewWithT(t)
	key := MasterKeyFromResourceID(rsrcId)
	g.Expect(key.ToString()).To(Equal(rsrcId))
}

func TestMasterKey_ToMap(t *testing.T) {
	g := NewWithT(t)
	key := MasterKey{
		credentialJSON: []byte("sensitive creds"),
		CreationDate:   time.Date(2016, time.October, 31, 10, 0, 0, 0, time.UTC),
		ResourceID:     testResourceID,
		EncryptedKey:   "this is encrypted",
	}
	g.Expect(key.ToMap()).To(Equal(map[string]interface{}{
		"resource_id": testResourceID,
		"enc":         "this is encrypted",
		"created_at":  "2016-10-31T10:00:00Z",
	}))
}

func TestMasterKey_createCloudKMSService(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		key       MasterKey
		errString string
	}{
		{
			key: MasterKey{
				ResourceID:     "/projects",
				credentialJSON: []byte("some secret"),
			},
			errString: "no valid resourceId",
		},
		{
			key: MasterKey{
				ResourceID: testResourceID,
				credentialJSON: []byte(`{ "client_id": "<client-id>.apps.googleusercontent.com",
 		"client_secret": "<secret>",
		"type": "authorized_user"}`),
			},
		},
	}

	for _, tt := range tests {
		_, err := tt.key.newKMSClient()
		if tt.errString != "" {
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tt.errString))
		} else {
			g.Expect(err).To(BeNil())
		}
	}
}

func TestMasterKey_Decrypt(t *testing.T) {
	g := NewWithT(t)

	mockKeyManagement.err = nil
	mockKeyManagement.reqs = nil
	mockKeyManagement.resps = append(mockKeyManagement.resps[:0], &kmspb.DecryptResponse{
		Plaintext: []byte(decryptedData),
	})
	key := MasterKey{
		grpcConn:     newGRPCServer("0"),
		ResourceID:   testResourceID,
		EncryptedKey: "encryptedKey",
	}
	data, err := key.Decrypt()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(data).To(BeEquivalentTo(decryptedData))
}

func TestMasterKey_Encrypt(t *testing.T) {
	g := NewWithT(t)

	mockKeyManagement.err = nil
	mockKeyManagement.reqs = nil
	mockKeyManagement.resps = append(mockKeyManagement.resps[:0], &kmspb.EncryptResponse{
		Ciphertext: []byte(encryptedData),
	})

	key := MasterKey{
		grpcConn:   newGRPCServer("0"),
		ResourceID: testResourceID,
	}
	err := key.Encrypt([]byte("encrypt"))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(key.EncryptedDataKey()).To(BeEquivalentTo(base64.StdEncoding.EncodeToString([]byte(encryptedData))))
}

var (
	mockKeyManagement mockKeyManagementServer
)

func newGRPCServer(port string) *grpc.ClientConn {
	serv := grpc.NewServer()
	kmspb.RegisterKeyManagementServiceServer(serv, &mockKeyManagement)

	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%s", port))
	if err != nil {
		log.Fatal(err)
	}
	go serv.Serve(lis)

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithInsecure())
	if err != nil {
		log.Fatal(err)
	}

	return conn
}
