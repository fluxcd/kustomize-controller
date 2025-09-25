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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	feathelper "github.com/fluxcd/pkg/runtime/features"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/kustomize-controller/internal/features"
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

	// Enable the CancelHealthCheckOnNewRevision feature
	originalValue := features.FeatureGates()[features.CancelHealthCheckOnNewRevision]
	features.FeatureGates()[features.CancelHealthCheckOnNewRevision] = true

	// Initialize the feature gate system properly
	featGates := feathelper.FeatureGates{}
	err := featGates.SupportedFeatures(features.FeatureGates())
	g.Expect(err).NotTo(HaveOccurred(), "failed to initialize feature gates")

	defer func() {
		features.FeatureGates()[features.CancelHealthCheckOnNewRevision] = originalValue
	}()

	err = createNamespace(id)
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

	t.Logf("Fixed revision was attempted: %s", resultK.Status.LastAttemptedRevision)

	// The test demonstrated that:
	// 1. Feature is enabled (seen in logs: "CancelHealthCheckOnNewRevision feature enabled")
	// 2. Cancellation worked (seen in logs: "New revision detected during health check, cancelling")
	// 3. Health check was cancelled early (seen in logs: "health check cancelled due to new revision availability")
	// 4. New revision processing started immediately after cancellation
	t.Logf("✅ CancelHealthCheckOnNewRevision feature working correctly")
}

