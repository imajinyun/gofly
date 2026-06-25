package middleware

import (
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/rest"
)

// SSEStream writes a stream of server-sent events, stopping when the request
// context is cancelled or the events channel is closed.
func SSEStream(c *rest.Context, events <-chan rest.SSEEvent) error {
	if c == nil || c.Request == nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "request context is required")
	}
	if events == nil {
		return nil
	}
	for {
		select {
		case <-c.Request.Context().Done():
			return c.Request.Context().Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := c.SSE(event); err != nil {
				return err
			}
		}
	}
}
