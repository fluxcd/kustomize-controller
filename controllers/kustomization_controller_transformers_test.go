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
	"os"
	"strings"
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"

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

	Context("Kustomize transformers", func() {
		var (
			namespace        *corev1.Namespace
			kubeconfig       *kustomizev1.KubeConfig
			artifactFile     string
			artifactChecksum string
			artifactURL      string
			kustomization    *kustomizev1.Kustomization
			deployNamespace  *corev1.Namespace
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "patch-" + randStringRunes(5)
			Expect(k8sClient.Create(context.Background(), namespace)).To(Succeed())

			deployNamespace = &corev1.Namespace{}
			deployNamespace.Name = "deploy-system"
			Expect(k8sClient.Create(context.Background(), deployNamespace)).To(Succeed())

			kubecfgSecret, err := kubeConfigSecret()
			Expect(err).ToNot(HaveOccurred())
			kubecfgSecret.Namespace = namespace.Name
			Expect(k8sClient.Create(context.Background(), kubecfgSecret)).To(Succeed())
			kubeconfig = &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
					Name: kubecfgSecret.Name,
				},
			}

			artifactFile = "patch-" + randStringRunes(5)
			artifactChecksum, err = initArtifact(artifactServer, "testdata/transformers", artifactFile)
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifactFile)
			Expect(err).ToNot(HaveOccurred())

			gitRepoKey := client.ObjectKey{
				Name:      fmt.Sprintf("patch-%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			gitRepo := readyGitRepository(gitRepoKey, artifactURL, "main/"+artifactChecksum, artifactChecksum)
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			kustomizationKey := types.NamespacedName{
				Name:      "patch-" + randStringRunes(5),
				Namespace: namespace.Name,
			}
			kustomization = &kustomizev1.Kustomization{
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
				},
			}

			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), deployNamespace)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("kustomize transformers", func() {
			quota := &corev1.ResourceQuota{}

			By("namespace and prefix transformers", func() {
				Expect(k8sClient.Get(context.TODO(), client.ObjectKey{
					Name:      "test-common-transform",
					Namespace: deployNamespace.Name,
				}, quota)).To(Succeed())
			})

			By("replacement transformer", func() {
				val := quota.Spec.Hard[corev1.ResourceLimitsMemory]
				Expect(val.String()).To(Equal("24Gi"))
			})

			By("annotations transformer", func() {
				Expect(quota.Annotations["test"]).To(Equal("annotations"))
			})

			By("label transformer", func() {
				Expect(quota.Labels["test"]).To(Equal("labels"))
			})

			By("configmap generator", func() {
				var configMapList corev1.ConfigMapList
				Expect(k8sClient.List(context.TODO(), &configMapList)).To(Succeed())
				Expect(checkConfigMap(&configMapList, "test-metas-transform")).To(Equal(true))
			})

			By("secret generator", func() {
				var secretList corev1.SecretList
				Expect(k8sClient.List(context.TODO(), &secretList)).To(Succeed())
				Expect(checkSecret(&secretList, "test-secret-transform")).To(Equal(true))
			})

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{
				Name:      "test-podinfo-transform",
				Namespace: deployNamespace.Name,
			}, deployment)).To(Succeed())

			By("patch6902 transformer", func() {
				Expect(deployment.Labels["patch"]).To(Equal("json6902"))
			})

			By("patches transformer", func() {
				Expect(deployment.Labels["app.kubernetes.io/version"]).To(Equal("1.21.0"))
			})

			By("patchStrategicMerge transformer", func() {
				Expect(deployment.Spec.Template.Spec.ServiceAccountName).
					To(Equal("test"))
			})

			By("image transformer", func() {
				Expect(deployment.Spec.Template.Spec.Containers[0].Image).
					To(Equal("podinfo:6.0.0"))
			})

			By("replica transformer", func() {
				Expect(int(*deployment.Spec.Replicas)).
					To(Equal(2))
			})
		})
	})

	Context("Kustomize file transformers", func() {
		var (
			namespace        *corev1.Namespace
			kubeconfig       *kustomizev1.KubeConfig
			artifactFile     string
			artifactChecksum string
			artifactURL      string
			kustomization    *kustomizev1.Kustomization
			deployNamespace  *corev1.Namespace
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "patch-" + randStringRunes(5)
			Expect(k8sClient.Create(context.Background(), namespace)).To(Succeed())

			deployNamespace = &corev1.Namespace{}
			deployNamespace.Name = "second-system"
			Expect(k8sClient.Create(context.Background(), deployNamespace)).To(Succeed())

			kubecfgSecret, err := kubeConfigSecret()
			Expect(err).ToNot(HaveOccurred())
			kubecfgSecret.Namespace = namespace.Name
			Expect(k8sClient.Create(context.Background(), kubecfgSecret)).To(Succeed())
			kubeconfig = &kustomizev1.KubeConfig{
				SecretRef: meta.LocalObjectReference{
					Name: kubecfgSecret.Name,
				},
			}

			artifactFile = "patch-" + randStringRunes(5)
			artifactChecksum, err = initArtifact(artifactServer, "testdata/file-transformer", artifactFile)
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifactFile)
			Expect(err).ToNot(HaveOccurred())

			gitRepoKey := client.ObjectKey{
				Name:      fmt.Sprintf("patch-%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			gitRepo := readyGitRepository(gitRepoKey, artifactURL, "main/"+artifactChecksum, artifactChecksum)
			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			kustomizationKey := types.NamespacedName{
				Name:      "patch-" + randStringRunes(5),
				Namespace: namespace.Name,
			}
			kustomization = &kustomizev1.Kustomization{
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
				},
			}

			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), deployNamespace)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("kustomize file transformers", func() {
			quota := &corev1.ResourceQuota{}

			By("namespace and prefix transformers", func() {
				Expect(k8sClient.Get(context.TODO(), client.ObjectKey{
					Name:      "test-common-transform",
					Namespace: deployNamespace.Name,
				}, quota)).To(Succeed())
			})

			By("replacement transformer", func() {
				val := quota.Spec.Hard[corev1.ResourceLimitsMemory]
				Expect(val.String()).To(Equal("24Gi"))
			})

			By("annotations transformer", func() {
				Expect(quota.Annotations["test"]).To(Equal("annotations"))
			})

			By("label transformer", func() {
				Expect(quota.Labels["test"]).To(Equal("labels"))
			})

			By("configmap generator", func() {
				var configMapList corev1.ConfigMapList
				Expect(k8sClient.List(context.TODO(), &configMapList)).To(Succeed())
				Expect(checkConfigMap(&configMapList, "test-metas-transform")).To(Equal(true))
			})

			By("secret generator", func() {
				var secretList corev1.SecretList
				Expect(k8sClient.List(context.TODO(), &secretList)).To(Succeed())
				Expect(checkSecret(&secretList, "test-secret-transform")).To(Equal(true))
			})

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(context.TODO(), types.NamespacedName{
				Name:      "test-podinfo-transform",
				Namespace: deployNamespace.Name,
			}, deployment)).To(Succeed())

			By("patch6902 transformer", func() {
				Expect(deployment.Labels["patch"]).To(Equal("json6902"))
			})

			By("patches transformer", func() {
				Expect(deployment.Labels["app.kubernetes.io/version"]).To(Equal("1.21.0"))
			})

			By("patchStrategicMerge transformer", func() {
				Expect(deployment.Spec.Template.Spec.ServiceAccountName).
					To(Equal("test"))
			})

			By("image transformer", func() {
				Expect(deployment.Spec.Template.Spec.Containers[0].Image).
					To(Equal("podinfo:6.0.0"))
			})

			By("replica transformer", func() {
				Expect(int(*deployment.Spec.Replicas)).
					To(Equal(2))
			})
		})
	})
})

func checkConfigMap(list *corev1.ConfigMapList, name string) bool {
	if list == nil {
		return false
	}

	for _, configMap := range list.Items {
		if strings.Contains(configMap.Name, name) {
			return true
		}
	}

	return false
}

func checkSecret(list *corev1.SecretList, name string) bool {
	if list == nil {
		return false
	}

	for _, secret := range list.Items {
		if strings.Contains(secret.Name, name) {
			return true
		}
	}

	return false
}
