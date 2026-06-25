// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"net/http"
	"strings"

	"github.com/imajinyun/gofly/rest"
)

// RegisterREST registers gateway routes as REST server routes.
func (g *Gateway) RegisterREST(s *rest.Server, opts ...rest.RouteOption) {
	if g == nil || s == nil {
		return
	}
	s.AddReadyCheck("gateway", g.HealthCheck)
	routes := g.Routes()
	seen := make(map[string]struct{})
	for _, route := range routes {
		methods := routeMethods(route.Method)
		patterns := restPatterns(route.PathPrefix)
		for _, method := range methods {
			for _, pattern := range patterns {
				key := method + " " + pattern
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				s.AddRoute(rest.Route{Method: method, Path: pattern, Handler: func(ctx *rest.Context) {
					g.ServeHTTP(ctx.Response, ctx.Request)
				}}, opts...)
			}
		}
	}
}

func routeMethods(method string) []string {
	if method != "" {
		return []string{method}
	}
	return []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions}
}

func restPatterns(prefix string) []string {
	if prefix == "/" {
		return []string{"/{path...}"}
	}
	return []string{prefix, strings.TrimRight(prefix, "/") + "/{path...}"}
}
