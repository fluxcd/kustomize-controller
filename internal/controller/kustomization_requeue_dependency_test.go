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

	"github.com/fluxcd/pkg/testserver"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_RequeueOnMissingRefs(t *testing.T) {
	g := NewWithT(t)
	id := "missing-refs-" + randStringRunes(5)
	revision := "v1.0.0/" + randStringRunes(7)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	t.Run("requeues at dependency interval when source is not found", func(t *testing.T) {
		g := NewWithT(t)

		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-source-" + randStringRunes(5),
				Namespace: id,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: 10 * time.Minute},
				Path:     "./",
				Prune:    true,
				SourceRef: kustomizev1.CrossNamespaceSourceReference{
					Name:      "does-not-exist",
					Namespace: id,
					Kind:      sourcev1.GitRepositoryKind,
				},
			},
		}
		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

		g.Eventually(func(g Gomega) {
			res, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(kustomization),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(res.RequeueAfter).To(Equal(reconciler.DependencyRequeueInterval))
		}, timeout, time.Second).Should(Succeed())
	})

	t.Run("requeues at dependency interval when substituteFrom is not found", func(t *testing.T) {
		g := NewWithT(t)

		manifests := []testserver.File{
			{
				Name: "configmap.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  key: value
`, id),
			},
		}
		artifact, err := testServer.ArtifactFromFiles(manifests)
		g.Expect(err).NotTo(HaveOccurred())

		repositoryName := types.NamespacedName{
			Name:      randStringRunes(5),
			Namespace: id,
		}
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		kustomization := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-substitute-" + randStringRunes(5),
				Namespace: id,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: 10 * time.Minute},
				Path:     "./",
				Prune:    true,
				SourceRef: kustomizev1.CrossNamespaceSourceReference{
					Name:      repositoryName.Name,
					Namespace: repositoryName.Namespace,
					Kind:      sourcev1.GitRepositoryKind,
				},
				PostBuild: &kustomizev1.PostBuild{
					SubstituteFrom: []kustomizev1.SubstituteReference{
						{
							Kind: "ConfigMap",
							Name: "does-not-exist",
						},
					},
				},
				TargetNamespace: id,
			},
		}
		g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

		g.Eventually(func(g Gomega) {
			res, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(kustomization),
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(res.RequeueAfter).To(Equal(reconciler.DependencyRequeueInterval))
		}, timeout, time.Second).Should(Succeed())
	})
}
