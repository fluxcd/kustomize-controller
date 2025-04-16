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

package keyservice

import (
	extage "filippo.io/age"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/azkv"
	"github.com/getsops/sops/v3/gcpkms"
	"github.com/getsops/sops/v3/hcvault"
	"github.com/getsops/sops/v3/keyservice"
	awskms "github.com/getsops/sops/v3/kms"
	"github.com/getsops/sops/v3/pgp"
	"golang.org/x/oauth2"

	intawskms "github.com/fluxcd/kustomize-controller/internal/sops/awskms"
)

// ServerOption is some configuration that modifies the Server.
type ServerOption interface {
	// ApplyToServer applies this configuration to the given Server.
	ApplyToServer(s *Server)
}

// WithGnuPGHome configures the GnuPG home directory on the Server.
type WithGnuPGHome string

// ApplyToServer applies this configuration to the given Server.
func (o WithGnuPGHome) ApplyToServer(s *Server) {
	s.gnuPGHome = pgp.GnuPGHome(o)
}

// WithVaultToken configures the Hashicorp Vault token on the Server.
type WithVaultToken string

// ApplyToServer applies this configuration to the given Server.
func (o WithVaultToken) ApplyToServer(s *Server) {
	s.vaultToken = hcvault.Token(o)
}

// WithAgeIdentities configures the parsed age identities on the Server.
type WithAgeIdentities []extage.Identity

// ApplyToServer applies this configuration to the given Server.
func (o WithAgeIdentities) ApplyToServer(s *Server) {
	s.ageIdentities = age.ParsedIdentities(o)
}

// WithAWSCredentialsProvider configures the AWS credentials on the Server
type WithAWSCredentialsProvider struct {
	CredentialsProvider func(region string) awssdk.CredentialsProvider
}

// ApplyToServer applies this configuration to the given Server.
func (o WithAWSCredentialsProvider) ApplyToServer(s *Server) {
	s.awsCredentialsProvider = func(arn string) *awskms.CredentialsProvider {
		region := intawskms.GetRegionFromKMSARN(arn)
		cp := o.CredentialsProvider(region)
		return awskms.NewCredentialsProvider(cp)
	}
}

// WithGCPTokenSource configures the GCP token source on the Server.
type WithGCPTokenSource struct {
	TokenSource oauth2.TokenSource
}

// ApplyToServer applies this configuration to the given Server.
func (o WithGCPTokenSource) ApplyToServer(s *Server) {
	s.gcpTokenSource = gcpkms.NewTokenSource(o.TokenSource)
}

// WithAzureTokenCredential configures the Azure credential token on the Server.
type WithAzureTokenCredential struct {
	TokenCredential azcore.TokenCredential
}

// ApplyToServer applies this configuration to the given Server.
func (o WithAzureTokenCredential) ApplyToServer(s *Server) {
	s.azureTokenCredential = azkv.NewTokenCredential(o.TokenCredential)
}

// WithDefaultServer configures the fallback default server on the Server.
type WithDefaultServer struct {
	Server keyservice.KeyServiceServer
}

// ApplyToServer applies this configuration to the given Server.
func (o WithDefaultServer) ApplyToServer(s *Server) {
	s.defaultServer = o.Server
}
