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

	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_WaitConditions(t *testing.T) {
	g := NewWithT(t)
	id := "wait-" + randStringRunes(5)
	revision := "v1.0.0"
	resultK := &kustomizev1.Kustomization{}
	reconcileRequestAt := metav1.Now().String()
	timeout := 60 * time.Second

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

		g.Expect(conditions.IsTrue(resultK, meta.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, meta.HealthyCondition)).To(BeIdenticalTo(meta.SucceededReason))

		g.Expect(resultK.Status.ObservedGeneration).To(BeIdenticalTo(resultK.Generation))

		kstatusCheck.CheckErr(ctx, resultK)

		g.Expect(resultK.Status.History).To(HaveLen(1))
		g.Expect(resultK.Status.History[0].TotalReconciliations).To(BeEquivalentTo(1))
		g.Expect(resultK.Status.History[0].LastReconciledStatus).To(Equal(meta.ReconciliationSucceededReason))
		g.Expect(resultK.Status.History[0].Metadata).To(ContainElements(revision))
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
			return isReconcileRunning(resultK) && conditions.IsUnknown(resultK, meta.HealthyCondition)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		expectedMessage := "Running health checks"
		for _, c := range []string{meta.ReconcilingCondition, meta.HealthyCondition} {
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

		for _, c := range []string{meta.HealthyCondition, meta.ReadyCondition} {
			g.Expect(conditions.IsFalse(resultK, c)).To(BeTrue())
			g.Expect(conditions.GetReason(resultK, c)).To(BeIdenticalTo(meta.HealthCheckFailedReason))
			g.Expect(conditions.GetObservedGeneration(resultK, c)).To(BeIdenticalTo(resultK.Generation))
		}

		expectedMessage := "Running health checks"
		g.Expect(conditions.GetReason(resultK, meta.ReconcilingCondition)).To(BeIdenticalTo(meta.ProgressingWithRetryReason))
		g.Expect(conditions.GetMessage(resultK, meta.ReconcilingCondition)).To(ContainSubstring(expectedMessage))

		g.Expect(resultK.Status.LastHandledReconcileAt).To(BeIdenticalTo(reconcileRequestAt))
		g.Expect(resultK.Status.ObservedGeneration).To(BeIdenticalTo(resultK.Generation - 1))

		kstatusCheck.CheckErr(ctx, resultK)

		g.Expect(resultK.Status.History).To(HaveLen(2))
		g.Expect(resultK.Status.History[0].TotalReconciliations).To(BeEquivalentTo(1))
		g.Expect(resultK.Status.History[0].LastReconciledStatus).To(Equal(meta.HealthCheckFailedReason))
		g.Expect(resultK.Status.History[0].Metadata).To(ContainElements(revision))
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
		g.Expect(conditions.IsTrue(resultK, meta.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, meta.HealthyCondition)).To(BeIdenticalTo(meta.SucceededReason))
		g.Expect(conditions.GetObservedGeneration(resultK, meta.HealthyCondition)).To(BeIdenticalTo(resultK.Generation))
		g.Expect(conditions.GetMessage(resultK, meta.HealthyCondition)).To(ContainSubstring(expectedMessage))

		g.Expect(conditions.IsTrue(resultK, meta.ReadyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, meta.ReadyCondition)).To(BeIdenticalTo(meta.ReconciliationSucceededReason))
		g.Expect(conditions.GetObservedGeneration(resultK, meta.ReadyCondition)).To(BeIdenticalTo(resultK.Generation))
		g.Expect(conditions.GetMessage(resultK, meta.ReadyCondition)).To(BeIdenticalTo(fmt.Sprintf("Applied revision: %s", revision)))

		g.Expect(resultK.Status.ObservedGeneration).To(BeIdenticalTo(resultK.Generation))

		kstatusCheck.CheckErr(ctx, resultK)

		g.Expect(resultK.Status.History).To(HaveLen(2))
		g.Expect(resultK.Status.History[0].TotalReconciliations).To(BeEquivalentTo(2))
		g.Expect(resultK.Status.History[0].LastReconciledStatus).To(Equal(meta.ReconciliationSucceededReason))
		g.Expect(resultK.Status.History[0].Metadata).To(ContainElements(revision))
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
		g.Expect(conditions.IsTrue(resultK, meta.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetMessage(resultK, meta.ReadyCondition)).To(BeIdenticalTo(fmt.Sprintf("Applied revision: %s", revision)))

		g.Expect(resultK.Status.LastAttemptedRevision).To(BeIdenticalTo(resultK.Status.LastAppliedRevision))

		kstatusCheck.CheckErr(ctx, resultK)

		g.Expect(resultK.Status.History).To(HaveLen(3))
		g.Expect(resultK.Status.History[0].TotalReconciliations).To(BeEquivalentTo(1))
		g.Expect(resultK.Status.History[0].LastReconciledStatus).To(Equal(meta.ReconciliationSucceededReason))
		g.Expect(resultK.Status.History[0].Metadata).To(ContainElements(revision))
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

// TestKustomizationReconciler_WaitIgnoresSkippedObjects verifies that objects
// annotated with kustomize.toolkit.fluxcd.io/ssa: Ignore are excluded from
// health checks when spec.wait is true. The user has declared that the
// controller should not manage these objects, so their state — healthy or not,
// existing or not — must not affect the Kustomization's reconciliation outcome.
// Without skipped-entry filtering in checkHealth the status poller would wait
// on the ignored object and the reconciliation could fail or stall on a state
// the user explicitly opted out of.
func TestKustomizationReconciler_WaitIgnoresSkippedObjects(t *testing.T) {
	g := NewWithT(t)
	id := "wait-skipped-" + randStringRunes(5)
	revision := "v1.0.0"
	resultK := &kustomizev1.Kustomization{}
	timeout := 60 * time.Second

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// The Deployment is annotated with ssa: Ignore so the controller will not
	// apply it. Its image is invalid so that, were it ever to be applied, it
	// would never reach a healthy state — modelling the realistic case where a
	// user opts out of managing an object that may exist but is unhealthy.
	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "config.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data:
  key: value
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s-ignored
  annotations:
    kustomize.toolkit.fluxcd.io/ssa: Ignore
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s-ignored
  template:
    metadata:
      labels:
        app: %[1]s-ignored
    spec:
      containers:
        - name: app
          image: registry.invalid/never-exists:nope
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
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
			Timeout:         &metav1.Duration{Duration: 5 * time.Second},
			Wait:            true,
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	// Reconciliation must succeed: the skipped Deployment is excluded from
	// the wait set, so health checks pass on the ConfigMap alone.
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return isReconcileSuccess(resultK)
	}, timeout, time.Second).Should(BeTrue())
	logStatus(t, resultK)

	g.Expect(conditions.IsTrue(resultK, meta.HealthyCondition)).To(BeTrue())
	g.Expect(conditions.GetReason(resultK, meta.HealthyCondition)).To(BeIdenticalTo(meta.SucceededReason))

	// The skipped Deployment must never have been created in the cluster.
	deploy := &unstructured.Unstructured{}
	deploy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	})
	err = k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      id + "-ignored",
		Namespace: id,
	}, deploy)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ssa: Ignore Deployment must not be applied")
}

func TestKustomizationReconciler_WaitsForCustomHealthChecks(t *testing.T) {
	g := NewWithT(t)
	id := "cel-" + randStringRunes(5)
	revision := "v1.0.0"
	resultK := &kustomizev1.Kustomization{}
	timeout := 60 * time.Second

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string) []testserver.File {
		return []testserver.File{
			{
				Name: "config.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
data: {}
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
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
			Timeout:         &metav1.Duration{Duration: time.Second},
			Wait:            true,
			HealthCheckExprs: []kustomize.CustomHealthCheck{{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				HealthCheckExpressions: kustomize.HealthCheckExpressions{
					InProgress: "has(data.foo.bar)",
					Current:    "true",
				},
			}},
		},
	}

	err = k8sClient.Create(context.Background(), kustomization)
	g.Expect(err).NotTo(HaveOccurred())

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return conditions.IsFalse(resultK, meta.ReadyCondition)
	}, timeout, time.Second).Should(BeTrue())
	logStatus(t, resultK)

	msg := conditions.GetMessage(resultK, meta.ReadyCondition)
	g.Expect(msg).
		To(ContainSubstring("timeout waiting for: [ConfigMap"))
	g.Expect(msg).
		To(ContainSubstring("failed to evaluate the CEL expression 'has(data.foo.bar)': no such attribute(s): data.foo.bar"))
}

func TestKustomizationReconciler_RESTMapper(t *testing.T) {
	g := NewWithT(t)
	id := "rm-" + randStringRunes(5)
	resultK := &kustomizev1.Kustomization{}

	restMapper, err := runtimeClient.NewDynamicRESTMapper(testEnv.Config)
	g.Expect(err).NotTo(HaveOccurred())

	err = createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata/restmapper", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	kustomization := &kustomizev1.Kustomization{}
	kustomization.Name = id
	kustomization.Namespace = id
	kustomization.Spec = kustomizev1.KustomizationSpec{
		Interval: metav1.Duration{Duration: 10 * time.Minute},
		Prune:    true,
		Path:     "./",
		Wait:     true,
		SourceRef: kustomizev1.CrossNamespaceSourceReference{
			Name:      repositoryName.Name,
			Namespace: repositoryName.Namespace,
			Kind:      sourcev1.GitRepositoryKind,
		},
		KubeConfig: &meta.KubeConfigReference{
			SecretRef: &meta.SecretKeyReference{
				Name: "kubeconfig",
			},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return isReconcileSuccess(resultK) && resultK.Status.LastAttemptedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	t.Run("discovers newly registered CRD and preferred version", func(t *testing.T) {
		mapping, err := restMapper.RESTMapping(schema.GroupKind{Kind: "ClusterCleanupPolicy", Group: "kyverno.io"})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(mapping.Resource.Version).To(Equal("v2"))
	})

	t.Run("finalizes object", func(t *testing.T) {
		g.Expect(k8sClient.Delete(context.Background(), resultK)).To(Succeed())

		g.Eventually(func() bool {
			err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("discovery fails for deleted CRD", func(t *testing.T) {
		newMapper, err := runtimeClient.NewDynamicRESTMapper(testEnv.Config)
		g.Expect(err).NotTo(HaveOccurred())
		_, err = newMapper.RESTMapping(schema.GroupKind{Kind: "ClusterCleanupPolicy", Group: "kyverno.io"})
		g.Expect(err).To(HaveOccurred())
	})
}

func TestKustomizationReconciler_CancelHealthCheckOnNewRevision(t *testing.T) {
	g := NewWithT(t)
	id := "cancel-" + randStringRunes(5)
	resultK := &kustomizev1.Kustomization{}
	timeout := 60 * time.Second

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// Create initial successful manifests
	successManifests := []testserver.File{
		{
			Name: "configmap.yaml",
			Body: fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
  namespace: %s
data:
  foo: bar`, id),
		},
	}
	artifact, err := testServer.ArtifactFromFiles(successManifests)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("cancel-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, "main/"+artifact)
	g.Expect(err).NotTo(HaveOccurred())

	kustomization := &kustomizev1.Kustomization{}
	kustomization.Name = id
	kustomization.Namespace = id
	kustomization.Spec = kustomizev1.KustomizationSpec{
		Interval: metav1.Duration{Duration: 10 * time.Minute},
		Path:     "./",
		Wait:     true,
		Timeout:  &metav1.Duration{Duration: 5 * time.Minute},
		SourceRef: kustomizev1.CrossNamespaceSourceReference{
			Name:      repositoryName.Name,
			Kind:      sourcev1.GitRepositoryKind,
			Namespace: id,
		},
		KubeConfig: &meta.KubeConfigReference{
			SecretRef: &meta.SecretKeyReference{
				Name: "kubeconfig",
			},
		},
	}

	err = k8sClient.Create(context.Background(), kustomization)
	g.Expect(err).NotTo(HaveOccurred())

	// Wait for initial reconciliation to succeed
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return conditions.IsReady(resultK)
	}, timeout, time.Second).Should(BeTrue())

	// Create failing manifests (deployment with bad image that will timeout)
	failingManifests := []testserver.File{
		{
			Name: "deployment.yaml",
			Body: fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: failing-deployment
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: failing-app
  template:
    metadata:
      labels:
        app: failing-app
    spec:
      containers:
      - name: app
        image: nonexistent.registry/badimage:latest
        ports:
        - containerPort: 8080`, id),
		},
	}

	// Apply failing revision
	failingArtifact, err := testServer.ArtifactFromFiles(failingManifests)
	g.Expect(err).ToNot(HaveOccurred())

	err = applyGitRepository(repositoryName, failingArtifact, "main/"+failingArtifact)
	g.Expect(err).NotTo(HaveOccurred())

	// Wait for reconciliation to start on failing revision
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAttemptedRevision == "main/"+failingArtifact
	}, timeout, time.Second).Should(BeTrue())

	// Now quickly apply a fixed revision while health check should be in progress
	fixedManifests := []testserver.File{
		{
			Name: "deployment.yaml",
			Body: fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: working-deployment
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: working-app
  template:
    metadata:
      labels:
        app: working-app
    spec:
      containers:
      - name: app
        image: nginx:latest
        ports:
        - containerPort: 80`, id),
		},
	}

	fixedArtifact, err := testServer.ArtifactFromFiles(fixedManifests)
	g.Expect(err).ToNot(HaveOccurred())

	// Apply the fixed revision shortly after the failing one
	time.Sleep(2 * time.Second) // Give some time for health check to start
	err = applyGitRepository(repositoryName, fixedArtifact, "main/"+fixedArtifact)
	g.Expect(err).NotTo(HaveOccurred())

	// The key test: verify that the fixed revision gets attempted
	// and that the health check cancellation worked
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAttemptedRevision == "main/"+fixedArtifact
	}, timeout, time.Second).Should(BeTrue())

	// Verify the HealthCheckCanceled event was emitted.
	g.Eventually(func() bool {
		events := getEvents(resultK.GetName(), nil)
		for _, event := range events {
			if event.Reason == meta.HealthCheckCanceledReason {
				t.Logf("Found HealthCheckCanceled event: %s", event.Message)
				return true
			}
		}
		return false
	}, timeout, time.Second).Should(BeTrue(), "HealthCheckCanceled event should be recorded")

	// Verify the event message indicates the trigger source.
	events := getEvents(resultK.GetName(), nil)
	var cancelEvent *corev1.Event
	for i := range events {
		if events[i].Reason == meta.HealthCheckCanceledReason {
			cancelEvent = &events[i]
			break
		}
	}
	g.Expect(cancelEvent).ToNot(BeNil())
	g.Expect(cancelEvent.Message).To(ContainSubstring("Health checks canceled"))
	g.Expect(cancelEvent.Message).To(ContainSubstring("GitRepository"))
}

func TestKustomizationReconciler_HealthCheckExprs_GroupOnly(t *testing.T) {
	g := NewWithT(t)
	id := "cel-grp-" + randStringRunes(5)
	revision := "v1.0.0"
	resultK := &kustomizev1.Kustomization{}
	timeout := 60 * time.Second

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// Unique group per test run to avoid CRD name collisions within the shared envtest.
	group := fmt.Sprintf("%s.flux-test.io", id)
	fooCRName := "foo-" + randStringRunes(5)
	barCRName := "bar-" + randStringRunes(5)

	// Two cluster-scoped CRDs in the same group, plus one CR of each kind.
	// The CEL expression reads `spec.ready`, applied via SSA from the artifact.
	buildFiles := func(barReady bool) []testserver.File {
		return []testserver.File{
			{
				Name: "crd-foo.yaml",
				Body: fmt.Sprintf(`---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.%[1]s
spec:
  group: %[1]s
  names:
    kind: Foo
    listKind: FooList
    plural: foos
    singular: foo
  scope: Cluster
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                ready:
                  type: boolean
`, group),
			},
			{
				Name: "crd-bar.yaml",
				Body: fmt.Sprintf(`---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: bars.%[1]s
spec:
  group: %[1]s
  names:
    kind: Bar
    listKind: BarList
    plural: bars
    singular: bar
  scope: Cluster
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                ready:
                  type: boolean
`, group),
			},
			{
				Name: "foo.yaml",
				Body: fmt.Sprintf(`---
apiVersion: %[1]s/v1
kind: Foo
metadata:
  name: %[2]s
spec:
  ready: true
`, group, fooCRName),
			},
			{
				Name: "bar.yaml",
				Body: fmt.Sprintf(`---
apiVersion: %[1]s/v1
kind: Bar
metadata:
  name: %[2]s
spec:
  ready: %[3]t
`, group, barCRName, barReady),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(buildFiles(true))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("grp-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("grp-%s", randStringRunes(5)),
			Namespace: id,
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: 2 * time.Minute},
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
			Wait:  true,
			// Single group-only healthcheck (empty Kind) that must be applied
			// to both Foo and Bar custom resources.
			HealthCheckExprs: []kustomize.CustomHealthCheck{{
				APIVersion: group + "/v1",
				HealthCheckExpressions: kustomize.HealthCheckExpressions{
					Current: "has(spec.ready) && spec.ready == true",
				},
			}},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	t.Run("group-only healthcheck succeeds for both kinds", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return isReconcileSuccess(resultK)
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		g.Expect(conditions.IsTrue(resultK, meta.HealthyCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultK, meta.HealthyCondition)).To(BeIdenticalTo(meta.SucceededReason))
	})

	t.Run("reports unhealthy when one kind stops satisfying the group expression", func(t *testing.T) {
		badArtifact, err := testServer.ArtifactFromFiles(buildFiles(false))
		g.Expect(err).NotTo(HaveOccurred())

		err = applyGitRepository(repositoryName, badArtifact, "v1.0.1")
		g.Expect(err).NotTo(HaveOccurred())

		// Shorten healthcheck timeout so the failure surfaces quickly.
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.Spec.Timeout = &metav1.Duration{Duration: 5 * time.Second}
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(BeNil())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return conditions.IsFalse(resultK, meta.HealthyCondition) &&
				conditions.GetReason(resultK, meta.HealthyCondition) == meta.HealthCheckFailedReason
		}, timeout, time.Second).Should(BeTrue())

		msg := conditions.GetMessage(resultK, meta.HealthyCondition)
		g.Expect(msg).To(ContainSubstring("Bar"))
		g.Expect(msg).To(ContainSubstring(barCRName))
	})
}
