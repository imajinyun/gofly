package middleware

import (
	"github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/rest"
)

// OpenTelemetryConfig configures W3C trace propagation middleware.
type OpenTelemetryConfig struct {
	Service string
	Sampler trace.Sampler
}

// OpenTelemetryMiddleware propagates W3C traceparent headers and attaches the
// span context to the request. The default sampler keeps parent-based behavior
// while sampling local roots, which is useful for examples and small services.
func OpenTelemetryMiddleware(config OpenTelemetryConfig) rest.Middleware {
	if config.Sampler == nil {
		config.Sampler = trace.ParentBasedSampler(trace.AlwaysSampler())
	}
	return rest.TraceMiddlewareWithSampler(config.Service, config.Sampler)
}
