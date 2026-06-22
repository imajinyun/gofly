package middleware

import (
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/rest"
)

// PrometheusMiddleware records low-cardinality request metrics backed by a
// gofly metrics registry. Route labels use gofly's route pattern to avoid
// unbounded URL cardinality.
func PrometheusMiddleware(registry *metrics.Registry) rest.Middleware {
	return rest.MetricsMiddleware(registry)
}

// PrometheusMetricsHandler exposes gofly metrics in the Prometheus text format.
func PrometheusMetricsHandler(registry *metrics.Registry) rest.HandlerFunc {
	return rest.PrometheusMetricsHandler(registry)
}
