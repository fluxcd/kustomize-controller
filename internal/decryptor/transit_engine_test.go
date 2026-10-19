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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cryptov1 "github.com/fluxcd/pkg/apis/crypto/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestDecryptor_SetAuthOptionsTransitEngine(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sops-identity",
			Namespace: "default",
			Annotations: map[string]string{
				cryptov1.AnnotationCredentialProvider:     cryptov1.CredentialProviderKubernetes,
				cryptov1.AnnotationCredentialType:         cryptov1.CredentialTypeJWT,
				cryptov1.AnnotationTransitEngineLoginPath: "auth/jwt/login",
				cryptov1.AnnotationTransitEngineRole:      "my-role",
			},
		},
	}

	d := &Decryptor{
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(sa).Build(),
		kustomization: &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: sa.Namespace,
			},
			Spec: kustomizev1.KustomizationSpec{
				Decryption: &kustomizev1.Decryption{
					Provider:           DecryptionProviderSOPS,
					ServiceAccountName: sa.Name,
				},
			},
		},
	}

	// The transit engine annotations enable Vault authentication without the
	// legacy --sops-vault-configmap flag.
	g.Expect(d.SetAuthOptions(context.Background())).To(Succeed())
	g.Expect(d.vaultK8sAuth).NotTo(BeNil())
	g.Expect(d.awsCredentialsProvider).NotTo(BeNil())
	g.Expect(d.azureTokenCredential).NotTo(BeNil())
	g.Expect(d.gcpTokenSource).NotTo(BeNil())
}

func TestNewVaultK8sAuthFunc(t *testing.T) {
	emptySpec := &cryptov1.Spec{
		Auth: &cryptov1.AuthSpec{
			TransitEngine: &cryptov1.TransitEngineSpec{},
		},
	}
	annotatedSpec := &cryptov1.Spec{
		Auth: &cryptov1.AuthSpec{
			TransitEngine: &cryptov1.TransitEngineSpec{
				LoginPath: "auth/jwt/login",
				Role:      "apps",
			},
		},
	}

	t.Run("not configured", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect((&Decryptor{}).newVaultK8sAuthFunc(emptySpec, nil)).To(BeNil())
	})

	t.Run("transit engine annotations", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect((&Decryptor{}).newVaultK8sAuthFunc(annotatedSpec, nil)).NotTo(BeNil())
	})

	t.Run("legacy ConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		d := &Decryptor{vaultConfigMap: &types.NamespacedName{Name: "sops-vault-config"}}
		g.Expect(d.newVaultK8sAuthFunc(emptySpec, nil)).NotTo(BeNil())
	})

	t.Run("annotations take precedence over ConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		d := &Decryptor{vaultConfigMap: &types.NamespacedName{Name: "sops-vault-config"}}
		g.Expect(d.newVaultK8sAuthFunc(annotatedSpec, nil)).NotTo(BeNil())
	})
}

func TestCacheVaultTokens(t *testing.T) {
	t.Run("caches per address", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		var calls []string
		cached := cacheVaultTokens(func(_ context.Context, vaultAddress string) (string, error) {
			calls = append(calls, vaultAddress)
			return "token-" + vaultAddress, nil
		})

		token, err := cached(ctx, "a")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(token).To(Equal("token-a"))

		token, err = cached(ctx, "a")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(token).To(Equal("token-a"))
		g.Expect(calls).To(Equal([]string{"a"}))

		_, err = cached(ctx, "b")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(calls).To(Equal([]string{"a", "b"}))
	})

	t.Run("does not cache errors", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		calls := 0
		fail := true
		cached := cacheVaultTokens(func(_ context.Context, _ string) (string, error) {
			calls++
			if fail {
				return "", errors.New("boom")
			}
			return "token", nil
		})

		_, err := cached(ctx, "a")
		g.Expect(err).To(MatchError("boom"))

		fail = false
		token, err := cached(ctx, "a")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(token).To(Equal("token"))
		g.Expect(calls).To(Equal(2))
	})
}

func TestDecryptorLoadVaultLoginPaths(t *testing.T) {
	scheme := runtime.NewScheme()
	NewWithT(t).Expect(corev1.AddToScheme(scheme)).To(Succeed())

	key := types.NamespacedName{Namespace: "flux-system", Name: "sops-vault-config"}
	configMap := func(data map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: data,
		}
	}
	newDecryptor := func(objects ...*corev1.ConfigMap) *Decryptor {
		builder := fake.NewClientBuilder().WithScheme(scheme)
		for _, cm := range objects {
			builder = builder.WithObjects(cm)
		}
		return &Decryptor{client: builder.Build(), vaultConfigMap: &key}
	}

	t.Run("valid", func(t *testing.T) {
		g := NewWithT(t)
		d := newDecryptor(configMap(map[string]string{
			"config.yaml": `instances:
  - address: https://vault-a.example.com:8200
    loginPath: auth/kubernetes/login
  - address: https://vault-b.example.com:8200
    loginPath: ns1/ns2/auth/kubernetes/login
`,
		}))

		got, err := d.loadVaultLoginPaths(context.Background())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(got).To(Equal(map[string]string{
			"https://vault-a.example.com:8200": "auth/kubernetes/login",
			"https://vault-b.example.com:8200": "ns1/ns2/auth/kubernetes/login",
		}))
	})

	t.Run("missing config key", func(t *testing.T) {
		g := NewWithT(t)
		d := newDecryptor(configMap(map[string]string{"other": "value"}))

		_, err := d.loadVaultLoginPaths(context.Background())
		g.Expect(err).To(MatchError(ContainSubstring("is missing the 'config.yaml' key")))
	})

	t.Run("invalid config", func(t *testing.T) {
		g := NewWithT(t)
		d := newDecryptor(configMap(map[string]string{"config.yaml": "instances: [unclosed"}))

		_, err := d.loadVaultLoginPaths(context.Background())
		g.Expect(err).To(MatchError(ContainSubstring("failed to parse 'config.yaml'")))
	})

	t.Run("missing ConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		d := newDecryptor()

		_, err := d.loadVaultLoginPaths(context.Background())
		g.Expect(err).To(MatchError(ContainSubstring("failed to get Vault ConfigMap")))
	})
}

func TestServiceAccountFromToken(t *testing.T) {
	jwtWithSubject := func(sub string) string {
		payload, err := json.Marshal(map[string]string{"sub": sub})
		if err != nil {
			t.Fatalf("failed to marshal claims: %v", err)
		}
		return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	}

	tests := []struct {
		name     string
		token    string
		wantNS   string
		wantName string
		wantErr  string
	}{
		{
			name:     "valid",
			token:    jwtWithSubject("system:serviceaccount:apps:sops"),
			wantNS:   "apps",
			wantName: "sops",
		},
		{
			name:    "not a JWT",
			token:   "not-a-jwt",
			wantErr: "not a JWT",
		},
		{
			name:    "invalid payload encoding",
			token:   "header.!!!.signature",
			wantErr: "failed to decode token payload",
		},
		{
			name:    "invalid payload JSON",
			token:   "header." + base64.RawURLEncoding.EncodeToString([]byte("not-json")) + ".signature",
			wantErr: "failed to unmarshal token payload",
		},
		{
			name:    "unexpected subject",
			token:   jwtWithSubject("user:foo"),
			wantErr: "unexpected token subject",
		},
		{
			name:    "empty namespace",
			token:   jwtWithSubject("system:serviceaccount::sops"),
			wantErr: "unexpected token subject",
		},
		{
			name:    "empty name",
			token:   jwtWithSubject("system:serviceaccount:apps:"),
			wantErr: "unexpected token subject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			ns, name, err := serviceAccountFromToken(tt.token)
			if tt.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ns).To(Equal(tt.wantNS))
			g.Expect(name).To(Equal(tt.wantName))
		})
	}
}

func TestVaultLogin(t *testing.T) {
	const (
		loginPath = "auth/jwt/login"
		role      = "apps"
		token     = "some-jwt"
	)

	t.Run("success", func(t *testing.T) {
		g := NewWithT(t)

		type request struct {
			path string
			body map[string]any
			err  error
		}
		requests := make(chan request, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{path: r.URL.Path}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				req.err = err
			} else {
				req.err = json.Unmarshal(body, &req.body)
			}
			requests <- req
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"auth":{"client_token":"s.token"}}`))
		}))
		defer srv.Close()

		got, err := vaultLogin(context.Background(), srv.URL, loginPath, role, token)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(got).To(Equal("s.token"))

		req := <-requests
		g.Expect(req.err).NotTo(HaveOccurred())
		g.Expect(req.path).To(Equal("/v1/" + loginPath))
		g.Expect(req.body).To(Equal(map[string]any{"role": role, "jwt": token}))
	})

	t.Run("no token", func(t *testing.T) {
		g := NewWithT(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"auth":{}}`))
		}))
		defer srv.Close()

		_, err := vaultLogin(context.Background(), srv.URL, loginPath, role, token)
		g.Expect(err).To(MatchError(ContainSubstring("returned no token")))
	})

	t.Run("server error", func(t *testing.T) {
		g := NewWithT(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		_, err := vaultLogin(context.Background(), srv.URL, loginPath, role, token)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("invalid address", func(t *testing.T) {
		g := NewWithT(t)

		_, err := vaultLogin(context.Background(), "http://[::1", loginPath, role, token)
		g.Expect(err).To(MatchError(ContainSubstring("failed to create Vault client")))
	})
}

func TestConfigMapVaultAuthFunc(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	key := types.NamespacedName{Namespace: "flux-system", Name: "sops-vault-config"}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Data: map[string]string{
			"config.yaml": `instances:
  - address: https://vault.example.com:8200
    loginPath: auth/kubernetes/login
`,
		},
	}
	d := &Decryptor{
		client:         fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build(),
		vaultConfigMap: &key,
	}
	spec := &cryptov1.Spec{Auth: &cryptov1.AuthSpec{
		TransitEngine: &cryptov1.TransitEngineSpec{},
	}}

	// An address that is not in the ConfigMap is rejected before any token is
	// issued.
	authFunc := d.newConfigMapVaultAuthFunc(spec, nil)
	_, err := authFunc(context.Background(), "https://unknown.example.com:8200")
	g.Expect(err).To(MatchError(ContainSubstring("no Vault login path configured")))
}
