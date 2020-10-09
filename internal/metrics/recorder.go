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
	readyGauge *prometheus.GaugeVec
}

func NewRecorder() *Recorder {
	return &Recorder{
		readyGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "",
				Subsystem: "",
				Name:      "gitops_toolkit_ready_condition",
				Help:      "The current ready condition status of a GitOps Toolkit resource.",
			},
			[]string{"kind", "name", "namespace", "status"},
		),
	}
}

func (r *Recorder) Collectors() []prometheus.Collector {
	return []prometheus.Collector{r.readyGauge}
}

func (r *Recorder) RecordReadyStatus(ref corev1.ObjectReference, condition meta.Condition, deleted bool) {
	for _, status := range []string{string(corev1.ConditionTrue), string(corev1.ConditionFalse), string(corev1.ConditionUnknown), ConditionDeleted} {
		var value float64
		if deleted {
			if status == ConditionDeleted {
				value = 1
			}
		} else {
			switch condition.Status {
			case corev1.ConditionTrue:
				if status == string(condition.Status) {
					value = 1
				}
			case corev1.ConditionFalse:
				if status == string(condition.Status) {
					value = 1
				}
			case corev1.ConditionUnknown:
				if status == string(condition.Status) {
					value = 1
				}
			}
		}

		r.readyGauge.WithLabelValues(ref.Kind, ref.Name, ref.Namespace, status).Set(value)
	}
}
