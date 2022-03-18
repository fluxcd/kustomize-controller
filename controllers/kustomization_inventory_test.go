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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/cli-utils/pkg/object"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
)

func TestKustomizationReconciler_Inventory(t *testing.T) {
	g := NewWithT(t)
	id := "inv-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "config.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: "%[1]s"
data:
  key: "%[2]s"
---
apiVersion: v1
kind: Secret
metadata:
  name: "%[1]s"
stringData:
  key: "%[2]s"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("inv-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("inv-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
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
			TargetNamespace: id,
			Prune:           true,
			Timeout:         &metav1.Duration{Duration: time.Second},
			Wait:            true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		ready := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
		return ready && resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	configMap := &corev1.ConfigMap{}
	configMapName := types.NamespacedName{Name: id, Namespace: id}

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
	}
	secretName := types.NamespacedName{Name: id, Namespace: id}

	t.Run("creates resources", func(t *testing.T) {
		g.Expect(k8sClient.Get(context.Background(), secretName, secret)).To(Succeed())
		g.Expect(secret.Data["key"]).To(Equal([]byte(id)))

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		g.Expect(configMap.Data["key"]).To(Equal(id))

		g.Expect(resultK.Status.Inventory.Entries).Should(ConsistOf([]kustomizev1.ResourceRef{
			{
				ID: object.ObjMetadata{
					Namespace: id,
					Name:      id,
					GroupKind: schema.GroupKind{
						Group: "",
						Kind:  "Secret",
					},
				}.String(),
				Version: "v1",
			},
			{
				ID: object.ObjMetadata{
					Namespace: id,
					Name:      id,
					GroupKind: schema.GroupKind{
						Group: "",
						Kind:  "ConfigMap",
					},
				}.String(),
				Version: "v1",
			},
		}))
	})

	t.Run("ignores drift", func(t *testing.T) {
		testRev := revision + "-1"
		testVal := "test"

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		configMapClone := configMap.DeepCopy()
		configMapClone.Data["key"] = testVal
		configMapClone.SetAnnotations(map[string]string{
			fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
		})
		g.Expect(k8sClient.Update(context.Background(), configMapClone)).To(Succeed())

		err = applyGitRepository(repositoryName, artifact, testRev)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
			return ready && resultK.Status.LastAppliedRevision == testRev
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		g.Expect(configMap.Data["key"]).To(Equal(testVal))
	})

	t.Run("corrects drift", func(t *testing.T) {
		testRev := revision + "-2"

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		configMapClone := configMap.DeepCopy()
		configMapClone.SetAnnotations(map[string]string{
			fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): "enabled",
		})
		g.Expect(k8sClient.Update(context.Background(), configMapClone)).To(Succeed())

		err = applyGitRepository(repositoryName, artifact, testRev)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
			return ready && resultK.Status.LastAppliedRevision == testRev
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		g.Expect(configMap.Data["key"]).To(Equal(id))
	})

	t.Run("renames resources", func(t *testing.T) {
		testId := id + randStringRunes(5)
		testRev := revision + "-3"

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		configMapClone := configMap.DeepCopy()
		configMapClone.SetAnnotations(map[string]string{
			fmt.Sprintf("%s/reconcile", kustomizev1.GroupVersion.Group): kustomizev1.DisabledValue,
		})
		g.Expect(k8sClient.Update(context.Background(), configMapClone)).To(Succeed())

		artifact, err := testServer.ArtifactFromFiles(manifests(testId, id))
		g.Expect(err).NotTo(HaveOccurred())

		err = applyGitRepository(repositoryName, artifact, testRev)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
			return ready && resultK.Status.LastAppliedRevision == testRev
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(resultK.Status.Inventory.Entries).Should(ConsistOf([]kustomizev1.ResourceRef{
			{
				ID: object.ObjMetadata{
					Namespace: id,
					Name:      testId,
					GroupKind: schema.GroupKind{
						Group: "",
						Kind:  "Secret",
					},
				}.String(),
				Version: "v1",
			},
			{
				ID: object.ObjMetadata{
					Namespace: id,
					Name:      testId,
					GroupKind: schema.GroupKind{
						Group: "",
						Kind:  "ConfigMap",
					},
				}.String(),
				Version: "v1",
			},
		}))

		old := &corev1.Secret{}
		err = k8sClient.Get(context.Background(), secretName, old)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

		g.Expect(k8sClient.Get(context.Background(), configMapName, configMap)).To(Succeed())
		g.Expect(configMap.Data["key"]).To(Equal(id))
	})
}
