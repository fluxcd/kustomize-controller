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
	"fmt"
	"testing"
	"time"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/testenv"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DisableCommitStatusEvent(t *testing.T) {
	g := NewWithT(t)
	id := "commit-status-" + randStringRunes(5)
	revision := "v1.0.0"
	group := kustomizev1.GroupVersion.Group

	g.Expect(createNamespace(id)).To(Succeed())

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
  key: "value"
`, name),
			},
		}
	}

	artifact, err := testServer.ArtifactFromFiles(manifests(id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      "commit-status-" + randStringRunes(5),
		Namespace: id,
	}
	g.Expect(applyGitRepository(repositoryName, artifact, revision)).To(Succeed())

	// newKustomization creates a Kustomization referencing the test artifact.
	newKustomization := func() *kustomizev1.Kustomization {
		return &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "commit-status-" + randStringRunes(5),
				Namespace: id,
			},
			Spec: kustomizev1.KustomizationSpec{
				Interval: metav1.Duration{Duration: time.Hour},
				Path:     "./",
				SourceRef: kustomizev1.CrossNamespaceSourceReference{
					Name:      repositoryName.Name,
					Namespace: repositoryName.Namespace,
					Kind:      sourcev1.GitRepositoryKind,
				},
				TargetNamespace: id,
				Prune:           true,
			},
		}
	}

	// commitStatusEvents returns the reconciliation success events carrying the
	// applied revision. These are the events used by notification-controller to
	// update the Git commit status.
	commitStatusEvents := func(name string) []eventsv1.Event {
		var result []eventsv1.Event
		events, _ := testenv.GetEvents(ctx, k8sClient, name, "", map[string]string{
			group + "/" + eventv1.MetaRevisionKey: revision,
		})
		for _, event := range events {
			if event.Reason == meta.ReconciliationSucceededReason {
				result = append(result, event)
			}
		}
		return result
	}

	t.Run("emits the commit status event when the gate is disabled", func(t *testing.T) {
		g := NewWithT(t)
		obj := newKustomization()
		g.Expect(k8sClient.Create(ctx, obj)).To(Succeed())

		resultK := &kustomizev1.Kustomization{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, interval).Should(BeTrue())

		g.Eventually(func() int {
			return len(commitStatusEvents(obj.Name))
		}, timeout, interval).Should(BeNumerically(">", 0))
	})

	t.Run("does not emit the commit status event when the gate is enabled", func(t *testing.T) {
		g := NewWithT(t)
		reconciler.DisableCommitStatusEvent = true
		t.Cleanup(func() { reconciler.DisableCommitStatusEvent = false })

		obj := newKustomization()
		g.Expect(k8sClient.Create(ctx, obj)).To(Succeed())

		resultK := &kustomizev1.Kustomization{}
		g.Eventually(func() bool {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), resultK)
			return resultK.Status.LastAppliedRevision == revision
		}, timeout, interval).Should(BeTrue())

		g.Consistently(func() int {
			return len(commitStatusEvents(obj.Name))
		}, 3*interval, interval).Should(BeNumerically("==", 0))
	})
}
