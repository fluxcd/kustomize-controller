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
	"time"

	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	runtimectrl "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/metrics"
)

// durationSeriesExists reports whether gotk_reconcile_duration_seconds has a
// series for the given kind/name/namespace, read directly from the
// *prometheus.HistogramVec since the Recorder has no presence-check method.
func durationSeriesExists(t *testing.T, rec *metrics.Recorder, kind, name, namespace string) bool {
	t.Helper()

	var hv *prometheus.HistogramVec
	for _, c := range rec.Collectors() {
		if v, ok := c.(*prometheus.HistogramVec); ok {
			hv = v
			break
		}
	}
	if hv == nil {
		t.Fatal("no *prometheus.HistogramVec collector found on the recorder")
	}

	ch := make(chan prometheus.Metric, 64)
	hv.Collect(ch)
	close(ch)

	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		labels := make(map[string]string, len(pb.GetLabel()))
		for _, lp := range pb.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		if labels["kind"] == kind && labels["name"] == name && labels["namespace"] == namespace {
			return true
		}
	}
	return false
}

// TestKustomizationReconciler_deleteMetricsOnNotFound reproduces the
// gotk_reconcile_duration_seconds leak: Reconcile only drops an object's
// duration series from the deferred RecordDuration call, which never runs
// when Get returns NotFound. Uses a fake, in-memory client with no manager
// watching it, so both Reconcile calls are driven directly and
// deterministically.
func TestKustomizationReconciler_deleteMetricsOnNotFound(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	const namespaceName = "kust-leak-test"
	const name = "leak-test"

	kustomization := &kustomizev1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespaceName,
			Finalizers: []string{kustomizev1.KustomizationFinalizer},
		},
		Spec: kustomizev1.KustomizationSpec{
			Interval: metav1.Duration{Duration: time.Hour},
			Prune:    true,
			Path:     "./",
			SourceRef: kustomizev1.CrossNamespaceSourceReference{
				Kind: "GitRepository",
				Name: "does-not-exist",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithStatusSubresource(&kustomizev1.Kustomization{}).
		WithObjects(kustomization).
		Build()

	// A private, unregistered Recorder, not testMetricsH's shared registry,
	// so this can't collide with series from other tests.
	rec := metrics.NewRecorder()

	r := &KustomizationReconciler{
		Client:        fakeClient,
		EventRecorder: events.NewFakeRecorder(32, true),
		Metrics: runtimectrl.Metrics{
			Scheme:          scheme.Scheme,
			MetricsRecorder: rec,
		},
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(kustomization)}

	// First reconcile: past the add-finalizer step already, so it proceeds
	// to the source lookup, which fails (not found) and requeues. The
	// deferred RecordDuration still runs, creating a duration series.
	_, err := r.Reconcile(ctx, req)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(durationSeriesExists(t, rec, kustomizev1.KustomizationKind, name, namespaceName)).
		To(BeTrue(), "expected a duration series after the first (successful Get) reconcile")

	// Clear the finalizer before deleting, so the object is removed in one
	// step with no finalizer left for finalize() to ever observe or
	// complete. The next Reconcile call sees it as fully gone.
	g.Expect(fakeClient.Get(ctx, req.NamespacedName, kustomization)).To(Succeed())
	kustomization.SetFinalizers(nil)
	g.Expect(fakeClient.Update(ctx, kustomization)).To(Succeed())
	g.Expect(fakeClient.Delete(ctx, kustomization)).To(Succeed())

	err = fakeClient.Get(ctx, req.NamespacedName, &kustomizev1.Kustomization{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object must be fully gone (NotFound), not merely marked for deletion")

	// Second reconcile: Get returns NotFound. Before the fix, the series
	// from the first reconcile survives forever. After the fix, it's gone.
	_, err = r.Reconcile(ctx, req)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(durationSeriesExists(t, rec, kustomizev1.KustomizationKind, name, namespaceName)).
		To(BeFalse(), "duration series must not survive the object's deletion")
}
