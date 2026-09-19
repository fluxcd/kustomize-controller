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
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

func TestKustomizationContentChangeQueue(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(kustomizev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(sourcev1.AddToScheme(scheme)).To(Succeed())
	r := &KustomizationReconciler{}
	var objs []client.Object
	for _, name := range []string{"ready", "reconciling"} {
		k := &kustomizev1.Kustomization{
			ObjectMeta: metav1.ObjectMeta{Namespace: "one", Name: name},
			Spec:       kustomizev1.KustomizationSpec{SourceRef: kustomizev1.CrossNamespaceSourceReference{Kind: sourcev1.GitRepositoryKind, Name: "source"}},
			Status:     kustomizev1.KustomizationStatus{LastAttemptedRevision: "main@sha1:abc"},
		}
		if name == "ready" {
			conditions.MarkTrue(k, meta.ReadyCondition, "Succeeded", "")
		} else {
			conditions.MarkReconciling(k, "Progressing", "")
		}
		objs = append(objs, k)
	}
	other := objs[0].(*kustomizev1.Kustomization).DeepCopy()
	other.Namespace = "two"
	objs = append(objs, other)
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithIndex(&kustomizev1.Kustomization{}, "source", r.indexBy(sourcev1.GitRepositoryKind)).Build()
	before := &sourcev1.GitRepository{ObjectMeta: metav1.ObjectMeta{Namespace: "one", Name: "source"}, Status: sourcev1.GitRepositoryStatus{Artifact: &meta.Artifact{Revision: "main@sha1:abc", Digest: "sha256:old"}}}
	after := before.DeepCopy()
	after.Status.Artifact.Digest = "sha256:new"
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer q.ShutDown()
	h := handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf("source"))
	p := SourceRevisionChangePredicate{}
	e := event.UpdateEvent{ObjectOld: before, ObjectNew: after}
	g.Expect(p.Update(e)).To(BeTrue())
	h.Update(context.Background(), e, q)
	g.Expect(q.Len()).To(Equal(2))
	var names []string
	for q.Len() > 0 {
		req, _ := q.Get()
		names = append(names, req.NamespacedName.String())
		q.Done(req)
	}
	g.Expect(names).To(ConsistOf("one/ready", "one/reconciling"))
	e.ObjectOld = after.DeepCopy()
	after.Status.Artifact.LastUpdateTime = metav1.Now()
	if p.Update(e) {
		h.Update(context.Background(), e, q)
	}
	g.Expect(q.Len()).To(BeZero())
}
