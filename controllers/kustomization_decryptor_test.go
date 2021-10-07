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

package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestKustomizationReconciler_Decryptor(t *testing.T) {
	g := NewWithT(t)
	id := "sops-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactFile := "sops-" + randStringRunes(5)
	artifactChecksum, err := createArtifact(testServer, "testdata/sops", artifactFile)
	g.Expect(err).ToNot(HaveOccurred())
	artifactURL, err := testServer.URLForFile(artifactFile)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("sops-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactURL, "main/"+artifactChecksum, artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	pgpKey, err := ioutil.ReadFile("testdata/sops/pgp.asc")
	g.Expect(err).ToNot(HaveOccurred())
	ageKey, err := ioutil.ReadFile("testdata/sops/age.txt")
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
			"pgp.asc":    string(pgpKey),
			"age.agekey": string(ageKey),
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
			Interval: metav1.Duration{Duration: reconciliationInterval},
			Path:     "./",
			KubeConfig: &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
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
		var pgpSecret corev1.Secret
		g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-pgp", Namespace: id}, &pgpSecret)).To(Succeed())
		g.Expect(pgpSecret.Data["secret"]).To(Equal([]byte(`my-sops-pgp-secret`)))

		var ageSecret corev1.Secret
		g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-age", Namespace: id}, &ageSecret)).To(Succeed())
		g.Expect(ageSecret.Data["secret"]).To(Equal([]byte(`my-sops-age-secret`)))

		var daySecret corev1.Secret
		g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-day", Namespace: id}, &daySecret)).To(Succeed())
		g.Expect(string(daySecret.Data["secret"])).To(Equal("day=Tuesday\n"))

		var encodedSecret corev1.Secret
		g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-month", Namespace: id}, &encodedSecret)).To(Succeed())
		g.Expect(string(encodedSecret.Data["month.yaml"])).To(Equal("month: May\n"))
	})

}
