/*
Copyright 2025 The Flux authors

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
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

// TestKustomizationReconciler_MultiplePatchDelete tests the handling of multiple
// $patch: delete directives in strategic merge patches.
// This test ensures that the controller properly handles scenarios where multiple
// resources are deleted using a single patch specification.
func TestKustomizationReconciler_MultiplePatchDelete(t *testing.T) {
	g := NewWithT(t)
	id := "multi-patch-delete-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// Create test files with multiple ConfigMaps
	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "configmaps.yaml",
				Body: `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: ` + name + `
data:
  key: ` + data + `1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm2
  namespace: ` + name + `
data:
  key: ` + data + `2
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm3
  namespace: ` + name + `
data:
  key: ` + data + `3
`,
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      randStringRunes(5),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      "patch-delete-" + randStringRunes(5),
		Namespace: id,
	}

	t.Run("multiple patch delete in single patch should work", func(t *testing.T) {
		// This test verifies that multiple $patch: delete directives in a single patch work correctly
		// Ref: https://github.com/fluxcd/kustomize-controller/issues/1306
		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kustomizationKey.Name,
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
				Prune: true,
				Patches: []kustomize.Patch{
					{
						// Multiple $patch: delete in a single patch
						Patch: `$patch: delete
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: ` + id + `
---
$patch: delete
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm2
  namespace: ` + id + ``,
					},
				},
			},
		}

		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

		// Wait for reconciliation and check that it succeeds without panic
		g.Eventually(func() bool {
			var obj kustomizev1.Kustomization
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), &obj)
			return obj.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		// Verify that only cm3 ConfigMap exists (cm1 and cm2 should be deleted)
		var cm corev1.ConfigMap
		err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "cm1", Namespace: id}, &cm)
		g.Expect(err).To(HaveOccurred(), "cm1 should have been deleted")

		err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "cm2", Namespace: id}, &cm)
		g.Expect(err).To(HaveOccurred(), "cm2 should have been deleted")

		err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "cm3", Namespace: id}, &cm)
		g.Expect(err).NotTo(HaveOccurred(), "cm3 should still exist")

		// Cleanup
		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("multiple patch delete in separate patches should work", func(t *testing.T) {
		// This test verifies that separate patches (which was previously a workaround) still work correctly
		kustomizationSeparate := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kustomizationKey.Name + "-separate",
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
				Prune: true,
				Patches: []kustomize.Patch{
					{
						Patch: `$patch: delete
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: ` + id + ``,
					},
					{
						Patch: `$patch: delete
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm2
  namespace: ` + id + ``,
					},
				},
			},
		}

		g.Expect(k8sClient.Create(context.Background(), kustomizationSeparate)).To(Succeed())

		// Wait for successful reconciliation
		g.Eventually(func() bool {
			var obj kustomizev1.Kustomization
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomizationSeparate), &obj)
			return obj.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		// Verify that only cm3 ConfigMap exists
		var cm corev1.ConfigMap
		err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "cm1", Namespace: id}, &cm)
		g.Expect(err).To(HaveOccurred(), "cm1 should have been deleted")

		err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "cm2", Namespace: id}, &cm)
		g.Expect(err).To(HaveOccurred(), "cm2 should have been deleted")

		err = k8sClient.Get(context.Background(), client.ObjectKey{Name: "cm3", Namespace: id}, &cm)
		g.Expect(err).NotTo(HaveOccurred(), "cm3 should still exist")

		// Cleanup
		g.Expect(k8sClient.Delete(context.Background(), kustomizationSeparate)).To(Succeed())
		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomizationSeparate), kustomizationSeparate)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})
}
