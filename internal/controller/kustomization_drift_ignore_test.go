/*
Copyright 2024 The Flux authors

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
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DriftIgnoreRules(t *testing.T) {
	g := NewWithT(t)
	id := "drift-ignore-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name, data string) []testserver.File {
		return []testserver.File{
			{
				Name: "configmap.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  managed-key: "%[2]s"
  ignored-key: "original"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, "v1"))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("drift-ignore-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("drift-ignore-%s", randStringRunes(5)),
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
			Force:           false,
			DriftIgnoreRules: []kustomizev1.DriftIgnoreRule{
				{
					Paths: []string{"/data/ignored-key"},
				},
			},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultCM := &corev1.ConfigMap{}

	t.Run("creates configmap with initial data", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultCM)).Should(Succeed())
		g.Expect(resultCM.Data["managed-key"]).To(Equal("v1"))
		g.Expect(resultCM.Data["ignored-key"]).To(Equal("original"))
	})

	t.Run("out-of-band change to ignored field is preserved on re-reconcile", func(t *testing.T) {
		// Modify the ignored field out-of-band.
		patch := client.RawPatch(types.MergePatchType, []byte(`{"data":{"ignored-key":"modified-externally"}}`))
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      id,
				Namespace: id,
			},
		}
		err = k8sClient.Patch(context.Background(), configMap, patch)
		g.Expect(err).NotTo(HaveOccurred())

		// Verify the out-of-band change was applied.
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultCM)).Should(Succeed())
		g.Expect(resultCM.Data["ignored-key"]).To(Equal("modified-externally"))

		// Trigger reconciliation by updating the source revision.
		artifact, err = testServer.ArtifactFromFiles(manifests(id, "v2"))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		// Wait for the new revision to be applied.
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)

		// Verify that the managed field was updated but the ignored field was preserved.
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultCM)).Should(Succeed())
		g.Expect(resultCM.Data["managed-key"]).To(Equal("v2"))
		g.Expect(resultCM.Data["ignored-key"]).To(Equal("modified-externally"))
	})

	t.Run("non-ignored field change is reverted on re-reconcile", func(t *testing.T) {
		// Modify the managed field out-of-band.
		patch := client.RawPatch(types.MergePatchType, []byte(`{"data":{"managed-key":"tampered"}}`))
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      id,
				Namespace: id,
			},
		}
		err = k8sClient.Patch(context.Background(), configMap, patch)
		g.Expect(err).NotTo(HaveOccurred())

		// Trigger reconciliation by updating the source.
		artifact, err = testServer.ArtifactFromFiles(manifests(id, "v3"))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v3.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)

		// The managed field should be restored, the ignored field should still be preserved.
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultCM)).Should(Succeed())
		g.Expect(resultCM.Data["managed-key"]).To(Equal("v3"))
		g.Expect(resultCM.Data["ignored-key"]).To(Equal("modified-externally"))
	})
}
