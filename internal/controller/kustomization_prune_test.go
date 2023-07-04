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
				SecretRef: meta.SecretKeyReference{
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
				SecretRef: meta.SecretKeyReference{
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
				SecretRef: meta.SecretKeyReference{
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
