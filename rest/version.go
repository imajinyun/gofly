// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

const (
	// DefaultAPIVersionHeader is the default header for API version selection.
	DefaultAPIVersionHeader = "X-API-Version"
	// DefaultAPIVersionQuery is the default query parameter for API version selection.
	DefaultAPIVersionQuery = "version"
)

type apiVersionKey struct{}

// APIVersionConfig controls API version negotiation.
type APIVersionConfig struct {
	Default        string
	Supported      []string
	HeaderName     string
	QueryName      string
	PathPrefix     bool
	ResponseHeader string
}

// APIVersionMiddleware returns middleware that resolves and validates API versions.
func APIVersionMiddleware(c APIVersionConfig) Middleware {
	c = resolveAPIVersionConfig(c)
	supported := make(map[string]struct{}, len(c.Supported))
	for _, version := range c.Supported {
		if version != "" {
			supported[version] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			version := resolveAPIVersion(r, c)
			if version == "" {
				version = c.Default
			}
			if len(supported) > 0 {
				if _, ok := supported[version]; !ok {
					writeError(w, http.StatusBadRequest, coreerrors.CodeInvalidArgument, "unsupported api version")
					return
				}
			}
			if c.ResponseHeader != "" {
				w.Header().Set(c.ResponseHeader, version)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiVersionKey{}, version)))
		})
	}
}

func APIVersionFromContext(ctx context.Context) string {
	version, _ := ctx.Value(apiVersionKey{}).(string)
	return version
}

func (c *Context) APIVersion() string { return APIVersionFromContext(c.Request.Context()) }

func resolveAPIVersionConfig(c APIVersionConfig) APIVersionConfig {
	if c.Default == "" {
		c.Default = "v1"
	}
	if c.HeaderName == "" {
		c.HeaderName = DefaultAPIVersionHeader
	}
	if c.QueryName == "" {
		c.QueryName = DefaultAPIVersionQuery
	}
	if c.ResponseHeader == "" {
		c.ResponseHeader = c.HeaderName
	}
	return c
}

func resolveAPIVersion(r *http.Request, c APIVersionConfig) string {
	if c.HeaderName != "" {
		if version := strings.TrimSpace(r.Header.Get(c.HeaderName)); version != "" {
			return version
		}
	}
	if c.QueryName != "" {
		if version := strings.TrimSpace(r.URL.Query().Get(c.QueryName)); version != "" {
			return version
		}
	}
	if c.PathPrefix {
		return apiVersionFromPath(r.URL.Path)
	}
	return ""
}

var apiVersionPathRE = regexp.MustCompile(`^/v[0-9]+(?:/|$)`)

func apiVersionFromPath(path string) string {
	match := apiVersionPathRE.FindString(path)
	if match == "" {
		return ""
	}
	return strings.Trim(match, "/")
}
