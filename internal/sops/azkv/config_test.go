// Copyright (C) 2022 The Flux authors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package azkv

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	. "github.com/onsi/gomega"
)

func TestLoadAADConfigFromBytes(t *testing.T) {
	tests := []struct {
		name    string
		b       []byte
		want    AADConfig
		wantErr bool
	}{
		{
			name: "Service Principal with Secret",
			b: []byte(`tenantId: "some-tenant-id"
clientId: "some-client-id"
clientSecret: "some-client-secret"`),
			want: AADConfig{
				TenantID:     "some-tenant-id",
				ClientID:     "some-client-id",
				ClientSecret: "some-client-secret",
			},
		},
		{
			name: "Service Principal with Certificate",
			b: []byte(`tenantId: "some-tenant-id"
clientId: "some-client-id"
clientCertificate: "some-client-certificate"`),
			want: AADConfig{
				TenantID:          "some-tenant-id",
				ClientID:          "some-client-id",
				ClientCertificate: "some-client-certificate",
			},
		},
		{
			name: "Managed Identity with Client ID",
			b:    []byte(`clientId: "some-client-id"`),
			want: AADConfig{
				ClientID: "some-client-id",
			},
		},
		{
			name: "Service Principal with Secret from az CLI",
			b:    []byte(`{"appId": "some-app-id", "tenant": "some-tenant", "password": "some-password"}`),
			want: AADConfig{
				AZConfig: AZConfig{
					AppID:    "some-app-id",
					Tenant:   "some-tenant",
					Password: "some-password",
				},
			},
		},
		{
			name: "Authority host",
			b:    []byte(`{"authorityHost": "https://example.com"}`),
			want: AADConfig{
				AuthorityHost: "https://example.com",
			},
		},
		{
			name:    "invalid",
			b:       []byte("some string"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got := AADConfig{}
			err := LoadAADConfigFromBytes(tt.b, &got)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(got).To(Equal(tt.want))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}

func TestTokenFromAADConfig(t *testing.T) {
	tlsMock := validTLS(t)

	tests := []struct {
		name    string
		config  AADConfig
		want    azcore.TokenCredential
		wantErr bool
	}{
		{
			name: "Service Principal with Secret",
			config: AADConfig{
				TenantID:     "some-tenant-id",
				ClientID:     "some-client-id",
				ClientSecret: "some-client-secret",
			},
			want: &azidentity.ClientSecretCredential{},
		},
		{
			name: "Service Principal with Certificate",
			config: AADConfig{
				TenantID:          "some-tenant-id",
				ClientID:          "some-client-id",
				ClientCertificate: string(tlsMock),
			},
			want: &azidentity.ClientCertificateCredential{},
		},
		{
			name: "Service Principal with az CLI format",
			config: AADConfig{
				AZConfig: AZConfig{
					AppID:    "some-app-id",
					Tenant:   "some-tenant",
					Password: "some-password",
				},
			},
			want: &azidentity.ClientSecretCredential{},
		},
		{
			name: "Managed Identity with Client ID",
			config: AADConfig{
				ClientID: "some-client-id",
			},
			want: &azidentity.ManagedIdentityCredential{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := TokenFromAADConfig(tt.config)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(got.token).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got.token).ToNot(BeNil())
			g.Expect(got.token).To(BeAssignableToTypeOf(tt.want))
		})
	}
}

func TestAADConfig_GetCloudConfig(t *testing.T) {
	g := NewWithT(t)

	g.Expect((AADConfig{}).GetCloudConfig()).To(Equal(cloud.AzurePublic))
	g.Expect((AADConfig{AuthorityHost: "https://example.com"}).GetCloudConfig()).To(Equal(cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://example.com",
		Services:                     map[cloud.ServiceName]cloud.ServiceConfiguration{},
	}))
}

func validTLS(t *testing.T) []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("Private key cannot be created.", err.Error())
	}

	out := bytes.NewBuffer(nil)

	var privateKey = &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	if err = pem.Encode(out, privateKey); err != nil {
		t.Fatal("Private key cannot be PEM encoded.", err.Error())
	}

	certTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1337),
	}
	cert, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal("Certificate cannot be created.", err.Error())
	}
	var certificate = &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	}
	if err = pem.Encode(out, certificate); err != nil {
		t.Fatal("Certificate cannot be PEM encoded.", err.Error())
	}

	return out.Bytes()
}
