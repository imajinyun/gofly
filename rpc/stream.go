// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/retry"
)

const (
	streamMaxFrameBytes = 4 << 20
	streamUpgradeToken  = "gofly-stream"
)

var ErrStreamClosed = errors.New("rpc stream closed")

type streamEnvelope struct {
	Payload      json.RawMessage `json:"payload,omitempty"`
	PayloadBytes []byte          `json:"payloadBytes,omitempty"`
	Codec        string          `json:"codec,omitempty"`
	Code         Code            `json:"code,omitempty"`
	Error        string          `json:"error,omitempty"`
	End          bool            `json:"end,omitempty"`
}

type Stream struct {
	conn           net.Conn
	rw             *bufio.ReadWriter
	codec          Codec
	maxFrame       int64
	readTimeout    time.Duration
	writeTimeout   time.Duration
	stateMu        sync.Mutex
	terminalCode   Code
	terminalReason string
	writeMu        sync.Mutex
	closeMu        sync.Mutex
	closeHooks     []func()
	once           sync.Once
	closed         chan struct{}
	lastActivity   atomic.Int64
}

type rpcStreamTransportRuntime struct {
	mu              sync.Mutex
	active          int64
	dials           int64
	dedicatedConns  int64
	closes          int64
	lastTarget      string
	lastDialedAt    time.Time
	lastClosedAt    time.Time
	lastCloseCode   Code
	lastCloseReason string
	closeCodes      map[Code]int64
	closeReasons    map[string]int64
}

func newRPCStreamTransportRuntime() *rpcStreamTransportRuntime {
	return &rpcStreamTransportRuntime{
		closeCodes:   make(map[Code]int64),
		closeReasons: make(map[string]int64),
	}
}

func (r *rpcStreamTransportRuntime) recordDial(target string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active++
	r.dials++
	r.dedicatedConns++
	r.lastTarget = target
	r.lastDialedAt = time.Now()
}

func (r *rpcStreamTransportRuntime) recordClose(code Code, reason string) {
	if r == nil {
		return
	}
	if code == "" {
		code = CodeOK
	}
	if reason == "" {
		reason = streamCloseReason(code)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active > 0 {
		r.active--
	}
	r.closes++
	r.lastClosedAt = time.Now()
	r.lastCloseCode = code
	r.lastCloseReason = reason
	if r.closeCodes == nil {
		r.closeCodes = make(map[Code]int64)
	}
	if r.closeReasons == nil {
		r.closeReasons = make(map[string]int64)
	}
	r.closeCodes[code]++
	r.closeReasons[reason]++
}

func (r *rpcStreamTransportRuntime) Snapshot() RPCStreamTransportSnapshot {
	if r == nil {
		return RPCStreamTransportSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return RPCStreamTransportSnapshot{
		Active:          r.active,
		Dials:           r.dials,
		DedicatedConns:  r.dedicatedConns,
		Closes:          r.closes,
		LastTarget:      r.lastTarget,
		LastDialedAt:    r.lastDialedAt,
		LastClosedAt:    r.lastClosedAt,
		LastCloseCode:   r.lastCloseCode,
		LastCloseReason: r.lastCloseReason,
		CloseCodes:      cloneCodeInt64Map(r.closeCodes),
		CloseReasons:    cloneStringInt64Map(r.closeReasons),
	}
}

func streamCloseReason(code Code) string {
	switch code {
	case "", CodeOK:
		return "ok"
	case CodeCanceled:
		return "canceled"
	case CodeDeadlineExceeded:
		return "deadline"
	case CodeUnavailable:
		return "unavailable"
	default:
		return "remote_error"
	}
}

func cloneCodeInt64Map(in map[Code]int64) map[Code]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[Code]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringInt64Map(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (s *HTTPServer) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), streamUpgradeToken) {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "invalid stream upgrade")
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/rpc/stream/")
	s.mu.RLock()
	desc, ok := s.streams[key]
	s.mu.RUnlock()
	if !ok {
		writeRPCError(w, http.StatusNotFound, CodeNotFound, "stream not found")
		return
	}
	service, rpcMethod := splitRPCMethod(key)
	governanceReq := governance.Request{
		Transport: governance.TransportRPC,
		Service:   service,
		Method:    rpcMethod,
		Path:      "/" + strings.TrimPrefix(key, "/"),
		Tags:      s.rpcTags(service, rpcMethod, desc.Metadata),
		Headers:   headerMap(r.Header),
	}
	decision := s.governanceDecisionContext(r.Context(), governanceReq)
	policy := decision.Policy
	runtimeKey := governanceRuntimeKey(decision, key)
	if limiter := s.ruleRateLimiter(runtimeKey, policy.RateLimit); limiter != nil && !limiter.Allow() {
		writeRPCError(w, http.StatusTooManyRequests, CodeResourceExhausted, "too many requests")
		return
	}
	if limiter := s.ruleConcurrencyLimiter(runtimeKey, policy.Concurrency); limiter != nil {
		if !limiter.TryAcquire() {
			writeRPCError(w, http.StatusServiceUnavailable, CodeUnavailable, "too many concurrent streams")
			return
		}
		defer limiter.Release()
	}
	brk := s.ruleBreaker(runtimeKey, policy.Breaker)
	if brk != nil {
		if err := brk.Allow(); err != nil {
			writeRPCError(w, http.StatusServiceUnavailable, CodeUnavailable, err.Error())
			return
		}
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeRPCError(w, http.StatusInternalServerError, CodeInternal, "stream hijack is unsupported")
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: %s\r\nConnection: Upgrade\r\n\r\n", streamUpgradeToken); err != nil {
		_ = conn.Close()
		return
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	stream := newStream(conn, rw, s.opts.codec)
	defer stream.Close()
	ctx := r.Context()
	if md := streamMetadataFromHeader(r.Header); len(md) > 0 {
		ctx = metadata.NewContext(ctx, md)
	}
	ctx = applyGovernanceMetadata(ctx, s.serviceMetadata(service))
	ctx = applyGovernanceMetadata(ctx, desc.Metadata)
	ctx = applyGovernanceMetadata(ctx, canaryMetadata(policy.Canary, governanceReq))
	ctx = applyGovernanceMetadata(ctx, policy.Metadata)
	streamTimeout := effectiveTimeout(policy.Timeout, desc.Timeout)
	ctx, cancel := withPolicyTimeout(ctx, streamTimeout)
	defer cancel()
	var terminalOnce sync.Once
	sendStreamError := func(err error) {
		terminalOnce.Do(func() {
			if serr := stream.SendError(CodeOf(err), textOf(err)); serr != nil {
				slog.Error("rpc stream send error failed", "code", CodeOf(err), "error", serr)
			}
		})
	}
	errCh := make(chan error, 1)
	handler := chainStreamMiddlewares(appendStreamMiddlewares(s.opts.streamMiddlewares, desc.Middlewares)...)(desc.Handler)
	go func() {
		errCh <- handler(ctx, stream)
	}()
	var handlerErr error
	if streamTimeout > 0 {
		select {
		case handlerErr = <-errCh:
		case <-ctx.Done():
			err := normalizeContextError(ctx, nil)
			if brk != nil {
				brk.MarkFailure()
			}
			sendStreamError(err)
			cancel()
			_ = stream.Close()
			waitStreamHandler(errCh, 100*time.Millisecond)
			return
		}
	} else {
		handlerErr = <-errCh
	}
	if err := handlerErr; err != nil {
		err = normalizeContextError(ctx, err)
		if brk != nil {
			brk.MarkFailure()
		}
		if errors.Is(err, breaker.ErrOpen) {
			sendStreamError(NewError(CodeUnavailable, err.Error()))
			return
		}
		sendStreamError(err)
		return
	}
	if err := streamContextError(ctx); err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		sendStreamError(err)
		return
	}
	if brk != nil {
		brk.MarkSuccess()
	}
}

func waitStreamHandler(errCh <-chan error, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-errCh:
	case <-timer.C:
	}
}

func chainStreamMiddlewares(mws ...StreamMiddleware) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] != nil {
				next = mws[i](next)
			}
		}
		return next
	}
}

func chainClientStreamMiddlewares(mws ...ClientStreamMiddleware) ClientStreamMiddleware {
	return func(next ClientStreamHandler) ClientStreamHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] != nil {
				next = mws[i](next)
			}
		}
		return next
	}
}

func streamContextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	// Give handlers that return nil immediately after the handshake a small grace
	// window so defer-cancelled contexts do not turn normal short streams into
	// canceled failures. Long-running governance timeouts still surface reliably.
	select {
	case <-ctx.Done():
		return normalizeContextError(ctx, nil)
	case <-time.After(time.Nanosecond):
		return nil
	}
}

func (c *HTTPClient) Stream(ctx context.Context, method string) (*Stream, error) {
	handler := chainClientStreamMiddlewares(c.opts.streamMiddlewares...)(c.openStream)
	policy := c.streamRetryPolicy()
	if policy.Attempts <= 1 {
		return handler(ctx, method)
	}
	var stream *Stream
	err := retry.Do(ctx, policy, func() error {
		var openErr error
		stream, openErr = handler(ctx, method)
		return openErr
	})
	if err != nil {
		return nil, normalizeContextError(ctx, err)
	}
	return stream, nil
}

// MuxStream opens an experimental multiplexed stream when the client was
// explicitly configured with WithExperimentalMuxClientAdapter.
func (c *HTTPClient) MuxStream(ctx context.Context, method string) (*ExperimentalMuxStream, error) {
	handler := c.openMuxStream
	policy := c.streamRetryPolicy()
	if policy.Attempts <= 1 {
		return handler(ctx, method)
	}
	var stream *ExperimentalMuxStream
	err := retry.Do(ctx, policy, func() error {
		var openErr error
		stream, openErr = handler(ctx, method)
		return openErr
	})
	if err != nil {
		return nil, normalizeContextError(ctx, err)
	}
	return stream, nil
}

func (c *HTTPClient) openMuxStream(ctx context.Context, method string) (*ExperimentalMuxStream, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.opts.muxClientAdapter == nil {
		return nil, NewError(CodeUnavailable, "rpc mux client adapter is not configured")
	}
	governanceReq := c.rpcGovernanceRequest(ctx, method)
	decision := c.governanceDecision(ctx, governanceReq)
	policy := decision.Policy
	rpcPolicy := c.effectiveRPCPolicy(policy)
	if c.opts.rpcPolicyProvider != nil {
		dynamicPolicy, err := c.opts.rpcPolicyProvider.RPCPolicy(ctx, governanceReq)
		if err != nil {
			return nil, fmt.Errorf("resolve dynamic rpc mux stream policy: %w", err)
		}
		rpcPolicy = mergeRPCPolicy(rpcPolicy, dynamicPolicy)
	}
	if err := rpcPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("apply rpc mux stream policy: %w", err)
	}
	rpcPolicy = rpcPolicyForMethod(rpcPolicy, method)
	streamTimeout := c.opts.streamTimeout
	if streamTimeout <= 0 && rpcPolicy.Timeout > 0 {
		streamTimeout = rpcPolicy.Timeout
	}
	if streamTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, streamTimeout)
		defer cancel()
	}
	return c.opts.muxClientAdapter.OpenStream(ctx, method)
}

func (c *HTTPClient) streamRetryPolicy() retry.Policy {
	if c == nil {
		return retry.Policy{Attempts: 1, ShouldRetry: isRetryable}
	}
	policy := c.opts.retryPolicy
	if policy.Attempts <= 0 {
		policy.Attempts = c.opts.retry
	}
	if c.opts.rpcPolicy != nil {
		if c.opts.rpcPolicy.Retry.Attempts > 0 {
			policy.Attempts = c.opts.rpcPolicy.Retry.Attempts
		}
		if c.opts.rpcPolicy.Retry.Backoff > 0 {
			policy.Backoff = c.opts.rpcPolicy.Retry.Backoff
		}
	}
	if policy.Attempts <= 0 {
		policy.Attempts = 1
	}
	if policy.Backoff <= 0 && policy.BackoffFunc == nil {
		policy.Backoff = 10 * time.Millisecond
	}
	if policy.ShouldRetry == nil {
		policy.ShouldRetry = isRetryable
	}
	return policy
}

func (c *HTTPClient) openStream(ctx context.Context, method string) (stream *Stream, err error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	governanceReq := c.rpcGovernanceRequest(ctx, method)
	decision := c.governanceDecision(ctx, governanceReq)
	policy := decision.Policy
	rpcPolicy := c.effectiveRPCPolicy(policy)
	if c.opts.rpcPolicyProvider != nil {
		dynamicPolicy, err := c.opts.rpcPolicyProvider.RPCPolicy(ctx, governanceReq)
		if err != nil {
			return nil, fmt.Errorf("resolve dynamic rpc stream policy: %w", err)
		}
		rpcPolicy = mergeRPCPolicy(rpcPolicy, dynamicPolicy)
	}
	if err := rpcPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("apply rpc stream policy: %w", err)
	}
	rpcPolicy = rpcPolicyForMethod(rpcPolicy, method)
	runtimeKey := governanceRuntimeKey(decision, method)
	if limiter := c.ruleRateLimiter(runtimeKey, policy.RateLimit); limiter != nil && !limiter.Allow() {
		return nil, NewError(CodeResourceExhausted, "too many stream requests")
	}
	releaseLimiters := make([]func(), 0, 2)
	releaseAll := func() {
		for i := len(releaseLimiters) - 1; i >= 0; i-- {
			releaseLimiters[i]()
		}
	}
	defer func() {
		if err != nil {
			releaseAll()
		}
	}()
	if limiter := c.ruleConcurrencyLimiter(runtimeKey, policy.Concurrency); limiter != nil {
		if !limiter.TryAcquire() {
			return nil, NewError(CodeUnavailable, "too many concurrent streams")
		}
		releaseLimiters = append(releaseLimiters, limiter.Release)
	}
	if limiter := c.ruleLoadShedderLimiter(runtimeKey, rpcPolicy.LoadShedder); limiter != nil {
		if !limiter.TryAcquire() {
			return nil, NewError(CodeResourceExhausted, "rpc stream load shedder rejected request")
		}
		releaseLimiters = append(releaseLimiters, limiter.Release)
	}
	var brk *breaker.AdaptiveBreaker
	if brk = c.ruleBreaker(runtimeKey, rpcPolicy.Breaker); brk != nil {
		if err := brk.Allow(); err != nil {
			return nil, NewError(CodeUnavailable, err.Error())
		}
		defer func() {
			if err != nil {
				brk.MarkFailure()
				return
			}
			brk.MarkSuccess()
		}()
	}
	ctx = applyGovernanceMetadata(ctx, canaryMetadata(policy.Canary, governanceReq))
	ctx = applyGovernanceMetadata(ctx, rpcPolicy.Metadata)
	ctx = applyGovernanceMetadata(ctx, rpcPolicy.Headers)
	streamTimeout := c.opts.streamTimeout
	if rpcPolicy.Timeout > 0 {
		streamTimeout = rpcPolicy.Timeout
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, rpcPolicy.Timeout)
		defer cancel()
	}
	target, balancer, err := c.pickTarget(ctx, rpcPolicy.Balancer, "")
	if err != nil {
		return nil, NewError(CodeUnavailable, err.Error())
	}
	defer c.reportEndpointWithBalancer(ctx, balancer, target, &err)
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse target: %w", err)
	}
	addr := u.Host
	if !strings.Contains(addr, ":") {
		if u.Scheme == "https" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}
	transport := normalizeTransportConfig(c.opts.transport)
	dialer := &net.Dialer{Timeout: transport.DialTimeout, KeepAlive: transport.KeepAlive}
	conn, err := c.dialStream(ctx, dialer, u.Scheme, addr)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, NewError(CodeUnavailable, "dial stream: "+err.Error())
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	path := "/rpc/stream/" + strings.TrimPrefix(method, "/")
	if u.Path != "" && u.Path != "/" {
		path = strings.TrimRight(u.Path, "/") + path
	}
	if _, err := fmt.Fprintf(rw, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: %s\r\nConnection: Upgrade\r\n", path, u.Host, streamUpgradeToken); err != nil {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	if err := writeStreamMetadataHeaders(rw, ctx); err != nil {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	if _, err := fmt.Fprint(rw, "\r\n"); err != nil {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	resp, err := http.ReadResponse(rw.Reader, nil)
	if err != nil {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		err := streamUpgradeError(resp)
		_ = resp.Body.Close()
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	_ = resp.Body.Close()
	if c.streams != nil {
		c.streams.recordDial(target)
	}
	stream = newStream(conn, rw, c.opts.codec)
	stream.readTimeout = streamTimeout
	stream.writeTimeout = streamTimeout
	defaultCloseCode := CodeOK
	stream.onClose(func() {
		c.streams.recordClose(stream.closeCode(defaultCloseCode), stream.closeReasonFor(defaultCloseCode))
	})
	if len(releaseLimiters) > 0 {
		releases := append([]func(){}, releaseLimiters...)
		stream.onClose(func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		})
		releaseLimiters = nil
	}
	if c.opts.streamIdleTimeout > 0 {
		stream.startIdleMonitor(c.opts.streamIdleTimeout)
	}
	return stream, nil
}

func streamUpgradeError(resp *http.Response) error {
	if resp == nil {
		return NewError(CodeUnavailable, "rpc stream upgrade failed")
	}
	msg := strings.TrimSpace(resp.Status)
	code := codeFromHTTPStatus(resp.StatusCode)
	if resp.Body != nil {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err == nil && len(data) > 0 {
			var env responseEnvelope
			if json.Unmarshal(data, &env) == nil && env.Error != "" {
				if env.Code != "" {
					code = env.Code
				}
				msg = env.Error
			} else if text := strings.TrimSpace(string(data)); text != "" {
				msg = text
			}
		}
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return NewError(code, msg)
}

func codeFromHTTPStatus(status int) Code {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return CodeInvalidArgument
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodePermissionDenied
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeAborted
	case http.StatusTooManyRequests:
		return CodeResourceExhausted
	case http.StatusGatewayTimeout:
		return CodeDeadlineExceeded
	case http.StatusNotImplemented:
		return CodeUnimplemented
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return CodeUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return CodeInternal
		}
		return CodeUnavailable
	}
}

func writeStreamMetadataHeaders(w *bufio.ReadWriter, ctx context.Context) error {
	md, ok := metadata.FromContext(ctx)
	if !ok || len(md) == 0 {
		return nil
	}
	for key, value := range md {
		if !validStreamHeaderKey(key) || strings.ContainsAny(value, "\r\n") {
			return NewError(CodeInvalidArgument, "invalid rpc stream metadata header")
		}
		if isReservedStreamHeader(key) {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s: %s\r\n", key, value); err != nil {
			return err
		}
	}
	return nil
}

func streamMetadataFromHeader(header http.Header) metadata.MD {
	if len(header) == 0 {
		return nil
	}
	out := make(metadata.MD, len(header)*2)
	for key, values := range header {
		if len(values) == 0 || isReservedStreamHeader(key) {
			continue
		}
		value := values[0]
		out[key] = value
		out[strings.ToLower(key)] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validStreamHeaderKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func isReservedStreamHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "host", "upgrade":
		return true
	default:
		return false
	}
}

func (c *HTTPClient) dialStream(ctx context.Context, dialer *net.Dialer, scheme string, addr string) (net.Conn, error) {
	if scheme != "https" {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	var cfg *tls.Config
	if c.opts.tls != nil {
		clientTLS, err := c.opts.tls.ClientTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("configure rpc stream tls: %w", err)
		}
		cfg = clientTLS
	}
	if cfg == nil {
		cfg = normalizeTransportConfig(c.opts.transport).TLSClientConfig
	}
	if cfg == nil {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		cfg = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	tlsDialer := tls.Dialer{NetDialer: dialer, Config: cfg}
	return tlsDialer.DialContext(ctx, "tcp", addr)
}

func newStream(conn net.Conn, rw *bufio.ReadWriter, codec Codec) *Stream {
	if codec == nil {
		codec = JSONCodec{}
	}
	stream := &Stream{conn: conn, rw: rw, codec: codec, maxFrame: streamMaxFrameBytes, closed: make(chan struct{})}
	stream.touch()
	return stream
}

func (s *Stream) Send(v any) error {
	if s == nil {
		return ErrStreamClosed
	}
	payload, err := s.codec.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal stream message: %w", err)
	}
	env := streamEnvelope{Codec: s.codec.Name(), Code: CodeOK}
	if s.codec.Name() == "json" {
		env.Payload = payload
	} else {
		env.PayloadBytes = payload
	}
	return s.writeEnvelope(env)
}

func (s *Stream) SendError(code Code, text string) error {
	if code == "" {
		code = CodeInternal
	}
	s.markTerminalCode(code)
	return s.writeEnvelope(streamEnvelope{Code: code, Error: text, End: true})
}

func (s *Stream) Recv(v any) error {
	env, err := s.readEnvelope()
	if err != nil {
		s.markTerminalError(err)
		return err
	}
	if env.Error != "" {
		err := NewError(env.Code, env.Error)
		s.markTerminalError(err)
		return err
	}
	if env.End {
		s.markTerminalCode(CodeOK)
		return io.EOF
	}
	payload := []byte(env.Payload)
	if len(env.PayloadBytes) > 0 {
		payload = env.PayloadBytes
	}
	if env.Codec != "" && env.Codec != s.codec.Name() {
		err := NewError(CodeInvalidArgument, fmt.Sprintf("rpc stream codec mismatch: got %q, want %q", env.Codec, s.codec.Name()))
		s.markTerminalError(err)
		return err
	}
	if v == nil {
		return nil
	}
	if err := s.codec.Unmarshal(payload, v); err != nil {
		err = fmt.Errorf("unmarshal stream message: %w", err)
		s.markTerminalError(err)
		return err
	}
	return nil
}

func (s *Stream) markTerminalError(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, io.EOF) || errors.Is(err, ErrStreamClosed) {
		return
	}
	s.markTerminalCode(CodeOf(err))
}

func (s *Stream) markTerminalCode(code Code) {
	s.markTerminal(code, "")
}

func (s *Stream) markTerminal(code Code, reason string) {
	if s == nil || code == "" {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.terminalCode == "" || s.terminalCode == CodeOK {
		s.terminalCode = code
	}
	if reason != "" {
		s.terminalReason = reason
	}
}

func (s *Stream) closeCode(defaultCode Code) Code {
	if s == nil {
		return defaultCode
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.terminalCode != "" {
		return s.terminalCode
	}
	if defaultCode == "" {
		return CodeOK
	}
	return defaultCode
}

func (s *Stream) closeReasonFor(defaultCode Code) string {
	if s == nil {
		return streamCloseReason(defaultCode)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.terminalReason != "" {
		return s.terminalReason
	}
	if s.terminalCode != "" {
		return streamCloseReason(s.terminalCode)
	}
	return streamCloseReason(defaultCode)
}

func (s *Stream) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		close(s.closed)
		err = s.conn.Close()
		s.closeMu.Lock()
		hooks := append([]func(){}, s.closeHooks...)
		s.closeHooks = nil
		s.closeMu.Unlock()
		for _, hook := range hooks {
			hook()
		}
	})
	return err
}

func (s *Stream) startIdleMonitor(timeout time.Duration) {
	if s == nil || timeout <= 0 {
		return
	}
	timer := time.NewTimer(timeout)
	go func() {
		defer timer.Stop()
		for {
			select {
			case <-s.closed:
				return
			case <-timer.C:
				idleFor := time.Since(time.Unix(0, s.lastActivity.Load()))
				if idleFor >= timeout {
					s.markTerminal(CodeDeadlineExceeded, "idle")
					_ = s.Close()
					return
				}
				timer.Reset(timeout - idleFor)
			}
		}
	}()
}

func (s *Stream) touch() {
	if s == nil {
		return
	}
	s.lastActivity.Store(time.Now().UnixNano())
}

func (s *Stream) onClose(hook func()) {
	if s == nil || hook == nil {
		return
	}
	runNow := false
	s.closeMu.Lock()
	select {
	case <-s.closed:
		runNow = true
	default:
		s.closeHooks = append(s.closeHooks, hook)
	}
	s.closeMu.Unlock()
	if runNow {
		hook()
	}
}

func (s *Stream) writeEnvelope(env streamEnvelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if int64(len(data)) > s.maxFrame || len(data) > math.MaxUint32 {
		return errors.New("rpc stream frame exceeds maximum size")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeTimeout > 0 {
		if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
			return fmt.Errorf("set rpc stream write deadline: %w", err)
		}
		defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	}
	var header [4]byte
	// #nosec G115 -- len(data) is checked against math.MaxUint32 immediately above.
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := s.rw.Write(header[:]); err != nil {
		return normalizeStreamTimeout(err, "write")
	}
	if _, err := s.rw.Write(data); err != nil {
		return normalizeStreamTimeout(err, "write")
	}
	if err := normalizeStreamTimeout(s.rw.Flush(), "write"); err != nil {
		return err
	}
	s.touch()
	return nil
}

func (s *Stream) readEnvelope() (streamEnvelope, error) {
	if s.readTimeout > 0 {
		if err := s.conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
			return streamEnvelope{}, fmt.Errorf("set rpc stream read deadline: %w", err)
		}
		defer func() { _ = s.conn.SetReadDeadline(time.Time{}) }()
	}
	var header [4]byte
	if _, err := io.ReadFull(s.rw, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return streamEnvelope{}, ErrStreamClosed
		}
		return streamEnvelope{}, normalizeStreamTimeout(err, "read")
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || int64(length) > s.maxFrame {
		return streamEnvelope{}, errors.New("invalid rpc stream frame size")
	}
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, s.rw, int64(length)); err != nil {
		return streamEnvelope{}, normalizeStreamTimeout(err, "read")
	}
	var env streamEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		return streamEnvelope{}, err
	}
	s.touch()
	return env, nil
}

func normalizeStreamTimeout(err error, op string) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return NewError(CodeDeadlineExceeded, "rpc stream "+op+" timeout")
	}
	return err
}
