// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import "strings"

// RouterGroup is a collection of routes sharing a common path prefix and
// middleware stack.
type RouterGroup struct {
	server      *Server
	prefix      string
	middlewares []Middleware
	options     []RouteOption
}

// Group creates a new RouterGroup with the given prefix and middleware.
func (s *Server) Group(prefix string, mws ...Middleware) *RouterGroup {
	return &RouterGroup{server: s, prefix: cleanPrefix(prefix), middlewares: append([]Middleware(nil), mws...)}
}

func (g *RouterGroup) Use(mw Middleware) {
	g.middlewares = append(g.middlewares, mw)
}

func (g *RouterGroup) With(opts ...RouteOption) {
	g.options = append(g.options, opts...)
}

func (g *RouterGroup) AddRoute(r Route, opts ...RouteOption) {
	r.Path = joinPath(g.prefix, r.Path)
	r.Middlewares = append(append([]Middleware(nil), g.middlewares...), r.Middlewares...)
	allOpts := append(append([]RouteOption(nil), g.options...), opts...)
	g.server.AddRoute(r, allOpts...)
}

func (g *RouterGroup) AddRoutes(routes []Route, opts ...RouteOption) {
	for _, r := range routes {
		g.AddRoute(r, opts...)
	}
}

func cleanPrefix(prefix string) string {
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

func joinPath(prefix, path string) string {
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if prefix == "" {
		return path
	}
	return prefix + path
}
