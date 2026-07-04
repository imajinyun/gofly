package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/imajinyun/gofly/rest"
)

// ErrOpenAPIPathsRequired reports that an OpenAPI document has no importable paths.
var ErrOpenAPIPathsRequired = errors.New("openapi paths are required")

// OpenAPIRouteOptions controls how an OpenAPI contract is exposed through the gateway.
type OpenAPIRouteOptions struct {
	NamePrefix     string
	GatewayPrefix  string
	UpstreamPrefix string
	Service        string
	Targets        []string
	Timeout        time.Duration
	MaxBodyBytes   int64
	PreserveHost   bool
	Retry          RetryPolicy
	Header         HeaderPolicy
	Breaker        BreakerConfig
	RateLimit      RateLimitConfig
	Concurrency    ConcurrencyConfig
	AllowedHosts   []string
	Tags           map[string]string
	Headers        map[string]string
}

// RouteConfigsFromOpenAPI imports OpenAPI paths as JSON-friendly gateway routes.
func RouteConfigsFromOpenAPI(doc rest.OpenAPIDocument, opts OpenAPIRouteOptions) ([]RouteConfig, error) {
	if len(doc.Paths) == 0 {
		return nil, ErrOpenAPIPathsRequired
	}
	if len(opts.Targets) == 0 && strings.TrimSpace(opts.Service) == "" {
		return nil, ErrRouteRequired
	}
	paths := sortedOpenAPIPaths(doc.Paths)
	out := make([]RouteConfig, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		methods := sortedOpenAPIMethods(doc.Paths[path])
		for _, method := range methods {
			httpMethod, ok := openAPIHTTPMethod(method)
			if !ok {
				return nil, fmt.Errorf("unsupported openapi method %q for path %q", method, path)
			}
			staticPrefix, err := openAPIStaticPrefix(path)
			if err != nil {
				return nil, err
			}
			route := openAPIRouteConfig(httpMethod, path, staticPrefix, doc.Paths[path][method], opts)
			key := route.Method + " " + route.PathPrefix
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, route)
		}
	}
	if len(out) == 0 {
		return nil, ErrOpenAPIPathsRequired
	}
	return out, nil
}

// RoutesFromOpenAPI imports OpenAPI paths as runtime gateway routes.
func RoutesFromOpenAPI(doc rest.OpenAPIDocument, opts OpenAPIRouteOptions) ([]Route, error) {
	configs, err := RouteConfigsFromOpenAPI(doc, opts)
	if err != nil {
		return nil, err
	}
	routes := make([]Route, 0, len(configs))
	for _, config := range configs {
		routes = append(routes, routeFromConfig(config))
	}
	return routes, nil
}

func openAPIRouteConfig(method, path, staticPrefix string, op rest.Operation, opts OpenAPIRouteOptions) RouteConfig {
	return RouteConfig{
		Name:           openAPIRouteName(method, path, staticPrefix, op.OperationID, opts.NamePrefix),
		Method:         method,
		PathPrefix:     joinURLPath(opts.GatewayPrefix, staticPrefix),
		UpstreamPrefix: openAPIUpstreamPrefix(opts.UpstreamPrefix, staticPrefix),
		Service:        strings.TrimSpace(opts.Service),
		Targets:        append([]string(nil), opts.Targets...),
		Timeout:        opts.Timeout,
		MaxBodyBytes:   opts.MaxBodyBytes,
		PreserveHost:   opts.PreserveHost,
		Retry:          normalizeRetryPolicy(opts.Retry),
		Header:         cloneHeaderPolicy(opts.Header),
		Breaker:        opts.Breaker,
		RateLimit:      opts.RateLimit,
		Concurrency:    opts.Concurrency,
		AllowedHosts:   append([]string(nil), opts.AllowedHosts...),
		Tags:           cloneMap(opts.Tags),
		Headers:        cloneMap(opts.Headers),
	}
}

func sortedOpenAPIPaths(paths map[string]map[string]rest.Operation) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func sortedOpenAPIMethods(operations map[string]rest.Operation) []string {
	out := make([]string, 0, len(operations))
	for method := range operations {
		out = append(out, method)
	}
	sort.Strings(out)
	return out
}

func openAPIHTTPMethod(method string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case strings.ToLower(http.MethodGet):
		return http.MethodGet, true
	case strings.ToLower(http.MethodHead):
		return http.MethodHead, true
	case strings.ToLower(http.MethodPost):
		return http.MethodPost, true
	case strings.ToLower(http.MethodPut):
		return http.MethodPut, true
	case strings.ToLower(http.MethodPatch):
		return http.MethodPatch, true
	case strings.ToLower(http.MethodDelete):
		return http.MethodDelete, true
	case strings.ToLower(http.MethodOptions):
		return http.MethodOptions, true
	default:
		return "", false
	}
}

func openAPIStaticPrefix(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("openapi path is required")
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("openapi path must start with /: %s", path)
	}
	if path == "/" {
		return "/", nil
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	static := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("openapi path has empty segment: %s", path)
		}
		if strings.Contains(segment, "{") || strings.Contains(segment, "}") {
			break
		}
		static = append(static, segment)
	}
	if len(static) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(static, "/"), nil
}

func openAPIUpstreamPrefix(prefix, staticPrefix string) string {
	if strings.TrimSpace(prefix) == "" && staticPrefix == "/" {
		return ""
	}
	return joinURLPath(prefix, staticPrefix)
}

func openAPIRouteName(method, path, staticPrefix, operationID, namePrefix string) string {
	if operationID = strings.TrimSpace(operationID); operationID != "" {
		return strings.TrimSpace(namePrefix) + operationID
	}
	name := strings.ToLower(method) + strings.ReplaceAll(staticPrefix, "/", "-")
	if staticPrefix == "/" {
		name = strings.ToLower(method) + "-root"
	}
	if strings.Contains(path, "{") {
		name += "-wildcard"
	}
	return strings.Trim(namePrefix+"-"+name, "-")
}
