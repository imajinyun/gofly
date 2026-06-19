// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import "time"

const (
	serverStateInitialized int32 = iota
	serverStateStarting
	serverStateRunning
	serverStateStopping
	serverStateStopped
)

type StateSnapshot struct {
	Address           string        `json:"address"`
	AdvertiseEndpoint string        `json:"advertiseEndpoint,omitempty"`
	State             string        `json:"state"`
	Since             time.Time     `json:"since"`
	For               time.Duration `json:"for"`
}

func (s *HTTPServer) State() StateSnapshot {
	since := time.Unix(0, s.since.Load())
	return StateSnapshot{
		Address:           s.opts.addr,
		AdvertiseEndpoint: s.opts.advertiseEndpoint,
		State:             serverStateName(s.state.Load()),
		Since:             since,
		For:               time.Since(since),
	}
}

func (s *HTTPServer) setState(state int32) {
	s.state.Store(state)
	s.since.Store(time.Now().UnixNano())
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
