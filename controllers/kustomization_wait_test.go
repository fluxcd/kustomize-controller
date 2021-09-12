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

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
)

func TestKustomizationReconciler_HealthCheck(t *testing.T) {
	g := NewWithT(t)
	id := "wait-" + randStringRunes(5)
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

	url := fmt.Sprintf("%s/%s", testServer.URL(), artifact)

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("wait-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, url, revision, "")
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("wait-%s", randStringRunes(5)),
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
			HealthChecks: []meta.NamespacedObjectKindReference{
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       id,
					Namespace:  id,
				},
			},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	t.Run("reports healthy status", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
			healthy := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, kustomizev1.HealthyCondition)
			return ready && healthy
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("reports unhealthy status", func(t *testing.T) {
		reconcileRequestAt := metav1.Now().String()
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.SetAnnotations(map[string]string{
				meta.ReconcileRequestAnnotation: reconcileRequestAt,
			})
			resultK.Spec.HealthChecks = []meta.NamespacedObjectKindReference{
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "does-not-exists",
					Namespace:  id,
				},
			}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		readyCondition := &metav1.Condition{}
		healthyCondition := &metav1.Condition{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			healthyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, kustomizev1.HealthyCondition)
			return healthyCondition.Reason == meta.ProgressingReason
		}, timeout, time.Second).Should(BeTrue())

		expectedMessage := "running health checks"
		g.Expect(readyCondition.Status).To(BeIdenticalTo(metav1.ConditionUnknown))
		g.Expect(readyCondition.Message).To(ContainSubstring(expectedMessage))
		g.Expect(healthyCondition.Status).To(BeIdenticalTo(metav1.ConditionUnknown))
		g.Expect(healthyCondition.Message).To(ContainSubstring(expectedMessage))

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			readyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, meta.ReadyCondition)
			healthyCondition = apimeta.FindStatusCondition(resultK.Status.Conditions, kustomizev1.HealthyCondition)
			return healthyCondition.Reason == kustomizev1.HealthCheckFailedReason
		}, time.Minute, time.Second).Should(BeTrue())

		g.Expect(resultK.Status.LastHandledReconcileAt).To(BeIdenticalTo(reconcileRequestAt))
		g.Expect(readyCondition.Status).To(BeIdenticalTo(metav1.ConditionFalse))
		g.Expect(healthyCondition.Status).To(BeIdenticalTo(metav1.ConditionFalse))
		g.Expect(healthyCondition.Message).To(BeIdenticalTo(kustomizev1.HealthCheckFailedReason))
	})

	t.Run("emits unhealthy event", func(t *testing.T) {
		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events) > 0).To(BeTrue())
		g.Expect(events[len(events)-1].Type).To(BeIdenticalTo("Warning"))
		g.Expect(events[len(events)-1].Message).To(ContainSubstring("NotFound"))
	})

	t.Run("recovers and reports healthy status", func(t *testing.T) {
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.HealthChecks = []meta.NamespacedObjectKindReference{
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       id,
					Namespace:  id,
				},
			}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			ready := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, meta.ReadyCondition)
			healthy := apimeta.IsStatusConditionTrue(resultK.Status.Conditions, kustomizev1.HealthyCondition)
			return ready && healthy
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("emits recovery event", func(t *testing.T) {
		expectedMessage := "Health check passed"
		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events) > 1).To(BeTrue())
		g.Expect(events[len(events)-2].Type).To(BeIdenticalTo("Normal"))
		g.Expect(events[len(events)-2].Message).To(ContainSubstring(expectedMessage))
	})
}
