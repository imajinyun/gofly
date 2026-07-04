// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/observability/trace"
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

func (g *Gateway) proxyWebSocket(w http.ResponseWriter, r *http.Request, route Route) (proxyResult, error) {
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
	target, err := buildTargetURL(endpoint, route, r.URL)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	upstream, upstreamRW, upstreamResp, err := dialWebSocketUpstream(r, target, route)
	if err != nil {
		g.reportEndpoint(route, endpoint, false)
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	downstream, downstreamRW, err := hijackGatewayWebSocket(w)
	if err != nil {
		_ = upstream.Close()
		g.reportEndpoint(route, endpoint, false)
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	if err := upstreamResp.Write(downstreamRW); err != nil {
		_ = downstream.Close()
		_ = upstream.Close()
		return proxyResult{Endpoint: endpoint, Status: http.StatusSwitchingProtocols, Hijacked: true, Err: err}, err
	}
	if err := downstreamRW.Flush(); err != nil {
		_ = downstream.Close()
		_ = upstream.Close()
		return proxyResult{Endpoint: endpoint, Status: http.StatusSwitchingProtocols, Hijacked: true, Err: err}, err
	}
	g.reportEndpoint(route, endpoint, true)
	if brk != nil {
		brk.MarkSuccess()
	}
	go tunnelWebSocket(downstream, downstreamRW, upstream, upstreamRW)
	return proxyResult{Endpoint: endpoint, Status: http.StatusSwitchingProtocols, Hijacked: true}, nil
}

func dialWebSocketUpstream(r *http.Request, target *url.URL, route Route) (net.Conn, *bufio.ReadWriter, *http.Response, error) {
	address := target.Host
	if address == "" {
		return nil, nil, nil, errors.New("websocket upstream host is required")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		switch target.Scheme {
		case "http", "ws":
			address = net.JoinHostPort(address, "80")
		case "https", "wss":
			address = net.JoinHostPort(address, "443")
		default:
			return nil, nil, nil, fmt.Errorf("unsupported websocket upstream scheme %q", target.Scheme)
		}
	}
	dialer := net.Dialer{}
	var conn net.Conn
	var err error
	if target.Scheme == "https" || target.Scheme == "wss" {
		conn, err = tls.DialWithDialer(&dialer, "tcp", address, &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: target.Hostname(),
		})
	} else {
		conn, err = dialer.DialContext(r.Context(), "tcp", address)
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial websocket upstream: %w", err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	upstreamReq := r.Clone(r.Context())
	upstreamReq.URL = target
	upstreamReq.RequestURI = ""
	upstreamReq.Host = target.Host
	upstreamReq.Body = nil
	upstreamReq.GetBody = nil
	upstreamReq.ContentLength = 0
	upstreamReq.Header = cloneHeader(r.Header)
	applyHeaderPolicy(upstreamReq.Header, route.Header)
	setForwardHeaders(upstreamReq, r, route)
	if err := upstreamReq.Write(rw); err != nil {
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("write websocket upstream request: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("flush websocket upstream request: %w", err)
	}
	resp, err := http.ReadResponse(rw.Reader, upstreamReq)
	if err != nil {
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("read websocket upstream response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("websocket upstream status = %d", resp.StatusCode)
	}
	if err := resp.Body.Close(); err != nil {
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("close websocket upstream response: %w", err)
	}
	return conn, rw, resp, nil
}

func hijackGatewayWebSocket(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("hijack websocket downstream: %w", err)
	}
	return conn, rw, nil
}

func tunnelWebSocket(downstream net.Conn, downstreamRW *bufio.ReadWriter, upstream net.Conn, upstreamRW *bufio.ReadWriter) {
	defer downstream.Close()
	defer upstream.Close()
	var once sync.Once
	closeBoth := func() {
		_ = downstream.Close()
		_ = upstream.Close()
	}
	go func() {
		_, _ = io.Copy(upstream, downstreamRW.Reader)
		once.Do(closeBoth)
	}()
	_, _ = io.Copy(downstream, upstreamRW.Reader)
	once.Do(closeBoth)
}

func isWebSocketUpgrade(r *http.Request) bool {
	return r != nil &&
		r.Method == http.MethodGet &&
		headerContainsToken(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func headerContainsToken(value string, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
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
	if resp.StatusCode < http.StatusInternalServerError && isStreamingResponse(resp.Header) {
		success := resp.StatusCode < http.StatusInternalServerError
		g.reportEndpoint(route, endpoint, success)
		if brk != nil {
			if success {
				brk.MarkSuccess()
			} else {
				brk.MarkFailure()
			}
		}
		return proxyResult{Endpoint: endpoint, Status: resp.StatusCode, Header: cloneHeader(resp.Header), BodyStream: resp.Body}, nil
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

func isStreamingResponse(header http.Header) bool {
	contentType := strings.ToLower(strings.TrimSpace(header.Get("Content-Type")))
	return strings.HasPrefix(contentType, "text/event-stream")
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
