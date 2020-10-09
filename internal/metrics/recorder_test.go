package metrics

import (
	"testing"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
)

func TestRecorder_RecordReadyStatus(t *testing.T) {
	rec := NewRecorder()
	reg := prometheus.NewRegistry()
	reg.MustRegister(rec.readyGauge)

	ref := corev1.ObjectReference{
		Kind:      "Kustomization",
		Namespace: "default",
		Name:      "test",
	}

	cond := meta.Condition{
		Type:   meta.ReadyCondition,
		Status: corev1.ConditionTrue,
	}

	rec.RecordReadyStatus(ref, cond, false)

	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Error(err)
	}

	if len(metricFamilies) != 1 {
		t.Errorf("expected one metric family, got %v", metricFamilies)
	}

	var value float64
	for _, m := range metricFamilies[0].Metric {
		for _, pair := range m.GetLabel() {
			if *pair.Name == "status" && *pair.Value == string(corev1.ConditionTrue) {
				value = *m.GetGauge().Value
			}
		}
	}

	if value != 1 {
		t.Errorf("expected ready guage value to be 1, got %v", value)
	}
}
