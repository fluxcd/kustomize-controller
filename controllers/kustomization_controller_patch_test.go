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
	"time"

	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
)

const timeout = 10 * time.Second

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

	Context("Kustomize patches", func() {
		var (
			namespace        *corev1.Namespace
			kubeconfig       *kustomizev1.KubeConfig
			artifactFile     string
			artifactChecksum string
			artifactURL      string
			kustomization    *kustomizev1.Kustomization
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "patch-" + randStringRunes(5)
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

			artifactFile = "patch-" + randStringRunes(5)
			artifactChecksum, err = initArtifact(artifactServer, "testdata/patch", artifactFile)
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
					TargetNamespace: namespace.Name,
				},
			}
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("patches images", func() {
			kustomization.Spec.Images = []kustomize.Image{
				{
					Name:    "podinfo",
					NewName: "ghcr.io/stefanprodan/podinfo",
					NewTag:  "5.2.0",
				},
			}
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

			var deployment appsv1.Deployment
			Expect(k8sClient.Get(context.TODO(), client.ObjectKey{Name: "podinfo", Namespace: namespace.Name}, &deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("ghcr.io/stefanprodan/podinfo:5.2.0"))
		})

		It("patches as JSON", func() {
			kustomization.Spec.Patches = []kustomize.Patch{
				{
					Patch: `
- op: add
  path: /metadata/labels/patch
  value: inline-json
						`,
					Target: kustomize.Selector{
						LabelSelector: "app=podinfo",
					},
				},
			}
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

			var deployment appsv1.Deployment
			Expect(k8sClient.Get(context.TODO(), client.ObjectKey{Name: "podinfo", Namespace: namespace.Name}, &deployment)).To(Succeed())
			Expect(deployment.ObjectMeta.Labels["patch"]).To(Equal("inline-json"))
		})

		It("patches as YAML", func() {
			kustomization.Spec.Patches = []kustomize.Patch{
				{
					Patch: `
apiVersion: v1
kind: Pod
metadata:
  name: podinfo
  labels:
    patch: inline-yaml
						`,
				},
			}
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

			var deployment appsv1.Deployment
			Expect(k8sClient.Get(context.TODO(), client.ObjectKey{Name: "podinfo", Namespace: namespace.Name}, &deployment)).To(Succeed())
			Expect(deployment.ObjectMeta.Labels["patch"]).To(Equal("inline-yaml"))
		})

		It("strategic merge patches", func() {
			kustomization.Spec.PatchesStrategicMerge = []apiextensionsv1.JSON{
				{
					Raw: []byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"podinfo","labels":{"patch":"strategic-merge"}}}`),
				},
			}
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

			var deployment appsv1.Deployment
			Expect(k8sClient.Get(context.TODO(), client.ObjectKey{Name: "podinfo", Namespace: namespace.Name}, &deployment)).To(Succeed())
			Expect(deployment.ObjectMeta.Labels["patch"]).To(Equal("strategic-merge"))
		})

		It("JSON6902 patches", func() {
			kustomization.Spec.PatchesJSON6902 = []kustomize.JSON6902Patch{
				{
					Patch: []kustomize.JSON6902{
						{Op: "add", Path: "/metadata/labels/patch", Value: &apiextensionsv1.JSON{Raw: []byte(`"json6902"`)}},
						{Op: "replace", Path: "/spec/replicas", Value: &apiextensionsv1.JSON{Raw: []byte("2")}},
					},
					Target: kustomize.Selector{
						Group:   "apps",
						Version: "v1",
						Kind:    "Deployment",
						Name:    "podinfo",
					},
				},
			}
			Expect(k8sClient.Create(context.TODO(), kustomization)).To(Succeed())

			Eventually(func() bool {
				var obj kustomizev1.Kustomization
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &obj)
				return obj.Status.LastAppliedRevision == "main/"+artifactChecksum
			}, timeout, time.Second).Should(BeTrue())

			var deployment appsv1.Deployment
			Expect(k8sClient.Get(context.TODO(), client.ObjectKey{Name: "podinfo", Namespace: namespace.Name}, &deployment)).To(Succeed())
			Expect(deployment.ObjectMeta.Labels["patch"]).To(Equal("json6902"))
			Expect(*deployment.Spec.Replicas).To(Equal(int32(2)))
		})
	})
})
