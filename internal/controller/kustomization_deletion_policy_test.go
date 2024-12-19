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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DeletionPolicyDelete(t *testing.T) {
	tests := []struct {
		name           string
		prune          bool
		deletionPolicy string
		wantDelete     bool
	}{
		{
			name:           "should delete when deletionPolicy overrides pruning disabled",
			prune:          false,
			deletionPolicy: kustomizev1.DeletionPolicyDelete,
			wantDelete:     true,
		},
		{
			name:           "should delete when deletionPolicy mirrors prune and pruning enabled",
			prune:          true,
			deletionPolicy: kustomizev1.DeletionPolicyMirrorPrune,
			wantDelete:     true,
		},
		{
			name:           "should orphan when deletionPolicy overrides pruning enabled",
			prune:          true,
			deletionPolicy: kustomizev1.DeletionPolicyOrphan,
			wantDelete:     false,
		},
		{
			name:           "should orphan when deletionPolicy mirrors prune and pruning disabled",
			prune:          false,
			deletionPolicy: kustomizev1.DeletionPolicyMirrorPrune,
			wantDelete:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
					Prune:           tt.prune,
					DeletionPolicy:  tt.deletionPolicy,
				},
			}

			g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

			resultK := &kustomizev1.Kustomization{}
			resultConfig := &corev1.ConfigMap{}

			g.Eventually(func() bool {
				_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
				return resultK.Status.LastAppliedRevision == revision
			}, timeout, time.Second).Should(BeTrue())

			g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: id, Namespace: id}, resultConfig)).Should(Succeed())

			g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())
			g.Eventually(func() bool {
				err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), kustomization)
				return apierrors.IsNotFound(err)
			}, timeout, time.Second).Should(BeTrue())

			if tt.wantDelete {
				err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resultConfig), resultConfig)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			} else {
				g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resultConfig), resultConfig)).Should(Succeed())
			}

		})
	}
}
