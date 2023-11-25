/*
Copyright 2023 The Flux authors

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DisallowedManagers(t *testing.T) {
	g := NewWithT(t)
	id := "disallowed-managers-" + randStringRunes(5)
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
  key: %[2]s
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, randStringRunes(5)))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("disallowed-managers-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("disallowed-managers-%s", randStringRunes(5)),
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
			HealthChecks: []meta.NamespacedObjectKindReference{
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       id,
					Namespace:  id,
				},
			},
			TargetNamespace: id,
			Force:           false,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	initialConfigMap := &corev1.ConfigMap{}
	badConfigMap := &corev1.ConfigMap{}
	fixedConfigMap := &corev1.ConfigMap{}

	t.Run("creates configmap", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, initialConfigMap)).Should(Succeed())
		g.Expect(initialConfigMap.Data).Should(HaveKey("key"))
	})

	t.Run("update configmap with new data", func(t *testing.T) {
		configMap := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      id,
				Namespace: id,
			},
		}
		err = k8sClient.Patch(context.Background(), &configMap, client.RawPatch(types.MergePatchType, []byte(`{"data":{"bad-key":"overridden field manager"}}`)), &client.PatchOptions{FieldManager: overrideManagerName})
		g.Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Patch(context.Background(), &configMap, client.RawPatch(types.MergePatchType, []byte(`{"data":{"key2":"not overridden field manager"}}`)), &client.PatchOptions{FieldManager: "good-name"})
		g.Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(initialConfigMap), badConfigMap)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(badConfigMap.Data).Should(HaveKey("bad-key"))
		g.Expect(badConfigMap.Data).Should(HaveKey("key2"))
	})

	t.Run("bad-key should be removed from the configmap", func(t *testing.T) {
		reconciler.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: kustomizationKey,
		})
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(initialConfigMap), fixedConfigMap)
			return g.Expect(fixedConfigMap.Data).ShouldNot(HaveKey("bad-key")) && g.Expect(fixedConfigMap.Data).Should(HaveKey("key2"))
		}, timeout, time.Second).Should(BeTrue())
	})
}
