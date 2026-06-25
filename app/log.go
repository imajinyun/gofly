// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	coretrace "github.com/imajinyun/gofly/core/observability/trace"
)

// LogConfig configures the slog logger used by the gofly runtime.
//
// Defaults applied by WithDefaults: Level="info", Format="json".
// When Format is empty or "json", a JSON handler is used; use "text" for
// human-readable console output.
type LogConfig struct {
	// Level is the minimum log level: "debug", "info", "warn", "error".
	// Default: "info".
	Level string `json:"level,omitempty"`
	// Format is the output format: "json" or "text". Default: "json".
	Format string `json:"format,omitempty"`
	// AddSource adds source file and line number to each log record.
	AddSource bool `json:"addSource,omitempty"`
	// Trace enables automatic trace context injection (trace_id, span_id)
	// into log records when a span is present in the context.
	Trace bool `json:"trace,omitempty"`
}

func NewLogger(w io.Writer, conf LogConfig) (*slog.Logger, error) {
	logger, _, err := NewLeveledLogger(w, conf)
	return logger, err
}

// NewLeveledLogger builds a logger like NewLogger but also returns the backing
// *slog.LevelVar so the caller can adjust the verbosity at runtime without
// rebuilding the logger or handler chain.
func NewLeveledLogger(w io.Writer, conf LogConfig) (*slog.Logger, *slog.LevelVar, error) {
	if w == nil {
		w = os.Stdout
	}
	level, err := parseLogLevel(conf.Level)
	if err != nil {
		return nil, nil, err
	}
	levelVar := new(slog.LevelVar)
	levelVar.Set(level)
	opts := &slog.HandlerOptions{Level: levelVar, AddSource: conf.AddSource}
	var handler slog.Handler
	switch strings.ToLower(conf.Format) {
	case "", "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, nil, fmt.Errorf("unsupported log format %q", conf.Format)
	}
	if conf.Trace {
		handler = traceHandler{next: handler}
	}
	return slog.New(handler), levelVar, nil
}

// LevelHandler exposes a small HTTP endpoint to inspect and change the log
// level backed by levelVar at runtime. GET returns the current level; PUT or
// POST with body {"level":"debug"} or form field level=debug updates it.
func LevelHandler(levelVar *slog.LevelVar) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if levelVar == nil {
			http.Error(w, "log level is not dynamic", http.StatusNotImplemented)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeLevelResponse(w, levelVar)
		case http.MethodPut, http.MethodPost:
			requested, err := readRequestedLevel(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			level, err := parseLogLevel(requested)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			levelVar.Set(level)
			writeLevelResponse(w, levelVar)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func readRequestedLevel(r *http.Request) (string, error) {
	if level := strings.TrimSpace(r.URL.Query().Get("level")); level != "" {
		return level, nil
	}
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var body struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("decode level body: %w", err)
		}
		return strings.TrimSpace(body.Level), nil
	}
	if level := strings.TrimSpace(r.FormValue("level")); level != "" {
		return level, nil
	}
	return "", fmt.Errorf("level is required")
}

func writeLevelResponse(w http.ResponseWriter, levelVar *slog.LevelVar) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"level": strings.ToLower(levelVar.Level().String()),
	})
}

func SetDefaultLogger(w io.Writer, conf LogConfig) error {
	_, err := SetDefaultLeveledLogger(w, conf)
	return err
}

// SetDefaultLeveledLogger installs a dynamic slog logger as the process default
// and returns its level control for admin endpoints or other runtime wiring.
func SetDefaultLeveledLogger(w io.Writer, conf LogConfig) (*slog.LevelVar, error) {
	logger, levelVar, err := NewLeveledLogger(w, conf)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger)
	return levelVar, nil
}

type traceHandler struct{ next slog.Handler }

func (h traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	if sc, ok := coretrace.FromContext(ctx); ok {
		record.AddAttrs(
			slog.String(coretrace.TraceIDKey, sc.TraceID),
			slog.String(coretrace.SpanIDKey, sc.SpanID),
			slog.Bool(coretrace.SampledKey, sc.Sampled),
		)
	}
	return h.next.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{next: h.next.WithGroup(name)}
}

func parseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level %q", level)
	}
}
