package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"

	"github.com/fluxcd/pkg/apis/meta"
)

func TestRecorder_RecordCondition(t *testing.T) {
	rec := NewRecorder()
	reg := prometheus.NewRegistry()
	reg.MustRegister(rec.conditionGauge)

	ref := corev1.ObjectReference{
		Kind:      "Kustomization",
		Namespace: "default",
		Name:      "test",
	}

	cond := meta.Condition{
		Type:   meta.ReadyCondition,
		Status: corev1.ConditionTrue,
	}

	rec.RecordCondition(ref, cond, false)

	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}

	if len(metricFamilies) != 1 {
		t.Fatalf("expected one metric family, got %v", metricFamilies)
	}

	if len(metricFamilies[0].Metric) != 4 {
		t.Fatalf("expected four metrics, got %v", metricFamilies[0].Metric)
	}

	var conditionTrueValue float64
	for _, m := range metricFamilies[0].Metric {
		for _, pair := range m.GetLabel() {
			if *pair.Name == "type" && *pair.Value != meta.ReadyCondition {
				t.Errorf("expected condition type to be %s, got %s", meta.ReadyCondition, *pair.Value)
			}
			if *pair.Name == "status" && *pair.Value == string(corev1.ConditionTrue) {
				conditionTrueValue = *m.GetGauge().Value
			} else if *pair.Name == "status" && *m.GetGauge().Value != 0 {
				t.Errorf("expected guage value to be 0, got %v", *m.GetGauge().Value)
			}
		}
	}

	if conditionTrueValue != 1 {
		t.Errorf("expected guage value to be 1, got %v", conditionTrueValue)
	}
}

func TestRecorder_RecordDuration(t *testing.T) {
	rec := NewRecorder()
	reg := prometheus.NewRegistry()
	reg.MustRegister(rec.durationHistogram)

	ref := corev1.ObjectReference{
		Kind:      "GitRepository",
		Namespace: "default",
		Name:      "test",
	}

	reconcileStart := time.Now().Add(-time.Second)
	rec.RecordDuration(ref, reconcileStart)

	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}

	if len(metricFamilies) != 1 {
		t.Fatalf("expected one metric family, got %v", metricFamilies)
	}

	if len(metricFamilies[0].Metric) != 1 {
		t.Fatalf("expected one metric, got %v", metricFamilies[0].Metric)
	}

	sampleCount := metricFamilies[0].Metric[0].Histogram.GetSampleCount()
	if sampleCount != 1 {
		t.Errorf("expected histogram sample count to be 1, got %v", sampleCount)
	}

	labels := metricFamilies[0].Metric[0].GetLabel()

	if len(labels) != 3 {
		t.Fatalf("expected three labels, got %v", metricFamilies[0].Metric[0].GetLabel())
	}

	for _, pair := range labels {
		if *pair.Name == "kind" && *pair.Value != ref.Kind {
			t.Errorf("expected kind label to be %s, got %s", ref.Kind, *pair.Value)
		}
		if *pair.Name == "name" && *pair.Value != ref.Name {
			t.Errorf("expected name label to be %s, got %s", ref.Name, *pair.Value)
		}
		if *pair.Name == "namespace" && *pair.Value != ref.Namespace {
			t.Errorf("expected namespace label to be %s, got %s", ref.Namespace, *pair.Value)
		}
	}
}
