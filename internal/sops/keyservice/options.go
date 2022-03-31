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
	"github.com/fluxcd/kustomize-controller/internal/sops/azkv"

	"go.mozilla.org/sops/v3/keyservice"
)

// ServerOption is some configuration that modifies the Server.
type ServerOption interface {
	// ApplyToServer applies this configuration to the given Server.
	ApplyToServer(s *Server)
}

// WithHomeDir configures the contained "home directory" on the Server.
type WithHomeDir string

// ApplyToServer applies this configuration to the given Server.
func (o WithHomeDir) ApplyToServer(s *Server) {
	s.homeDir = string(o)
}

// WithVaultToken configures the Hashicorp Vault token on the Server.
type WithVaultToken string

// ApplyToServer applies this configuration to the given Server.
func (o WithVaultToken) ApplyToServer(s *Server) {
	s.vaultToken = string(o)
}

// WithAgePrivateKey configures an age private key on the Server.
// It can be used multiple times, as the value is appended to the set
// of keys already configured on the Server.
type WithAgePrivateKey string

// ApplyToServer applies this configuration to the given Server.
func (o WithAgePrivateKey) ApplyToServer(s *Server) {
	s.agePrivateKeys = append(s.agePrivateKeys, string(o))
}

// WithAgePrivateKeys configures a set of age private keys on the Server.
// It can be used multiple times, as the set is appended to the set
// of keys already configured on the Server.
type WithAgePrivateKeys []string

// ApplyToServer applies this configuration to the given Server.
func (o WithAgePrivateKeys) ApplyToServer(s *Server) {
	for _, k := range o {
		s.agePrivateKeys = append(s.agePrivateKeys, k)
	}
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
