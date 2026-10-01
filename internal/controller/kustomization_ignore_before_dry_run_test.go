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

	"github.com/fluxcd/pkg/apis/kustomize"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

// TestKustomizationReconciler_IgnoreBeforeDryRun is the controller-level repro of
// https://github.com/fluxcd/kustomize-controller/issues/1739.
//
// It uses a PersistentVolumeClaim whose spec.resources.requests.storage is
// decreased in Git. The API server rejects any storage decrease at (dry-run)
// update validation with a Forbidden error — the same shape as the Gardener
// version-downgrade case. This lets us reproduce the wedge deterministically
// without a custom admission webhook:
//
//   - With the ignore rule left at the default (post-dry-run), the dry-run runs
//     against the rejected desired value and reconciliation fails: the ignore
//     rule never gets a chance to run.
//   - With beforeDryRun: true, the rule is resolved before the dry-run. Flux is
//     the sole owner of the drifted storage request, so the live value is adopted
//     into the payload, the dry-run validates the accepted value, and
//     reconciliation succeeds.
func TestKustomizationReconciler_IgnoreBeforeDryRun(t *testing.T) {
	g := NewWithT(t)
	id := "ignore-bdr-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	// A single PVC whose storage request is templated so we can shrink it in a
	// later revision.
	pvcManifest := func(name, storage string) []testserver.File {
		return []testserver.File{
			{
				Name: "pvc.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %[1]s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: "%[2]s"
`, name, storage),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(pvcManifest(id, "2Gi"))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create artifact from files")

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("ignore-bdr-%s", randStringRunes(5)),
		Namespace: id,
	}
	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	// bdr is the pointer used for the opt-in; it must stay addressable.
	bdr := true

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("ignore-bdr-%s", randStringRunes(5)),
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
			Prune:           true,
			Ignore: []kustomizev1.IgnoreRule{
				{
					Paths:        []string{"/spec/resources/requests/storage"},
					BeforeDryRun: &bdr,
					Target: &kustomize.Selector{
						Kind: "PersistentVolumeClaim",
					},
				},
			},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	resultPVC := &corev1.PersistentVolumeClaim{}
	pvcKey := types.NamespacedName{Name: id, Namespace: id}

	t.Run("creates PVC with initial storage", func(t *testing.T) {
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		g.Expect(k8sClient.Get(context.Background(), pvcKey, resultPVC)).Should(Succeed())
		g.Expect(resultPVC.Spec.Resources.Requests.Storage().String()).To(Equal("2Gi"))
	})

	t.Run("forbidden storage downgrade is unblocked before dry-run", func(t *testing.T) {
		// Shrink the storage request in Git. Without the before-dry-run opt-in
		// this would wedge the dry-run; with it, the live 2Gi is adopted and the
		// reconciliation succeeds.
		artifact, err = testServer.ArtifactFromFiles(pvcManifest(id, "1Gi"))
		g.Expect(err).NotTo(HaveOccurred())
		revision = "v2.0.0"
		err = applyGitRepository(repositoryName, artifact, revision)
		g.Expect(err).NotTo(HaveOccurred())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, time.Second).Should(BeTrue())
		logStatus(t, resultK)

		// The reconciliation succeeded and the live value was preserved (the
		// rejected 1Gi never reached the API server).
		g.Expect(conditions.IsReady(resultK)).To(BeTrue())
		g.Expect(k8sClient.Get(context.Background(), pvcKey, resultPVC)).Should(Succeed())
		g.Expect(resultPVC.Spec.Resources.Requests.Storage().String()).To(Equal("2Gi"))
	})
}

// TestKustomizationReconciler_IgnoreBeforeDryRun_DefaultWedges is the
// backward-compatibility / opt-in gate: the SAME forbidden storage downgrade,
// but with the ignore rule left at the default (beforeDryRun unset, i.e.
// post-dry-run), must still wedge the dry-run and fail reconciliation. This
// proves the new behavior is strictly opt-in and does not change the shipped
// post-dry-run semantics.
func TestKustomizationReconciler_IgnoreBeforeDryRun_DefaultWedges(t *testing.T) {
	g := NewWithT(t)
	id := "ignore-bdr-def-" + randStringRunes(5)
	revision := "v1.0.0"

	err := createNamespace(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

	err = createKubeConfigSecret(id)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	pvcManifest := func(name, storage string) []testserver.File {
		return []testserver.File{
			{
				Name: "pvc.yaml",
				Body: fmt.Sprintf(`---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %[1]s
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: "%[2]s"
`, name, storage),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(pvcManifest(id, "2Gi"))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("ignore-bdr-def-%s", randStringRunes(5)),
		Namespace: id,
	}
	err = applyGitRepository(repositoryName, artifact, revision)
	g.Expect(err).NotTo(HaveOccurred())

	kustomizationKey := types.NamespacedName{
		Name:      fmt.Sprintf("ignore-bdr-def-%s", randStringRunes(5)),
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
			Prune:           true,
			// Default post-dry-run ignore rule (beforeDryRun unset).
			Ignore: []kustomizev1.IgnoreRule{
				{
					Paths: []string{"/spec/resources/requests/storage"},
					Target: &kustomize.Selector{
						Kind: "PersistentVolumeClaim",
					},
				},
			},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	pvcKey := types.NamespacedName{Name: id, Namespace: id}
	resultPVC := &corev1.PersistentVolumeClaim{}

	// Wait for the initial create to succeed.
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	// Shrink the storage request in Git.
	artifact, err = testServer.ArtifactFromFiles(pvcManifest(id, "1Gi"))
	g.Expect(err).NotTo(HaveOccurred())
	failRevision := "v2.0.0"
	err = applyGitRepository(repositoryName, artifact, failRevision)
	g.Expect(err).NotTo(HaveOccurred())

	// The dry-run wedges on the forbidden downgrade: Ready goes False with a
	// reconciliation failure, and the applied revision stays at v1.
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return conditions.IsFalse(resultK, meta.ReadyCondition) &&
			conditions.GetReason(resultK, meta.ReadyCondition) == meta.ReconciliationFailedReason
	}, timeout, time.Second).Should(BeTrue())
	logStatus(t, resultK)

	g.Expect(resultK.Status.LastAppliedRevision).To(Equal(revision))
	// The live PVC is untouched and still at 2Gi.
	g.Expect(k8sClient.Get(context.Background(), pvcKey, resultPVC)).Should(Succeed())
	g.Expect(resultPVC.Spec.Resources.Requests.Storage().String()).To(Equal("2Gi"))
}
