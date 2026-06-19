// Package metrics provides a Prometheus-compatible metrics registry with
// counters, gauges, histograms and runtime snapshots for gofly services.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// RegisterPrometheusCollectors registers gofly metrics as Prometheus collectors.
func RegisterPrometheusCollectors(reg prometheus.Registerer, namespace string, metrics *Registry) error {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	if metrics == nil {
		metrics = Default
	}
	if namespace == "" {
		namespace = "gofly"
	}
	collectors := []prometheus.Collector{
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Name: "requests_total", Help: "Total number of handled requests."}, func() float64 { return float64(metrics.Snapshot().Requests) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Name: "errors_total", Help: "Total number of failed requests."}, func() float64 { return float64(metrics.Snapshot().Errors) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Name: "inflight_requests", Help: "Current number of in-flight requests."}, func() float64 { return float64(metrics.Snapshot().InFlight) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Name: "runtime_goroutines", Help: "Current number of goroutines."}, func() float64 { return float64(metrics.Snapshot().Runtime.Goroutines) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Name: "runtime_heap_alloc_bytes", Help: "Current heap allocation in bytes."}, func() float64 { return float64(metrics.Snapshot().Runtime.HeapAlloc) }),
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
				continue
			}
			return err
		}
	}
	return nil
}
