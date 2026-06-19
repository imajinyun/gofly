// Package metadata provides a request-scoped key-value map that propagates
// through RPC and REST call chains. It is the gofly equivalent of gRPC
// metadata, stored in context.Context and safe for concurrent read-only use.
package metadata

import "context"

type contextKey struct{}

// MD is a string-to-string metadata map.
type MD map[string]string

// New creates an MD from alternating key/value strings.
func New(kv ...string) MD {
	md := make(MD)
	for i := 0; i+1 < len(kv); i += 2 {
		md[kv[i]] = kv[i+1]
	}
	return md
}

// FromContext extracts MD from ctx. The returned MD is a clone and safe to
// mutate without affecting the stored value.
func FromContext(ctx context.Context) (MD, bool) {
	md, ok := ctx.Value(contextKey{}).(MD)
	if !ok {
		return nil, false
	}
	return md.Clone(), true
}

// NewContext returns a new context carrying md. The stored value is a clone
// of the supplied map.
func NewContext(ctx context.Context, md MD) context.Context {
	return context.WithValue(ctx, contextKey{}, md.Clone())
}

// Append adds key/value pairs to the MD stored in ctx, creating a new MD if
// none exists. Keys are overwritten on duplicate.
func Append(ctx context.Context, kv ...string) context.Context {
	md, _ := FromContext(ctx)
	if md == nil {
		md = make(MD)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		md[kv[i]] = kv[i+1]
	}
	return NewContext(ctx, md)
}

// Clone returns a shallow copy of md.
func (md MD) Clone() MD {
	if len(md) == 0 {
		return MD{}
	}
	out := make(MD, len(md))
	for k, v := range md {
		out[k] = v
	}
	return out
}

// Get returns the value for key or "" if md is nil or the key is absent.
func (md MD) Get(key string) string {
	if md == nil {
		return ""
	}
	return md[key]
}
