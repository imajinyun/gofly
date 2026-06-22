package middleware

import (
	"context"
	"time"

	"github.com/gofly/gofly/rest"
)

// WebSocketConfig configures WebSocket upgrade limits and metrics accounting.
type WebSocketConfig struct {
	Manager         *rest.WebSocketManager
	MaxMessageBytes int64
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
}

// WebSocketEcho upgrades the request to a bounded WebSocket echo loop and
// records manager stats. Use it from a route handler after copying this file
// into internal/middleware.
func WebSocketEcho(c *rest.Context, config WebSocketConfig) error {
	config = resolveWebSocketConfig(config)
	return c.WebSocket(func(_ context.Context, conn *rest.WebSocketConn) {
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, payload); err != nil {
				return
			}
		}
	}, rest.WithWebSocketManager(config.Manager), rest.WithWebSocketMaxMessageBytes(config.MaxMessageBytes), rest.WithWebSocketReadTimeout(config.ReadTimeout), rest.WithWebSocketWriteTimeout(config.WriteTimeout))
}

func resolveWebSocketConfig(config WebSocketConfig) WebSocketConfig {
	if config.Manager == nil {
		config.Manager = rest.DefaultWebSocketManager
	}
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = 64 * 1024
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = 30 * time.Second
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 5 * time.Second
	}
	return config
}
