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

package decryptor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	vaultapi "github.com/hashicorp/vault/api"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	cryptov1 "github.com/fluxcd/pkg/apis/crypto/v1"
	"github.com/fluxcd/pkg/auth"
	"github.com/fluxcd/pkg/auth/generic"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

// newVaultK8sAuthFunc returns a function that exchanges a Kubernetes
// ServiceAccount token for a Vault token, or nil when Vault secret-less
// authentication is not configured.
//
// Unlike a static sops.vault-token (which takes precedence and is imported in
// ImportKeys), this exchanges a Kubernetes ServiceAccount token for a Vault
// token (any JWT-backed auth method). The login path and role come from the
// transit-engine.auth.fluxcd.io ServiceAccount annotations, or, for backward
// compatibility, from the operator-provided address-to-login-path ConfigMap with
// the role derived from the token identity as "{namespace}_{name}". The two
// mechanisms are mutually exclusive, as enforced by the configuration
// validation. The token is issued for the decryption ServiceAccount when set
// (object-level workload identity), otherwise for the controller's own
// ServiceAccount (controller-level workload identity). The token can only be
// obtained when a request is handled, as the Vault address is only known from
// the SOPS metadata then (the address is used as the default token audience when
// no credential audiences are configured).
func (d *Decryptor) newVaultK8sAuthFunc(baseSpec *cryptov1.Spec,
	baseOpts []auth.Option) func(ctx context.Context, vaultAddress string) (string, error) {

	if baseSpec.Auth != nil {
		if te := baseSpec.Auth.TransitEngine; te != nil && te.LoginPath != "" {
			return d.newTransitEngineVaultAuthFunc(te, baseSpec, baseOpts)
		}
	}

	if d.vaultConfigMap != nil {
		return d.newConfigMapVaultAuthFunc(baseSpec, baseOpts)
	}

	return nil
}

// newTransitEngineVaultAuthFunc returns a Vault auth function that uses the
// transit-engine.auth.fluxcd.io annotations for the login path and role.
func (d *Decryptor) newTransitEngineVaultAuthFunc(transitEngine *cryptov1.TransitEngineSpec,
	baseSpec *cryptov1.Spec, baseOpts []auth.Option) func(ctx context.Context, vaultAddress string) (string, error) {

	return cacheVaultTokens(func(ctx context.Context, vaultAddress string) (string, error) {
		ksaToken, err := d.issueVaultK8sToken(ctx, baseSpec, baseOpts, vaultAddress)
		if err != nil {
			return "", err
		}
		return vaultLogin(ctx, vaultAddress, transitEngine.LoginPath, transitEngine.Role, ksaToken.Token)
	})
}

// newConfigMapVaultAuthFunc returns a Vault auth function that resolves the
// login path per Vault address from the operator-provided ConfigMap and derives
// the role from the token identity as "{namespace}_{name}".
func (d *Decryptor) newConfigMapVaultAuthFunc(baseSpec *cryptov1.Spec,
	baseOpts []auth.Option) func(ctx context.Context, vaultAddress string) (string, error) {

	// Content of --sops-vault-configmap.
	var loginPaths map[string]string

	return cacheVaultTokens(func(ctx context.Context, vaultAddress string) (string, error) {

		// Lazy-load the login paths from the ConfigMap.
		if loginPaths == nil {
			var err error
			loginPaths, err = d.loadVaultLoginPaths(ctx)
			if err != nil {
				return "", err
			}
		}

		// Resolve the login path for this address from the operator-managed
		// ConfigMap, which acts as an allowlist of trusted Vault servers.
		loginPath := loginPaths[vaultAddress]
		if loginPath == "" {
			return "", fmt.Errorf("no Vault login path configured for address '%s' in ConfigMap '%s'",
				vaultAddress, d.vaultConfigMap)
		}
		ksaToken, err := d.issueVaultK8sToken(ctx, baseSpec, baseOpts, vaultAddress)
		if err != nil {
			return "", err
		}
		ns, name, err := serviceAccountFromToken(ksaToken.Token)
		if err != nil {
			return "", fmt.Errorf("failed to derive Vault role for authentication: %w", err)
		}
		role := fmt.Sprintf("%s_%s", ns, name)
		return vaultLogin(ctx, vaultAddress, loginPath, role, ksaToken.Token)
	})
}

// cacheVaultTokens wraps a Vault token fetcher with a per-address in-memory
// cache, serialized by a mutex.
func cacheVaultTokens(fetch func(ctx context.Context, vaultAddress string) (string, error)) func(ctx context.Context, vaultAddress string) (string, error) {
	var mu sync.Mutex
	tokens := make(map[string]string)
	return func(ctx context.Context, vaultAddress string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if token, ok := tokens[vaultAddress]; ok {
			return token, nil
		}
		token, err := fetch(ctx, vaultAddress)
		if err != nil {
			return "", err
		}
		tokens[vaultAddress] = token
		return token, nil
	}
}

// issueVaultK8sToken issues a Kubernetes ServiceAccount token for the
// decryption identity (object- or controller-level), audience-scoped to the
// Vault address.
func (d *Decryptor) issueVaultK8sToken(ctx context.Context, baseSpec *cryptov1.Spec,
	baseOpts []auth.Option, vaultAddress string) (*generic.Token, error) {
	var audiences []string
	if baseSpec.Auth != nil && baseSpec.Auth.Credential != nil {
		audiences = baseSpec.Auth.Credential.Audiences
	}
	if len(audiences) == 0 {
		audiences = []string{vaultAddress}
	}
	tokenOpts := slices.Clone(baseOpts)
	tokenOpts = append(tokenOpts, auth.WithCredentialAudiences(audiences...))
	if d.tokenCache != nil {
		tokenOpts = append(tokenOpts,
			auth.WithCache(*d.tokenCache),
			auth.WithCacheOperation(kustomizev1.MetricDecryptWithTransitEngine))
	}
	accessToken, err := auth.GetAccessToken(ctx, generic.Provider{}, tokenOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to issue Kubernetes token for Vault authentication: %w", err)
	}
	ksaToken, ok := accessToken.(*generic.Token)
	if !ok {
		return nil, fmt.Errorf("unexpected token type %T for Vault authentication", accessToken)
	}
	return ksaToken, nil
}

// loadVaultLoginPaths reads the operator-managed ConfigMap and returns the
// mapping from Vault/OpenBao address to login path. The mapping is parsed from
// the 'config.yaml' entry, which has the form:
//
//	instances:
//	  - address: https://vault.example.com:8200
//	    loginPath: auth/kubernetes/login
func (d *Decryptor) loadVaultLoginPaths(ctx context.Context) (map[string]string, error) {
	// vaultConfigKey is the ConfigMap data key holding the Vault/OpenBao
	// configuration consumed by loadVaultLoginPaths.
	const vaultConfigKey = "config.yaml"

	var cm corev1.ConfigMap
	if err := d.client.Get(ctx, *d.vaultConfigMap, &cm); err != nil {
		return nil, fmt.Errorf("failed to get Vault ConfigMap '%s': %w", d.vaultConfigMap, err)
	}
	data, ok := cm.Data[vaultConfigKey]
	if !ok {
		return nil, fmt.Errorf("Vault ConfigMap '%s' is missing the '%s' key",
			d.vaultConfigMap, vaultConfigKey)
	}
	var config kustomizev1.VaultConfig
	if err := yaml.Unmarshal([]byte(data), &config); err != nil {
		return nil, fmt.Errorf("failed to parse '%s' in Vault ConfigMap '%s': %w",
			vaultConfigKey, d.vaultConfigMap, err)
	}
	loginPaths := make(map[string]string, len(config.Instances))
	for _, instance := range config.Instances {
		loginPaths[instance.Address] = instance.LoginPath
	}
	return loginPaths, nil
}

// serviceAccountFromToken extracts the ServiceAccount namespace and name from
// the subject claim of a Kubernetes ServiceAccount token, which has the form
// "system:serviceaccount:<namespace>:<name>".
func serviceAccountFromToken(token string) (namespace, name string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("failed to decode token payload: %w", err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", fmt.Errorf("failed to unmarshal token payload: %w", err)
	}
	const prefix = "system:serviceaccount:"
	rest, ok := strings.CutPrefix(claims.Sub, prefix)
	if !ok {
		return "", "", fmt.Errorf("unexpected token subject '%s'", claims.Sub)
	}
	if ns, n, ok := strings.Cut(rest, ":"); ok && ns != "" && n != "" {
		return ns, n, nil
	}
	return "", "", fmt.Errorf("unexpected token subject '%s'", claims.Sub)
}

// vaultLogin authenticates with the OpenBao/Vault server at the given
// address by writing the provided Kubernetes ServiceAccount token to loginPath
// (e.g. "auth/kubernetes/login", optionally namespace-prefixed), logging in as
// role, and returns the resulting Vault token. The login path is used verbatim,
// so any JWT-backed auth method is supported. TLS and proxy settings are read
// from the standard VAULT_* environment variables, with the address overridden
// by vaultAddress.
func vaultLogin(ctx context.Context, vaultAddress, loginPath, role, jwt string) (string, error) {
	cfg := vaultapi.DefaultConfig()
	cfg.Address = vaultAddress
	client, err := vaultapi.NewClient(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create Vault client for '%s': %w", vaultAddress, err)
	}
	secret, err := client.Logical().WriteWithContext(ctx, loginPath,
		map[string]any{"role": role, "jwt": jwt})
	if err != nil {
		return "", fmt.Errorf("failed to authenticate with Vault '%s' at login path '%s' as role '%s': %w",
			vaultAddress, loginPath, role, err)
	}
	if secret == nil || secret.Auth == nil || secret.Auth.ClientToken == "" {
		return "", fmt.Errorf("Vault '%s' returned no token for login at '%s' as role '%s'", vaultAddress, loginPath, role)
	}
	return secret.Auth.ClientToken, nil
}
