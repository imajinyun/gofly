package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

type gatewayInstanceResolverStub struct {
	instances []rpc.ServiceInstance
	err       error
}

func (r *gatewayInstanceResolverStub) Resolve(context.Context) ([]string, error) {
	return endpointsFromServiceInstances(r.instances), r.err
}

func (r *gatewayInstanceResolverStub) ResolveInstances(context.Context) ([]rpc.ServiceInstance, error) {
	return cloneServiceInstances(r.instances), r.err
}

func TestGatewayFailoverResolverContractBoundaries(t *testing.T) {
	t.Run("endpoint cache and cancellation", func(t *testing.T) {
		var endpoints = []string{" http://127.0.0.1:8080 ", "http://127.0.0.1:8080"}
		var resolveErr error
		resolver := newGatewayFailoverResolver(rpc.ResolverFunc(func(context.Context) ([]string, error) {
			return append([]string(nil), endpoints...), resolveErr
		}))

		got, err := resolver.Resolve(context.Background())
		wantEndpoints := []string{"http://127.0.0.1:8080", "http://127.0.0.1:8080"}
		if err != nil || !reflect.DeepEqual(got, wantEndpoints) {
			t.Fatalf("initial resolve = %#v, %v", got, err)
		}
		endpoints = nil
		resolveErr = errors.New("registry unavailable")
		got, err = resolver.Resolve(context.Background())
		if err != nil || !reflect.DeepEqual(got, wantEndpoints) {
			t.Fatalf("fallback resolve = %#v, %v", got, err)
		}
		snapshot := resolver.Snapshot()
		if !snapshot.Stale || snapshot.Fallbacks != 1 || snapshot.Updates != 1 ||
			!strings.Contains(snapshot.Error, "registry unavailable") {
			t.Fatalf("fallback snapshot = %+v", snapshot)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := resolver.Resolve(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled resolve error = %v", err)
		}
	})

	t.Run("instance metadata is normalized and defensively copied", func(t *testing.T) {
		source := &gatewayInstanceResolverStub{instances: []rpc.ServiceInstance{
			{
				Endpoint: " http://127.0.0.1:8081 ",
				Weight:   2,
				Tags:     map[string]string{"zone": "a"},
				Metadata: map[string]string{"protocol": "h2"},
			},
			{Endpoint: " "},
		}}
		resolver := newGatewayFailoverResolver(source)
		instances, err := resolver.ResolveInstances(context.Background())
		if err != nil || len(instances) != 1 || instances[0].Endpoint != "http://127.0.0.1:8081" {
			t.Fatalf("resolved instances = %+v, %v", instances, err)
		}
		instances[0].Tags["zone"] = "mutated"
		instances[0].Metadata["protocol"] = "mutated"

		source.instances = nil
		source.err = errors.New("discovery unavailable")
		fallback, err := resolver.ResolveInstances(context.Background())
		if err != nil || len(fallback) != 1 ||
			fallback[0].Tags["zone"] != "a" ||
			fallback[0].Metadata["protocol"] != "h2" {
			t.Fatalf("fallback instances = %+v, %v", fallback, err)
		}
	})

	t.Run("endpoint-only resolver converts service instances", func(t *testing.T) {
		resolver := newGatewayFailoverResolver(rpc.ResolverFunc(func(context.Context) ([]string, error) {
			return []string{"http://127.0.0.1:8082", " ", "http://127.0.0.1:8082"}, nil
		}))
		instances, err := resolver.ResolveInstances(context.Background())
		if err != nil || len(instances) != 2 ||
			instances[0].Endpoint != "http://127.0.0.1:8082" ||
			instances[1].Endpoint != "http://127.0.0.1:8082" {
			t.Fatalf("converted instances = %+v, %v", instances, err)
		}
	})

	var nilResolver *gatewayFailoverResolver
	if _, err := nilResolver.Resolve(context.Background()); err == nil {
		t.Fatal("nil failover resolver should reject Resolve")
	}
	if _, err := nilResolver.ResolveInstances(context.Background()); err == nil {
		t.Fatal("nil failover resolver should reject ResolveInstances")
	}
	if snapshot := nilResolver.Snapshot(); !reflect.DeepEqual(snapshot, rpc.ResolverSnapshot{}) {
		t.Fatalf("nil snapshot = %+v", snapshot)
	}
	if got := serviceInstancesFromEndpoints(nil); got != nil {
		t.Fatalf("nil endpoints converted to %+v", got)
	}
}

func TestGatewayTranscodeMappingContractBoundaries(t *testing.T) {
	source := transcodePayloadSource{
		Body: map[string]any{
			"user":  map[string]any{"name": "ada"},
			"items": []any{map[string]any{"sku": "a"}, map[string]any{"sku": "b"}},
		},
		Path:   map[string]any{"id": int64(7)},
		Query:  map[string]any{"active": true},
		Header: map[string]any{"x-tenant": "acme"},
	}
	payload, err := transcodePayloadFromMappings(source, []TranscodePayloadMapping{
		{Source: "body.user.name", Target: "profile.name"},
		{Source: "body.items[].sku", Target: "profile.skus[]"},
		{Source: "path.id", Target: "profile.id"},
		{Source: "query.missing", Target: "profile.region", Default: "cn"},
		{Source: "header.x-tenant", Target: "profile.tenant"},
		{Source: "body.missing", Target: "ignored"},
		{Source: "body.user.name", Target: " "},
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := payload["profile"].(map[string]any)
	if profile["name"] != "ada" || profile["id"] != int64(7) ||
		profile["region"] != "cn" || profile["tenant"] != "acme" ||
		!reflect.DeepEqual(profile["skus"], []any{"a", "b"}) {
		t.Fatalf("mapped profile = %#v", profile)
	}
	if _, ok := payload["ignored"]; ok {
		t.Fatalf("missing source unexpectedly mapped: %#v", payload)
	}

	for _, test := range []struct {
		name    string
		source  string
		target  string
		wantErr string
	}{
		{name: "unknown source root", source: "cookie.session", target: "session", wantErr: "must start with"},
		{name: "empty source segment", source: "body..name", target: "name", wantErr: "empty segment"},
		{name: "empty target segment", source: "body.user.name", target: "profile..name", wantErr: "empty segment"},
		{name: "empty target array", source: "body.items", target: "[]", wantErr: "empty array segment"},
		{name: "non-terminal target array", source: "body.items", target: "items[].sku", wantErr: "must be terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := transcodePayloadFromMappings(source, []TranscodePayloadMapping{{
				Source: test.source,
				Target: test.target,
			}})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("mapping error = %v, want %q", err, test.wantErr)
			}
		})
	}

	conflict := map[string]any{"profile": "scalar"}
	if err := setTranscodeMappingTarget(conflict, "profile.name", "ada"); err == nil ||
		!strings.Contains(err.Error(), "conflicts with existing scalar") {
		t.Fatalf("target conflict error = %v", err)
	}
	if value, ok, err := transcodeMappingSourceValue(source, ""); err != nil || ok || value != nil {
		t.Fatalf("empty source = %#v, %t, %v", value, ok, err)
	}
}

func TestGatewayTypedTranscodeAndSchemaContractBoundaries(t *testing.T) {
	schema := TranscodeSchemaConfig{
		Type:     "object",
		Required: []string{"count", "items", "enabled", "ratio"},
		Properties: map[string]TranscodeSchemaConfig{
			"count":   {Type: "integer"},
			"items":   {Type: "array", Items: &TranscodeSchemaConfig{Type: "string"}},
			"enabled": {Type: "boolean"},
			"ratio":   {Type: "number"},
		},
	}
	valid := map[string]any{
		"count": float64(2), "items": []any{"a", "b"}, "enabled": true, "ratio": 0.5,
	}
	if err := validateTranscodeSchemaValue("body", valid, schema); err != nil {
		t.Fatalf("valid schema payload: %v", err)
	}
	for _, test := range []struct {
		name    string
		value   any
		schema  TranscodeSchemaConfig
		wantErr string
	}{
		{name: "object", value: "x", schema: TranscodeSchemaConfig{Type: "object"}, wantErr: "must be object"},
		{name: "required", value: map[string]any{}, schema: TranscodeSchemaConfig{Required: []string{"id"}}, wantErr: "is required"},
		{name: "array", value: "x", schema: TranscodeSchemaConfig{Type: "array"}, wantErr: "must be array"},
		{name: "array item", value: []any{float64(1)}, schema: TranscodeSchemaConfig{Type: "array", Items: &TranscodeSchemaConfig{Type: "string"}}, wantErr: "must be string"},
		{name: "integer fraction", value: 1.5, schema: TranscodeSchemaConfig{Type: "integer"}, wantErr: "must be integer"},
		{name: "number", value: "1", schema: TranscodeSchemaConfig{Type: "number"}, wantErr: "must be number"},
		{name: "boolean", value: "true", schema: TranscodeSchemaConfig{Type: "boolean"}, wantErr: "must be boolean"},
		{name: "string", value: true, schema: TranscodeSchemaConfig{Type: "string"}, wantErr: "must be string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateTranscodeSchemaValue("body", test.value, test.schema)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("schema error = %v, want %q", err, test.wantErr)
			}
		})
	}

	parameters := []TranscodeParameterConfig{
		{Name: "id", Type: "integer", Required: true},
		{Name: "ratio", Type: "number"},
		{Name: "active", Type: "boolean"},
		{Name: "labels", Type: "array", Items: &TranscodeParameterConfig{Type: "integer"}},
	}
	path, err := transcodeTypedPathValues(map[string]string{"id": "7"}, parameters)
	if err != nil || path["id"] != int64(7) {
		t.Fatalf("typed path = %#v, %v", path, err)
	}
	if _, err := transcodeTypedPathValues(nil, parameters); err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("missing required path error = %v", err)
	}

	query, err := transcodeQueryValues(url.Values{
		"ratio":  {"0.5"},
		"active": {"true"},
		"labels": {"1, 2", "3"},
	}, []string{"ratio", "active", "labels", "missing"}, parameters)
	if err != nil || query["ratio"] != 0.5 || query["active"] != true ||
		!reflect.DeepEqual(query["labels"], []any{int64(1), int64(2), int64(3)}) {
		t.Fatalf("typed query = %#v, %v", query, err)
	}
	if _, err := transcodeQueryValues(url.Values{}, []string{"id"}, parameters); err == nil {
		t.Fatal("missing required query parameter should fail")
	}

	headers, err := transcodeHeaderValues(http.Header{"X-Tenant": []string{"acme"}}, []string{"X-Tenant"},
		[]TranscodeParameterConfig{{Name: "X-Tenant", Type: "string", Required: true}})
	if err != nil || headers["X-Tenant"] != "acme" || headers["x-tenant"] != "acme" {
		t.Fatalf("typed headers = %#v, %v", headers, err)
	}
	if _, err := transcodeHeaderValues(http.Header{}, []string{"X-Tenant"},
		[]TranscodeParameterConfig{{Name: "X-Tenant", Required: true}}); err == nil {
		t.Fatal("missing required header should fail")
	}

	for _, test := range []struct {
		name  string
		value string
		kind  string
	}{
		{name: "integer", value: "nope", kind: "integer"},
		{name: "number", value: "nope", kind: "number"},
		{name: "boolean", value: "nope", kind: "boolean"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := convertTranscodeParameterValue(test.value, TranscodeParameterConfig{Name: test.name, Type: test.kind}); err == nil {
				t.Fatalf("%s conversion should fail", test.kind)
			}
		})
	}
}

func TestGatewayOpenAPIInferenceContractBoundaries(t *testing.T) {
	if got := openAPIPathTemplateParamNames("/users/{ id }/orders/{orderID}/plain/{ }"); !reflect.DeepEqual(got, []string{"id", "orderID"}) {
		t.Fatalf("path template names = %#v", got)
	}
	payload := openAPITranscodePayloadConfig("/users/{id}", rest.Operation{}, OpenAPITranscodeOptions{Enabled: true})
	if !reflect.DeepEqual(payload.PathParams, []string{"id"}) ||
		len(payload.PathParameters) != 1 ||
		payload.PathParameters[0].Type != "string" {
		t.Fatalf("inferred payload = %+v", payload)
	}
	if configured := openAPITranscodePayloadConfig("/users/{id}", rest.Operation{}, OpenAPITranscodeOptions{}); configured.Mode != "" {
		t.Fatalf("disabled transcode payload = %+v", configured)
	}

	mappings := normalizeTranscodePayloadMappings([]TranscodePayloadMapping{
		{Source: " body.id ", Target: " request.id "},
		{Source: "body.skip", Target: " "},
	})
	if len(mappings) != 1 || mappings[0].Source != "body.id" || mappings[0].Target != "request.id" {
		t.Fatalf("normalized mappings = %+v", mappings)
	}
	if got := normalizeTranscodePayloadMappings(nil); got != nil {
		t.Fatalf("nil mappings = %+v", got)
	}
}

func TestGatewayTranscodeBodyMergeContracts(t *testing.T) {
	t.Run("optional empty body is ignored", func(t *testing.T) {
		payload := map[string]any{"existing": true}
		if err := mergeTranscodeBody(payload, nil, "", false, nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payload, map[string]any{"existing": true}) {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("required empty body is rejected", func(t *testing.T) {
		if err := mergeTranscodeBody(map[string]any{}, []byte(" \n"), "", true, nil); err == nil ||
			!strings.Contains(err.Error(), "body is required") {
			t.Fatalf("required body error = %v", err)
		}
	})

	t.Run("plain text body is preserved without schema", func(t *testing.T) {
		payload := map[string]any{}
		if err := mergeTranscodeBody(payload, []byte("plain text"), "raw", false, nil); err != nil {
			t.Fatal(err)
		}
		if payload["raw"] != "plain text" {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("invalid JSON is rejected with schema", func(t *testing.T) {
		schema := TranscodeSchemaConfig{Type: "object"}
		if err := mergeTranscodeBody(map[string]any{}, []byte("{"), "", false, &schema); err == nil ||
			!strings.Contains(err.Error(), "valid json") {
			t.Fatalf("schema body error = %v", err)
		}
	})

	t.Run("object body merges into root", func(t *testing.T) {
		payload := map[string]any{"existing": true}
		schema := TranscodeSchemaConfig{
			Type:     "object",
			Required: []string{"name"},
			Properties: map[string]TranscodeSchemaConfig{
				"name": {Type: "string"},
			},
		}
		if err := mergeTranscodeBody(payload, []byte(`{"name":"ada"}`), "", true, &schema); err != nil {
			t.Fatal(err)
		}
		if payload["existing"] != true || payload["name"] != "ada" {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("array body uses configured field", func(t *testing.T) {
		payload := map[string]any{}
		schema := TranscodeSchemaConfig{Type: "array", Items: &TranscodeSchemaConfig{Type: "integer"}}
		if err := mergeTranscodeBody(payload, []byte(`[1,2]`), "items", false, &schema); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(payload["items"], []any{float64(1), float64(2)}) {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("schema failure propagates", func(t *testing.T) {
		schema := TranscodeSchemaConfig{Type: "object", Required: []string{"id"}}
		if err := mergeTranscodeBody(map[string]any{}, []byte(`{}`), "", false, &schema); err == nil ||
			!strings.Contains(err.Error(), "body.id is required") {
			t.Fatalf("schema error = %v", err)
		}
	})

	if got := transcodeBodyField(" "); got != "body" {
		t.Fatalf("default body field = %q", got)
	}
	if got := transcodeBodyField(" payload "); got != "payload" {
		t.Fatalf("trimmed body field = %q", got)
	}
}

func TestGatewayMappedPayloadMergeContracts(t *testing.T) {
	source := transcodePayloadSource{
		Body:  map[string]any{"bodyOnly": "body", "shared": "body"},
		Path:  map[string]any{"pathOnly": "path", "shared": "path"},
		Query: map[string]any{"queryOnly": "query", "shared": "query"},
	}
	payload, err := mappedTranscodePayload(source, TranscodePayloadConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if payload["bodyOnly"] != "body" || payload["pathOnly"] != "path" ||
		payload["queryOnly"] != "query" || payload["shared"] != "query" {
		t.Fatalf("merged payload = %#v", payload)
	}

	mapped, err := mappedTranscodePayload(source, TranscodePayloadConfig{
		Mappings: []TranscodePayloadMapping{{Source: "body.bodyOnly", Target: "request.value"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mapped, map[string]any{"request": map[string]any{"value": "body"}}) {
		t.Fatalf("explicit mapped payload = %#v", mapped)
	}

	payload, err = mappedTranscodePayload(transcodePayloadSource{Body: []any{"not", "an", "object"}}, TranscodePayloadConfig{})
	if err != nil || len(payload) != 0 {
		t.Fatalf("non-object body payload = %#v, err=%v", payload, err)
	}
}

func TestGatewayAggregationPayloadContracts(t *testing.T) {
	source := transcodePayloadSource{Body: map[string]any{
		"profile": map[string]any{"id": "u1"},
		"items":   []any{"a", "b"},
	}}
	template := map[string]any{"meta": map[string]any{"source": "template"}}
	payload, err := aggregationPayloadFromMappingsInto(template, source, []AggregationPayloadMapping{
		{Source: "body.profile.id", Target: "user.id", AsArray: true},
		{Source: "body.items", Target: "items", AsArray: true},
		{Source: "body.missing", Target: "region", Default: json.RawMessage(`"cn"`)},
		{Source: "body.missing", Target: "ignored"},
		{Source: "body.profile.id", Target: " "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload["user"], map[string]any{"id": []any{"u1"}}) ||
		!reflect.DeepEqual(payload["items"], []any{"a", "b"}) ||
		payload["region"] != "cn" {
		t.Fatalf("aggregation payload = %#v", payload)
	}
	if _, ok := payload["ignored"]; ok {
		t.Fatalf("missing mapping unexpectedly emitted: %#v", payload)
	}

	emptyPayload, err := aggregationPayloadFromMappingsInto(nil, source, nil)
	if err != nil || emptyPayload == nil || len(emptyPayload) != 0 {
		t.Fatalf("nil payload normalization = %#v, err=%v", emptyPayload, err)
	}
	if got := aggregationArrayValue(nil); !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("nil array value = %#v", got)
	}
	if got := aggregationArrayValue("one"); !reflect.DeepEqual(got, []any{"one"}) {
		t.Fatalf("scalar array value = %#v", got)
	}

	validTemplate, err := aggregationPayloadTemplate(json.RawMessage(`{"meta":{"source":"template"}}`))
	wantTemplate := map[string]any{"meta": map[string]any{"source": "template"}}
	if err != nil || !reflect.DeepEqual(validTemplate, wantTemplate) {
		t.Fatalf("valid template = %#v, err=%v", validTemplate, err)
	}
	if empty, err := aggregationPayloadTemplate(nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty template = %#v, err=%v", empty, err)
	}
	if _, err := aggregationPayloadTemplate(json.RawMessage(`{`)); err == nil ||
		!strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("invalid template error = %v", err)
	}
	if _, err := aggregationPayloadTemplate(json.RawMessage(`[]`)); err == nil ||
		!strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("non-object template error = %v", err)
	}
}

func TestGatewayAggregationRequiredAndSourceContracts(t *testing.T) {
	payload := map[string]any{
		"name":   "ada",
		"items":  []any{"one"},
		"nested": map[string]any{"enabled": true},
	}
	if err := validateAggregationRequiredPayload(payload, []string{"name", "body.items", "nested.enabled", " "}); err != nil {
		t.Fatalf("valid required payload: %v", err)
	}
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "nil", value: nil},
		{name: "empty string", value: ""},
		{name: "empty array", value: []any{}},
		{name: "empty object", value: map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !aggregationValueEmpty(test.value) {
				t.Fatalf("aggregationValueEmpty(%#v) = false", test.value)
			}
		})
	}
	for _, value := range []any{false, float64(0), []any{"x"}, map[string]any{"x": true}} {
		if aggregationValueEmpty(value) {
			t.Fatalf("aggregationValueEmpty(%#v) = true", value)
		}
	}
	if err := validateAggregationRequiredPayload(map[string]any{"name": ""}, []string{"name"}); err == nil ||
		!strings.Contains(err.Error(), `"name" is missing`) {
		t.Fatalf("missing required error = %v", err)
	}
	if err := validateAggregationRequiredPayload(map[string]any{"bad": map[string]any{}}, []string{"bad..path"}); err == nil ||
		!strings.Contains(err.Error(), "empty segment") {
		t.Fatalf("invalid required path error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://gateway/users/42?tag=a&tag=b&single=one", strings.NewReader(`{"name":"ada"}`))
	request.Header.Add("X-Tenant", "acme")
	request.Header.Add("X-Role", "reader")
	request.Header.Add("X-Role", "writer")
	source := aggregationRequestSource(request, Route{PathPrefix: "/users"}, []byte(`{"name":"ada"}`))
	if source.Body.(map[string]any)["name"] != "ada" ||
		!reflect.DeepEqual(source.Query["tag"], []any{"a", "b"}) ||
		source.Query["single"] != "one" ||
		source.Header["x-tenant"] != "acme" ||
		!reflect.DeepEqual(source.Header["x-role"], []any{"reader", "writer"}) {
		t.Fatalf("aggregation source = %#v", source)
	}

	nonObject := aggregationRequestSource(request, Route{}, []byte(`["a"]`))
	if !reflect.DeepEqual(nonObject.Body, map[string]any{"body": []any{"a"}}) {
		t.Fatalf("non-object body source = %#v", nonObject.Body)
	}
	invalid := aggregationRequestSource(request, Route{}, []byte(`{`))
	if len(invalid.Body.(map[string]any)) != 0 {
		t.Fatalf("invalid body source = %#v", invalid.Body)
	}
	if got := aggregationPathAny(nil); got != nil {
		t.Fatalf("nil path conversion = %#v", got)
	}
	if got := aggregationPathAny(map[string]string{"id": "42"}); !reflect.DeepEqual(got, map[string]any{"id": "42"}) {
		t.Fatalf("path conversion = %#v", got)
	}
}

func TestGatewayAggregationScalarAndRetryContracts(t *testing.T) {
	if got := aggregationScalarString("value"); got != "value" {
		t.Fatalf("string scalar = %q", got)
	}
	if got := aggregationScalarString(fmt.Stringer(stringerValue("formatted"))); got != "formatted" {
		t.Fatalf("Stringer scalar = %q", got)
	}
	if got := aggregationScalarString(map[string]any{"ok": true}); got != `{"ok":true}` {
		t.Fatalf("object scalar = %q", got)
	}
	if got := aggregationScalarString(func() {}); !strings.Contains(got, "0x") {
		t.Fatalf("unsupported scalar fallback = %q", got)
	}

	if normalizeAggregationStepAttempts(0) != 1 || normalizeAggregationStepAttempts(3) != 3 {
		t.Fatal("aggregation attempts normalization mismatch")
	}
	if aggregationStepShouldRetry(aggregationStepResult{err: errors.New("down")}, RetryPolicy{Attempts: 1}) {
		t.Fatal("single attempt must not retry")
	}
	if !aggregationStepShouldRetry(aggregationStepResult{err: errors.New("down")}, RetryPolicy{Attempts: 2}) {
		t.Fatal("transport error should retry")
	}
	if !aggregationStepShouldRetry(aggregationStepResult{status: http.StatusBadGateway}, RetryPolicy{Attempts: 2}) {
		t.Fatal("5xx should retry by default")
	}
	if aggregationStepShouldRetry(aggregationStepResult{status: http.StatusBadRequest}, RetryPolicy{Attempts: 2}) {
		t.Fatal("4xx should not retry by default")
	}
	if !aggregationStepShouldRetry(aggregationStepResult{status: http.StatusTooManyRequests}, RetryPolicy{Attempts: 2, Statuses: []int{http.StatusTooManyRequests}}) {
		t.Fatal("configured status should retry")
	}
	if aggregationStepShouldRetry(aggregationStepResult{status: http.StatusBadGateway}, RetryPolicy{Attempts: 2, Statuses: []int{http.StatusTooManyRequests}}) {
		t.Fatal("unconfigured status should not retry")
	}

	for _, test := range []struct {
		name     string
		endpoint string
		step     AggregationStep
		want     string
		wantErr  string
	}{
		{name: "endpoint", endpoint: "https://api.example.com/base/", step: AggregationStep{Path: "/profile"}, want: "https://api.example.com/base/profile"},
		{name: "override", endpoint: "https://ignored.example.com", step: AggregationStep{Target: "https://api.example.com", Path: "orders"}, want: "https://api.example.com/orders"},
		{name: "missing target", step: AggregationStep{Path: "/profile"}, wantErr: "target is required"},
		{name: "relative target", endpoint: "api.example.com", step: AggregationStep{Path: "/profile"}, wantErr: "scheme and host"},
		{name: "missing path", endpoint: "https://api.example.com", step: AggregationStep{}, wantErr: "path is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := aggregationStepURL(test.endpoint, test.step)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got.String() != test.want {
				t.Fatalf("URL = %v, err=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestGatewayAggregationCloneAndNormalizeContracts(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "empty", body: nil, want: "null"},
		{name: "valid JSON", body: []byte(` {"ok":true} `), want: `{"ok":true}`},
		{name: "plain text", body: []byte("not-json"), want: `"not-json"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := string(normalizeAggregationBody(test.body)); got != test.want {
				t.Fatalf("normalized body = %q, want %q", got, test.want)
			}
		})
	}
	if got := cloneAggregationRawValue(nil); got != nil {
		t.Fatalf("empty raw clone = %#v", got)
	}
	if got := cloneAggregationRawValue(json.RawMessage(`{"nested":{"value":1}}`)); !reflect.DeepEqual(got, map[string]any{"nested": map[string]any{"value": float64(1)}}) {
		t.Fatalf("JSON raw clone = %#v", got)
	}
	if got := cloneAggregationRawValue(json.RawMessage(`{`)); got != "{" {
		t.Fatalf("invalid raw clone = %#v", got)
	}
	errorsByStep := map[string]string{"profile": "down"}
	cloned := cloneAggregationErrors(errorsByStep)
	cloned["profile"] = "mutated"
	if errorsByStep["profile"] != "down" {
		t.Fatalf("source errors mutated = %#v", errorsByStep)
	}
	if cloneAggregationErrors(nil) != nil || aggregationErrorsAny(nil) != nil {
		t.Fatal("empty error maps must stay nil")
	}
	if got := aggregationErrorsAny(errorsByStep); got["profile"] != "down" {
		t.Fatalf("error map = %#v", got)
	}
}

func TestGatewayAggregationStepFailureGovernanceContracts(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://gateway.local/users/42?tenant=acme", strings.NewReader(`{"name":"ada"}`))
	request.Header.Set("X-Trace", "trace-1")
	route := Route{PathPrefix: "/users"}

	t.Run("response read failure preserves step metadata", func(t *testing.T) {
		gateway := &Gateway{client: &http.Client{Transport: gatewayRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.String() != "https://api.example.com/profile" {
				t.Fatalf("upstream request = %s %s", req.Method, req.URL)
			}
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body:       errorReadCloser{err: errors.New("response read failed")},
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})}}
		result := gateway.runAggregationStepAttempt(request, route, "https://api.example.com", []byte(`{"name":"ada"}`), 2, AggregationStep{
			Name:   "profile",
			Method: http.MethodPost,
			Path:   "/profile",
		}, "profile")
		if result.index != 2 || result.name != "profile" || result.status != http.StatusAccepted ||
			result.err == nil || !strings.Contains(result.err.Error(), "response read failed") {
			t.Fatalf("read failure result = %+v", result)
		}
	})

	t.Run("oversized response is rejected", func(t *testing.T) {
		body := strings.Repeat("x", int(maxAggregationBodyBytes)+1)
		gateway := &Gateway{client: &http.Client{Transport: gatewayRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})}}
		result := gateway.runAggregationStepAttempt(request, route, "https://api.example.com", nil, 0, AggregationStep{
			Name: "large",
			Path: "/large",
		}, "large")
		if result.status != http.StatusOK || result.err == nil ||
			!strings.Contains(result.err.Error(), "response too large") {
			t.Fatalf("oversized response result = %+v", result)
		}
	})

	t.Run("context cancellation interrupts retry backoff", func(t *testing.T) {
		var attempts int
		ctx, cancel := context.WithCancel(request.Context())
		gateway := &Gateway{client: &http.Client{Transport: gatewayRoundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			cancel()
			return nil, errors.New("upstream unavailable")
		})}}
		canceledRequest := request.Clone(ctx)
		result := gateway.runAggregationStep(canceledRequest, route, "https://api.example.com", nil, 0, AggregationStep{
			Name: "retrying",
			Path: "/retry",
			Retry: RetryPolicy{
				Attempts: 3,
				Backoff:  time.Hour,
			},
		})
		if attempts != 1 || result.retries != 0 || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("canceled retry result = %+v, attempts=%d", result, attempts)
		}
	})
}

type stringerValue string

func (s stringerValue) String() string {
	return string(s)
}
