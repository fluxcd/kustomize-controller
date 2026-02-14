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

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DirectSourceFetch(t *testing.T) {
	g := NewWithT(t)
	id := "direct-fetch-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "configmap.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  key: "%[2]s"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("direct-fetch-repo-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("direct-fetch-kust-%s", randStringRunes(5)),
		Namespace: id,
	}

	t.Run("reconciles with DirectSourceFetch enabled (uses APIReader)", func(t *testing.T) {
		g := NewWithT(t)

		// Enable DirectSourceFetch to use APIReader
		reconciler.DirectSourceFetch = true
		defer func() { reconciler.DirectSourceFetch = false }() // Reset after test

		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kustomizationKey.Name + "-direct",
				Namespace: kustomizationKey.Namespace,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: reconciliationInterval},
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
				TargetNamespace: id,
			},
		}

		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		})

		resultK := &kustomizev1.Kustomization{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition) &&
				resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("source not found with DirectSourceFetch enabled", func(t *testing.T) {
		g := NewWithT(t)

		// Enable DirectSourceFetch to use APIReader
		reconciler.DirectSourceFetch = true
		defer func() { reconciler.DirectSourceFetch = false }() // Reset after test

		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kustomizationKey.Name + "-notfound",
				Namespace: kustomizationKey.Namespace,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: reconciliationInterval},
				Path:     "./",
				KubeConfig: &meta.KubeConfigReference{
					SecretRef: &meta.SecretKeyReference{
						Name: "kubeconfig",
					},
				},
				SourceRef: kustomizev1.CrossNamespaceSourceReference{
					Name:      "non-existent-repo",
					Namespace: id,
					Kind:      sourcev1.GitRepositoryKind,
				},
				TargetNamespace: id,
			},
		}

		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		})

		resultK := &kustomizev1.Kustomization{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			if ready == nil {
				return false
			}
			return ready.Status == metav1.ConditionFalse &&
				ready.Reason == meta.ArtifactFailedReason
		}, timeout, time.Second).Should(BeTrue())
	})
}

func TestKustomizationReconciler_DirectSourceFetch_OCIRepository(t *testing.T) {
	g := NewWithT(t)
	id := "direct-fetch-oci-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	t.Run("handles OCIRepository with DirectSourceFetch enabled", func(t *testing.T) {
		g := NewWithT(t)

		// Enable DirectSourceFetch to use APIReader
		reconciler.DirectSourceFetch = true
		defer func() { reconciler.DirectSourceFetch = false }() // Reset after test

		// Create an OCIRepository (without artifact - just to test source fetching)
		ociRepoName := types.NamespacedName{
			Name:      fmt.Sprintf("oci-repo-%s", randStringRunes(5)),
			Namespace: id,
		}

		ociRepo := &sourcev1.OCIRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ociRepoName.Name,
				Namespace: ociRepoName.Namespace,
			},
			Spec: sourcev1.OCIRepositorySpec{
				URL:      "oci://ghcr.io/test/repo",
				Interval: metav1.Duration{Duration: time.Minute},
			},
		}
		g.Expect(k8sClient.Create(context.Background(), ociRepo)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(k8sClient.Delete(context.Background(), ociRepo)).To(Succeed())
		})

		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("kust-oci-%s", randStringRunes(5)),
				Namespace: id,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: reconciliationInterval},
				Path:     "./",
				KubeConfig: &meta.KubeConfigReference{
					SecretRef: &meta.SecretKeyReference{
						Name: "kubeconfig",
					},
				},
				SourceRef: kustomizev1.CrossNamespaceSourceReference{
					Name:      ociRepoName.Name,
					Namespace: ociRepoName.Namespace,
					Kind:      sourcev1.OCIRepositoryKind,
				},
				TargetNamespace: id,
			},
		}

		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		})

		// The kustomization should be able to find the source (even though it has no artifact)
		// and eventually report that the artifact is not ready
		resultK := &kustomizev1.Kustomization{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			// Source should be found but artifact should not be ready
			return len(resultK.Status.Conditions) > 0
		}, timeout, time.Second).Should(BeTrue())
	})
}

func TestKustomizationReconciler_DirectSourceFetch_Bucket(t *testing.T) {
	g := NewWithT(t)
	id := "direct-fetch-bucket-" + randStringRunes(5)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	t.Run("handles Bucket with DirectSourceFetch enabled", func(t *testing.T) {
		g := NewWithT(t)

		// Enable DirectSourceFetch to use APIReader
		reconciler.DirectSourceFetch = true
		defer func() { reconciler.DirectSourceFetch = false }() // Reset after test

		// Create a Bucket source (without artifact - just to test source fetching)
		bucketName := types.NamespacedName{
			Name:      fmt.Sprintf("bucket-%s", randStringRunes(5)),
			Namespace: id,
		}

		bucket := &sourcev1.Bucket{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucketName.Name,
				Namespace: bucketName.Namespace,
			},
			Spec: sourcev1.BucketSpec{
				BucketName: "test-bucket",
				Endpoint:   "s3.amazonaws.com",
				Interval:   metav1.Duration{Duration: time.Minute},
			},
		}
		g.Expect(k8sClient.Create(context.Background(), bucket)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(k8sClient.Delete(context.Background(), bucket)).To(Succeed())
		})

		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("kust-bucket-%s", randStringRunes(5)),
				Namespace: id,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: reconciliationInterval},
				Path:     "./",
				KubeConfig: &meta.KubeConfigReference{
					SecretRef: &meta.SecretKeyReference{
						Name: "kubeconfig",
					},
				},
				SourceRef: kustomizev1.CrossNamespaceSourceReference{
					Name:      bucketName.Name,
					Namespace: bucketName.Namespace,
					Kind:      sourcev1.BucketKind,
				},
				TargetNamespace: id,
			},
		}

		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())
		t.Cleanup(func() {
			g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		})

		// The kustomization should be able to find the source (even though it has no artifact)
		resultK := &kustomizev1.Kustomization{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			// Source should be found but artifact should not be ready
			return len(resultK.Status.Conditions) > 0
		}, timeout, time.Second).Should(BeTrue())
	})
}
