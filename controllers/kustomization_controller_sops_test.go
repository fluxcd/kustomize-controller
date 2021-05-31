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
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

var _ = Describe("KustomizationReconciler", func() {
	var (
		artifactServer *testserver.ArtifactServer
	)

	BeforeEach(func() {
		var err error
		artifactServer, err = testserver.NewTempArtifactServer()
		Expect(err).ToNot(HaveOccurred())
		artifactServer.Start()
	})

	AfterEach(func() {
		artifactServer.Stop()
		os.RemoveAll(artifactServer.Root())
	})

	Context("SOPS decryption", func() {
		var (
			namespace        *corev1.Namespace
			kubeconfig       *kustomizev1.KubeConfig
			artifactFile     string
			artifactChecksum string
			artifactURL      string
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "sops-" + randStringRunes(5)
			Expect(k8sClient.Create(context.Background(), namespace)).To(Succeed())

			kubecfgSecret, err := kubeConfigSecret()
			Expect(err).ToNot(HaveOccurred())
			kubecfgSecret.Namespace = namespace.Name
			Expect(k8sClient.Create(context.Background(), kubecfgSecret)).To(Succeed())
			kubeconfig = &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
					Name: kubecfgSecret.Name,
				},
			}

			artifactFile = "sops-test-" + randStringRunes(5)
			artifactChecksum, err = initArtifact(artifactServer, "testdata/sops", artifactFile)
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifactFile)
			Expect(err).ToNot(HaveOccurred())

			gitRepoKey := client.ObjectKey{
				Name:      fmt.Sprintf("sops-%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			gitRepo := readyGitRepository(gitRepoKey, artifactURL, "main/"+artifactChecksum, artifactChecksum)
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			pgpKey, err := ioutil.ReadFile("testdata/sops/pgp.asc")
			Expect(err).ToNot(HaveOccurred())
			ageKey, err := ioutil.ReadFile("testdata/sops/age.txt")
			Expect(err).ToNot(HaveOccurred())
			dayKey, err := ioutil.ReadFile("testdata/sops/day.txt.encrypted")
			Expect(err).ToNot(HaveOccurred())

			sopsSecretKey := types.NamespacedName{
				Name:      "sops-" + randStringRunes(5),
				Namespace: namespace.Name,
			}

			sopsEncodedSecretKey := types.NamespacedName{
				Name:      "sops-encoded-" + randStringRunes(5),
				Namespace: namespace.Name,
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

			sopsEncodedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sopsEncodedSecretKey.Name,
					Namespace: sopsEncodedSecretKey.Namespace,
				},
				StringData: map[string]string{
					// base64.StdEncoding.EncodeToString replicates kustomize.secretGenerator
					"day.dayKey": base64.StdEncoding.EncodeToString(dayKey),
				},
			}

			Expect(k8sClient.Create(context.Background(), sopsSecret)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), sopsEncodedSecret)).To(Succeed())

			kustomizationKey := types.NamespacedName{
				Name:      "sops-" + randStringRunes(5),
				Namespace: namespace.Name,
			}
			kustomization := &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kustomizationKey.Name,
					Namespace: kustomizationKey.Namespace,
				},
				Spec: kustomizev1.KustomizationSpec{
					Path:       "./",
					KubeConfig: kubeconfig,
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Name:      gitRepoKey.Name,
						Namespace: gitRepoKey.Namespace,
						Kind:      sourcev1.GitRepositoryKind,
					},
					Decryption: &kustomizev1.Decryption{
						Provider: "sops",
						SecretRef: &meta.LocalObjectReference{
							Name: sopsSecretKey.Name,
						},
					},
					TargetNamespace: namespace.Name,
				},
			}
			Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				k8sClient.Get(context.TODO(), kustomizationKey, &obj)
				return obj.Status.LastAppliedRevision == gitRepo.Status.Artifact.Revision
			}, timeout, time.Second).Should(BeTrue())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("decrypts SOPS secrets", func() {
			var pgpSecret corev1.Secret
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-pgp", Namespace: namespace.Name}, &pgpSecret)).To(Succeed())
			Expect(pgpSecret.Data["secret"]).To(Equal([]byte(`my-sops-pgp-secret`)))

			var ageSecret corev1.Secret
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-age", Namespace: namespace.Name}, &ageSecret)).To(Succeed())
			Expect(ageSecret.Data["secret"]).To(Equal([]byte(`my-sops-age-secret`)))

			var daySecret corev1.Secret
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-day", Namespace: namespace.Name}, &daySecret)).To(Succeed())
			Expect(string(daySecret.Data["secret"])).To(Equal("day=Tuesday\n"))

			var encodedSecret corev1.Secret
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: "sops-month", Namespace: namespace.Name}, &encodedSecret)).To(Succeed())
			Expect(string(encodedSecret.Data["month.yaml"])).To(Equal("month: May\n"))
		})
	})
})
