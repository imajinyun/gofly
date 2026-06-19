// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gofly/gofly/core/metadata"
)

type GenericInvoker struct {
	client GenericClient
}

type GenericResponse struct {
	Payload  json.RawMessage `json:"payload,omitempty"`
	Metadata metadata.MD     `json:"metadata,omitempty"`
}

type GenericHandler func(context.Context, json.RawMessage) (any, error)

func NewGenericInvoker(client GenericClient) (*GenericInvoker, error) {
	if client == nil {
		return nil, errors.New("generic rpc client is nil")
	}
	return &GenericInvoker{client: client}, nil
}

func (g *GenericInvoker) Invoke(ctx context.Context, service string, method string, request any) (GenericResponse, error) {
	if g == nil || g.client == nil {
		return GenericResponse{}, errors.New("generic rpc invoker is nil")
	}
	path, err := genericMethodPath(service, method)
	if err != nil {
		return GenericResponse{}, err
	}
	payload, md, err := g.client.CallRaw(ctx, path, request)
	if err != nil {
		return GenericResponse{}, err
	}
	return GenericResponse{Payload: payload, Metadata: md}, nil
}

func (g *GenericInvoker) InvokeMethod(ctx context.Context, desc ServiceDesc, method string, request any) (GenericResponse, error) {
	if g == nil || g.client == nil {
		return GenericResponse{}, errors.New("generic rpc invoker is nil")
	}
	path, err := desc.MethodPath(method)
	if err != nil {
		return GenericResponse{}, err
	}
	payload, md, err := g.client.CallRaw(ctx, path, request)
	if err != nil {
		return GenericResponse{}, err
	}
	return GenericResponse{Payload: payload, Metadata: md}, nil
}

func GenericMethod(name string, handler GenericHandler) MethodDesc {
	return MethodDesc{
		Name:       name,
		NewRequest: func() any { return new(json.RawMessage) },
		Request:    "json.RawMessage",
		Response:   "any",
		Handler: func(ctx context.Context, req any) (any, error) {
			if handler == nil {
				return nil, errors.New("generic rpc handler is nil")
			}
			raw, ok := req.(*json.RawMessage)
			if !ok || raw == nil {
				return nil, fmt.Errorf("generic rpc request has unexpected type %T", req)
			}
			return handler(ctx, append(json.RawMessage(nil), (*raw)...))
		},
	}
}

func BindGenericHandlers(desc ServiceDesc, handlers map[string]GenericHandler) (ServiceDesc, error) {
	if err := desc.Validate(); err != nil {
		return ServiceDesc{}, err
	}
	out := CloneServiceDesc(desc)
	canonicalHandlers := make(map[string]GenericHandler, len(handlers))
	for name, handler := range handlers {
		if handler == nil {
			continue
		}
		methodName := canonicalRPCName(name)
		if _, ok := canonicalHandlers[methodName]; ok {
			return ServiceDesc{}, fmt.Errorf("generic rpc handler for %s/%s is duplicated", canonicalRPCName(out.Name), methodName)
		}
		canonicalHandlers[methodName] = handler
	}
	used := make(map[string]struct{}, len(out.Methods))
	for i, method := range out.Methods {
		methodName := canonicalRPCName(method.Name)
		handler := canonicalHandlers[methodName]
		if handler == nil {
			return ServiceDesc{}, fmt.Errorf("generic rpc handler for %s/%s is required", canonicalRPCName(out.Name), methodName)
		}
		generic := GenericMethod(methodName, handler)
		out.Methods[i].Name = methodName
		out.Methods[i].NewRequest = generic.NewRequest
		out.Methods[i].Handler = generic.Handler
		used[methodName] = struct{}{}
	}
	for methodName := range canonicalHandlers {
		if _, ok := used[methodName]; !ok {
			return ServiceDesc{}, fmt.Errorf("generic rpc handler for undeclared method %s/%s", canonicalRPCName(out.Name), methodName)
		}
	}
	return out, nil
}

func EncodeJSONPayload(v any) (json.RawMessage, error) {
	if raw, ok := v.(json.RawMessage); ok {
		return append(json.RawMessage(nil), raw...), nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal generic json payload: %w", err)
	}
	return json.RawMessage(data), nil
}

func DecodeJSONPayload[T any](payload json.RawMessage) (T, error) {
	var out T
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return out, fmt.Errorf("unmarshal generic json payload: %w", err)
	}
	return out, nil
}

func MethodPath(service string, method string) (string, error) {
	return genericMethodPath(service, method)
}

func genericMethodPath(service string, method string) (string, error) {
	service = canonicalRPCName(service)
	method = canonicalRPCName(method)
	if service == "" {
		return "", errors.New("generic rpc service is required")
	}
	if method == "" {
		return "", errors.New("generic rpc method is required")
	}
	return service + "/" + method, nil
}

func canonicalRPCName(name string) string {
	return strings.Trim(strings.TrimSpace(name), "/")
}
