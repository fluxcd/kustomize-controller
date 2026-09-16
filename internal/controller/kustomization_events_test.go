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

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/testserver"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_EventAggregation(t *testing.T) {
	g := NewWithT(t)
	id := "events-" + randStringRunes(5)
	revision := "v1.0.0"
	timeout := 30 * time.Second
	group := kustomizev1.GroupVersion.Group
	successMessage := fmt.Sprintf("Reconciliation finished revision %s, next run in 1h0m0s", revision)

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

	g.Expect(createNamespace(id)).To(Succeed())

	artifact, err := testServer.ArtifactFromFiles(manifests(id, id))
	g.Expect(err).NotTo(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("events-%s", randStringRunes(5)),
		Namespace: id,
	}
	g.Expect(applyGitRepository(repositoryName, artifact, revision)).To(Succeed())

	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("events-%s", randStringRunes(5)),
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
	g.Expect(k8sClient.Create(context.Background(), kustomization)).To(Succeed())

	resultK := &kustomizev1.Kustomization{}
	g.Eventually(func() bool {
		_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
		return conditions.IsReady(resultK) && resultK.Status.LastAppliedRevision == revision
	}, timeout, time.Second).Should(BeTrue())

	// successEvents returns the aggregated reconciliation success events
	// carrying the applied revision.
	successEvents := func() []corev1.Event {
		var result []corev1.Event
		for _, event := range getEvents(resultK.GetName(), map[string]string{
			group + "/" + eventv1.MetaRevisionKey: revision,
		}) {
			if event.Reason == meta.ReconciliationSucceededReason && event.Message == successMessage {
				result = append(result, event)
			}
		}
		return result
	}

	t.Run("emits a stable success event with a unique token", func(t *testing.T) {
		var events []corev1.Event
		g.Eventually(func() bool {
			events = successEvents()
			return len(events) == 1
		}, timeout, time.Second).Should(BeTrue())

		// The message must not contain the reconciliation duration, otherwise
		// the Kubernetes event recorder cannot aggregate the events.
		g.Expect(events[0].Message).To(Equal(successMessage))
		g.Expect(events[0].Annotations).To(HaveKey(group + "/" + eventv1.MetaTokenKey))
		_, err := uuid.Parse(events[0].Annotations[group+"/"+eventv1.MetaTokenKey])
		g.Expect(err).NotTo(HaveOccurred(), "token annotation is not a valid UUID")
	})

	t.Run("aggregates repeated reconciliations into a single event", func(t *testing.T) {
		req := metav1.Now().String()
		g.Eventually(func() error {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			resultK.SetAnnotations(map[string]string{meta.ReconcileRequestAnnotation: req})
			return k8sClient.Update(context.Background(), resultK)
		}, timeout, time.Second).Should(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(kustomization), resultK)
			return resultK.Status.LastHandledReconcileAt == req
		}, timeout, time.Second).Should(BeTrue())

		g.Eventually(func() bool {
			events := successEvents()
			if len(events) != 1 {
				return false
			}
			return events[0].Count >= 2
		}, timeout, time.Second).Should(BeTrue())
	})
}
