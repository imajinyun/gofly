// Package admin provides HTTP handlers and types for gofly control-plane
// endpoints: health, metrics, governance rules and diagnostics.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/security"
)

// Endpoint describes a registered admin HTTP route.
type Endpoint struct {
	Method string
	Path   string
}

// ErrorWriter renders an HTTP error response.
type ErrorWriter func(http.ResponseWriter, int, string)

// ErrorResponse is the standard JSON error envelope for admin endpoints.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the error code and human-readable message.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AuditEvent records a single admin endpoint invocation.
type AuditEvent struct {
	Component  string    `json:"component"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	RemoteAddr string    `json:"remoteAddr,omitempty"`
	Status     int       `json:"status"`
	Duration   string    `json:"duration"`
	At         time.Time `json:"at"`
}

// AuditSink receives audit events for persistence or forwarding.
type AuditSink func(context.Context, AuditEvent)

// GovernanceEndpointOption customises governance endpoint behaviour.
type GovernanceEndpointOption func(*governanceEndpointConfig)

type governanceEndpointConfig struct {
	root    bool
	explain bool
}

// WithGovernanceRoot enables the root governance endpoint.
func WithGovernanceRoot() GovernanceEndpointOption {
	return func(c *governanceEndpointConfig) {
		c.root = true
	}
}

func WithGovernanceExplain() GovernanceEndpointOption {
	return func(c *governanceEndpointConfig) {
		c.explain = true
	}
}

func CleanPathPrefix(path string, fallback string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return fallback
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Code: ErrorCode(status), Message: message}})
}

func AuditMiddleware(component string, sink AuditSink) func(http.Handler) http.Handler {
	if sink == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	component = strings.TrimSpace(component)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			Audit(r.Context(), sink, AuditEvent{
				Component:  component,
				Method:     r.Method,
				Path:       r.URL.Path,
				RemoteAddr: r.RemoteAddr,
				Status:     rec.status,
				Duration:   time.Since(start).String(),
				At:         time.Now(),
			})
		})
	}
}

func Audit(ctx context.Context, sink AuditSink, event AuditEvent) {
	if sink == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	sink(ctx, event)
}

func SlogAuditSink(logger *slog.Logger) AuditSink {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, event AuditEvent) {
		logger.InfoContext(ctx, "admin audit",
			"component", event.Component,
			"method", event.Method,
			"path", event.Path,
			"remote_addr", event.RemoteAddr,
			"status", event.Status,
			"duration", event.Duration,
		)
	}
}

type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func ErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusServiceUnavailable:
		return "unavailable"
	default:
		if status >= 500 {
			return "internal"
		}
		return "error"
	}
}

func MaskSensitiveMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if IsSensitiveKey(key) {
			out[key] = "***"
			continue
		}
		out[key] = value
	}
	return out
}

func IsSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"authorization", "cookie", "token", "secret", "password", "passwd", "api-key", "apikey", "credential"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func AuthorizeBearerOrLocal(
	w http.ResponseWriter,
	r *http.Request,
	token string,
	writeError ErrorWriter,
) bool {
	if writeError == nil {
		writeError = WriteError
	}
	if token == "" {
		if security.IsLocalRemote(r.RemoteAddr) {
			return true
		}
		writeError(w, http.StatusForbidden, "admin is only available from localhost without token")
		return false
	}
	got, ok := auth.ExtractBearer(r.Header.Get(auth.AuthorizationHeader))
	if !ok || !security.ConstantTimeEqual(got, token) {
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return false
	}
	return true
}

func GovernanceEndpoints(opts ...GovernanceEndpointOption) []Endpoint {
	conf := governanceEndpointConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&conf)
		}
	}
	paths := []Endpoint{}
	if conf.root {
		paths = append(paths, Endpoint{Method: http.MethodGet, Path: "/governance"})
	}
	paths = append(paths,
		Endpoint{Method: http.MethodGet, Path: "/governance/snapshot"},
		Endpoint{Method: http.MethodGet, Path: "/governance/components"},
		Endpoint{Method: http.MethodGet, Path: "/governance/rules"},
		Endpoint{Method: http.MethodPost, Path: "/governance/rules"},
		Endpoint{Method: http.MethodPut, Path: "/governance/rules"},
		Endpoint{Method: http.MethodGet, Path: "/governance/status"},
		Endpoint{Method: http.MethodGet, Path: "/governance/stats"},
		Endpoint{Method: http.MethodGet, Path: "/governance/history"},
		Endpoint{Method: http.MethodGet, Path: "/governance/events"},
		Endpoint{Method: http.MethodGet, Path: "/governance/versions"},
		Endpoint{Method: http.MethodGet, Path: "/governance/diff"},
		Endpoint{Method: http.MethodPost, Path: "/governance/diff"},
		Endpoint{Method: http.MethodPost, Path: "/governance/rollback"},
		Endpoint{Method: http.MethodPost, Path: "/governance/reload"},
		Endpoint{Method: http.MethodPost, Path: "/governance/validate"},
	)
	if conf.explain {
		paths = append(paths, Endpoint{Method: http.MethodGet, Path: "/governance/explain"})
	}
	return paths
}
