/*
Copyright 2026 The Flux authors

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

package v1

// VaultConfig is the controller-level configuration that enables and scopes
// authentication to OpenBao/Vault instances for SOPS decryption. The
// controller presents a Kubernetes ServiceAccount token to a JWT-backed auth
// method (e.g. the Kubernetes or JWT auth method). The operator provides this
// config through a ConfigMap, listing the instances the controller may
// authenticate to along with each instance's login path. It only governs this
// ServiceAccount-token authentication: the existing static token decryption
// paths (the sops.vault-token Secret entry and the VAULT_TOKEN environment
// variable) are unaffected and continue to work for any address.
type VaultConfig struct {
	// Instances is the list of known OpenBao/Vault instances.
	Instances []VaultInstance `json:"instances"`
}

// VaultInstance describes a single OpenBao/Vault instance and how the
// controller should authenticate to it.
type VaultInstance struct {
	// Address is the address of the OpenBao/Vault instance, matching
	// the address stored in the SOPS metadata of the encrypted data key.
	Address string `json:"address"`

	// LoginPath is the API path of the login endpoint to authenticate to this
	// instance with, e.g. "auth/kubernetes/login". It is used verbatim, so it
	// supports any JWT-backed auth method (e.g. the Kubernetes or JWT auth
	// method) and namespace-prefixed paths (e.g. "ns1/ns2/auth/kubernetes/login").
	LoginPath string `json:"loginPath"`
}
