package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"

	"github.com/fluxcd/pkg/apis/meta"
)

const (
	ConditionDeleted = "Deleted"
)

type Recorder struct {
	conditionGauge    *prometheus.GaugeVec
	durationHistogram *prometheus.HistogramVec
}

func NewRecorder() *Recorder {
	return &Recorder{
		conditionGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gotk_reconcile_condition",
				Help: "The current condition status of a GitOps Toolkit resource reconciliation.",
			},
			[]string{"kind", "name", "namespace", "type", "status"},
		),
		durationHistogram: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gotk_reconcile_duration",
				Help:    "The duration in seconds of a GitOps Toolkit resource reconciliation.",
				Buckets: prometheus.ExponentialBuckets(10e-9, 10, 10),
			},
			[]string{"kind", "name", "namespace"},
		),
	}
}

func (r *Recorder) Collectors() []prometheus.Collector {
	return []prometheus.Collector{r.conditionGauge, r.durationHistogram}
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

		r.conditionGauge.WithLabelValues(ref.Kind, ref.Name, ref.Namespace, condition.Type, status).Set(value)
	}
}

func (r *Recorder) RecordDuration(ref corev1.ObjectReference, start time.Time) {
	r.durationHistogram.WithLabelValues(ref.Kind, ref.Name, ref.Namespace).Observe(time.Since(start).Seconds())
}
