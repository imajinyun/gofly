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
	"time"

	core "github.com/gofly/gofly/core"
	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/metadata"
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
	conn         net.Conn
	rw           *bufio.ReadWriter
	codec        Codec
	maxFrame     int64
	readTimeout  time.Duration
	writeTimeout time.Duration
	writeMu      sync.Mutex
	closeMu      sync.Mutex
	closeHooks   []func()
	once         sync.Once
	closed       chan struct{}
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
	return handler(ctx, method)
}

func (c *HTTPClient) openStream(ctx context.Context, method string) (*Stream, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := c.pickTarget(ctx)
	if err != nil {
		return nil, NewError(CodeUnavailable, err.Error())
	}
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
	dialer := &net.Dialer{}
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
	status, err := rw.ReadString('\n')
	if err != nil {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, err
	}
	if !strings.Contains(status, "101") {
		_ = conn.Close() // best-effort cleanup after handshake failure
		return nil, NewError(CodeUnavailable, strings.TrimSpace(status))
	}
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			_ = conn.Close() // best-effort cleanup after handshake failure
			return nil, err
		}
		if line == "\r\n" {
			break
		}
	}
	stream := newStream(conn, rw, c.opts.codec)
	stream.readTimeout = c.opts.streamTimeout
	stream.writeTimeout = c.opts.streamTimeout
	return stream, nil
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
	return &Stream{conn: conn, rw: rw, codec: codec, maxFrame: streamMaxFrameBytes, closed: make(chan struct{})}
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
	return s.writeEnvelope(streamEnvelope{Code: code, Error: text, End: true})
}

func (s *Stream) Recv(v any) error {
	env, err := s.readEnvelope()
	if err != nil {
		return err
	}
	if env.Error != "" {
		return NewError(env.Code, env.Error)
	}
	if env.End {
		return io.EOF
	}
	payload := []byte(env.Payload)
	if len(env.PayloadBytes) > 0 {
		payload = env.PayloadBytes
	}
	if env.Codec != "" && env.Codec != s.codec.Name() {
		return NewError(CodeInvalidArgument, fmt.Sprintf("rpc stream codec mismatch: got %q, want %q", env.Codec, s.codec.Name()))
	}
	if v == nil {
		return nil
	}
	if err := s.codec.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("unmarshal stream message: %w", err)
	}
	return nil
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
	return normalizeStreamTimeout(s.rw.Flush(), "write")
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
