// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/imajinyun/gofly/core/governance"
	controladmin "github.com/imajinyun/gofly/ops/admin"
	"github.com/imajinyun/gofly/rest"
)

const defaultAdminPathPrefix = "/_gofly/gateway"

// RegisterAdmin registers gateway admin endpoints on s.
func (g *Gateway) RegisterAdmin(s *rest.Server, pathPrefix string, token string, opts ...rest.RouteOption) {
	g.RegisterAdminWithAudit(s, pathPrefix, token, nil, opts...)
}

func (g *Gateway) RegisterAdminWithAudit(s *rest.Server, pathPrefix string, token string, audit controladmin.AuditSink, opts ...rest.RouteOption) {
	if g == nil || s == nil {
		return
	}
	pathPrefix = controladmin.CleanPathPrefix(pathPrefix, defaultAdminPathPrefix)
	if audit != nil {
		opts = append([]rest.RouteOption{rest.WithMiddlewares(controladmin.AuditMiddleware("gateway", audit))}, opts...)
	}
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: pathPrefix + "/snapshot", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, g.Snapshot())
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: pathPrefix + "/discovery", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, g.Snapshot().Discovery)
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: pathPrefix + "/routes", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, sanitizedGatewayRouteConfigs(g.RouteConfigs()))
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: pathPrefix + "/descriptors", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, g.Descriptors())
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodPost, Path: pathPrefix + "/routes", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		conf, ok := decodeGatewayRouteConfig(ctx)
		if !ok {
			return
		}
		if err := g.AddRoute(routeFromConfig(conf)); err != nil {
			writeGatewayRouteError(ctx, err)
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusCreated, sanitizedGatewayRouteConfig(routeConfigFromRoute(routeFromConfig(conf))))
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodPut, Path: pathPrefix + "/routes", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		conf, ok := decodeGatewayRouteConfig(ctx)
		if !ok {
			return
		}
		if err := g.UpsertRoute(routeFromConfig(conf)); err != nil {
			writeGatewayRouteError(ctx, err)
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, sanitizedGatewayRouteConfig(routeConfigFromRoute(routeFromConfig(conf))))
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodDelete, Path: pathPrefix + "/routes", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		query := ctx.Request.URL.Query()
		if !g.RemoveRoute(query.Get("method"), query.Get("pathPrefix")) {
			controladmin.WriteError(ctx.Response, http.StatusNotFound, ErrNoRoute.Error())
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, map[string]string{"status": "ok"})
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: pathPrefix + "/metrics", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		ctx.Response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err := g.stats.WritePrometheus(ctx.Response); err != nil {
			slog.Error("gateway write prometheus failed", "error", err)
		}
	}}, opts...)
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: pathPrefix + "/health", Handler: func(ctx *rest.Context) {
		if !authorizeGatewayAdmin(ctx, token) {
			return
		}
		if err := g.HealthCheck(ctx.Request.Context()); err != nil {
			controladmin.WriteJSON(ctx.Response, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "error": controladmin.ErrorBody{Code: controladmin.ErrorCode(http.StatusServiceUnavailable), Message: err.Error()}})
			return
		}
		controladmin.WriteJSON(ctx.Response, http.StatusOK, map[string]string{"status": "ok"})
	}}, opts...)
	governanceAdmin := governance.NewAdmin(g.rules, nil,
		governance.WithAdminManager(g.manager),
		governance.WithAdminPathPrefix(pathPrefix+"/governance"),
		governance.WithAdminDefaultRequest(governance.Request{Transport: governance.TransportGateway}),
	)
	for _, route := range controladmin.GovernanceEndpoints(controladmin.WithGovernanceRoot(), controladmin.WithGovernanceExplain()) {
		route := route
		s.AddRoute(rest.Route{Method: route.Method, Path: pathPrefix + route.Path, Handler: func(ctx *rest.Context) {
			if !authorizeGatewayAdmin(ctx, token) {
				return
			}
			governanceAdmin.ServeHTTP(ctx.Response, ctx.Request)
		}}, opts...)
	}
}

func decodeGatewayRouteConfig(ctx *rest.Context) (RouteConfig, bool) {
	var conf RouteConfig
	if err := json.NewDecoder(ctx.Request.Body).Decode(&conf); err != nil {
		controladmin.WriteError(ctx.Response, http.StatusBadRequest, err.Error())
		return RouteConfig{}, false
	}
	return conf, true
}

func writeGatewayRouteError(ctx *rest.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrRouteRequired):
		status = http.StatusBadRequest
	case errors.Is(err, ErrRouteExists):
		status = http.StatusConflict
	case errors.Is(err, ErrNoRoute):
		status = http.StatusNotFound
	}
	controladmin.WriteError(ctx.Response, status, err.Error())
}

func authorizeGatewayAdmin(ctx *rest.Context, token string) bool {
	return controladmin.AuthorizeBearerOrLocal(ctx.Response, ctx.Request, token, gatewayAdminErrorWriter(ctx))
}

func gatewayAdminErrorWriter(ctx *rest.Context) controladmin.ErrorWriter {
	return func(_ http.ResponseWriter, status int, message string) {
		controladmin.WriteError(ctx.Response, status, message)
	}
}

func sanitizedGatewayRouteConfigs(routes []RouteConfig) []RouteConfig {
	out := make([]RouteConfig, 0, len(routes))
	for _, route := range routes {
		out = append(out, sanitizedGatewayRouteConfig(route))
	}
	return out
}

func sanitizedGatewayRouteConfig(route RouteConfig) RouteConfig {
	route.Headers = controladmin.MaskSensitiveMap(route.Headers)
	route.Header.SetRequest = controladmin.MaskSensitiveMap(route.Header.SetRequest)
	route.Header.SetResponse = controladmin.MaskSensitiveMap(route.Header.SetResponse)
	for i := range route.Canary {
		route.Canary[i].Headers = controladmin.MaskSensitiveMap(route.Canary[i].Headers)
		route.Canary[i].MatchHeaders = controladmin.MaskSensitiveMap(route.Canary[i].MatchHeaders)
	}
	for i := range route.Shadow {
		route.Shadow[i].Headers = controladmin.MaskSensitiveMap(route.Shadow[i].Headers)
	}
	return route
}
