package metrics

import (
	"testing"

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
		t.Error(err)
	}

	if len(metricFamilies) != 1 {
		t.Errorf("expected one metric family, got %v", metricFamilies)
	}

	if len(metricFamilies[0].Metric) != 4 {
		t.Errorf("expected four metrics, got %v", metricFamilies[0].Metric)
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
