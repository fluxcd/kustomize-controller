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
	"strings"
	"testing"
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DependsOn(t *testing.T) {
	g := NewWithT(t)
	id := "dep-" + randStringRunes(5)
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
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: "v2-%[1]s"
  namespace: "%[2]s"
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: test
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
  - type: Pods
    pods:
      metric:
        name: packets-per-second
      target:
        type: AverageValue
        averageValue: 1k
  - type: Object
    object:
      metric:
        name: requests-per-second
      describedObject:
        apiVersion: networking.k8s.io/v1beta1
        kind: Ingress
        name: main-route
      target:
        type: Value
        value: 10k
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("dep-%s", randStringRunes(5)),
		Namespace: id,
	}

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("dep-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: time.Hour},
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
		return conditions.Has(resultK, meta.ReadyCondition)
	}, timeout, time.Second).Should(BeTrue())

	t.Run("fails due to source not found", func(t *testing.T) {
		g := NewWithT(t)
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return conditions.HasAnyReason(resultK, meta.ReadyCondition, meta.ArtifactFailedReason)
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("reconciles when source is found", func(t *testing.T) {
		g := NewWithT(t)
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return conditions.IsReady(resultK)
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("fails due to dependency not found", func(t *testing.T) {
		g := NewWithT(t)
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.DependsOn = []kustomizev1.DependencyReference{
				{
					Namespace: id,
					Name:      "root",
				},
			}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return conditions.HasAnyReason(resultK, meta.ReadyCondition, meta.DependencyNotReadyReason)
		}, timeout, time.Second).Should(BeTrue())
	})
}

func TestKustomizationReconciler_DependsOn_CEL(t *testing.T) {
	g := NewWithT(t)
	id := "dep-cel" + randStringRunes(5)
	depID := "test-dep-" + randStringRunes(5)
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
		Name:      fmt.Sprintf("dep-%s", randStringRunes(5)),
		Namespace: id,
	}
	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("dep-%s", randStringRunes(5)),
		Namespace: id,
	}
	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kustomizationKey.Name,
			Namespace: kustomizationKey.Namespace,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: time.Hour},
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
		return conditions.Has(resultK, meta.ReadyCondition)
	}, timeout, time.Second).Should(BeTrue())

	t.Run("succeeds with readyExpr dependency check", func(t *testing.T) {
		g := NewWithT(t)

		// Create a dependency Kustomization with matching annotations
		dependency := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      depID,
				Namespace: id,
				Annotations: map[string]string{
					"app/version": "v1.2.3",
				},
			},
			Spec: kustomizev1.KustomizationSpec{
				Suspend:  true, // Suspended dependency should work with readyExpr and AdditiveCELDependencyCheck disabled
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
			},
		}

		g.Expect(k8sClient.Create(context.Background(), dependency)).To(Succeed())

		// Update the main Kustomization with matching annotations and readyExpr
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.ObjectMeta.Annotations = map[string]string{
				"app/version": "v1.2.3",
			}
			resultK.Spec.DependsOn = []kustomizev1.DependencyReference{
				{
					Name:      dependency.Name,
					ReadyExpr: `self.metadata.annotations['app/version'] == dep.metadata.annotations['app/version']`,
				},
			}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		// Should succeed because CEL expression evaluates to true
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return conditions.IsReady(resultK)
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("fails with readyExpr when condition not met", func(t *testing.T) {
		g := NewWithT(t)

		// Update the main kustomization with mismatched annotations
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.ObjectMeta.Annotations = map[string]string{
				"app/version": "v1.2.4",
			}
			resultK.Spec.DependsOn = []kustomizev1.DependencyReference{
				{
					Namespace: id,
					Name:      depID,
					ReadyExpr: `self.metadata.annotations['app/version'] == dep.metadata.annotations['app/version']`,
				},
			}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		// Should fail because CEL expression evaluates to false
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := conditions.Get(resultK, meta.ReadyCondition)
			return ready.Reason == meta.DependencyNotReadyReason &&
				strings.Contains(ready.Message, "not ready according to readyExpr")
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.IsStalled(resultK)).Should(BeFalse())
	})

	t.Run("fails terminally with invalid readyExpr", func(t *testing.T) {
		g := NewWithT(t)

		// Update the main kustomization with invalid CEL expression
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.DependsOn = []kustomizev1.DependencyReference{
				{
					Name:      depID,
					ReadyExpr: `self.generation == deps.generation`, // Invalid vars
				},
			}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		// Should be marked as stalled because CEL expression is invalid
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return conditions.IsStalled(resultK)
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.IsReady(resultK)).Should(BeFalse())
		g.Expect(conditions.GetReason(resultK, meta.ReadyCondition)).Should(BeIdenticalTo(meta.InvalidCELExpressionReason))
		g.Expect(conditions.GetMessage(resultK, meta.ReadyCondition)).Should(ContainSubstring("failed to parse"))
	})

	t.Run("GC works with failing dependency", func(t *testing.T) {
		g := NewWithT(t)

		g.Expect(k8sClient.Delete(context.Background(), kustomization)).To(Succeed())

		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return errors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})
}
