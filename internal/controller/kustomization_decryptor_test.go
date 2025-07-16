/*
Copyright 2021 The Flux authors

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

package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/hashicorp/vault/api"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_Decryptor(t *testing.T) {
	g := NewWithT(t)

	cli, err := api.NewClient(api.DefaultConfig())
	g.Expect(err).NotTo(HaveOccurred(), "failed to create vault client")

	// create a master key on the vault transit engine
	path, data := "sops/keys/vault", map[string]interface{}{"type": "rsa-4096"}
	_, err = cli.Logical().Write(path, data)
	g.Expect(err).NotTo(HaveOccurred(), "failed to write key")

	// encrypt the testdata vault secret
	cmd := exec.Command("sops", "--hc-vault-transit", cli.Address()+"/v1/sops/keys/vault", "--encrypt", "--encrypted-regex", "^(data|stringData)$", "--in-place", "./testdata/sops/algorithms/vault.yaml")
	err = cmd.Run()
	g.Expect(err).NotTo(HaveOccurred(), "failed to encrypt file")

	// defer the testdata vault secret decryption, to leave a clean testdata vault secret
	defer func() {
		cmd := exec.Command("sops", "--hc-vault-transit", cli.Address()+"/v1/sops/keys/firstkey", "--decrypt", "--encrypted-regex", "^(data|stringData)$", "--in-place", "./testdata/sops/algorithms/vault.yaml")
		err = cmd.Run()
	}()

	id := "sops-" + randStringRunes(5)

	err = createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactName := "sops-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata/sops", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("sops-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	pgpKey, err := os.ReadFile("testdata/sops/keys/pgp.asc")
	g.Expect(err).ToNot(HaveOccurred())
	ageKey, err := os.ReadFile("testdata/sops/keys/age.txt")
	g.Expect(err).ToNot(HaveOccurred())

	sopsSecretKey := types.NamespacedName{
		Name:      "sops-" + randStringRunes(5),
		Namespace: id,
	}
	sopsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sopsSecretKey.Name,
			Namespace: sopsSecretKey.Namespace,
		},
		StringData: map[string]string{
			"pgp.asc":          string(pgpKey),
			"age.agekey":       string(ageKey),
			"sops.vault-token": "secret",
		},
	}

	g.Expect(k8sClient.Create(context.Background(), sopsSecret)).To(Succeed())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("sops-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
			Path:     "./",
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
					Name: "kubeconfig",
				},
			},
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Name:      repositoryName.Name,
				Namespace: repositoryName.Namespace,
				Kind:      sourcev1.GitRepositoryKind,
			},
			Decryption: &kustomizev1.Decryption{
				Provider: "sops",
				SecretRef: &meta.LocalObjectReference{
					Name: sopsSecretKey.Name,
				},
			},
			TargetNamespace: id,
		},
	}
	g.Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		var obj kustomizev1.Kustomization
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
		return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	t.Run("decrypts SOPS secrets", func(t *testing.T) {
		g := NewWithT(t)

		secretNames := []string{
			"sops-algo-age",
			"sops-algo-pgp",
			"sops-algo-vault",
			"sops-component",
			"sops-envs-secret",
			"sops-files-secret",
			"sops-inside-secret",
			"sops-remote-secret",
		}
		for _, name := range secretNames {
			var secret corev1.Secret
			g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: id}, &secret)).To(Succeed())
			g.Expect(string(secret.Data["key"])).To(Equal("value"), fmt.Sprintf("failed on secret %s", name))
		}

		configMapNames := []string{
			"sops-envs-configmap",
			"sops-files-configmap",
			"sops-remote-configmap",
		}
		for _, name := range configMapNames {
			var configMap corev1.ConfigMap
			g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: id}, &configMap)).To(Succeed())
			g.Expect(string(configMap.Data["key"])).To(Equal("value"), fmt.Sprintf("failed on configmap %s", name))
		}

		var patchedSecret corev1.Secret
		g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-patches-secret", Namespace: id}, &patchedSecret)).To(Succeed())
		g.Expect(string(patchedSecret.Data["key"])).To(Equal("merge1"))
		g.Expect(string(patchedSecret.Data["merge2"])).To(Equal("merge2"))
	})

	t.Run("does not emit change events for identical secrets", func(t *testing.T) {
		g := NewWithT(t)

		resultK := &kustomizev1.Kustomization{}
		revision := "v2.0.0"
		err = applyGitRepository(repositoryName, artifactName, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events)).To(BeIdenticalTo(1))
		g.Expect(events[0].Message).Should(ContainSubstring("Reconciliation finished"))
		g.Expect(events[0].Message).ShouldNot(ContainSubstring("configured"))
	})
}
