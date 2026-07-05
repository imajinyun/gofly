package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/imajinyun/gofly/rest"
)

const defaultOpenAPIImportMaxBytes int64 = 2 << 20

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
	Transcode      OpenAPITranscodeOptions
	Groups         []OpenAPIRouteGroup
}

// OpenAPIRouteGroup overrides upstream routing for matching OpenAPI operations.
type OpenAPIRouteGroup struct {
	Name           string
	MatchTags      []string
	NamePrefix     string
	GatewayPrefix  string
	UpstreamPrefix string
	Service        string
	Targets        []string
	Headers        map[string]string
	Transcode      OpenAPITranscodeOptions
}

// OpenAPITranscodeOptions maps imported OpenAPI operations to RPC transcode routes.
type OpenAPITranscodeOptions struct {
	Enabled               bool
	Descriptor            string
	DescriptorMethod      string
	Service               string
	Method                string
	MethodFromOperationID bool
}

// OpenAPIURLSource describes a remote OpenAPI contract endpoint.
type OpenAPIURLSource struct {
	URL      string
	Client   *http.Client
	MaxBytes int64
	Headers  map[string]string
}

// RouteConfigsFromOpenAPI imports OpenAPI paths as JSON-friendly gateway routes.
func RouteConfigsFromOpenAPI(doc rest.OpenAPIDocument, opts OpenAPIRouteOptions) ([]RouteConfig, error) {
	if len(doc.Paths) == 0 {
		return nil, ErrOpenAPIPathsRequired
	}
	if !openAPIOptionsHaveAnyUpstream(opts) {
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
			op := doc.Paths[path][method]
			routeOpts := openAPIRouteOptionsForOperation(opts, op)
			if len(routeOpts.Targets) == 0 && strings.TrimSpace(routeOpts.Service) == "" {
				return nil, fmt.Errorf("openapi route %s %s: %w", httpMethod, path, ErrRouteRequired)
			}
			route := openAPIRouteConfig(httpMethod, path, staticPrefix, op, routeOpts)
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

// RouteConfigsFromOpenAPIURL fetches an OpenAPI document endpoint and imports gateway routes.
func RouteConfigsFromOpenAPIURL(ctx context.Context, source OpenAPIURLSource, opts OpenAPIRouteOptions) ([]RouteConfig, error) {
	doc, err := FetchOpenAPIDocument(ctx, source)
	if err != nil {
		return nil, err
	}
	return RouteConfigsFromOpenAPI(doc, opts)
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

// RoutesFromOpenAPIURL fetches an OpenAPI document endpoint and imports runtime gateway routes.
func RoutesFromOpenAPIURL(ctx context.Context, source OpenAPIURLSource, opts OpenAPIRouteOptions) ([]Route, error) {
	configs, err := RouteConfigsFromOpenAPIURL(ctx, source, opts)
	if err != nil {
		return nil, err
	}
	routes := make([]Route, 0, len(configs))
	for _, config := range configs {
		routes = append(routes, routeFromConfig(config))
	}
	return routes, nil
}

// FetchOpenAPIDocument loads and decodes an OpenAPI document from a trusted endpoint.
func FetchOpenAPIDocument(ctx context.Context, source OpenAPIURLSource) (rest.OpenAPIDocument, error) {
	if ctx == nil {
		return rest.OpenAPIDocument{}, errors.New("openapi fetch context is required")
	}
	endpoint, err := parseOpenAPIURL(source.URL)
	if err != nil {
		return rest.OpenAPIDocument{}, err
	}
	client := source.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil) // #nosec G107 -- endpoint is an explicit control-plane OpenAPI source validated by parseOpenAPIURL.
	if err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("build openapi request: %w", err)
	}
	for key, value := range source.Headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("fetch openapi document: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return rest.OpenAPIDocument{}, fmt.Errorf("fetch openapi document status = %d", resp.StatusCode)
	}
	maxBytes := source.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultOpenAPIImportMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("read openapi document: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return rest.OpenAPIDocument{}, fmt.Errorf("openapi document exceeds %d bytes", maxBytes)
	}
	var doc rest.OpenAPIDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("decode openapi document: %w", err)
	}
	return doc, nil
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
		Transcode:      openAPITranscodeConfig(op, opts.Transcode),
	}
}

func openAPIOptionsHaveAnyUpstream(opts OpenAPIRouteOptions) bool {
	if len(opts.Targets) > 0 || strings.TrimSpace(opts.Service) != "" {
		return true
	}
	for _, group := range opts.Groups {
		if len(group.Targets) > 0 || strings.TrimSpace(group.Service) != "" {
			return true
		}
	}
	return false
}

func openAPIRouteOptionsForOperation(opts OpenAPIRouteOptions, op rest.Operation) OpenAPIRouteOptions {
	for _, group := range opts.Groups {
		if !openAPIGroupMatchesOperation(group, op) {
			continue
		}
		selected := opts
		if group.NamePrefix != "" {
			selected.NamePrefix = group.NamePrefix
		} else if group.Name != "" {
			selected.NamePrefix = strings.Trim(group.Name, "-") + "-"
		}
		if group.GatewayPrefix != "" {
			selected.GatewayPrefix = group.GatewayPrefix
		}
		if group.UpstreamPrefix != "" {
			selected.UpstreamPrefix = group.UpstreamPrefix
		}
		if group.Service != "" {
			selected.Service = group.Service
		}
		if len(group.Targets) > 0 {
			selected.Targets = append([]string(nil), group.Targets...)
		}
		selected.Headers = mergeOpenAPIStringMaps(opts.Headers, group.Headers)
		if openAPITranscodeOptionsConfigured(group.Transcode) {
			selected.Transcode = group.Transcode
		}
		selected.Groups = nil
		return selected
	}
	opts.Headers = cloneMap(opts.Headers)
	opts.Groups = nil
	return opts
}

func openAPIGroupMatchesOperation(group OpenAPIRouteGroup, op rest.Operation) bool {
	if len(group.MatchTags) == 0 {
		return false
	}
	for _, want := range group.MatchTags {
		for _, got := range op.Tags {
			if strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(got)) {
				return true
			}
		}
	}
	return false
}

func mergeOpenAPIStringMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := cloneMap(base)
	if out == nil {
		out = make(map[string]string, len(override))
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func openAPITranscodeConfig(op rest.Operation, opts OpenAPITranscodeOptions) TranscodeConfig {
	if !opts.Enabled {
		return TranscodeConfig{}
	}
	method := strings.TrimSpace(opts.Method)
	descriptorMethod := strings.TrimSpace(opts.DescriptorMethod)
	if opts.MethodFromOperationID {
		operationMethod := openAPITranscodeMethodFromOperationID(op.OperationID)
		if method == "" {
			method = operationMethod
		}
		if descriptorMethod == "" {
			descriptorMethod = operationMethod
		}
	}
	return TranscodeConfig{
		Enabled:          true,
		Descriptor:       strings.TrimSpace(opts.Descriptor),
		DescriptorMethod: descriptorMethod,
		Service:          strings.TrimSpace(opts.Service),
		Method:           method,
	}
}

func openAPITranscodeOptionsConfigured(opts OpenAPITranscodeOptions) bool {
	return opts.Enabled ||
		strings.TrimSpace(opts.Descriptor) != "" ||
		strings.TrimSpace(opts.DescriptorMethod) != "" ||
		strings.TrimSpace(opts.Service) != "" ||
		strings.TrimSpace(opts.Method) != "" ||
		opts.MethodFromOperationID
}

func openAPITranscodeMethodFromOperationID(operationID string) string {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(operationID)
	if first == utf8.RuneError && size == 0 {
		return ""
	}
	if first == utf8.RuneError {
		return operationID
	}
	return string(unicode.ToUpper(first)) + operationID[size:]
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

func parseOpenAPIURL(rawURL string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse openapi url: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("openapi url scheme must be http or https: %s", rawURL)
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("openapi url host is required: %s", rawURL)
	}
	return endpoint, nil
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
