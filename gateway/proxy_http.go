// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/trace"
)

// proxyOnce executes a single proxy attempt for the matched route.
func (g *Gateway) proxyOnce(r *http.Request, route Route, body []byte) (proxyResult, error) {
	brk := g.breakerFor(route)
	if brk != nil {
		if err := brk.Allow(); err != nil {
			return proxyResult{Err: err}, err
		}
	}
	endpoint, err := g.pickEndpoint(r.Context(), route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Err: err}, err
	}
	return gatewayRouteDispatcher{gateway: g}.proxy(r, route, endpoint, body, brk)
}

func (g *Gateway) proxyHTTPOnce(r *http.Request, route Route, endpoint string, body []byte, brk *breaker.AdaptiveBreaker) (proxyResult, error) {
	target, err := buildTargetURL(endpoint, route, r.URL)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	out, err := cloneProxyRequest(r, target, route, body)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	resp, err := g.client.Do(out)
	if err != nil {
		g.reportEndpoint(route, endpoint, false)
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	defer resp.Body.Close()
	respBody, copyErr := io.ReadAll(resp.Body)
	success := resp.StatusCode < http.StatusInternalServerError && copyErr == nil
	g.reportEndpoint(route, endpoint, success)
	if brk != nil {
		if success {
			brk.MarkSuccess()
		} else {
			brk.MarkFailure()
		}
	}
	return proxyResult{Endpoint: endpoint, Status: resp.StatusCode, Header: cloneHeader(resp.Header), Body: respBody, Err: copyErr}, copyErr
}

func cloneProxyRequest(r *http.Request, target *url.URL, route Route, body []byte) (*http.Request, error) {
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out.Header = cloneHeader(r.Header)
	applyHeaderPolicy(out.Header, route.Header)
	setForwardHeaders(out, r, route)
	out.ContentLength = int64(len(body))
	out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	if route.PreserveHost {
		out.Host = r.Host
	} else {
		out.Host = target.Host
	}
	return out, nil
}

func buildTargetURL(endpoint string, route Route, original *url.URL) (*url.URL, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, errors.New("endpoint must include scheme and host")
	}
	path := rewritePath(route, original.Path)
	base.Path = joinURLPath(base.Path, path)
	base.RawPath = ""
	base.RawQuery = original.RawQuery
	return base, nil
}

func rewritePath(route Route, requestPath string) string {
	if route.PathPrefix == "/" {
		if route.UpstreamPrefix == "" {
			return requestPath
		}
		return joinURLPath(route.UpstreamPrefix, requestPath)
	}
	if strings.HasPrefix(requestPath, route.PathPrefix) {
		suffix := strings.TrimPrefix(requestPath, route.PathPrefix)
		if route.UpstreamPrefix == "" {
			if suffix == "" {
				return "/"
			}
			return suffix
		}
		return joinURLPath(route.UpstreamPrefix, suffix)
	}
	return requestPath
}

func setForwardHeaders(out *http.Request, original *http.Request, route Route) {
	if clientIP, _, err := net.SplitHostPort(original.RemoteAddr); err == nil && clientIP != "" {
		prior := out.Header.Get(HeaderForwardedFor)
		if prior != "" {
			clientIP = prior + ", " + clientIP
		}
		out.Header.Set(HeaderForwardedFor, clientIP)
	}
	out.Header.Set(HeaderForwardedHost, original.Host)
	if original.TLS == nil {
		out.Header.Set(HeaderForwardedProto, "http")
	} else {
		out.Header.Set(HeaderForwardedProto, "https")
	}
	if route.Service != "" {
		out.Header.Set(HeaderGatewayService, route.Service)
	}
	if route.Name != "" {
		out.Header.Set(HeaderGatewayRoute, route.Name)
	}
	for key, value := range route.Headers {
		out.Header.Set(key, value)
	}
	// Propagate W3C trace context to the upstream service.
	if sc, ok := trace.FromContext(original.Context()); ok {
		out.Header.Set(trace.TraceParentHeader, trace.TraceParent(sc))
	}
}
