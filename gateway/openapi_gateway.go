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
const openAPITranscodeExtensionKey = "x-gofly-transcode"
const openAPIAggregationExtensionKey = "x-gofly-aggregation"

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

type openAPITranscodeExtension struct {
	PayloadMappings []TranscodePayloadMapping `json:"payloadMappings"`
}

type openAPIAggregationExtension struct {
	Enabled bool              `json:"enabled"`
	Steps   []AggregationStep `json:"steps"`
	Shape   AggregationShape  `json:"shape"`
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
			if aggregation := openAPIAggregationConfig(op); aggregation.Enabled {
				if err := validateOpenAPIAggregationRequestShape(op, aggregation); err != nil {
					return nil, fmt.Errorf("openapi route %s %s aggregation request shape: %w", httpMethod, path, err)
				}
			}
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
		Transcode:      openAPITranscodeConfig(path, op, opts.Transcode),
		Aggregation:    openAPIAggregationConfig(op),
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

func openAPITranscodeConfig(path string, op rest.Operation, opts OpenAPITranscodeOptions) TranscodeConfig {
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
		Payload:          openAPITranscodePayloadConfig(path, op, opts),
	}
}

func openAPITranscodePayloadConfig(path string, op rest.Operation, opts OpenAPITranscodeOptions) TranscodePayloadConfig {
	if !opts.Enabled {
		return TranscodePayloadConfig{}
	}
	payload := TranscodePayloadConfig{Mode: "openapi", PathTemplate: strings.TrimSpace(path), MergeBodyObject: true}
	for _, parameter := range op.Parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(parameter.In)) {
		case "path":
			payload.PathParams = append(payload.PathParams, name)
			payload.PathParameters = append(payload.PathParameters, openAPITranscodeParameterConfig(name, parameter.Required, parameter.Schema))
		case "query":
			payload.QueryParams = append(payload.QueryParams, name)
			payload.QueryParameters = append(payload.QueryParameters, openAPITranscodeParameterConfig(name, parameter.Required, parameter.Schema))
		case "header":
			payload.HeaderParams = append(payload.HeaderParams, name)
			payload.HeaderParameters = append(payload.HeaderParameters, openAPITranscodeParameterConfig(name, parameter.Required, parameter.Schema))
		}
	}
	if len(payload.PathParams) == 0 {
		payload.PathParams = openAPIPathTemplateParamNames(path)
		payload.PathParameters = make([]TranscodeParameterConfig, 0, len(payload.PathParams))
		for _, name := range payload.PathParams {
			payload.PathParameters = append(payload.PathParameters, TranscodeParameterConfig{Name: name, Type: "string"})
		}
	}
	if op.RequestBody != nil {
		payload.BodyRequired = op.RequestBody.Required
		payload.BodySchema = openAPITranscodeBodySchema(op.RequestBody)
	}
	if extension, ok := openAPITranscodeExtensionFromOperation(op); ok {
		payload.Mappings = append(payload.Mappings, extension.PayloadMappings...)
	}
	return payload
}

func openAPITranscodeParameterConfig(name string, required bool, schema *rest.Schema) TranscodeParameterConfig {
	parameter := TranscodeParameterConfig{Name: strings.TrimSpace(name), Required: required}
	if schema == nil {
		parameter.Type = "string"
		return parameter
	}
	parameter.Type = strings.ToLower(strings.TrimSpace(schema.Type))
	parameter.Format = strings.TrimSpace(schema.Format)
	if parameter.Type == "" {
		parameter.Type = "string"
	}
	if schema.Items != nil {
		item := openAPITranscodeParameterConfig("", false, schema.Items)
		parameter.Items = &item
	}
	return parameter
}

func openAPITranscodeBodySchema(body *rest.RequestBody) *TranscodeSchemaConfig {
	if body == nil {
		return nil
	}
	if media, ok := body.Content["application/json"]; ok && media.Schema != nil {
		return openAPITranscodeSchemaConfig(media.Schema)
	}
	for _, media := range body.Content {
		if media.Schema != nil {
			return openAPITranscodeSchemaConfig(media.Schema)
		}
	}
	return nil
}

func openAPITranscodeSchemaConfig(schema *rest.Schema) *TranscodeSchemaConfig {
	if schema == nil {
		return nil
	}
	out := &TranscodeSchemaConfig{
		Type:     strings.ToLower(strings.TrimSpace(schema.Type)),
		Format:   strings.TrimSpace(schema.Format),
		Required: append([]string(nil), schema.Required...),
	}
	if out.Type == "" {
		out.Type = "object"
	}
	if schema.Items != nil {
		out.Items = openAPITranscodeSchemaConfig(schema.Items)
	}
	if len(schema.Properties) > 0 {
		out.Properties = make(map[string]TranscodeSchemaConfig, len(schema.Properties))
		for name, property := range schema.Properties {
			if propertySchema := openAPITranscodeSchemaConfig(&property); propertySchema != nil {
				out.Properties[name] = *propertySchema
			}
		}
	}
	return out
}

func openAPITranscodeExtensionFromOperation(op rest.Operation) (openAPITranscodeExtension, bool) {
	raw, ok := op.Extensions[openAPITranscodeExtensionKey]
	if !ok {
		return openAPITranscodeExtension{}, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return openAPITranscodeExtension{}, false
	}
	var extension openAPITranscodeExtension
	if err := json.Unmarshal(data, &extension); err != nil {
		return openAPITranscodeExtension{}, false
	}
	extension.PayloadMappings = normalizeTranscodePayloadMappings(extension.PayloadMappings)
	return extension, len(extension.PayloadMappings) > 0
}

func openAPIAggregationConfig(op rest.Operation) AggregationConfig {
	extension, ok := openAPIAggregationExtensionFromOperation(op)
	if !ok {
		return AggregationConfig{}
	}
	return normalizeAggregationConfig(AggregationConfig{
		Enabled: true,
		Steps:   extension.Steps,
		Shape:   extension.Shape,
	})
}

func openAPIAggregationExtensionFromOperation(op rest.Operation) (openAPIAggregationExtension, bool) {
	raw, ok := op.Extensions[openAPIAggregationExtensionKey]
	if !ok {
		return openAPIAggregationExtension{}, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return openAPIAggregationExtension{}, false
	}
	var extension openAPIAggregationExtension
	if err := json.Unmarshal(data, &extension); err != nil {
		return openAPIAggregationExtension{}, false
	}
	extension.Steps = normalizeOpenAPIAggregationSteps(extension.Steps)
	extension.Shape = normalizeOpenAPIAggregationShape(extension.Shape)
	return extension, extension.Enabled || len(extension.Steps) > 0 || len(extension.Shape.Mappings) > 0 || extension.Shape.Mode != ""
}

func normalizeOpenAPIAggregationSteps(steps []AggregationStep) []AggregationStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]AggregationStep, 0, len(steps))
	for _, step := range steps {
		step.Name = strings.TrimSpace(step.Name)
		step.Method = strings.ToUpper(strings.TrimSpace(step.Method))
		step.Path = strings.TrimSpace(step.Path)
		step.Target = strings.TrimRight(strings.TrimSpace(step.Target), "/")
		step.Retry = normalizeRetryPolicy(step.Retry)
		if step.Path == "" {
			continue
		}
		out = append(out, step)
	}
	return out
}

func normalizeOpenAPIAggregationShape(shape AggregationShape) AggregationShape {
	shape = cloneAggregationShape(shape)
	if len(shape.Mappings) == 0 {
		return shape
	}
	out := make([]AggregationPayloadMapping, 0, len(shape.Mappings))
	for _, mapping := range shape.Mappings {
		if mapping.Target == "" {
			continue
		}
		out = append(out, mapping)
	}
	shape.Mappings = out
	return shape
}

func validateOpenAPIAggregationRequestShape(op rest.Operation, aggregation AggregationConfig) error {
	queryParams := map[string]struct{}{}
	headerParams := map[string]struct{}{}
	for _, parameter := range op.Parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(parameter.In)) {
		case "query":
			queryParams[name] = struct{}{}
		case "header":
			headerParams[name] = struct{}{}
			headerParams[strings.ToLower(name)] = struct{}{}
		}
	}
	bodySchema := openAPITranscodeBodySchema(op.RequestBody)
	for _, step := range aggregation.Steps {
		if err := validateAggregationRequestShape(step.Request); err != nil {
			return err
		}
		for _, mapping := range step.Request.QueryMappings {
			if err := validateOpenAPIAggregationMappingSource("query", mapping.Source, queryParams); err != nil {
				return err
			}
		}
		for _, mapping := range step.Request.HeaderMappings {
			if err := validateOpenAPIAggregationMappingSource("header", mapping.Source, headerParams); err != nil {
				return err
			}
		}
		for _, mapping := range step.Request.BodyMappings {
			if err := validateOpenAPIAggregationBodySource(mapping.Source, bodySchema); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOpenAPIAggregationMappingSource(kind string, source string, known map[string]struct{}) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	parts := strings.Split(source, ".")
	if len(parts) != 2 || parts[0] != kind {
		return nil
	}
	name := strings.TrimSuffix(strings.TrimSpace(parts[1]), "[]")
	if _, ok := known[name]; ok {
		return nil
	}
	if _, ok := known[strings.ToLower(name)]; ok {
		return nil
	}
	return fmt.Errorf("%s source %q references unknown OpenAPI %s parameter", kind, source, kind)
}

func validateOpenAPIAggregationBodySource(source string, schema *TranscodeSchemaConfig) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	parts := strings.Split(source, ".")
	if len(parts) == 0 || parts[0] != "body" {
		return nil
	}
	if schema == nil {
		return fmt.Errorf("body source %q requires OpenAPI requestBody schema", source)
	}
	if len(parts) == 1 {
		return nil
	}
	if err := validateTranscodeSchemaPath("body", *schema, parts[1:]); err != nil {
		return fmt.Errorf("body source %q %w", source, err)
	}
	return nil
}

func normalizeTranscodePayloadMappings(mappings []TranscodePayloadMapping) []TranscodePayloadMapping {
	if len(mappings) == 0 {
		return nil
	}
	out := make([]TranscodePayloadMapping, 0, len(mappings))
	for _, mapping := range mappings {
		mapping.Source = strings.TrimSpace(mapping.Source)
		mapping.Target = strings.TrimSpace(mapping.Target)
		if mapping.Target == "" {
			continue
		}
		out = append(out, mapping)
	}
	return out
}

func openAPIPathTemplateParamNames(path string) []string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if len(segment) < 3 || !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}"))
		if name != "" {
			out = append(out, name)
		}
	}
	return out
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
