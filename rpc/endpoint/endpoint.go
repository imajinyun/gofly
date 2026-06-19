// Package endpoint provides transport-neutral abstractions for RPC middleware:
//
//   - Endpoint — the primitive unit of remote-procedure-call work.
//   - Middleware — decorators that wrap an Endpoint with cross-cutting concerns.
//
// The Endpoint / Middleware / Chain pattern was popularised by Go kit
// (github.com/go-kit/kit/endpoint) and is widely adopted in the Go ecosystem.
// This implementation is original — the function signatures differ from those
// in Go kit, Kitex, or any other specific framework — but the conceptual
// decorator-chain pattern is a well-known Go idiom.
package endpoint

import "context"

// Endpoint is the transport-neutral unit of work used by RPC clients and servers.
type Endpoint func(ctx context.Context, req any) (any, error)

// Middleware wraps an Endpoint with cross-cutting behavior.
type Middleware func(Endpoint) Endpoint
