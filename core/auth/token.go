// Package auth provides request authentication primitives for gofly services,
// including bearer-token extraction, static validation and RBAC helpers on a
// Principal type.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
)

const (
	// AuthorizationHeader is the HTTP header carrying bearer tokens.
	AuthorizationHeader = "Authorization"
	// BearerPrefix is the literal prefix expected before the token value.
	BearerPrefix = "Bearer "
	// MetadataKey is the metadata map key used for authorization tokens.
	MetadataKey = "authorization"
)

var (
	// ErrMissingCredentials is returned when no token is present.
	ErrMissingCredentials = errors.New("missing credentials")
	// ErrInvalidCredentials is returned when a token fails validation.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrPermissionDenied is returned when an authenticated principal lacks the
	// role or permission required to perform an action.
	ErrPermissionDenied = errors.New("permission denied")
)

type contextKey struct{}

// Principal is the authenticated identity attached to a request context. Beyond
// the Subject it carries RBAC material (Roles and Permissions) and arbitrary
// claim metadata.
type Principal struct {
	Subject     string
	Roles       []string
	Permissions []string
	Claims      map[string]any
}

// HasRole reports whether the principal owns the given role.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole reports whether the principal owns at least one of the roles.
func (p Principal) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if p.HasRole(role) {
			return true
		}
	}
	return false
}

// HasAllRoles reports whether the principal owns every listed role.
func (p Principal) HasAllRoles(roles ...string) bool {
	for _, role := range roles {
		if !p.HasRole(role) {
			return false
		}
	}
	return true
}

// HasPermission reports whether the principal holds the given permission.
// A permission ending in ":*" (or "*") acts as a wildcard prefix grant, so a
// principal holding "orders:*" satisfies a required "orders:read".
func (p Principal) HasPermission(permission string) bool {
	for _, granted := range p.Permissions {
		if permissionMatches(granted, permission) {
			return true
		}
	}
	return false
}

// HasAnyPermission reports whether the principal holds at least one permission.
func (p Principal) HasAnyPermission(permissions ...string) bool {
	for _, permission := range permissions {
		if p.HasPermission(permission) {
			return true
		}
	}
	return false
}

// HasAllPermissions reports whether the principal holds every permission.
func (p Principal) HasAllPermissions(permissions ...string) bool {
	for _, permission := range permissions {
		if !p.HasPermission(permission) {
			return false
		}
	}
	return true
}

func permissionMatches(granted, required string) bool {
	if granted == required || granted == "*" {
		return true
	}
	if strings.HasSuffix(granted, ":*") {
		prefix := strings.TrimSuffix(granted, "*")
		return strings.HasPrefix(required, prefix)
	}
	return false
}

// Validator validates a token and returns an enriched context carrying the
// authenticated Principal.
type Validator func(ctx context.Context, token string) (context.Context, error)

// NewContext returns a context carrying principal.
func NewContext(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

// FromContext extracts the Principal from ctx.
func FromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

// SubjectFromContext returns the principal's Subject or "" if absent.
func SubjectFromContext(ctx context.Context) string {
	principal, ok := FromContext(ctx)
	if !ok {
		return ""
	}
	return principal.Subject
}

// StaticTokenValidator returns a Validator that compares the presented token
// with expected using constant-time comparison.
func StaticTokenValidator(expected string, subject string) Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		if expected == "" || token == "" {
			return ctx, ErrMissingCredentials
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
			return ctx, ErrInvalidCredentials
		}
		return NewContext(ctx, Principal{Subject: subject}), nil
	}
}

// ExtractBearer parses a Bearer token from an Authorization header value.
func ExtractBearer(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if len(header) < len(BearerPrefix) || !strings.EqualFold(header[:len(BearerPrefix)], BearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(BearerPrefix):])
	return token, token != ""
}

// BearerValue returns the full header value for a raw token string.
func BearerValue(token string) string {
	if token == "" {
		return ""
	}
	return BearerPrefix + token
}
