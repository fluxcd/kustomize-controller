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
	"go.mozilla.org/sops/v3/keyservice"

	"github.com/fluxcd/kustomize-controller/internal/sops/age"
	"github.com/fluxcd/kustomize-controller/internal/sops/azkv"
	"github.com/fluxcd/kustomize-controller/internal/sops/hcvault"
	"github.com/fluxcd/kustomize-controller/internal/sops/pgp"
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
	s.vaultToken = hcvault.VaultToken(o)
}

// WithAgeIdentities configures the parsed age identities on the Server.
type WithAgeIdentities []extage.Identity

// ApplyToServer applies this configuration to the given Server.
func (o WithAgeIdentities) ApplyToServer(s *Server) {
	s.ageIdentities = age.ParsedIdentities(o)
}

// WithAzureAADConfig configures the Azure AAD config on the Server.
type WithAzureAADConfig azkv.AADConfig

// ApplyToServer applies this configuration to the given Server.
func (o WithAzureAADConfig) ApplyToServer(s *Server) {
	s.azureAADConfig = (*azkv.AADConfig)(&o)
}

// WithDefaultServer configures the fallback default server on the Server.
type WithDefaultServer struct {
	Server keyservice.KeyServiceServer
}

// ApplyToServer applies this configuration to the given Server.
func (o WithDefaultServer) ApplyToServer(s *Server) {
	s.defaultServer = o.Server
}
