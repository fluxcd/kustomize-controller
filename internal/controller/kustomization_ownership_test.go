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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/ssa"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

func TestKustomizationReconciler_OwnershipWarning(t *testing.T) {
	const (
		nameLabel       = "kustomize.toolkit.fluxcd.io/name"
		namespaceLabel  = "kustomize.toolkit.fluxcd.io/namespace"
		ssaAnnotation   = "kustomize.toolkit.fluxcd.io/ssa"
		pruneAnnotation = "kustomize.toolkit.fluxcd.io/prune"
	)

	g := NewWithT(t)
	targetClient, err := client.NewWithWatch(testEnv.Config, client.Options{Scheme: k8sClient.Scheme()})
	g.Expect(err).NotTo(HaveOccurred())

	for _, tt := range []struct {
		name               string
		ownerName          string
		ownerNamespace     string
		desiredAnnotations map[string]string
		liveAnnotations    map[string]string
		desiredLabels      map[string]string
		lookupError        error
		newResource        bool
		clusterScoped      bool
		wantWarning        bool
		wantSkipped        bool
	}{
		{name: "different name", ownerName: "other", ownerNamespace: "flux-system", wantWarning: true},
		{name: "different namespace", ownerName: "current", ownerNamespace: "other-system", wantWarning: true},
		{name: "different name and namespace", ownerName: "other", ownerNamespace: "other-system", wantWarning: true},
		{name: "cluster scoped", ownerName: "other", ownerNamespace: "flux-system", clusterScoped: true, wantWarning: true},
		{name: "same owner", ownerName: "current", ownerNamespace: "flux-system"},
		{name: "unowned"},
		{name: "missing owner namespace", ownerName: "other"},
		{name: "missing owner name", ownerNamespace: "other-system"},
		{name: "new resource", newResource: true},
		{name: "desired ignore", ownerName: "other", ownerNamespace: "flux-system", desiredAnnotations: map[string]string{ssaAnnotation: "Ignore"}, wantSkipped: true},
		{name: "live ignore", ownerName: "other", ownerNamespace: "flux-system", liveAnnotations: map[string]string{ssaAnnotation: "Ignore"}, wantSkipped: true},
		{name: "desired disabled", ownerName: "other", ownerNamespace: "flux-system", desiredAnnotations: map[string]string{"kustomize.toolkit.fluxcd.io/reconcile": "disabled"}, wantSkipped: true},
		{name: "live disabled", ownerName: "other", ownerNamespace: "flux-system", liveAnnotations: map[string]string{"kustomize.toolkit.fluxcd.io/reconcile": "disabled"}, wantSkipped: true},
		{name: "if not present", ownerName: "other", ownerNamespace: "flux-system", desiredAnnotations: map[string]string{ssaAnnotation: "IfNotPresent"}, wantSkipped: true},
		{name: "ignore label", ownerName: "other", ownerNamespace: "flux-system", desiredLabels: map[string]string{ssaAnnotation: "Ignore"}, wantSkipped: true},
		{name: "new if not present", newResource: true, desiredAnnotations: map[string]string{ssaAnnotation: "IfNotPresent"}},
		{name: "live if not present", ownerName: "other", ownerNamespace: "flux-system", liveAnnotations: map[string]string{ssaAnnotation: "IfNotPresent"}, wantWarning: true},
		{name: "lookup timeout", ownerName: "other", ownerNamespace: "flux-system", lookupError: apierrors.NewTimeoutError("ownership lookup timed out", 1)},
		{name: "lookup forbidden", ownerName: "other", ownerNamespace: "flux-system", lookupError: apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "shared", fmt.Errorf("ownership lookup denied"))},
		{name: "lookup no match", ownerName: "other", ownerNamespace: "flux-system", lookupError: &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Kind: "ConfigMap"}, SearchedVersions: []string{"v1"}}},
		{name: "merge", ownerName: "other", ownerNamespace: "flux-system", desiredAnnotations: map[string]string{ssaAnnotation: "Merge"}, wantWarning: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ownership-" + randStringRunes(5)}}
			g.Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			t.Cleanup(func() { g.Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })

			obj := &kustomizev1.Kustomization{
				ObjectMeta: metav1.ObjectMeta{Name: "current", Namespace: "flux-system"},
			}
			conditions.MarkTrue(obj, meta.ReadyCondition, meta.ReconciliationSucceededReason, "Applied")
			beforeConditions := obj.DeepCopy().Status.Conditions
			recorder := events.NewFakeRecorder(32, true)
			r := &KustomizationReconciler{ControllerName: "kustomize-controller", EventRecorder: recorder}
			poller := polling.NewStatusPoller(k8sClient, k8sClient.RESTMapper(), polling.Options{})
			var lookupCount atomic.Int32
			var applyAttempted, warningBeforeApply atomic.Bool
			scopedClient := interceptor.NewClient(targetClient, interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if lookupCount.Add(1) == 1 && tt.lookupError != nil {
						return tt.lookupError
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if !applyAttempted.Swap(true) {
						warningBeforeApply.Store(len(recorder.Events) > 0)
					}
					return c.Patch(ctx, obj, patch, opts...)
				},
			})
			manager := ssa.NewResourceManager(scopedClient, poller, ssa.Owner{
				Field: r.ControllerName, Group: kustomizev1.GroupVersion.Group,
			})

			desired := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "v1", "kind": "ConfigMap",
				"metadata": map[string]interface{}{"name": "shared", "namespace": ns.Name},
			}}
			if tt.clusterScoped {
				desired.SetKind("Namespace")
				desired.SetName("shared-" + randStringRunes(5))
				desired.SetNamespace("")
			}
			live := desired.DeepCopy()
			live.SetLabels(map[string]string{nameLabel: tt.ownerName, namespaceLabel: tt.ownerNamespace})
			annotations := map[string]string{pruneAnnotation: "disabled"}
			for k, v := range tt.liveAnnotations {
				annotations[k] = v
			}
			live.SetAnnotations(annotations)
			if !tt.newResource {
				g.Expect(k8sClient.Patch(ctx, live, client.Apply, client.ForceOwnership, client.FieldOwner(r.ControllerName))).To(Succeed())
			}
			if tt.clusterScoped {
				t.Cleanup(func() { g.Expect(k8sClient.Delete(ctx, desired)).To(Succeed()) })
			}
			desired.SetLabels(tt.desiredLabels)
			desired.SetAnnotations(tt.desiredAnnotations)
			manager.SetOwnerLabels([]*unstructured.Unstructured{desired}, obj.Name, obj.Namespace)

			_, changes, err := r.apply(ctx, manager, obj, &sourcev1.GitRepository{}, "main/test", "", []*unstructured.Unstructured{desired})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(lookupCount.Load()).To(Equal(int32(1)), "ownership checks must reuse the SSA lookup")
			g.Expect(changes.Entries).To(HaveLen(1))
			g.Expect(obj.Status.Conditions).To(Equal(beforeConditions))

			current := desired.DeepCopy()
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(desired), current)).To(Succeed())
			if tt.wantSkipped {
				g.Expect(changes.Entries[0].Action).To(Equal(ssa.SkippedAction))
				g.Expect(current.GetLabels()[nameLabel]).To(Equal(tt.ownerName))
				g.Expect(current.GetAnnotations()[pruneAnnotation]).To(Equal("disabled"))
			} else {
				g.Expect(current.GetLabels()[nameLabel]).To(Equal(obj.Name))
				g.Expect(current.GetLabels()[namespaceLabel]).To(Equal(obj.Namespace))
				g.Expect(current.GetAnnotations()).NotTo(HaveKey(pruneAnnotation))
			}

			var warnings []corev1.Event
			for _, event := range recorder.GetEvents() {
				if event.Type == corev1.EventTypeWarning {
					warnings = append(warnings, event)
				}
			}
			if tt.wantWarning {
				g.Expect(warningBeforeApply.Load()).To(BeTrue(), "warning must be emitted before the first apply request")
				g.Expect(warnings).To(HaveLen(1), "foreign ownership must be visible before applying")
				g.Expect(warnings[0].Reason).To(Equal("OwnershipConflict"))
				g.Expect(warnings[0].Action).To(Equal(kustomizev1.ActionApply.String()))
				g.Expect(warnings[0].InvolvedObject.Name).To(Equal(obj.Name))
				g.Expect(warnings[0].Message).To(ContainSubstring(fmt.Sprintf("Kustomization %s/%s", tt.ownerNamespace, tt.ownerName)))
				g.Expect(warnings[0].Message).To(ContainSubstring(desired.GetKind() + "/"))
				g.Expect(warnings[0].Message).To(ContainSubstring(desired.GetName()))
			} else {
				g.Expect(warnings).To(BeEmpty())
			}
		})
	}
}

func TestKustomizationReconciler_OwnershipReadConcurrency(t *testing.T) {
	g := NewWithT(t)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ownership-" + randStringRunes(5)}}
	g.Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	t.Cleanup(func() { g.Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })

	targetClient, err := client.NewWithWatch(testEnv.Config, client.Options{Scheme: k8sClient.Scheme()})
	g.Expect(err).NotTo(HaveOccurred())
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var seen sync.Map
	var active, peak atomic.Int32
	scopedClient := interceptor.NewClient(targetClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, loaded := seen.LoadOrStore(key, true); !loaded {
				n := active.Add(1)
				defer active.Add(-1)
				for previous := peak.Load(); n > previous; previous = peak.Load() {
					if peak.CompareAndSwap(previous, n) {
						break
					}
				}
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	obj := &kustomizev1.Kustomization{ObjectMeta: metav1.ObjectMeta{Name: "current", Namespace: ns.Name}}
	r := &KustomizationReconciler{
		ControllerName: "kustomize-controller", ConcurrentSSA: 2, EventRecorder: events.NewFakeRecorder(32, true),
	}
	manager := ssa.NewResourceManager(scopedClient, nil, ssa.Owner{Field: r.ControllerName, Group: kustomizev1.GroupVersion.Group})
	manager.SetConcurrency(r.ConcurrentSSA)
	var objects []*unstructured.Unstructured
	for i := 0; i < cap(started); i++ {
		objects = append(objects, &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]interface{}{"name": fmt.Sprintf("shared-%d", i), "namespace": ns.Name},
		}})
	}
	manager.SetOwnerLabels(objects, obj.Name, obj.Namespace)
	done := make(chan error, 1)
	go func() {
		_, _, err := r.apply(checkCtx, manager, obj, &sourcev1.GitRepository{}, "main/test", "", objects)
		done <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	arrived := 0
waitForConcurrentReads:
	for arrived < r.ConcurrentSSA {
		select {
		case <-started:
			arrived++
		case <-timer.C:
			break waitForConcurrentReads
		}
	}
	close(release)
	g.Expect(<-done).To(Succeed())
	g.Expect(arrived).To(Equal(r.ConcurrentSSA), "ownership reads must honor SSA concurrency")
	g.Expect(peak.Load()).To(Equal(int32(r.ConcurrentSSA)), "ownership reads must stay within the configured limit")
}
