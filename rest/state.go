// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import "time"

const (
	serverStateInitialized int32 = iota
	serverStateStarting
	serverStateRunning
	serverStateStopping
	serverStateStopped
)

// StateSnapshot captures the current server state and uptime.
type StateSnapshot struct {
	Service string        `json:"service,omitempty"`
	Address string        `json:"address"`
	State   string        `json:"state"`
	Since   time.Time     `json:"since"`
	For     time.Duration `json:"for"`
}

// State returns the current server state snapshot.
func (s *Server) State() StateSnapshot {
	since := time.Unix(0, s.stateSince.Load())
	return StateSnapshot{
		Service: s.conf.Name,
		Address: s.addr(),
		State:   serverStateName(s.state.Load()),
		Since:   since,
		For:     time.Since(since),
	}
}

func (s *Server) setState(state int32) {
	s.state.Store(state)
	s.stateSince.Store(time.Now().UnixNano())
}

func serverStateName(state int32) string {
	switch state {
	case serverStateInitialized:
		return "initialized"
	case serverStateStarting:
		return "starting"
	case serverStateRunning:
		return "running"
	case serverStateStopping:
		return "stopping"
	case serverStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
