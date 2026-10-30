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
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	crtlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
)

func TestKustomizationReconciler_DeletesDurationMetricOnNotFound(t *testing.T) {
	g := NewWithT(t)

	name := "metrics-" + randStringRunes(5)
	namespace := "default"
	ref := corev1.ObjectReference{
		Kind:      kustomizev1.KustomizationKind,
		Name:      name,
		Namespace: namespace,
	}

	// Simulate a series left behind by a previous reconcile of an object that
	// was force-deleted before the controller observed its finalizer removal.
	g.Expect(testMetricsH.MetricsRecorder).NotTo(BeNil())
	testMetricsH.MetricsRecorder.RecordDuration(ref, time.Now())
	g.Expect(countDurationSeries(g, name, namespace)).To(Equal(1))

	// The object does not exist, so Get returns NotFound and the deferred
	// recording is never registered.
	_, err := reconciler.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(countDurationSeries(g, name, namespace)).To(Equal(0))
}

// countDurationSeries returns the number of gotk_reconcile_duration_seconds
// series matching the given object name and namespace.
func countDurationSeries(g *WithT, name, namespace string) int {
	families, err := crtlmetrics.Registry.Gather()
	g.Expect(err).NotTo(HaveOccurred())

	count := 0
	for _, family := range families {
		if family.GetName() != "gotk_reconcile_duration_seconds" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["name"] == name && labels["namespace"] == namespace {
				count++
			}
		}
	}
	return count
}
