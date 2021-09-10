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
	"testing"
	"time"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	url := fmt.Sprintf("%s/%s", testServer.URL(), artifact)

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, url, revision, "")
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      "sops-" + randStringRunes(5),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Path: "./",
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
			TargetNamespace: id,
			Prune:           true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultSecret := &corev1.Secret{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	t.Run("creates secret", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultSecret)).Should(Succeed())
	})

	t.Run("deletes stale secret", func(t *testing.T) {
		newID := randStringRunes(5)
		artifact, err := testServer.ArtifactFromFiles(manifests(newID, newID))
		g.Expect(err).NotTo(HaveOccurred())
		url := fmt.Sprintf("%s/%s", testServer.URL(), artifact)
		revision := "v2.0.0"
		err = applyGitRepository(repositoryName, url, revision, "")
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

	t.Run("handles blockOwnerDeletion", func(t *testing.T) {
		owner := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: id,
			},
		}
		g.Expect(k8sClient.Create(context.Background(), owner)).To(Succeed())

		sa := &corev1.ServiceAccount{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(owner), sa)).To(Succeed())

		blockOwnerDeletion := true
		owned := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test1",
				Namespace:   id,
				Labels:      resultSecret.GetLabels(),
				Annotations: resultSecret.GetAnnotations(),
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "v1",
						Kind:               "ServiceAccount",
						Name:               sa.Name,
						UID:                sa.UID,
						Controller:         &blockOwnerDeletion,
						BlockOwnerDeletion: &blockOwnerDeletion,
					},
				},
			},
		}
		g.Expect(k8sClient.Create(context.Background(), owned)).To(Succeed())

		blockOwnerDeletion = false
		notOwned := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test2",
				Namespace:   id,
				Labels:      resultSecret.GetLabels(),
				Annotations: resultSecret.GetAnnotations(),
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "v1",
						Kind:               "ServiceAccount",
						Name:               sa.Name,
						UID:                sa.UID,
						Controller:         &blockOwnerDeletion,
						BlockOwnerDeletion: &blockOwnerDeletion,
					},
				},
			},
		}
		g.Expect(k8sClient.Create(context.Background(), notOwned)).To(Succeed())

		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKey{Name: kustomization.Name, Namespace: id}, kustomization)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())

		cf := &corev1.ConfigMap{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(owned), cf)).To(Succeed())

		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(notOwned), cf)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("preserves objects with pruning disabled", func(t *testing.T) {
		cf := &corev1.ConfigMap{}
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, cf)).Should(Succeed())
	})
}
