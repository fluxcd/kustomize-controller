package controllers

import (
	"context"
	"strings"
	"testing"

	pkgclient "github.com/fluxcd/pkg/runtime/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

func TestParseApplyError(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		filtered string
	}{
		{
			"apply",
			[]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged
ingressroute.traefik.containo.us/flux-receiver configured
service/notification-controller created
The Service "webhook-receiver" is invalid: spec.clusterIP: Invalid value: "10.200.133.61": field is immutable
`),
			`The Service "webhook-receiver" is invalid: spec.clusterIP: Invalid value: "10.200.133.61": field is immutable`,
		},
		{
			"client dry-run",
			[]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged (dry run)
ingressroute.traefik.containo.us/flux-receiver configured (dry run)
service/notification-controller created (dry run)
error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container
`),
			`error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container`,
		},
		{
			"server dry-run",
			[]byte(`
gitrepository.source.toolkit.fluxcd.io/flux-workspaces unchanged (server dry run)
ingressroute.traefik.containo.us/flux-receiver configured (server dry run)
service/notification-controller created (server dry run)
error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container
`),
			`error: error validating data: unknown field "ima  ge" in io.k8s.api.core.v1.Container`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := parseApplyError(tt.in)
			filtered = strings.TrimSpace(filtered)

			if tt.filtered != filtered {
				t.Errorf("expected %q, but actual %q", tt.filtered, filtered)
			}
		})
	}
}

func TestBuildKubectlCmdImpersonation(t *testing.T) {
	tests := []struct {
		kustomization kustomizev1.Kustomization
		imp           *KustomizeImpersonation
		contain       string
		notContain    []string
	}{
		{
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: false,
			},
			contain:    "",
			notContain: []string{"--as", "--token", "--as-group"},
		},
		{
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: kustomizev1.KustomizationSpec{
					ServiceAccountName: "test-sa",
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: false,
			},
			contain:    "--token random-token",
			notContain: []string{"--as", "--as-group"},
		},
		{
			// Use token impersonation once serviceAccountName is set and principal is unset
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: kustomizev1.KustomizationSpec{
					ServiceAccountName: "test-sa",
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: true,
			},
			contain:    "--token random-token",
			notContain: []string{"--as", "--as-group"},
		},
		{
			// Should use token impersonation if --user-impersonation is disabled
			// and both serviceAccountName and principal
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: kustomizev1.KustomizationSpec{
					ServiceAccountName: "test-sa",
					Principal: &kustomizev1.Principal{
						Kind: pkgclient.UserType,
						Name: "dev",
					},
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: false,
			},
			contain:    "--token random-token",
			notContain: []string{"--as", "--as-group"},
		},
		{
			// Should use user impersonation if --user-impersonation is enabled
			// and both serviceAccountName and principal are set
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: kustomizev1.KustomizationSpec{
					ServiceAccountName: "test-sa",
					Principal: &kustomizev1.Principal{
						Kind: pkgclient.UserType,
						Name: "dev",
					},
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: true,
			},
			contain:    "--as flux:user:test:dev",
			notContain: []string{"--token"},
		},
		{
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: kustomizev1.KustomizationSpec{
					Principal: &kustomizev1.Principal{
						Kind: pkgclient.UserType,
						Name: "dev",
					},
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: true,
			},
			contain:    "--as flux:user:test:dev",
			notContain: []string{"--token"},
		},
		{
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: true,
			},
			contain:    "--as flux:user:test:reconciler",
			notContain: []string{"--token"},
		},
		{
			kustomization: kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: kustomizev1.KustomizationSpec{
					Principal: &kustomizev1.Principal{
						Kind: pkgclient.ServiceAccountType,
						Name: "dev",
					},
				},
			},
			imp: &KustomizeImpersonation{
				enableUserImpersonation: true,
			},
			contain:    "--as system:serviceaccount:test:dev",
			notContain: []string{"--token"},
		},
	}

	for _, tt := range tests {
		ctx := context.Background()
		fakeClient := setupFakeClient()
		r := KustomizationReconciler{
			Client: fakeClient,
		}
		r.EnableUserImpersonation = tt.imp.enableUserImpersonation
		cmd, err := r.buildKubectlCmdImpersonation(ctx, tt.kustomization, tt.imp, "")
		if err != nil {
			t.Fatalf("error while building cmd for impersonation: %s", err)
		}

		if !strings.Contains(cmd, tt.contain) {
			t.Errorf("impersonation cmd %q should contain substring %q",
				cmd, tt.contain)
		}

		for _, str := range tt.notContain {
			if strings.Contains(cmd, str) {
				t.Errorf("impersonation cmd %q shouldn't contain %q",
					cmd, str)
			}
		}
	}
}

func setupFakeClient() client.Client {
	clientBuilder := fake.NewClientBuilder()
	serviceaccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sa",
		},
		Secrets: []corev1.ObjectReference{
			{
				Name: "test-sa-token",
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sa-token",
		},
		Data: map[string][]byte{
			"token": []byte("random-token"),
		},
	}
	clientBuilder.WithRuntimeObjects(serviceaccount, secret)

	return clientBuilder.Build()
}
