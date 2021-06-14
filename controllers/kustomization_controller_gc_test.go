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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

	Context("Garbage collection", func() {
		var (
			namespace     *corev1.Namespace
			kubeconfig    *kustomizev1.KubeConfig
			gitRepo       *sourcev1.GitRepository
			kustomization *kustomizev1.Kustomization
		)

		BeforeEach(func() {
			namespace = &corev1.Namespace{}
			namespace.Name = "gc-" + randStringRunes(5)
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

			gitRepoKey := client.ObjectKey{
				Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			gitRepo = readyGitRepository(gitRepoKey, "", "", "")

			kustomizationKey := types.NamespacedName{
				Name:      "gc-" + randStringRunes(5),
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
					Prune:           true,
				},
			}
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), namespace)).To(Succeed())
		})

		It("garbage collects deleted manifests", func() {
			configMapManifest := func(name string) string {
				return fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  value: %[1]s
`, name)
			}
			manifest := testserver.File{Name: "configmap.yaml", Body: configMapManifest("first")}
			artifact, err := artifactServer.ArtifactFromFiles([]testserver.File{manifest})
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err := artifactServer.URLForFile(artifact)
			Expect(err).ToNot(HaveOccurred())

			gitRepo.Status.Artifact.URL = artifactURL
			gitRepo.Status.Artifact.Revision = "first"

			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

			var got kustomizev1.Kustomization
			Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &got)
				c := apimeta.FindStatusCondition(got.Status.Conditions, meta.ReadyCondition)
				return c != nil && c.Reason == meta.ReconciliationSucceededReason
			}, timeout, time.Second).Should(BeTrue())

			var configMap corev1.ConfigMap
			Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: "first", Namespace: namespace.Name}, &configMap)).To(Succeed())
			Expect(configMap.Annotations[fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group)]).NotTo(BeEmpty())

			manifest.Body = configMapManifest("second")
			artifact, err = artifactServer.ArtifactFromFiles([]testserver.File{manifest})
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifact)
			Expect(err).ToNot(HaveOccurred())

			gitRepo.Status.Artifact.URL = artifactURL
			gitRepo.Status.Artifact.Revision = "second"
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &got)
				return got.Status.LastAppliedRevision == gitRepo.Status.Artifact.Revision
			}, timeout, time.Second).Should(BeTrue())
			err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "first", Namespace: namespace.Name}, &configMap)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: "second", Namespace: namespace.Name}, &configMap)).To(Succeed())
			Expect(configMap.Annotations[fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group)]).NotTo(BeEmpty())

			Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
			Eventually(func() bool {
				err = k8sClient.Get(context.Background(), client.ObjectKey{Name: kustomization.Name, Namespace: namespace.Name}, kustomization)
				return apierrors.IsNotFound(err)
			}, timeout, time.Second).Should(BeTrue())

			err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "second", Namespace: namespace.Name}, &configMap)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("skips deleted manifests labeled with prune disabled", func() {
			configMapManifest := func(name string, skip string) string {
				return fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  labels:
    kustomize.toolkit.fluxcd.io/prune: "%[2]s"
data:
  value: %[1]s
`, name, skip)
			}
			manifest := testserver.File{Name: "configmap.yaml", Body: configMapManifest("first", "disabled")}
			artifact, err := artifactServer.ArtifactFromFiles([]testserver.File{manifest})
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err := artifactServer.URLForFile(artifact)
			Expect(err).ToNot(HaveOccurred())

			gitRepo.Status.Artifact.URL = artifactURL
			gitRepo.Status.Artifact.Revision = "first"

			Expect(k8sClient.Create(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

			var got kustomizev1.Kustomization
			Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &got)
				c := apimeta.FindStatusCondition(got.Status.Conditions, meta.ReadyCondition)
				return c != nil && c.Reason == meta.ReconciliationSucceededReason
			}, timeout, time.Second).Should(BeTrue())

			var configMap corev1.ConfigMap
			Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: "first", Namespace: namespace.Name}, &configMap)).To(Succeed())
			Expect(configMap.Annotations[fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group)]).NotTo(BeEmpty())

			manifest.Body = configMapManifest("second", "enabled")
			artifact, err = artifactServer.ArtifactFromFiles([]testserver.File{manifest})
			Expect(err).ToNot(HaveOccurred())
			artifactURL, err = artifactServer.URLForFile(artifact)
			Expect(err).ToNot(HaveOccurred())

			gitRepo.Status.Artifact.URL = artifactURL
			gitRepo.Status.Artifact.Revision = "second"
			Expect(k8sClient.Status().Update(context.Background(), gitRepo)).To(Succeed())

			Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), ObjectKey(kustomization), &got)
				return got.Status.LastAppliedRevision == gitRepo.Status.Artifact.Revision
			}, timeout, time.Second).Should(BeTrue())
			err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "first", Namespace: namespace.Name}, &configMap)
			Expect(err).ToNot(HaveOccurred())

			Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
			Eventually(func() bool {
				err = k8sClient.Get(context.Background(), client.ObjectKey{Name: kustomization.Name, Namespace: namespace.Name}, kustomization)
				return apierrors.IsNotFound(err)
			}, timeout, time.Second).Should(BeTrue())

			err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "second", Namespace: namespace.Name}, &configMap)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "first", Namespace: namespace.Name}, &configMap)
			Expect(err).ToNot(HaveOccurred())
		})

		It("does not set the checksum annotation when GC is disabled", func() {
			deploymentManifest := func(namespace string) string {
				return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: %s
spec:
  selector:
    matchLabels:
      app: test-deployment
  template:
    metadata:
      labels:
        app: test-deployment
    spec:
      containers:
      - name: test
        image: podinfo
`,
					namespace)
			}

			manifests := []testserver.File{
				{
					Name: "deployment.yaml",
					Body: deploymentManifest(namespace.Name),
				},
			}
			artifact, err := artifactServer.ArtifactFromFiles(manifests)
			Expect(err).NotTo(HaveOccurred())

			url := fmt.Sprintf("%s/%s", artifactServer.URL(), artifact)

			repositoryName := types.NamespacedName{
				Name:      fmt.Sprintf("%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			repository := readyGitRepository(repositoryName, url, "v1", "")
			Expect(k8sClient.Create(context.Background(), repository)).To(Succeed())
			Expect(k8sClient.Status().Update(context.Background(), repository)).To(Succeed())
			defer k8sClient.Delete(context.Background(), repository)

			kName := types.NamespacedName{
				Name:      fmt.Sprintf("%s", randStringRunes(5)),
				Namespace: namespace.Name,
			}
			k := &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kName.Name,
					Namespace: kName.Namespace,
				},
				Spec: kustomizev1.KustomizationSpec{
					KubeConfig: kubeconfig,
					Interval:   metav1.Duration{Duration: time.Hour},
					Path:       "./",
					Prune:      false,
					SourceRef: kustomizev1.CrossNamespaceSourceReference{
						Kind: sourcev1.GitRepositoryKind,
						Name: repository.Name,
					},
					Suspend:    false,
					Timeout:    &metav1.Duration{Duration: 60 * time.Second},
					Validation: "client",
					Force:      true,
				},
			}
			Expect(k8sClient.Create(context.Background(), k)).To(Succeed())
			defer k8sClient.Delete(context.Background(), k)

			got := &kustomizev1.Kustomization{}
			Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), kName, got)
				c := apimeta.FindStatusCondition(got.Status.Conditions, meta.ReadyCondition)
				return c != nil && c.Reason == meta.ReconciliationSucceededReason
			}, timeout, time.Second).Should(BeTrue())
			Expect(got.Status.LastAppliedRevision).To(Equal("v1"))

			deployment := &appsv1.Deployment{}
			deploymentName := types.NamespacedName{Name: "test-deployment", Namespace: namespace.Name}
			Expect(k8sClient.Get(context.Background(), deploymentName, deployment)).To(Succeed())
			Expect(deployment.Annotations[fmt.Sprintf("%s/checksum", kustomizev1.GroupVersion.Group)]).To(BeEmpty())

			Expect(k8sClient.Delete(context.Background(), k)).To(Succeed())
			Eventually(func() bool {
				err = k8sClient.Get(context.Background(), kName, got)
				return apierrors.IsNotFound(err)
			}, timeout, time.Second).Should(BeTrue())

			Expect(k8sClient.Get(context.Background(), deploymentName, deployment)).To(Succeed())
		})
	})
})
