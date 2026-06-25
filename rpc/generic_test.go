package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

func TestGenericInvokerInvoke(t *testing.T) {
	s := NewServer(WithServerMiddleware(func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			ctx = metadata.Append(ctx, "generic", "true")
			return next(ctx, req)
		}
	}))
	if err := s.RegisterService(ServiceDesc{Name: "generic", Methods: []MethodDesc{
		GenericMethod("Echo", func(ctx context.Context, raw json.RawMessage) (any, error) {
			var req helloReq
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			return helloResp{Message: "hello " + req.Name}, nil
		}),
	}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	invoker, err := NewGenericInvoker(c)
	if err != nil {
		t.Fatalf("NewGenericInvoker: %v", err)
	}
	resp, err := invoker.Invoke(context.Background(), "/generic/", "/Echo/", json.RawMessage(`{"name":"raw"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	decoded, err := DecodeJSONPayload[helloResp](resp.Payload)
	if err != nil {
		t.Fatalf("DecodeJSONPayload: %v", err)
	}
	if decoded.Message != "hello raw" {
		t.Fatalf("message = %q, want hello raw", decoded.Message)
	}
	if resp.Metadata.Get("generic") != "true" {
		t.Fatalf("metadata generic = %q, want true", resp.Metadata.Get("generic"))
	}
}

func TestGenericInvokerInvokeMethodUsesDescriptor(t *testing.T) {
	client := &recordingGenericClient{payload: json.RawMessage(`{"message":"ok"}`)}
	invoker, err := NewGenericInvoker(client)
	if err != nil {
		t.Fatalf("NewGenericInvoker: %v", err)
	}
	desc := ServiceDesc{Name: "greeter.v1.Greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
	}}}
	resp, err := invoker.InvokeMethod(context.Background(), desc, "SayHello", helloReq{Name: "gofly"})
	if err != nil {
		t.Fatalf("InvokeMethod: %v", err)
	}
	if client.method != "greeter.v1.Greeter/SayHello" {
		t.Fatalf("method = %q, want greeter.v1.Greeter/SayHello", client.method)
	}
	if string(resp.Payload) != `{"message":"ok"}` {
		t.Fatalf("payload = %s", resp.Payload)
	}
	if _, err := invoker.InvokeMethod(context.Background(), desc, "Missing", nil); err == nil {
		t.Fatal("InvokeMethod missing descriptor method succeeded, want error")
	}
}

func TestBindGenericHandlers(t *testing.T) {
	desc := ServiceDesc{Name: "/generic/", Methods: []MethodDesc{{
		Name:       "/Echo/",
		NewRequest: func() any { return new(helloReq) },
		Metadata:   map[string]string{"request": "helloReq"},
	}}}
	bound, err := BindGenericHandlers(desc, map[string]GenericHandler{
		" /Echo/ ": func(ctx context.Context, raw json.RawMessage) (any, error) {
			var req helloReq
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			return helloResp{Message: "hello " + req.Name}, nil
		},
	})
	if err != nil {
		t.Fatalf("BindGenericHandlers: %v", err)
	}
	if desc.Methods[0].Handler != nil {
		t.Fatal("BindGenericHandlers mutated source descriptor handler")
	}
	if bound.Methods[0].Name != "Echo" {
		t.Fatalf("bound method name = %q, want canonical name", bound.Methods[0].Name)
	}
	req := bound.Methods[0].NewRequest()
	raw, ok := req.(*json.RawMessage)
	if !ok {
		t.Fatalf("generic request type = %T, want *json.RawMessage", req)
	}
	*raw = json.RawMessage(`{"name":"raw"}`)
	resp, err := bound.Methods[0].Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("generic handler: %v", err)
	}
	if got := resp.(helloResp).Message; got != "hello raw" {
		t.Fatalf("generic response = %q, want hello raw", got)
	}
	if _, err := BindGenericHandlers(desc, nil); err == nil {
		t.Fatal("BindGenericHandlers without handler succeeded, want error")
	}
	if _, err := BindGenericHandlers(desc, map[string]GenericHandler{
		"Echo": func(context.Context, json.RawMessage) (any, error) { return helloResp{}, nil },
		"Typo": func(context.Context, json.RawMessage) (any, error) { return helloResp{}, nil },
	}); err == nil || !strings.Contains(err.Error(), "undeclared method generic/Typo") {
		t.Fatalf("BindGenericHandlers extra handler error = %v, want undeclared method", err)
	}
	if _, err := BindGenericHandlers(desc, map[string]GenericHandler{
		"Echo":   func(context.Context, json.RawMessage) (any, error) { return helloResp{}, nil },
		"/Echo/": func(context.Context, json.RawMessage) (any, error) { return helloResp{}, nil },
	}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("BindGenericHandlers duplicate canonical handler error = %v, want duplicated", err)
	}
}

func TestGenericInvokerErrors(t *testing.T) {
	if _, err := NewGenericInvoker(nil); err == nil {
		t.Fatal("NewGenericInvoker nil client succeeded, want error")
	}
	invoker := &GenericInvoker{}
	if _, err := invoker.Invoke(context.Background(), "svc", "method", nil); err == nil {
		t.Fatal("Invoke nil invoker client succeeded, want error")
	}
	client := fakeGenericClient{}
	invoker, err := NewGenericInvoker(client)
	if err != nil {
		t.Fatalf("NewGenericInvoker: %v", err)
	}
	if _, err := invoker.Invoke(context.Background(), "", "method", nil); err == nil {
		t.Fatal("Invoke empty service succeeded, want error")
	}
	if _, err := invoker.Invoke(context.Background(), "svc", "", nil); err == nil {
		t.Fatal("Invoke empty method succeeded, want error")
	}
	if _, err := invoker.Invoke(context.Background(), "svc", "method", nil); !errors.Is(err, errFakeGeneric) {
		t.Fatalf("Invoke error = %v, want fake generic error", err)
	}
}

func TestServiceDescMethodPathContract(t *testing.T) {
	desc := ServiceDesc{Name: "/greeter.v1.Greeter/", Methods: []MethodDesc{{Name: "/SayHello/", NewRequest: func() any { return new(helloReq) }}}}
	path, err := desc.MethodPath(" /SayHello/ ")
	if err != nil {
		t.Fatal(err)
	}
	if path != "greeter.v1.Greeter/SayHello" {
		t.Fatalf("method path = %q, want canonical service/method path", path)
	}
	if got := desc.MustMethodPath("SayHello"); got != path {
		t.Fatalf("must method path = %q, want %q", got, path)
	}
	if _, err := desc.MethodPath("Missing"); err == nil || !strings.Contains(err.Error(), "not declared in service greeter.v1.Greeter") {
		t.Fatalf("missing method error = %v", err)
	}
	if err := (ServiceDesc{Name: "svc", Methods: []MethodDesc{{Name: "Call", NewRequest: func() any { return new(helloReq) }}, {Name: " /Call/ ", NewRequest: func() any { return new(helloReq) }}}}).Validate(); err == nil {
		t.Fatal("Validate accepted duplicate canonical method names, want error")
	}
}

func TestServiceDescStreamPathContract(t *testing.T) {
	desc := ServiceDesc{Name: "/greeter.v1.Greeter/", Streams: []StreamDesc{{Name: "/Chat/", NewMessage: func() any { return new(helloReq) }}}}
	path, err := desc.StreamPath(" /Chat/ ")
	if err != nil {
		t.Fatal(err)
	}
	if path != "greeter.v1.Greeter/Chat" {
		t.Fatalf("stream path = %q, want canonical service/stream path", path)
	}
	if got := desc.MustStreamPath("Chat"); got != path {
		t.Fatalf("must stream path = %q, want %q", got, path)
	}
	stream, ok := desc.Stream("Chat")
	if !ok || stream.Name != "/Chat/" {
		t.Fatalf("stream lookup = %+v ok=%v, want cloned descriptor", stream, ok)
	}
	if _, err := desc.StreamPath("Missing"); err == nil || !strings.Contains(err.Error(), "not declared in service greeter.v1.Greeter") {
		t.Fatalf("missing stream error = %v", err)
	}
	if err := (ServiceDesc{Name: "svc", Streams: []StreamDesc{{Name: "Chat", NewMessage: func() any { return new(helloReq) }}, {Name: " /Chat/ ", NewMessage: func() any { return new(helloReq) }}}}).Validate(); err == nil {
		t.Fatal("Validate accepted duplicate canonical stream names, want error")
	}
}

func TestGenericJSONPayloadHelpers(t *testing.T) {
	payload, err := EncodeJSONPayload(helloReq{Name: "helper"})
	if err != nil {
		t.Fatalf("EncodeJSONPayload: %v", err)
	}
	decoded, err := DecodeJSONPayload[helloReq](payload)
	if err != nil {
		t.Fatalf("DecodeJSONPayload: %v", err)
	}
	if decoded.Name != "helper" {
		t.Fatalf("decoded name = %q, want helper", decoded.Name)
	}
	raw, err := EncodeJSONPayload(json.RawMessage(`{"ok":true}`))
	if err != nil {
		t.Fatalf("EncodeJSONPayload raw: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("raw payload = %s, want unchanged raw json", string(raw))
	}
}

func TestGenericMethodBoundaries(t *testing.T) {
	nilHandler := GenericMethod("Echo", nil)
	raw := json.RawMessage(`{"name":"gofly"}`)
	if _, err := nilHandler.Handler(context.Background(), &raw); err == nil || !strings.Contains(err.Error(), "handler is nil") {
		t.Fatalf("nil generic handler error = %v, want handler is nil", err)
	}

	called := false
	method := GenericMethod("Echo", func(ctx context.Context, got json.RawMessage) (any, error) {
		called = true
		got[0] = '['
		return string(got), nil
	})
	resp, err := method.Handler(context.Background(), &raw)
	if err != nil {
		t.Fatalf("generic handler: %v", err)
	}
	if !called || resp.(string) != `["name":"gofly"}` {
		t.Fatalf("generic response = %#v called=%v, want cloned payload passed to handler", resp, called)
	}
	if string(raw) != `{"name":"gofly"}` {
		t.Fatalf("source raw payload mutated to %s, want defensive copy", raw)
	}
	if _, err := method.Handler(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "unexpected type") {
		t.Fatalf("non-pointer raw error = %v, want unexpected type", err)
	}
	if _, err := method.Handler(context.Background(), (*json.RawMessage)(nil)); err == nil || !strings.Contains(err.Error(), "unexpected type") {
		t.Fatalf("nil raw pointer error = %v, want unexpected type", err)
	}
}

func TestGenericPathAndJSONErrorBoundaries(t *testing.T) {
	path, err := MethodPath(" /svc/ ", " /Call/ ")
	if err != nil {
		t.Fatalf("MethodPath: %v", err)
	}
	if path != "svc/Call" {
		t.Fatalf("path = %q, want svc/Call", path)
	}
	if got := canonicalRPCName(" /svc/Call/ "); got != "svc/Call" {
		t.Fatalf("canonical name = %q, want svc/Call", got)
	}
	if _, err := DecodeJSONPayload[helloReq](json.RawMessage(`{"name":`)); err == nil || !strings.Contains(err.Error(), "unmarshal generic json payload") {
		t.Fatalf("DecodeJSONPayload invalid error = %v, want wrapped unmarshal error", err)
	}
	var decoded *helloReq
	decoded, err = DecodeJSONPayload[*helloReq](nil)
	if err != nil {
		t.Fatalf("DecodeJSONPayload nil payload: %v", err)
	}
	if decoded != nil {
		t.Fatalf("decoded nil payload = %#v, want nil pointer from JSON null", decoded)
	}
	if _, err := EncodeJSONPayload(func() {}); err == nil || !strings.Contains(err.Error(), "marshal generic json payload") {
		t.Fatalf("EncodeJSONPayload unsupported error = %v, want wrapped marshal error", err)
	}
}

func TestServiceDescHelpers(t *testing.T) {
	desc := ServiceDesc{Name: "greeter.v1.Greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Request:    "helloReq",
		Response:   "helloResp",
		Metadata:   map[string]string{"request": "helloReq"},
	}}}
	if err := desc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	path, err := desc.MethodPath("/SayHello/")
	if err != nil {
		t.Fatalf("MethodPath: %v", err)
	}
	if path != "greeter.v1.Greeter/SayHello" {
		t.Fatalf("method path = %q, want greeter.v1.Greeter/SayHello", path)
	}
	method, ok := desc.Method("SayHello")
	if !ok {
		t.Fatal("Method returned false, want true")
	}
	method.Metadata["request"] = "mutated"
	if desc.Methods[0].Metadata["request"] != "helloReq" {
		t.Fatalf("Method should return cloned metadata, got %q", desc.Methods[0].Metadata["request"])
	}
	cloned := CloneServiceDesc(desc)
	cloned.Methods[0].Metadata["request"] = "cloned"
	if desc.Methods[0].Metadata["request"] != "helloReq" {
		t.Fatalf("CloneServiceDesc should deep clone metadata, got %q", desc.Methods[0].Metadata["request"])
	}
	descriptor := desc.Descriptor()
	if descriptor.Name != "greeter.v1.Greeter" || len(descriptor.Methods) != 1 || descriptor.Methods[0].Request != "helloReq" || descriptor.Methods[0].Response != "helloResp" {
		t.Fatalf("Descriptor = %#v", descriptor)
	}
	descriptor.Methods[0].Metadata["request"] = "descriptor-mutated"
	if desc.Methods[0].Metadata["request"] != "helloReq" {
		t.Fatalf("Descriptor should deep clone metadata, got %q", desc.Methods[0].Metadata["request"])
	}
	if _, err := desc.MethodPath("Unknown"); err == nil {
		t.Fatal("MethodPath for unknown method succeeded, want error")
	}
}

func TestServiceDescDescriptorPrefersExplicitTypeFields(t *testing.T) {
	desc := ServiceDesc{
		Name: "greeter.v1.Greeter",
		Methods: []MethodDesc{{
			Name:       "SayHello",
			NewRequest: func() any { return new(helloReq) },
			Request:    "HelloRequest",
			Response:   "HelloResponse",
			Metadata:   map[string]string{"request": "legacyReq", "response": "legacyResp"},
		}},
		Streams: []StreamDesc{{
			Name:       "Watch",
			NewMessage: func() any { return new(helloReq) },
			Message:    "WatchMessage",
			Metadata:   map[string]string{"message": "legacyMessage"},
		}},
	}

	descriptor := desc.Descriptor()
	if got := descriptor.Methods[0]; got.Request != "HelloRequest" || got.Response != "HelloResponse" {
		t.Fatalf("method descriptor types = %#v, want explicit request/response", got)
	}
	if got := descriptor.Streams[0]; got.Message != "WatchMessage" {
		t.Fatalf("stream descriptor type = %#v, want explicit message", got)
	}
}

func TestServiceDescDescriptorSortsMethodsAndStreams(t *testing.T) {
	desc := ServiceDesc{
		Name: "greeter.v1.Greeter",
		Methods: []MethodDesc{
			{Name: "Zeta", NewRequest: func() any { return new(helloReq) }},
			{Name: "Alpha", NewRequest: func() any { return new(helloReq) }},
		},
		Streams: []StreamDesc{
			{Name: "WatchZeta", NewMessage: func() any { return new(helloReq) }},
			{Name: "WatchAlpha", NewMessage: func() any { return new(helloReq) }},
		},
	}

	descriptor := desc.Descriptor()
	if len(descriptor.Methods) != 2 || descriptor.Methods[0].Name != "Alpha" || descriptor.Methods[1].Name != "Zeta" {
		t.Fatalf("descriptor methods = %#v, want sorted by name", descriptor.Methods)
	}
	if len(descriptor.Streams) != 2 || descriptor.Streams[0].Name != "WatchAlpha" || descriptor.Streams[1].Name != "WatchZeta" {
		t.Fatalf("descriptor streams = %#v, want sorted by name", descriptor.Streams)
	}
}

func TestServiceDescDescriptorUsesStrongContractFields(t *testing.T) {
	desc := ServiceDesc{
		Name: "greeter.v1.Greeter",
		Methods: []MethodDesc{{
			Name:       "SayHello",
			NewRequest: func() any { return new(helloReq) },
			Request:    "HelloReq",
			Response:   "HelloResp",
			Codec:      "proto",
			HTTP:       HTTPBinding{Method: http.MethodPost, Path: "/v1/hello", Body: "*", ResponseBody: "message"},
		}},
		Streams: []StreamDesc{{
			Name:       "Watch",
			NewMessage: func() any { return new(helloReq) },
			Message:    "WatchEvent",
			Codec:      "json",
			Mode:       StreamModeServerStream,
		}},
	}

	descriptor := desc.Descriptor()
	if got := descriptor.Methods[0]; got.Request != "HelloReq" || got.Response != "HelloResp" || got.Codec != "proto" || got.HTTP == nil || got.HTTP.Path != "/v1/hello" {
		t.Fatalf("method descriptor = %#v, want strong contract fields", got)
	}
	if got := descriptor.Streams[0]; got.Message != "WatchEvent" || got.Codec != "json" || got.Mode != StreamModeServerStream {
		t.Fatalf("stream descriptor = %#v, want stream contract fields", got)
	}
}

func TestServiceDescDescriptorKeepsMetadataFallbackCompatibility(t *testing.T) {
	desc := ServiceDesc{
		Name: "greeter.v1.Greeter",
		Methods: []MethodDesc{{
			Name:       "SayHello",
			NewRequest: func() any { return new(helloReq) },
			Metadata: map[string]string{
				"request":           "HelloReq",
				"response":          "HelloResp",
				"codec":             "json",
				"http.method":       http.MethodGet,
				"http.path":         "/v1/hello/{name}",
				"http.responseBody": "message",
			},
		}},
		Streams: []StreamDesc{{
			Name:       "Watch",
			NewMessage: func() any { return new(helloReq) },
			Metadata:   map[string]string{"message": "WatchEvent", "clientStream": "true", "serverStream": "true"},
		}},
	}

	descriptor := desc.Descriptor()
	if got := descriptor.Methods[0]; got.Request != "HelloReq" || got.Response != "HelloResp" || got.Codec != "json" || got.HTTP == nil || got.HTTP.Method != http.MethodGet {
		t.Fatalf("method descriptor fallback = %#v, want metadata-derived fields", got)
	}
	if got := descriptor.Streams[0]; got.Message != "WatchEvent" || got.Mode != StreamModeBidiStream {
		t.Fatalf("stream descriptor fallback = %#v, want metadata-derived fields", got)
	}
}

func TestDescriptorValidateContractFields(t *testing.T) {
	valid := Descriptor{
		Name: "greeter.v1.Greeter",
		Methods: []MethodDescriptor{{
			Name:    "SayHello",
			Codec:   "json",
			HTTP:    &HTTPBinding{Method: http.MethodPost, Path: "/v1/hello", Body: "*"},
			Timeout: time.Second,
		}},
		Streams: []StreamDescriptor{{
			Name:    "Watch",
			Codec:   "proto",
			Mode:    StreamModeServerStream,
			Timeout: time.Second,
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid descriptor rejected: %v", err)
	}

	tests := []struct {
		name string
		desc Descriptor
		want string
	}{
		{
			name: "method negative timeout",
			desc: Descriptor{Name: "svc", Methods: []MethodDescriptor{{
				Name:    "Call",
				Timeout: -time.Second,
			}}},
			want: "timeout must be non-negative",
		},
		{
			name: "unsupported method codec",
			desc: Descriptor{Name: "svc", Methods: []MethodDescriptor{{
				Name:  "Call",
				Codec: "xml",
			}}},
			want: "codec \"xml\" is unsupported",
		},
		{
			name: "incomplete http binding",
			desc: Descriptor{Name: "svc", Methods: []MethodDescriptor{{
				Name: "Call",
				HTTP: &HTTPBinding{Method: http.MethodGet},
			}}},
			want: "http binding requires both method and path",
		},
		{
			name: "relative http path",
			desc: Descriptor{Name: "svc", Methods: []MethodDescriptor{{
				Name: "Call",
				HTTP: &HTTPBinding{Method: http.MethodGet, Path: "v1/users"},
			}}},
			want: "must be absolute",
		},
		{
			name: "unsupported http method",
			desc: Descriptor{Name: "svc", Methods: []MethodDescriptor{{
				Name: "Call",
				HTTP: &HTTPBinding{Method: "TRACE", Path: "/v1/users"},
			}}},
			want: "method \"TRACE\" is unsupported",
		},
		{
			name: "unsupported stream mode",
			desc: Descriptor{Name: "svc", Streams: []StreamDescriptor{{
				Name: "Watch",
				Mode: "server",
			}}},
			want: "stream mode \"server\" is unsupported",
		},
		{
			name: "stream negative timeout",
			desc: Descriptor{Name: "svc", Streams: []StreamDescriptor{{
				Name:    "Watch",
				Timeout: -time.Second,
			}}},
			want: "timeout must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.desc.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestServiceDescValidateChecksDescriptorContractFields(t *testing.T) {
	desc := ServiceDesc{
		Name: "svc",
		Methods: []MethodDesc{{
			Name:       "Call",
			NewRequest: func() any { return new(helloReq) },
			Codec:      "xml",
		}},
	}
	err := desc.Validate()
	if err == nil || !strings.Contains(err.Error(), "codec \"xml\" is unsupported") {
		t.Fatalf("Validate() error = %v, want unsupported codec", err)
	}
}

func TestCompareDescriptors(t *testing.T) {
	base := Descriptor{
		Name:    "greeter.v1.Greeter",
		Version: "v1",
		Methods: []MethodDescriptor{
			{Name: "SayHello", Request: "HelloReq", Response: "HelloResp", Timeout: time.Second},
			{Name: "Legacy", Request: "LegacyReq", Response: "LegacyResp"},
		},
		Streams: []StreamDescriptor{{Name: "Watch", Message: "WatchEvent", Timeout: 2 * time.Second}},
	}
	target := Descriptor{
		Name:    "greeter.v1.Greeter",
		Version: "v2",
		Methods: []MethodDescriptor{
			{Name: "SayHello", Request: "HelloReq", Response: "HelloRespV2", Timeout: 500 * time.Millisecond},
			{Name: "Create", Request: "CreateReq", Response: "CreateResp"},
		},
		Streams: []StreamDescriptor{{Name: "Watch", Message: "WatchEventV2", Timeout: 3 * time.Second}},
	}

	report := CompareDescriptors(base, target)
	if report.Breaking != 3 || report.Warnings != 1 || !report.HasBreaking() || report.IsCompatible() {
		t.Fatalf("report = %#v, want 3 breaking and 1 warning", report)
	}
	assertDescriptorChange(t, report, DescriptorChangeMethod, DescriptorChangeBreaking, "greeter.v1.Greeter/Legacy")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeBreaking, "greeter.v1.Greeter/SayHello response")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeBreaking, "greeter.v1.Greeter/Watch message")
	assertDescriptorChange(t, report, DescriptorChangeTimeout, DescriptorChangeWarning, "greeter.v1.Greeter/SayHello")
	assertDescriptorChange(t, report, DescriptorChangeMethod, DescriptorChangeInfo, "greeter.v1.Greeter/Create")
	assertDescriptorChange(t, report, DescriptorChangeVersion, DescriptorChangeInfo, "greeter.v1.Greeter")
}

func TestCompareDescriptorsCompatibleAdditions(t *testing.T) {
	base := Descriptor{Name: "greeter", Methods: []MethodDescriptor{{Name: "SayHello", Request: "HelloReq", Response: "HelloResp"}}}
	target := Descriptor{Name: "greeter", Methods: []MethodDescriptor{{Name: "SayHello", Request: "HelloReq", Response: "HelloResp"}, {Name: "Health", Request: "HealthReq", Response: "HealthResp"}}}

	report := CompareDescriptors(base, target)
	if report.HasBreaking() || !report.IsCompatible() {
		t.Fatalf("report = %#v, want compatible additions", report)
	}
	assertDescriptorChange(t, report, DescriptorChangeMethod, DescriptorChangeInfo, "greeter/Health")
}

func TestDescriptorValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		desc Descriptor
	}{
		{name: "empty service", desc: Descriptor{}},
		{name: "empty method", desc: Descriptor{Name: "greeter", Methods: []MethodDescriptor{{}}}},
		{name: "duplicated method", desc: Descriptor{Name: "greeter", Methods: []MethodDescriptor{{Name: "Call"}, {Name: "Call"}}}},
		{name: "empty stream", desc: Descriptor{Name: "greeter", Streams: []StreamDescriptor{{}}}},
		{name: "duplicated stream", desc: Descriptor{Name: "greeter", Streams: []StreamDescriptor{{Name: "Watch"}, {Name: "Watch"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.desc.Validate(); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

func TestDescriptorValidateAllowsNamedMembers(t *testing.T) {
	desc := Descriptor{
		Name:    "greeter",
		Methods: []MethodDescriptor{{Name: "SayHello"}},
		Streams: []StreamDescriptor{{Name: "Watch"}},
	}
	if err := desc.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestCompareDescriptorsChangeOrderIsDeterministic(t *testing.T) {
	base := Descriptor{
		Name: "greeter",
		Methods: []MethodDescriptor{
			{Name: "Zulu", Request: "Req", Response: "Resp"},
			{Name: "Alpha", Request: "Req", Response: "Resp"},
		},
		Streams: []StreamDescriptor{
			{Name: "StreamZulu", Message: "Event"},
			{Name: "StreamAlpha", Message: "Event"},
		},
	}
	target := Descriptor{
		Name: "greeter",
		Methods: []MethodDescriptor{
			{Name: "Zulu", Request: "Req", Response: "RespV2"},
			{Name: "Beta", Request: "Req", Response: "Resp"},
		},
		Streams: []StreamDescriptor{
			{Name: "StreamZulu", Message: "EventV2"},
			{Name: "StreamBeta", Message: "Event"},
		},
	}

	report := CompareDescriptors(base, target)
	got := make([]string, 0, len(report.Changes))
	for _, change := range report.Changes {
		got = append(got, string(change.Category)+":"+change.Subject)
	}
	want := []string{
		"method:greeter/Alpha",
		"signature:greeter/Zulu response",
		"method:greeter/Beta",
		"stream:greeter/StreamAlpha",
		"signature:greeter/StreamZulu message",
		"stream:greeter/StreamBeta",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("change order = %#v, want %#v", got, want)
	}
}

func TestCompareDescriptorsBoundaryBranches(t *testing.T) {
	base := Descriptor{
		Name:    "greeter.v1.Greeter",
		Version: "v1",
		Methods: []MethodDescriptor{
			{
				Name:     "Changed",
				Request:  "Req",
				Response: "Resp",
				Codec:    "json",
				HTTP:     &HTTPBinding{Method: http.MethodPost, Path: "/v1/changed", Body: "*", ResponseBody: "resp"},
				Timeout:  time.Second,
			},
			{Name: "AddedTypes"},
		},
		Streams: []StreamDescriptor{
			{Name: "ModeChanged", Message: "Event", Codec: "proto", Mode: StreamModeBidiStream, Timeout: 2 * time.Second},
			{Name: "AddedStreamMetadata"},
		},
	}
	target := Descriptor{
		Name:    "greeter.v1.GreeterV2",
		Version: "v2",
		Methods: []MethodDescriptor{
			{
				Name:     "Changed",
				Response: "RespV2",
				HTTP:     &HTTPBinding{Path: "/v2/changed", Body: "request"},
				Timeout:  3 * time.Second,
			},
			{Name: "AddedTypes", Request: "AddedReq", Response: "AddedResp", Codec: "thrift", HTTP: &HTTPBinding{Method: http.MethodGet, Path: "/v1/added"}},
		},
		Streams: []StreamDescriptor{
			{Name: "ModeChanged", Message: "Event", Timeout: time.Second},
			{Name: "AddedStreamMetadata", Message: "AddedEvent", Codec: "json", Mode: StreamModeServerStream},
		},
	}

	report := CompareDescriptors(base, target)
	assertDescriptorChange(t, report, DescriptorChangeService, DescriptorChangeBreaking, "greeter.v1.Greeter")
	assertDescriptorChange(t, report, DescriptorChangeVersion, DescriptorChangeInfo, "greeter.v1.Greeter")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed request")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed response")
	assertDescriptorChange(t, report, DescriptorChangeCodec, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed codec")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed http method")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed http path")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed http body")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeBreaking, "greeter.v1.Greeter/Changed http response body")
	assertDescriptorChange(t, report, DescriptorChangeTimeout, DescriptorChangeInfo, "greeter.v1.Greeter/Changed")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeInfo, "greeter.v1.Greeter/AddedTypes request")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeInfo, "greeter.v1.Greeter/AddedTypes response")
	assertDescriptorChange(t, report, DescriptorChangeCodec, DescriptorChangeInfo, "greeter.v1.Greeter/AddedTypes codec")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeInfo, "greeter.v1.Greeter/AddedTypes http method")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeInfo, "greeter.v1.Greeter/AddedTypes http path")
	assertDescriptorChange(t, report, DescriptorChangeCodec, DescriptorChangeBreaking, "greeter.v1.Greeter/ModeChanged codec")
	assertDescriptorChange(t, report, DescriptorChangeBinding, DescriptorChangeBreaking, "greeter.v1.Greeter/ModeChanged mode")
	assertDescriptorChange(t, report, DescriptorChangeSignature, DescriptorChangeInfo, "greeter.v1.Greeter/AddedStreamMetadata message")
}

func TestDescriptorStreamModeMetadataBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		explicit StreamMode
		metadata map[string]string
		want     StreamMode
	}{
		{name: "explicit wins", explicit: StreamModeClientStream, metadata: map[string]string{"stream.mode": string(StreamModeBidiStream)}, want: StreamModeClientStream},
		{name: "empty metadata", metadata: nil, want: ""},
		{name: "stream mode metadata", metadata: map[string]string{"stream.mode": " server_stream "}, want: StreamModeServerStream},
		{name: "client and server flags", metadata: map[string]string{"clientStream": "true", "serverStream": "TRUE"}, want: StreamModeBidiStream},
		{name: "client flag", metadata: map[string]string{"clientStream": " true "}, want: StreamModeClientStream},
		{name: "server flag", metadata: map[string]string{"serverStream": " true "}, want: StreamModeServerStream},
		{name: "flags absent defaults unary", metadata: map[string]string{"clientStream": "false", "serverStream": "false"}, want: StreamModeUnary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := descriptorStreamMode(tt.explicit, tt.metadata); got != tt.want {
				t.Fatalf("descriptorStreamMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceDescMustPathPanics(t *testing.T) {
	desc := ServiceDesc{Name: "greeter", Methods: []MethodDesc{{Name: "SayHello", NewRequest: func() any { return new(helloReq) }}}, Streams: []StreamDesc{{Name: "Chat", NewMessage: func() any { return new(helloReq) }}}}
	if got := desc.MustMethodPath("SayHello"); got != "greeter/SayHello" {
		t.Fatalf("MustMethodPath = %q, want greeter/SayHello", got)
	}
	if got := desc.MustStreamPath("Chat"); got != "greeter/Chat" {
		t.Fatalf("MustStreamPath = %q, want greeter/Chat", got)
	}

	assertPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		fn()
	}
	assertPanic("missing method", func() { _ = desc.MustMethodPath("Missing") })
	assertPanic("missing stream", func() { _ = desc.MustStreamPath("Missing") })
}

func assertDescriptorChange(t *testing.T, report DescriptorCompatibilityReport, category DescriptorChangeCategory, severity DescriptorChangeSeverity, subject string) {
	t.Helper()
	for _, change := range report.Changes {
		if change.Category == category && change.Severity == severity && change.Subject == subject {
			return
		}
	}
	t.Fatalf("missing descriptor change category=%s severity=%s subject=%s in %#v", category, severity, subject, report.Changes)
}

func TestServiceDescValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		desc ServiceDesc
	}{
		{name: "empty service", desc: ServiceDesc{}},
		{name: "empty method", desc: ServiceDesc{Name: "svc", Methods: []MethodDesc{{}}}},
		{name: "missing request factory", desc: ServiceDesc{Name: "svc", Methods: []MethodDesc{{Name: "Call"}}}},
		{name: "duplicated method", desc: ServiceDesc{Name: "svc", Methods: []MethodDesc{{Name: "Call", NewRequest: func() any { return new(helloReq) }}, {Name: "Call", NewRequest: func() any { return new(helloReq) }}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.desc.Validate(); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

var errFakeGeneric = errors.New("fake generic error")

type fakeGenericClient struct{}

func (fakeGenericClient) CallRaw(context.Context, string, any) (json.RawMessage, metadata.MD, error) {
	return nil, nil, errFakeGeneric
}

type recordingGenericClient struct {
	method  string
	payload json.RawMessage
}

func (c *recordingGenericClient) CallRaw(ctx context.Context, method string, request any) (json.RawMessage, metadata.MD, error) {
	c.method = method
	return append(json.RawMessage(nil), c.payload...), nil, nil
}
