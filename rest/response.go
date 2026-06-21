// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	coreerrors "github.com/gofly/gofly/core/errors"
)

// ErrorResponse is the standard JSON error envelope for REST handlers.
type ErrorResponse struct {
	Code    coreerrors.Code     `json:"code"`
	Text    string              `json:"text"`
	Message string              `json:"message,omitempty"`
	Status  int                 `json:"status,omitempty"`
	Fields  []ValidationFailure `json:"fields,omitempty"`
}

// WriteError writes a JSON error response derived from err.
func WriteError(w http.ResponseWriter, err error) {
	code := coreerrors.CodeOf(err)
	status := coreerrors.HTTPStatus(code)
	fields := ValidationFailuresOf(err)
	if len(fields) > 0 {
		code = coreerrors.CodeInvalidArgument
		status = http.StatusBadRequest
	}
	writeError(w, status, code, coreerrors.TextOf(err), fields)
}

func writeError(w http.ResponseWriter, status int, code coreerrors.Code, text string, fieldGroups ...[]ValidationFailure) {
	var fields []ValidationFailure
	if len(fieldGroups) > 0 {
		fields = fieldGroups[0]
	}
	if code == "" {
		code = coreerrors.CodeInternal
	}
	if text == "" {
		text = http.StatusText(status)
	}
	if text == "" {
		text = string(code)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Code: code, Text: text, Message: text, Status: status, Fields: fields})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func newStatusResponseWriter(w http.ResponseWriter) *statusResponseWriter {
	return &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *statusResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *statusResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
