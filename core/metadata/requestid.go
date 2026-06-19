// Package metadata provides a request-scoped key-value map that propagates
// through RPC and REST call chains.
package metadata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// RequestIDKey is the metadata key for request identifiers.
const RequestIDKey = "request_id"

// RequestIDFromContext extracts the request_id stored in the context metadata.
// Returns an empty string when absent.
func RequestIDFromContext(ctx context.Context) string {
	md, ok := FromContext(ctx)
	if !ok {
		return ""
	}
	return md.Get(RequestIDKey)
}

// NewRequestID returns a 32-hex-character random identifier. Falls back to
// a base-36 timestamp if the system RNG fails.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// WithRequestID returns a derived context carrying the given request id in
// its metadata. If id is empty a fresh id is generated.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		id = NewRequestID()
	}
	return Append(ctx, RequestIDKey, id)
}

// EnsureRequestID returns ctx unchanged when it already carries a request id,
// or a derived context with a freshly generated id otherwise. The second return
// value is the resolved id.
func EnsureRequestID(ctx context.Context) (context.Context, string) {
	if id := RequestIDFromContext(ctx); id != "" {
		return ctx, id
	}
	id := NewRequestID()
	return WithRequestID(ctx, id), id
}
