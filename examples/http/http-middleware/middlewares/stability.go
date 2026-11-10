package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/rest"
)

// RateLimitConfig configures token-bucket request limiting.
type RateLimitConfig struct {
	Rate  int
	Burst int
}

// RecoverMiddleware converts panics into standard JSON 500 responses.
func RecoverMiddleware() rest.Middleware {
	return rest.RecoverMiddleware()
}

// RateLimitMiddleware rejects requests above the configured token-bucket rate.
func RateLimitMiddleware(config RateLimitConfig) rest.Middleware {
	return rest.RateLimitMiddleware(config.Rate, config.Burst)
}

// MaxConcurrencyMiddleware limits simultaneously executing requests.
func MaxConcurrencyMiddleware(limit int) rest.Middleware {
	return rest.MaxConcurrencyMiddleware(limit)
}

// CircuitBreakerConfig configures a simple consecutive-failure circuit breaker.
type CircuitBreakerConfig struct {
	FailureThreshold int
	OpenTimeout      time.Duration
}

// CircuitBreakerMiddleware opens after consecutive 5xx responses and sheds
// load while the circuit is open.
func CircuitBreakerMiddleware(config CircuitBreakerConfig) rest.Middleware {
	brk := breaker.New(
		breaker.WithFailureThreshold(defaultPositive(config.FailureThreshold, 3)),
		breaker.WithOpenTimeout(defaultDuration(config.OpenTimeout, time.Second)),
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := brk.Do(r.Context(), func() error {
				sw := &statusCaptureResponseWriter{ResponseWriter: w, status: http.StatusOK}
				next.ServeHTTP(sw, r)
				if sw.status >= http.StatusInternalServerError {
					return http.ErrAbortHandler
				}
				return nil
			})
			if errors.Is(err, breaker.ErrOpen) {
				rest.WriteError(w, coreerrors.New(coreerrors.CodeUnavailable, err.Error()))
			}
		})
	}
}

// AdaptiveLimitConfig configures adaptive overload protection.
type AdaptiveLimitConfig struct {
	MinLimit         int
	MaxLimit         int
	InitialLimit     int
	Window           time.Duration
	TargetLatency    time.Duration
	TargetErrorRatio float64
	MinSamples       int64
	CPUThreshold     int
	CPUReader        func() int
}

// AdaptiveLimitMiddleware adjusts concurrency based on latency, error ratio,
// and optional CPU pressure.
func AdaptiveLimitMiddleware(config AdaptiveLimitConfig) rest.Middleware {
	options := []limit.AdaptiveLimiterOption{
		limit.WithAdaptiveLimits(config.MinLimit, config.MaxLimit),
		limit.WithAdaptiveInitialLimit(config.InitialLimit),
		limit.WithAdaptiveLimitWindow(config.Window),
		limit.WithAdaptiveTargetLatency(config.TargetLatency),
		limit.WithAdaptiveTargetErrorRatio(config.TargetErrorRatio),
		limit.WithAdaptiveMinSamples(config.MinSamples),
		limit.WithAdaptiveCPUThreshold(config.CPUThreshold),
	}
	if config.CPUReader != nil {
		options = append(options, limit.WithAdaptiveCPUReader(config.CPUReader))
	}
	return rest.AdaptiveRateLimitMiddleware(limit.NewAdaptiveLimiter(options...))
}

func defaultPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func defaultDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

type statusCaptureResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusCaptureResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCaptureResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusCaptureResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusCaptureResponseWriter) Flush() {
	_ = w.FlushError()
}

func (w *statusCaptureResponseWriter) FlushError() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusCaptureResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusCaptureResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}
