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

	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_WaitConditions(t *testing.T) {
	g := NewWithT(t)
	id := "wait-" + randStringRunes(5)
	revision := "v1.0.0"
	resultK := &kustomizev1.Kustomization{}
	reconcileRequestAt := metav1.Now().String()

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
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: aws-efs
  namespace: was-system
provisioner: aws-efs
parameters:
  gidMin: "2200"
  gidMax: "2299"
  gidAllocate: "true"
`, name, data),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("wait-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
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
			Timeout:         &metav1.Duration{Duration: time.Second},
			Wait:            true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	t.Run("reports healthy status", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return isReconcileSuccess(resultK)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		g.Expect(conditions.IsTrue(resultK, kustomizev1.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, kustomizev1.HealthyCondition)).To(BeIdenticalTo(meta.SucceededReason))

		g.Expect(resultK.Status.ObservedGeneration).To(BeIdenticalTo(resultK.Generation))

		kstatusCheck.CheckErr(ctx, resultK)
	})

	t.Run("reports progressing status", func(t *testing.T) {
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.SetAnnotations(map[string]string{
				meta.ReconcileRequestAnnotation: reconcileRequestAt,
			})
			resultK.Spec.Wait = false
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

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return isReconcileRunning(resultK) && conditions.IsUnknown(resultK, kustomizev1.HealthyCondition)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		expectedMessage := "Running health checks"
		for _, c := range []string{meta.ReconcilingCondition, kustomizev1.HealthyCondition} {
			g.Expect(conditions.GetReason(resultK, c)).To(BeIdenticalTo(meta.ProgressingReason))
			g.Expect(conditions.GetMessage(resultK, c)).To(ContainSubstring(expectedMessage))
			g.Expect(conditions.GetObservedGeneration(resultK, c)).To(BeIdenticalTo(resultK.Generation))
		}
		kstatusInProgressCheck.CheckErr(ctx, resultK)
	})

	t.Run("reports unhealthy status", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return isReconcileFailure(resultK)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		for _, c := range []string{kustomizev1.HealthyCondition, meta.ReadyCondition} {
			g.Expect(conditions.IsFalse(resultK, c)).To(BeTrue())
			g.Expect(conditions.GetReason(resultK, c)).To(BeIdenticalTo(kustomizev1.HealthCheckFailedReason))
			g.Expect(conditions.GetObservedGeneration(resultK, c)).To(BeIdenticalTo(resultK.Generation))
		}

		expectedMessage := "Running health checks"
		g.Expect(conditions.GetReason(resultK, meta.ReconcilingCondition)).To(BeIdenticalTo(kustomizev1.ProgressingWithRetryReason))
		g.Expect(conditions.GetMessage(resultK, meta.ReconcilingCondition)).To(ContainSubstring(expectedMessage))

		g.Expect(resultK.Status.LastHandledReconcileAt).To(BeIdenticalTo(reconcileRequestAt))
		g.Expect(resultK.Status.ObservedGeneration).To(BeIdenticalTo(resultK.Generation - 1))

		kstatusCheck.CheckErr(ctx, resultK)
	})

	t.Run("emits unhealthy event", func(t *testing.T) {
		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events) > 0).To(BeTrue())
		g.Expect(events[len(events)-1].Type).To(BeIdenticalTo("Warning"))
		g.Expect(events[len(events)-1].Message).To(ContainSubstring("does-not-exists"))
	})

	t.Run("recovers and reports healthy status", func(t *testing.T) {
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.Wait = true
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return isReconcileSuccess(resultK)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		expectedMessage := "Health check passed"
		g.Expect(conditions.IsTrue(resultK, kustomizev1.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, kustomizev1.HealthyCondition)).To(BeIdenticalTo(meta.SucceededReason))
		g.Expect(conditions.GetObservedGeneration(resultK, kustomizev1.HealthyCondition)).To(BeIdenticalTo(resultK.Generation))
		g.Expect(conditions.GetMessage(resultK, kustomizev1.HealthyCondition)).To(ContainSubstring(expectedMessage))

		g.Expect(conditions.IsTrue(resultK, meta.ReadyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, meta.ReadyCondition)).To(BeIdenticalTo(kustomizev1.ReconciliationSucceededReason))
		g.Expect(conditions.GetObservedGeneration(resultK, meta.ReadyCondition)).To(BeIdenticalTo(resultK.Generation))
		g.Expect(conditions.GetMessage(resultK, meta.ReadyCondition)).To(BeIdenticalTo(fmt.Sprintf("Applied revision: %s", revision)))

		g.Expect(resultK.Status.ObservedGeneration).To(BeIdenticalTo(resultK.Generation))

		kstatusCheck.CheckErr(ctx, resultK)
	})

	t.Run("emits recovery event", func(t *testing.T) {
		expectedMessage := "Health check passed"
		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events) > 1).To(BeTrue())
		g.Expect(events[len(events)-2].Type).To(BeIdenticalTo("Normal"))
		g.Expect(events[len(events)-2].Message).To(ContainSubstring(expectedMessage))
	})

	t.Run("reports new revision healthy status", func(t *testing.T) {
		revision = "v2.0.0"
		artifact, err = testServer.ArtifactFromFiles(manifests(id, revision))
		g.Expect(err).NotTo(HaveOccurred())
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		g.Expect(isReconcileSuccess(resultK)).To(BeTrue())
		g.Expect(conditions.IsTrue(resultK, kustomizev1.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetMessage(resultK, meta.ReadyCondition)).To(BeIdenticalTo(fmt.Sprintf("Applied revision: %s", revision)))

		g.Expect(resultK.Status.LastAttemptedRevision).To(BeIdenticalTo(resultK.Status.LastAppliedRevision))

		kstatusCheck.CheckErr(ctx, resultK)
	})

	t.Run("emits event for the new revision", func(t *testing.T) {
		expectedMessage := "Health check passed"
		events := getEvents(resultK.GetName(), map[string]string{"kustomize.toolkit.fluxcd.io/revision": revision})
		g.Expect(len(events) > 1).To(BeTrue())
		g.Expect(events[len(events)-2].Type).To(BeIdenticalTo("Normal"))
		g.Expect(events[len(events)-2].Message).To(ContainSubstring(expectedMessage))
	})

	t.Run("finalizes object", func(t *testing.T) {
		g.Expect(controllerutil.ContainsFinalizer(resultK, kustomizev1.KustomizationFinalizer)).To(BeTrue())
		g.Expect(k8sClient.Delete(context.Background(), resultK)).To(Succeed())

		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})
}
