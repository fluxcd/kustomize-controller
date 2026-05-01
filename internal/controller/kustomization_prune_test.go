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
	"testing"
	"time"

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

func TestKustomizationReconciler_Prune(t *testing.T) {
	g := NewWithT(t)
	id := "gc-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "secret.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: "%[2]s"
`, name, data),
			},
			{
				Name: "config.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  labels:
    kustomize.toolkit.fluxcd.io/prune: "disabled"
data:
  key: "%[2]s"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
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
			Prune:           true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultSecret := &corev1.Secret{}
	resultConfig := &corev1.ConfigMap{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	t.Run("creates objects", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultSecret)).Should(Succeed())
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultConfig)).Should(Succeed())
	})

	t.Run("deletes stale objects", func(t *testing.T) {
		newID := randStringRunes(5)
		artifact, err := testServer.ArtifactFromFiles(manifests(newID, newID))
		g.Expect(err).NotTo(HaveOccurred())
		revision := "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: newID, Namespace: id}, resultSecret)).Should(Succeed())

		old := &corev1.Secret{}
		err = k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, old)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("handles finalizer", func(t *testing.T) {
		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())

		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resultSecret), resultSecret)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("preserves objects with pruning disabled", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resultConfig), resultConfig)).Should(Succeed())
	})
}

func TestKustomizationReconciler_PruneEmpty(t *testing.T) {
	g := NewWithT(t)
	id := "gc-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "secret.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: "%[2]s"
`, name, data),
			},
			{
				Name: "config.yaml",
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

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
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
			Prune:           true,
			Wait:            true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	g.Expect(len(resultK.Status.Inventory.Entries)).Should(BeIdenticalTo(2))

	t.Run("deletes stale objects", func(t *testing.T) {
		artifact, err := testServer.ArtifactFromFiles([]testserver.File{})
		g.Expect(err).NotTo(HaveOccurred())

		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(len(resultK.Status.Inventory.Entries)).Should(BeIdenticalTo(0))
	})

	t.Run("reconciles empty kustomization", func(t *testing.T) {
		empty := []testserver.File{
			{
				Name: "kustomization.yaml",
				Body: `---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
`,
			},
		}
		artifact, err := testServer.ArtifactFromFiles(empty)
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v3.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(len(resultK.Status.Inventory.Entries)).Should(BeIdenticalTo(0))
	})

	t.Run("restores objects", func(t *testing.T) {
		artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v4.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(len(resultK.Status.Inventory.Entries)).Should(BeIdenticalTo(2))
	})

	t.Run("deletes suspended", func(t *testing.T) {
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.Suspend = true
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})
}

func TestKustomizationReconciler_PruneSkipNotOwned(t *testing.T) {
	g := NewWithT(t)
	id := "gc-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "secret.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: "%[2]s"
`, name, data),
			},
			{
				Name: "config.yaml",
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

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
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
			Prune:           true,
			Wait:            true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultSecret := &corev1.Secret{}
	resultConfig := &corev1.ConfigMap{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	t.Run("creates objects with owner labels", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultSecret)).Should(Succeed())
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultConfig)).Should(Succeed())

		name := resultConfig.GetLabels()["kustomize.toolkit.fluxcd.io/name"]
		g.Expect(name).Should(BeIdenticalTo(kustomizationKey.Name))

		namespace := resultConfig.GetLabels()["kustomize.toolkit.fluxcd.io/namespace"]
		g.Expect(namespace).Should(BeIdenticalTo(kustomizationKey.Namespace))
	})

	resultConfig.Labels["kustomize.toolkit.fluxcd.io/name"] = "new-owner"
	g.Expect(k8sClient.Update(context.Background(), resultConfig)).Should(Succeed())

	t.Run("deletes owned objects", func(t *testing.T) {
		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())

		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resultSecret), resultSecret)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("preserves objects with different owner", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resultConfig), resultConfig)).Should(Succeed())
	})

}

// TestKustomizationReconciler_PruneExcludesSSAIgnore verifies that objects
// carrying the kustomize.toolkit.fluxcd.io/ssa: Ignore annotation are excluded
// from garbage collection in both prune paths: stale-object cleanup during
// reconciliation and finalizer cleanup on Kustomization deletion. The
// annotation declares that the controller must not modify the object, so its
// removal from the source manifest or the deletion of the Kustomization
// itself must not cause the object to be deleted.
func TestKustomizationReconciler_PruneExcludesSSAIgnore(t *testing.T) {
	g := NewWithT(t)
	id := "gc-ssa-ignore-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// v1 has both objects without the annotation so they are applied with
	// owner labels; the test then patches the ConfigMap with the ssa: Ignore
	// annotation to model a user opting out of further management.
	manifestsV1 := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "objects.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: value
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  key: value
`, name),
			},
		}
	}

	// v2 drops the ConfigMap entirely so it becomes a stale object subject
	// to garbage collection during reconciliation.
	manifestsV2 := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "objects.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: value
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifestsV1(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
		Namespace: id,
	}
	g.Expect(applyGitRepository(repositoryName, artifact, revision)).To(Succeed())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
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
			Prune:           true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	configMapKey := types.NamespacedName{Name: id, Namespace: id}
	secretKey := types.NamespacedName{Name: id, Namespace: id}

	resultConfig := &corev1.ConfigMap{}
	g.Expect(k8sClient.Get(context.Background(), configMapKey, resultConfig)).To(Succeed())

	// Patch the in-cluster ConfigMap with the ssa: Ignore annotation, modelling
	// a user opting out of further reconciliation by the controller.
	annotations := resultConfig.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["kustomize.toolkit.fluxcd.io/ssa"] = "Ignore"
	resultConfig.SetAnnotations(annotations)
	g.Expect(k8sClient.Update(context.Background(), resultConfig)).To(Succeed())

	t.Run("preserves ssa: Ignore objects on stale prune", func(t *testing.T) {
		// Push v2 which removes the ConfigMap from the source. This makes
		// the ConfigMap a stale object — without the deletion exclusion the
		// reconcile-time prune would delete it.
		artifact, err := testServer.ArtifactFromFiles(manifestsV2(id))
		g.Expect(err).NotTo(HaveOccurred())
		newRevision := "v2.0.0"
		g.Expect(applyGitRepository(repositoryName, artifact, newRevision)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == newRevision
		}, timeout, time.Second).Should(BeTrue())

		// The Secret stays because it's still in the source.
		secret := &corev1.Secret{}
		g.Expect(k8sClient.Get(context.Background(), secretKey, secret)).To(Succeed())

		// The ConfigMap is preserved despite being stale.
		got := &corev1.ConfigMap{}
		g.Expect(k8sClient.Get(context.Background(), configMapKey, got)).To(Succeed())
		g.Expect(got.GetAnnotations()).To(HaveKeyWithValue("kustomize.toolkit.fluxcd.io/ssa", "Ignore"))
	})

	t.Run("preserves ssa: Ignore objects on finalizer prune", func(t *testing.T) {
		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())

		// Secret was deleted by the finalizer prune because it was managed
		// without an opt-out annotation.
		secret := &corev1.Secret{}
		err := k8sClient.Get(context.Background(), secretKey, secret)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "managed Secret should be pruned by the finalizer")

		// ConfigMap is preserved because of the ssa: Ignore annotation.
		got := &corev1.ConfigMap{}
		g.Expect(k8sClient.Get(context.Background(), configMapKey, got)).To(Succeed())
		g.Expect(got.GetAnnotations()).To(HaveKeyWithValue("kustomize.toolkit.fluxcd.io/ssa", "Ignore"))
	})
}

// TestKustomizationReconciler_PruneIncludesSSAIfNotPresent verifies that
// objects annotated with kustomize.toolkit.fluxcd.io/ssa: IfNotPresent are
// still subject to garbage collection — both as stale objects during
// reconciliation and via the finalizer on Kustomization deletion. Unlike
// ssa: Ignore (which orphans the object), IfNotPresent declares ownership;
// the controller simply chooses not to re-apply when the object already
// exists. Lifecycle remains tied to the Kustomization.
func TestKustomizationReconciler_PruneIncludesSSAIfNotPresent(t *testing.T) {
	g := NewWithT(t)
	id := "gc-ifnotpresent-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifestsV1 := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "objects.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: value
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  annotations:
    kustomize.toolkit.fluxcd.io/ssa: IfNotPresent
data:
  key: value
`, name),
			},
		}
	}

	manifestsV2 := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "objects.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
  key: value
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifestsV1(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
		Namespace: id,
	}
	g.Expect(applyGitRepository(repositoryName, artifact, revision)).To(Succeed())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("gc-%s", randStringRunes(5)),
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
			Prune:           true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	configMapKey := types.NamespacedName{Name: id, Namespace: id}
	secretKey := types.NamespacedName{Name: id, Namespace: id}

	// First apply must create the ConfigMap: IfNotPresent only skips when the
	// object already exists. The annotation is preserved on the in-cluster
	// object.
	gotConfig := &corev1.ConfigMap{}
	g.Expect(k8sClient.Get(context.Background(), configMapKey, gotConfig)).To(Succeed())
	g.Expect(gotConfig.GetAnnotations()).To(HaveKeyWithValue("kustomize.toolkit.fluxcd.io/ssa", "IfNotPresent"))

	// Trigger a second reconciliation against the same content under a new
	// revision so that the IfNotPresent ConfigMap goes through SkippedAction.
	// This is the state in which the original regression silently dropped
	// the entry from the inventory and orphaned the object on subsequent
	// removals or finalization.
	intermediateRevision := "v1.0.1"
	g.Expect(applyGitRepository(repositoryName, artifact, intermediateRevision)).To(Succeed())
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == intermediateRevision
	}, timeout, time.Second).Should(BeTrue())

	t.Run("deletes stale ssa: IfNotPresent objects", func(t *testing.T) {
		// Push v2 which removes the ConfigMap from the source. The ConfigMap
		// becomes stale and must be garbage collected — IfNotPresent does
		// not opt out of pruning.
		artifact, err := testServer.ArtifactFromFiles(manifestsV2(id))
		g.Expect(err).NotTo(HaveOccurred())
		newRevision := "v2.0.0"
		g.Expect(applyGitRepository(repositoryName, artifact, newRevision)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == newRevision
		}, timeout, time.Second).Should(BeTrue())

		// Secret is still managed and present.
		secret := &corev1.Secret{}
		g.Expect(k8sClient.Get(context.Background(), secretKey, secret)).To(Succeed())

		// ConfigMap is deleted.
		got := &corev1.ConfigMap{}
		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), configMapKey, got)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue(), "ssa: IfNotPresent ConfigMap should be pruned when removed from source")
	})

	t.Run("deletes ssa: IfNotPresent objects via finalizer", func(t *testing.T) {
		// Re-add the ConfigMap by pushing v1 again so the finalizer has
		// something to clean up.
		artifact, err := testServer.ArtifactFromFiles(manifestsV1(id))
		g.Expect(err).NotTo(HaveOccurred())
		newRevision := "v3.0.0"
		g.Expect(applyGitRepository(repositoryName, artifact, newRevision)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == newRevision
		}, timeout, time.Second).Should(BeTrue())

		// Push the same content under another revision so the next
		// reconciliation produces a SkippedAction for the (now-existing)
		// ConfigMap. This is the state in which the original regression
		// dropped the entry from Status.Inventory, causing the finalizer to
		// orphan it on Kustomization deletion.
		intermediateRevision := "v3.0.1"
		g.Expect(applyGitRepository(repositoryName, artifact, intermediateRevision)).To(Succeed())
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == intermediateRevision
		}, timeout, time.Second).Should(BeTrue())

		// Confirm the ConfigMap exists before triggering the finalizer.
		g.Expect(k8sClient.Get(context.Background(), configMapKey, &corev1.ConfigMap{})).To(Succeed())

		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())

		// Both the Secret and the IfNotPresent-annotated ConfigMap must be
		// pruned by the finalizer.
		secret := &corev1.Secret{}
		err = k8sClient.Get(context.Background(), secretKey, secret)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

		got := &corev1.ConfigMap{}
		err = k8sClient.Get(context.Background(), configMapKey, got)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ssa: IfNotPresent ConfigMap should be pruned by the finalizer")
	})
}
