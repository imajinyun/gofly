// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"context"
	"net/http"
	"sort"
	"time"
)

// CheckFunc is a health or readiness probe.
type CheckFunc func(context.Context) error

// CheckReport aggregates the results of all health checks.
type CheckReport struct {
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks,omitempty"`
}

// CheckResult is the outcome of a single health check.
type CheckResult struct {
	Status   string        `json:"status"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

// WithHealthCheck adds a liveness probe to the server.
func WithHealthCheck(name string, check CheckFunc) Option {
	return func(s *Server) {
		s.AddHealthCheck(name, check)
	}
}

// WithReadyCheck adds a readiness probe to the server.
func WithReadyCheck(name string, check CheckFunc) Option {
	return func(s *Server) {
		s.AddReadyCheck(name, check)
	}
}

// AddHealthCheck registers a liveness probe.
func (s *Server) AddHealthCheck(name string, check CheckFunc) {
	if name == "" || check == nil {
		return
	}
	if s.health == nil {
		s.health = make(map[string]CheckFunc)
	}
	s.health[name] = check
}

func (s *Server) AddReadyCheck(name string, check CheckFunc) {
	if name == "" || check == nil {
		return
	}
	if s.ready == nil {
		s.ready = make(map[string]CheckFunc)
	}
	s.ready[name] = check
}

func (s *Server) runChecks(ctx context.Context, checks map[string]CheckFunc) CheckReport {
	report := CheckReport{Status: "ok"}
	if len(checks) == 0 {
		return report
	}
	report.Checks = make(map[string]CheckResult, len(checks))
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		start := time.Now()
		err := s.runCheck(ctx, checks[name])
		result := CheckResult{Status: "ok", Duration: time.Since(start)}
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			report.Status = "failed"
		}
		report.Checks[name] = result
	}
	return report
}

func (s *Server) runCheck(ctx context.Context, check CheckFunc) error {
	timeout := s.healthTimeout()
	if timeout <= 0 {
		return check(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return check(ctx)
}

func writeCheckReport(ctx *Context, report CheckReport) {
	status := http.StatusOK
	if report.Status != "ok" {
		status = http.StatusServiceUnavailable
	}
	ctx.JSON(status, report)
}
