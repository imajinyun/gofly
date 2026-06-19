// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"fmt"
	"net/http"
	"strings"
)

// SSEEvent is a single Server-Sent Events payload.
type SSEEvent struct {
	Event string
	ID    string
	Retry int
	Data  string
}

// SSE writes a Server-Sent Event to the response.
func (c *Context) SSE(event SSEEvent) error {
	c.Response.Header().Set("Content-Type", "text/event-stream")
	c.Response.Header().Set("Cache-Control", "no-cache")
	c.Response.Header().Set("Connection", "keep-alive")
	if event.ID != "" {
		if _, err := fmt.Fprintf(c.Response, "id: %s\n", sanitizeSSELine(event.ID)); err != nil {
			return err
		}
	}
	if event.Event != "" {
		if _, err := fmt.Fprintf(c.Response, "event: %s\n", sanitizeSSELine(event.Event)); err != nil {
			return err
		}
	}
	if event.Retry > 0 {
		if _, err := fmt.Fprintf(c.Response, "retry: %d\n", event.Retry); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(event.Data, "\n") {
		if _, err := fmt.Fprintf(c.Response, "data: %s\n", strings.TrimSuffix(line, "\r")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(c.Response, "\n"); err != nil {
		return err
	}
	if flusher, ok := c.Response.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func sanitizeSSELine(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return value
}
