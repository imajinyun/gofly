// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gofly/gofly/core/breaker"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/rpc"
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
	client, err := g.transcoderFor(endpoint, route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	payload := transcodeRequestPayload(body)
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
	return proxyResult{
		Endpoint: endpoint,
		Status:   http.StatusOK,
		Header:   transcodeResponseHeader(md),
		Body:     append([]byte(nil), raw...),
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
	return transcodeResolvedTarget{service: desc.Name, method: method}, nil
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

func transcodeRequestPayload(body []byte) json.RawMessage {
	if len(bytes.TrimSpace(body)) == 0 {
		return json.RawMessage("null")
	}
	return json.RawMessage(append([]byte(nil), body...))
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
