package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"

	"github.com/fluxcd/pkg/apis/meta"
)

const (
	ConditionDeleted = "Deleted"
)

type Recorder struct {
	conditionGauge *prometheus.GaugeVec
}

func NewRecorder() *Recorder {
	return &Recorder{
		conditionGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gitops_toolkit_condition",
				Help: "The current condition status of a GitOps Toolkit resource.",
			},
			[]string{"type", "kind", "name", "namespace", "status"},
		),
	}
}

func (r *Recorder) Collectors() []prometheus.Collector {
	return []prometheus.Collector{r.conditionGauge}
}

func (r *Recorder) RecordCondition(ref corev1.ObjectReference, condition meta.Condition, deleted bool) {
	for _, status := range []string{string(corev1.ConditionTrue), string(corev1.ConditionFalse), string(corev1.ConditionUnknown), ConditionDeleted} {
		var value float64
		if deleted {
			if status == ConditionDeleted {
				value = 1
			}
		} else {
			if status == string(condition.Status) {
				value = 1
			}
		}

		r.conditionGauge.WithLabelValues(condition.Type, ref.Kind, ref.Name, ref.Namespace, status).Set(value)
	}
}
