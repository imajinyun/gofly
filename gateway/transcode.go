// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/core/breaker"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc"
)

// TranscoderFactory builds a generic RPC client for a resolved upstream
// endpoint. It allows callers to customize how transcoded requests reach the
// backend (codec, TLS, protocol). When unset the gateway uses a default
// JSON-over-HTTP generic client.
type TranscoderFactory func(endpoint string, route Route) (rpc.GenericClient, error)

// transcodeOnce converts an inbound HTTP/JSON request into a generic RPC call
// against the resolved upstream endpoint and maps the RPC response back to an
// HTTP proxyResult.
func (g *Gateway) transcodeOnce(r *http.Request, route Route, endpoint string, body []byte, brk *breaker.AdaptiveBreaker) (proxyResult, error) {
	target, err := g.transcodeTarget(r, route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	payload, err := transcodeRequestPayload(r, route, body, target.profile)
	if err != nil {
		callErr := rpc.NewError(rpc.CodeInvalidArgument, err.Error())
		return proxyResult{
			Endpoint: endpoint,
			Status:   http.StatusBadRequest,
			Header:   transcodeResponseHeader(nil),
			Body:     transcodeErrorBody(callErr),
		}, nil
	}
	client, err := g.transcoderFor(endpoint, route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	ctx := transcodeContext(r.Context(), r, route)
	methodPath, err := rpc.MethodPath(target.service, target.method)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	raw, md, callErr := client.CallRaw(ctx, methodPath, payload)
	if callErr != nil {
		g.reportEndpoint(route, endpoint, false)
		if brk != nil {
			brk.MarkFailure()
		}
		status := coreerrors.HTTPStatus(rpc.CodeOf(callErr))
		result := proxyResult{
			Endpoint: endpoint,
			Status:   status,
			Header:   transcodeResponseHeader(nil),
			Body:     transcodeErrorBody(callErr),
		}
		// Surface non-retryable failures as a completed response so callers see
		// the mapped status, while retryable errors propagate for retry.
		if rpc.CodeOf(callErr) == rpc.CodeUnavailable || rpc.CodeOf(callErr) == rpc.CodeDeadlineExceeded {
			result.Err = callErr
			return result, callErr
		}
		return result, nil
	}
	g.reportEndpoint(route, endpoint, true)
	if brk != nil {
		brk.MarkSuccess()
	}
	responseBody, err := transcodeResponsePayload(raw, target.profile)
	if err != nil {
		callErr := rpc.NewError(rpc.CodeInvalidArgument, err.Error())
		return proxyResult{
			Endpoint: endpoint,
			Status:   http.StatusBadRequest,
			Header:   transcodeResponseHeader(nil),
			Body:     transcodeErrorBody(callErr),
		}, nil
	}
	return proxyResult{
		Endpoint: endpoint,
		Status:   http.StatusOK,
		Header:   transcodeResponseHeader(md),
		Body:     responseBody,
	}, nil
}

func (g *Gateway) transcoderFor(endpoint string, route Route) (rpc.GenericClient, error) {
	g.transcoderMu.Lock()
	defer g.transcoderMu.Unlock()
	if g.transcoders == nil {
		g.transcoders = make(map[string]rpc.GenericClient)
	}
	if client, ok := g.transcoders[endpoint]; ok {
		return client, nil
	}
	factory := g.transcoderFactory
	if factory == nil {
		factory = defaultTranscoderFactory
	}
	client, err := factory(endpoint, route)
	if err != nil {
		return nil, err
	}
	g.transcoders[endpoint] = client
	return client, nil
}

func defaultTranscoderFactory(endpoint string, route Route) (rpc.GenericClient, error) {
	target := endpoint
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	return rpc.NewClient(strings.TrimRight(target, "/"))
}

type transcodeResolvedTarget struct {
	service string
	method  string
	profile *TranscodeProfile
}

func (g *Gateway) transcodeTarget(r *http.Request, route Route) (transcodeResolvedTarget, error) {
	if descriptorName := strings.TrimSpace(route.Transcode.Descriptor); descriptorName != "" {
		return g.transcodeDescriptorTarget(r, route, descriptorName)
	}
	if strings.TrimSpace(route.Transcode.DescriptorMethod) != "" {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor is required when descriptorMethod is set")
	}
	service, method, err := transcodeTarget(r, route)
	if err != nil {
		return transcodeResolvedTarget{}, err
	}
	return transcodeResolvedTarget{service: service, method: method}, nil
}

func (g *Gateway) transcodeDescriptorTarget(r *http.Request, route Route, descriptorName string) (transcodeResolvedTarget, error) {
	desc, ok := g.descriptor(descriptorName)
	if !ok {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor not found")
	}
	method := strings.Trim(strings.TrimSpace(route.Transcode.DescriptorMethod), "/")
	if method == "" {
		method = strings.Trim(strings.TrimSpace(route.Transcode.Method), "/")
	}
	if method == "" {
		method = transcodeMethodFromPath(r.URL.Path, route.PathPrefix)
	}
	if method == "" {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor method is required")
	}
	if !descriptorHasMethod(desc, method) {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor method not found")
	}
	return transcodeResolvedTarget{service: desc.Name, method: method, profile: g.transcodeProfile(desc.Name, method)}, nil
}

func (g *Gateway) descriptor(name string) (rpc.Descriptor, bool) {
	if g == nil {
		return rpc.Descriptor{}, false
	}
	name = strings.TrimSpace(name)
	g.mu.RLock()
	defer g.mu.RUnlock()
	desc, ok := g.descriptors[name]
	if !ok {
		return rpc.Descriptor{}, false
	}
	return cloneDescriptor(desc), true
}

func descriptorHasMethod(desc rpc.Descriptor, name string) bool {
	name = strings.Trim(strings.TrimSpace(name), "/")
	for _, method := range desc.Methods {
		if strings.Trim(strings.TrimSpace(method.Name), "/") == name {
			return true
		}
	}
	return false
}

func (g *Gateway) transcodeProfile(descriptor, method string) *TranscodeProfile {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	profile, ok := g.transcodeProfiles[transcodeProfileKey(descriptor, method)]
	if !ok {
		return nil
	}
	cloned := cloneTranscodeProfile(profile)
	return &cloned
}

func transcodeTarget(r *http.Request, route Route) (string, string, error) {
	service := strings.Trim(strings.TrimSpace(route.Transcode.Service), "/")
	if service == "" {
		service = strings.Trim(strings.TrimSpace(route.Service), "/")
	}
	if service == "" {
		return "", "", errors.New("transcode service is required")
	}
	method := strings.Trim(strings.TrimSpace(route.Transcode.Method), "/")
	if method == "" {
		method = transcodeMethodFromPath(r.URL.Path, route.PathPrefix)
	}
	if method == "" {
		return "", "", errors.New("transcode method is required")
	}
	return service, method, nil
}

func transcodeMethodFromPath(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, strings.TrimRight(prefix, "/"))
	trimmed = strings.Trim(trimmed, "/")
	return trimmed
}

func transcodeRequestPayload(r *http.Request, route Route, body []byte, profile *TranscodeProfile) (json.RawMessage, error) {
	if route.Transcode.Payload.Mode != "" {
		return transcodeMappedRequestPayload(r, route, body, profile)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(append([]byte(nil), body...)), nil
}

func transcodeMappedRequestPayload(r *http.Request, route Route, body []byte, profile *TranscodeProfile) (json.RawMessage, error) {
	source := transcodePayloadSource{Body: map[string]any{}}
	if route.Transcode.Payload.MergeBodyObject {
		if err := mergeTranscodeBody(source.Body, body, route.Transcode.Payload.BodyField, route.Transcode.Payload.BodyRequired, route.Transcode.Payload.BodySchema); err != nil {
			return nil, err
		}
	}
	pathValues := transcodePathValues(r.URL.Path, route.PathPrefix, route.Transcode.Payload.PathTemplate, route.Transcode.Payload.PathParams)
	typedPath, err := transcodeTypedPathValues(pathValues, route.Transcode.Payload.PathParameters)
	if err != nil {
		return nil, err
	}
	source.Path = typedPath
	queryValues, err := transcodeQueryValues(r.URL.Query(), route.Transcode.Payload.QueryParams, route.Transcode.Payload.QueryParameters)
	if err != nil {
		return nil, err
	}
	source.Query = queryValues
	payloadConfig := route.Transcode.Payload
	if len(payloadConfig.Mappings) == 0 && profile != nil {
		payloadConfig.Mappings = profile.RequestMappings
	}
	payload, err := mappedTranscodePayload(source, payloadConfig)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(data), nil
}

type transcodePayloadSource struct {
	Body  map[string]any
	Path  map[string]any
	Query map[string]any
}

func mappedTranscodePayload(source transcodePayloadSource, config TranscodePayloadConfig) (map[string]any, error) {
	if len(config.Mappings) > 0 {
		return transcodePayloadFromMappings(source, config.Mappings)
	}
	payload := make(map[string]any, len(source.Body)+len(source.Path)+len(source.Query))
	for key, value := range source.Body {
		payload[key] = value
	}
	for key, value := range source.Path {
		payload[key] = value
	}
	for key, value := range source.Query {
		payload[key] = value
	}
	return payload, nil
}

func transcodePayloadFromMappings(source transcodePayloadSource, mappings []TranscodePayloadMapping) (map[string]any, error) {
	payload := map[string]any{}
	for _, mapping := range mappings {
		targetPath := strings.TrimSpace(mapping.Target)
		if targetPath == "" {
			continue
		}
		value, ok, err := transcodeMappingSourceValue(source, mapping.Source)
		if err != nil {
			return nil, err
		}
		if !ok {
			if mapping.Default == nil {
				continue
			}
			value = cloneTranscodeMappingDefault(mapping.Default)
		}
		if err := setTranscodeMappingTarget(payload, targetPath, value); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func transcodeMappingSourceValue(source transcodePayloadSource, path string) (any, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false, nil
	}
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil, false, nil
	}
	var current any
	switch parts[0] {
	case "body":
		current = source.Body
	case "path":
		current = source.Path
	case "query":
		current = source.Query
	default:
		return nil, false, fmt.Errorf("transcode mapping source %s must start with body, path, or query", path)
	}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("transcode mapping source %s has empty segment", path)
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		current, ok = object[part]
		if !ok {
			return nil, false, nil
		}
	}
	return current, true, nil
}

func setTranscodeMappingTarget(payload map[string]any, path string, value any) error {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 {
		return nil
	}
	current := payload
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("transcode mapping target %s has empty segment", path)
		}
		if index == len(parts)-1 {
			current[part] = cloneTranscodeMappingDefault(value)
			return nil
		}
		next, ok := current[part]
		if !ok {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("transcode mapping target %s conflicts with existing scalar", path)
		}
		current = child
	}
	return nil
}

func transcodeResponsePayload(raw json.RawMessage, profile *TranscodeProfile) ([]byte, error) {
	if profile == nil || len(profile.ResponseMappings) == 0 {
		return append([]byte(nil), raw...), nil
	}
	var body any
	if len(bytes.TrimSpace(raw)) == 0 {
		body = nil
	} else if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("transcode response body must be valid json")
	}
	object, _ := body.(map[string]any)
	payload, err := transcodePayloadFromMappings(transcodePayloadSource{Body: object}, profile.ResponseMappings)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func mergeTranscodeBody(payload map[string]any, body []byte, bodyField string, required bool, schema *TranscodeSchemaConfig) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		if required {
			return errors.New("transcode body is required")
		}
		return nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		if schema != nil {
			return errors.New("transcode body must be valid json")
		}
		payload[transcodeBodyField(bodyField)] = string(body)
		return nil
	}
	if schema != nil {
		if err := validateTranscodeSchemaValue(transcodeBodyField(bodyField), value, *schema); err != nil {
			return err
		}
	}
	if object, ok := value.(map[string]any); ok && bodyField == "" {
		for key, item := range object {
			payload[key] = item
		}
		return nil
	}
	payload[transcodeBodyField(bodyField)] = value
	return nil
}

func transcodeBodyField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "body"
	}
	return value
}

func validateTranscodeSchemaValue(path string, value any, schema TranscodeSchemaConfig) error {
	switch strings.ToLower(strings.TrimSpace(schema.Type)) {
	case "", "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("transcode body field %s must be object", path)
		}
		for _, name := range schema.Required {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := object[name]; !ok {
				return fmt.Errorf("transcode body field %s.%s is required", path, name)
			}
		}
		for name, property := range schema.Properties {
			item, ok := object[name]
			if !ok || item == nil {
				continue
			}
			if err := validateTranscodeSchemaValue(path+"."+name, item, property); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("transcode body field %s must be array", path)
		}
		if schema.Items != nil {
			for index, item := range items {
				if err := validateTranscodeSchemaValue(fmt.Sprintf("%s[%d]", path, index), item, *schema.Items); err != nil {
					return err
				}
			}
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number {
			return fmt.Errorf("transcode body field %s must be integer", path)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("transcode body field %s must be number", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("transcode body field %s must be boolean", path)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("transcode body field %s must be string", path)
		}
	}
	return nil
}

func transcodePathValues(path, routePrefix, template string, names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 {
		return out
	}
	templateSegments := strings.Split(strings.Trim(template, "/"), "/")
	pathSuffix := strings.Trim(strings.TrimPrefix(path, strings.TrimRight(routePrefix, "/")), "/")
	pathSegments := strings.Split(pathSuffix, "/")
	for len(pathSegments) == 1 && pathSegments[0] == "" {
		pathSegments = nil
	}
	firstDynamic := -1
	for index, segment := range templateSegments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			firstDynamic = index
			break
		}
	}
	if firstDynamic >= 0 {
		templateSegments = templateSegments[firstDynamic:]
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			nameSet[name] = struct{}{}
		}
	}
	for index, segment := range templateSegments {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}"))
		if _, ok := nameSet[name]; !ok {
			continue
		}
		if index < len(pathSegments) {
			out[name] = pathSegments[index]
		}
	}
	if len(out) == len(nameSet) {
		return out
	}
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := out[name]; ok {
			continue
		}
		if i < len(pathSegments) {
			out[name] = pathSegments[i]
		}
	}
	return out
}

func transcodeTypedPathValues(values map[string]string, parameters []TranscodeParameterConfig) (map[string]any, error) {
	out := make(map[string]any, len(values))
	byName := transcodeParameterByName(parameters)
	for name, value := range values {
		converted, err := convertTranscodeParameterValue(value, byName[name])
		if err != nil {
			return nil, err
		}
		out[name] = converted
	}
	return out, nil
}

func transcodeQueryValues(values url.Values, names []string, parameters []TranscodeParameterConfig) (map[string]any, error) {
	out := map[string]any{}
	byName := transcodeParameterByName(parameters)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		items, ok := values[name]
		if !ok {
			continue
		}
		converted, err := convertTranscodeQueryValues(items, byName[name])
		if err != nil {
			return nil, err
		}
		out[name] = converted
	}
	return out, nil
}

func transcodeParameterByName(parameters []TranscodeParameterConfig) map[string]TranscodeParameterConfig {
	out := make(map[string]TranscodeParameterConfig, len(parameters))
	for _, parameter := range parameters {
		name := strings.TrimSpace(parameter.Name)
		if name != "" {
			out[name] = parameter
		}
	}
	return out
}

func convertTranscodeQueryValues(values []string, parameter TranscodeParameterConfig) (any, error) {
	if len(values) == 0 {
		return convertTranscodeParameterValue("", parameter)
	}
	if strings.EqualFold(strings.TrimSpace(parameter.Type), "array") {
		items := flattenTranscodeArrayValues(values)
		out := make([]any, 0, len(items))
		itemSchema := TranscodeParameterConfig{Type: "string"}
		if parameter.Items != nil {
			itemSchema = *parameter.Items
		}
		for _, item := range items {
			converted, err := convertTranscodeParameterValue(item, itemSchema)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	}
	if len(values) > 1 {
		out := make([]any, 0, len(values))
		for _, value := range values {
			converted, err := convertTranscodeParameterValue(value, parameter)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	}
	return convertTranscodeParameterValue(values[0], parameter)
}

func flattenTranscodeArrayValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			out = append(out, strings.TrimSpace(part))
		}
	}
	return out
}

func convertTranscodeParameterValue(value string, parameter TranscodeParameterConfig) (any, error) {
	switch strings.ToLower(strings.TrimSpace(parameter.Type)) {
	case "integer":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("transcode parameter %s must be integer", transcodeParameterName(parameter))
		}
		return parsed, nil
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("transcode parameter %s must be number", transcodeParameterName(parameter))
		}
		return parsed, nil
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("transcode parameter %s must be boolean", transcodeParameterName(parameter))
		}
		return parsed, nil
	case "array":
		return convertTranscodeQueryValues([]string{value}, parameter)
	default:
		return value, nil
	}
}

func transcodeParameterName(parameter TranscodeParameterConfig) string {
	name := strings.TrimSpace(parameter.Name)
	if name == "" {
		return "value"
	}
	return name
}

func transcodeContext(ctx context.Context, r *http.Request, route Route) context.Context {
	md := metadata.MD{}
	for _, name := range route.Header.AllowRequest {
		if value := r.Header.Get(name); value != "" {
			md[strings.ToLower(name)] = value
		}
	}
	if len(md) == 0 {
		return ctx
	}
	return metadata.NewContext(ctx, md)
}

func transcodeResponseHeader(md metadata.MD) http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	for key, value := range md {
		header.Set("X-Gofly-Md-"+key, value)
	}
	return header
}

func transcodeErrorBody(err error) []byte {
	payload := struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}{
		Code:  string(rpc.CodeOf(err)),
		Error: err.Error(),
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return []byte(`{"error":"transcode failure"}`)
	}
	return data
}
