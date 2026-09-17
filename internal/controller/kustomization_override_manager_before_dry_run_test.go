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

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

// TestKustomizationReconciler_OverrideManagerBeforeDryRun reproduces the
// "break-glass" server-side apply wedge and verifies that
// --override-manager-before-dry-run (wired to overrideManagerBeforeDryRunName
// on the shared test reconciler, see suite_test.go) recovers it end-to-end
// through the real KustomizationReconciler.
//
// The wedge: a legacy, non-Flux field manager re-creates a container via a
// client-side Update (simulating a break-glass kubectl-style write made while
// Flux was disabled) after Flux already owns that container's image field.
// The legacy manager ends up co-owning the container's identity ("." + name)
// without owning its image. When the Kustomization's source subsequently
// drops that container, server-side apply cannot prune the element (the
// legacy manager still owns it) but does drop the field Flux solely owned,
// leaving a half-removed, invalid container and failing the dry-run on every
// reconcile. --override-manager-before-dry-run reclaims the legacy manager's
// fields before the dry-run runs, so the merge stays valid and the container
// is pruned cleanly.
//
// This complements the ssa-level reproduction in
// fluxcd/pkg/ssa.TestApply_FieldManagersBeforeDryRun by covering the actual
// wiring in this repo: flag -> KustomizationReconciler field ->
// ssa.FieldManager entries -> ssa.ApplyCleanupOptions.FieldManagersBeforeDryRun.
func TestKustomizationReconciler_OverrideManagerBeforeDryRun(t *testing.T) {
	g := NewWithT(t)
	id := "override-before-dry-run-" + randStringRunes(5)
	revision := "v1.0.0"

	const (
		gitImage = "registry.k8s.io/pause:3.9"
		bgImage  = "registry.k8s.io/pause:3.8"
	)

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	manifests := func(name string, includePDH bool) []testserver.File {
		containers := fmt.Sprintf(`      - name: main
        image: %s`, gitImage)
		if includePDH {
			containers += fmt.Sprintf(`
      - name: pdh
        image: %s`, gitImage)
		}
		return []testserver.File{
			{
				Name: "deployment.yaml",
				Body: fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s
  template:
    metadata:
      labels:
        app: %[1]s
    spec:
      containers:
%[2]s
`, name, containers),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id, true))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("override-before-dry-run-%s", randStringRunes(5)),
		Namespace: id,
	}

	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("override-before-dry-run-%s", randStringRunes(5)),
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
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	deployment := &appsv1.Deployment{}
	deploymentKey := types.NamespacedName{Name: id, Namespace: id}

	t.Run("creates deployment with main and pdh containers", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)
		g.Expect(k8sClient.Get(context.Background(), deploymentKey, deployment)).Should(Succeed())
		g.Expect(deployment.Spec.Template.Spec.Containers).Should(HaveLen(2))
	})

	t.Run("break-glass: legacy manager re-creates pdh via client-side Update", func(t *testing.T) {
		// Remove pdh via a legacy client-side Update, simulating an operator
		// disabling Flux and hand-patching the workload.
		g.Expect(k8sClient.Get(context.Background(), deploymentKey, deployment)).Should(Succeed())
		deployment.Spec.Template.Spec.Containers = deployment.Spec.Template.Spec.Containers[:1]
		g.Expect(k8sClient.Update(context.Background(), deployment, client.FieldOwner(overrideManagerBeforeDryRunName))).To(Succeed())

		// Re-add pdh at a hotfix image, still as the legacy manager. This write
		// anchors the legacy manager's ownership of the container element and
		// its identity fields, without it owning the image field.
		g.Expect(k8sClient.Get(context.Background(), deploymentKey, deployment)).Should(Succeed())
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:  "pdh",
			Image: bgImage,
		})
		g.Expect(k8sClient.Update(context.Background(), deployment, client.FieldOwner(overrideManagerBeforeDryRunName))).To(Succeed())
	})

	t.Run("re-enabling Flux reasserts the git image, forming the fossil ownership", func(t *testing.T) {
		// No source change, so force a reconcile rather than waiting on a
		// watch-triggered one.
		_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: kustomizationKey})
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			if err := k8sClient.Get(context.Background(), deploymentKey, deployment); err != nil {
				return false
			}
			for _, c := range deployment.Spec.Template.Spec.Containers {
				if c.Name == "pdh" {
					return c.Image == gitImage
				}
			}
			return false
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("build drops pdh, dry-run wedge is recovered by override-manager-before-dry-run", func(t *testing.T) {
		artifact, err = testServer.ArtifactFromFiles(manifests(id, false))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		kstatusCheck.CheckErr(ctx, resultK)
		g.Expect(k8sClient.Get(context.Background(), deploymentKey, deployment)).Should(Succeed())
		g.Expect(deployment.Spec.Template.Spec.Containers).Should(HaveLen(1))
		g.Expect(deployment.Spec.Template.Spec.Containers[0].Name).Should(Equal("main"))
	})
}
