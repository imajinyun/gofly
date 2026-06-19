// Package core provides common utilities used across gofly subsystems.
package core

import "context"

// Context returns ctx when it is non-nil, otherwise context.TODO().
//
// Library entry points use this helper to keep nil-context compatibility while
// making the fallback policy explicit and searchable. Application code should
// still pass a real request, command, or shutdown context whenever possible.
func Context(ctx context.Context) context.Context {
	if ctx == nil {
		return context.TODO()
	}
	return ctx
}
