package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/security"
)

const (
	experimentalMuxCandidateDefaultProtocol = "gofly-mux/experimental-v1"
	experimentalMuxCandidatePrefacePrefix   = "GOFLY-MUX/1 "
	experimentalMuxCandidateMaxPrefaceBytes = 128
)

// ExperimentalMuxCandidateConfig describes the opt-in production-candidate
// adapter surface for the experimental mux transport. It keeps the default HTTP
// RPC transport untouched while making the mux path configurable enough for
// real TCP/TLS smoke and diagnosis.
type ExperimentalMuxCandidateConfig struct {
	Protocol             string             `json:"protocol,omitempty"`
	TLS                  security.TLSConfig `json:"tls,omitempty"`
	DialTimeout          time.Duration      `json:"dialTimeout,omitempty"`
	KeepAlive            time.Duration      `json:"keepAlive,omitempty"`
	HandshakeTimeout     time.Duration      `json:"handshakeTimeout,omitempty"`
	KeepaliveInterval    time.Duration      `json:"keepaliveInterval,omitempty"`
	KeepaliveIdle        time.Duration      `json:"keepaliveIdle,omitempty"`
	MaxFrameBytes        int64              `json:"maxFrameBytes,omitempty"`
	MaxMessageBytes      int64              `json:"maxMessageBytes,omitempty"`
	MaxConcurrentStreams int                `json:"maxConcurrentStreams,omitempty"`
	ReceiveQueueSize     int                `json:"receiveQueueSize,omitempty"`
	ConnectionWindow     int                `json:"connectionWindow,omitempty"`
	PayloadCodec         string             `json:"payloadCodec,omitempty"`
	FrameCodec           string             `json:"frameCodec,omitempty"`
}

// ExperimentalMuxCandidateSnapshot is intentionally path-free so runtime
// diagnosis can report the active transport policy without leaking cert paths.
type ExperimentalMuxCandidateSnapshot struct {
	Enabled              bool          `json:"enabled"`
	Protocol             string        `json:"protocol,omitempty"`
	NegotiatedProtocol   string        `json:"negotiatedProtocol,omitempty"`
	TLS                  bool          `json:"tls"`
	MutualTLS            bool          `json:"mutualTLS"`
	DialTimeout          time.Duration `json:"dialTimeout,omitempty"`
	KeepAlive            time.Duration `json:"keepAlive,omitempty"`
	HandshakeTimeout     time.Duration `json:"handshakeTimeout,omitempty"`
	KeepaliveInterval    time.Duration `json:"keepaliveInterval,omitempty"`
	KeepaliveIdle        time.Duration `json:"keepaliveIdle,omitempty"`
	MaxFrameBytes        int64         `json:"maxFrameBytes,omitempty"`
	MaxMessageBytes      int64         `json:"maxMessageBytes,omitempty"`
	MaxConcurrentStreams int           `json:"maxConcurrentStreams,omitempty"`
	ReceiveQueueSize     int           `json:"receiveQueueSize,omitempty"`
	ConnectionWindow     int           `json:"connectionWindow,omitempty"`
	PayloadCodec         string        `json:"payloadCodec,omitempty"`
	FrameCodec           string        `json:"frameCodec,omitempty"`
}

func (c ExperimentalMuxCandidateConfig) normalized() ExperimentalMuxCandidateConfig {
	c.Protocol = normalizeExperimentalMuxCandidateProtocol(c.Protocol)
	if c.DialTimeout <= 0 {
		c.DialTimeout = 30 * time.Second
	}
	if c.KeepAlive <= 0 {
		c.KeepAlive = 30 * time.Second
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = 10 * time.Second
	}
	c.PayloadCodec = normalizeExperimentalMuxCandidatePayloadCodec(c.PayloadCodec)
	c.FrameCodec = normalizeExperimentalMuxCandidateFrameCodec(c.FrameCodec)
	return c
}

func (c ExperimentalMuxCandidateConfig) transportOptions() []ExperimentalMuxTransportOption {
	c = c.normalized()
	opts := make([]ExperimentalMuxTransportOption, 0, 8)
	if c.KeepaliveInterval > 0 || c.KeepaliveIdle > 0 {
		opts = append(opts, WithExperimentalMuxKeepalive(c.KeepaliveInterval, c.KeepaliveIdle))
	}
	if c.MaxFrameBytes > 0 {
		opts = append(opts, WithExperimentalMuxMaxFrameBytes(c.MaxFrameBytes))
	}
	if c.MaxMessageBytes > 0 {
		opts = append(opts, WithExperimentalMuxMaxMessageBytes(c.MaxMessageBytes))
	}
	if c.MaxConcurrentStreams > 0 {
		opts = append(opts, WithExperimentalMuxMaxConcurrentStreams(c.MaxConcurrentStreams))
	}
	if c.ReceiveQueueSize > 0 {
		opts = append(opts, WithExperimentalMuxReceiveQueueSize(c.ReceiveQueueSize))
	}
	if c.ConnectionWindow > 0 {
		opts = append(opts, WithExperimentalMuxConnectionWindow(c.ConnectionWindow))
	}
	switch c.PayloadCodec {
	case "gzip":
		opts = append(opts, WithExperimentalMuxPayloadCodec(GzipPayloadCodec{}))
	default:
		opts = append(opts, WithExperimentalMuxPayloadCodec(NoopPayloadCodec{}))
	}
	switch c.FrameCodec {
	case "binary":
		opts = append(opts, WithExperimentalMuxFrameCodec(BinaryFrameCodec{}))
	default:
		opts = append(opts, WithExperimentalMuxFrameCodec(JSONFrameCodec{}))
	}
	return opts
}

func (c ExperimentalMuxCandidateConfig) snapshot(role string, negotiated string) ExperimentalMuxCandidateSnapshot {
	c = c.normalized()
	return ExperimentalMuxCandidateSnapshot{
		Enabled:              true,
		Protocol:             c.Protocol,
		NegotiatedProtocol:   negotiated,
		TLS:                  muxCandidateTLSConfigured(c.TLS, role),
		MutualTLS:            muxCandidateMutualTLSConfigured(c.TLS, role),
		DialTimeout:          c.DialTimeout,
		KeepAlive:            c.KeepAlive,
		HandshakeTimeout:     c.HandshakeTimeout,
		KeepaliveInterval:    c.KeepaliveInterval,
		KeepaliveIdle:        c.KeepaliveIdle,
		MaxFrameBytes:        c.MaxFrameBytes,
		MaxMessageBytes:      c.MaxMessageBytes,
		MaxConcurrentStreams: c.MaxConcurrentStreams,
		ReceiveQueueSize:     c.ReceiveQueueSize,
		ConnectionWindow:     c.ConnectionWindow,
		PayloadCodec:         c.PayloadCodec,
		FrameCodec:           c.FrameCodec,
	}
}

func (c ExperimentalMuxCandidateConfig) clientTLSConfig() (*tls.Config, error) {
	c = c.normalized()
	cfg, err := c.TLS.ClientTLSConfig()
	if err != nil || cfg == nil {
		return cfg, err
	}
	cfg = cfg.Clone()
	cfg.NextProtos = muxCandidateNextProtos(cfg.NextProtos, c.Protocol)
	return cfg, nil
}

func (c ExperimentalMuxCandidateConfig) serverTLSConfig() (*tls.Config, error) {
	c = c.normalized()
	cfg, err := c.TLS.ServerTLSConfig()
	if err != nil || cfg == nil {
		return cfg, err
	}
	cfg = cfg.Clone()
	cfg.NextProtos = muxCandidateNextProtos(cfg.NextProtos, c.Protocol)
	return cfg, nil
}

func dialExperimentalMuxCandidateConn(ctx context.Context, network string, address string, cfg ExperimentalMuxCandidateConfig) (net.Conn, ExperimentalMuxCandidateSnapshot, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	cfg = cfg.normalized()
	dialer := net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: cfg.KeepAlive}
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	var negotiated string
	if tlsCfg, err := cfg.clientTLSConfig(); err != nil {
		_ = conn.Close()
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	} else if tlsCfg != nil {
		tlsConn := tls.Client(conn, tlsCfg)
		if err := muxCandidateHandshake(ctx, tlsConn, cfg.HandshakeTimeout); err != nil {
			_ = tlsConn.Close()
			return nil, ExperimentalMuxCandidateSnapshot{}, err
		}
		state := tlsConn.ConnectionState()
		negotiated = state.NegotiatedProtocol
		conn = tlsConn
	}
	if negotiated == "" {
		negotiated = cfg.Protocol
	}
	if err := exchangeExperimentalMuxCandidateProtocol(ctx, conn, cfg); err != nil {
		_ = conn.Close()
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	return conn, cfg.snapshot("client", negotiated), nil
}

func acceptExperimentalMuxCandidateConn(ctx context.Context, conn net.Conn, cfg ExperimentalMuxCandidateConfig) (net.Conn, ExperimentalMuxCandidateSnapshot, error) {
	ctx = core.Context(ctx)
	cfg = cfg.normalized()
	var negotiated string
	if tlsCfg, err := cfg.serverTLSConfig(); err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	} else if tlsCfg != nil {
		tlsConn := tls.Server(conn, tlsCfg)
		if err := muxCandidateHandshake(ctx, tlsConn, cfg.HandshakeTimeout); err != nil {
			_ = tlsConn.Close()
			return nil, ExperimentalMuxCandidateSnapshot{}, err
		}
		state := tlsConn.ConnectionState()
		negotiated = state.NegotiatedProtocol
		conn = tlsConn
	}
	if negotiated == "" {
		negotiated = cfg.Protocol
	}
	if err := exchangeExperimentalMuxCandidateProtocol(ctx, conn, cfg); err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	return conn, cfg.snapshot("server", negotiated), nil
}

func exchangeExperimentalMuxCandidateProtocol(ctx context.Context, conn net.Conn, cfg ExperimentalMuxCandidateConfig) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(cfg.HandshakeTimeout)
	if deadlineFromContext, ok := ctx.Deadline(); ok && deadlineFromContext.Before(deadline) {
		deadline = deadlineFromContext
	}
	if !deadline.IsZero() {
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}
	preface := []byte(experimentalMuxCandidatePrefacePrefix + cfg.Protocol + "\n")
	if len(preface) > experimentalMuxCandidateMaxPrefaceBytes {
		return NewError(CodeInvalidArgument, "mux candidate protocol preface too large")
	}
	if _, err := conn.Write(preface); err != nil {
		return fmt.Errorf("write mux candidate protocol preface: %w", err)
	}
	peer, err := readExperimentalMuxCandidatePreface(conn)
	if err != nil {
		return err
	}
	if peer != cfg.Protocol {
		return NewError(CodeUnavailable, "mux candidate protocol mismatch")
	}
	return nil
}

func readExperimentalMuxCandidatePreface(conn net.Conn) (string, error) {
	buf := make([]byte, 0, experimentalMuxCandidateMaxPrefaceBytes)
	var one [1]byte
	for len(buf) < experimentalMuxCandidateMaxPrefaceBytes {
		n, err := conn.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				line := string(buf)
				if !strings.HasPrefix(line, experimentalMuxCandidatePrefacePrefix) {
					return "", NewError(CodeUnavailable, "mux candidate protocol preface mismatch")
				}
				return strings.TrimPrefix(line, experimentalMuxCandidatePrefacePrefix), nil
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", ErrExperimentalMuxTransportClosed
			}
			return "", fmt.Errorf("read mux candidate protocol preface: %w", err)
		}
	}
	return "", NewError(CodeUnavailable, "mux candidate protocol preface too large")
}

func muxCandidateHandshake(ctx context.Context, conn *tls.Conn, timeout time.Duration) error {
	if timeout > 0 {
		deadline := time.Now().Add(timeout)
		if deadlineFromContext, ok := ctx.Deadline(); ok && deadlineFromContext.Before(deadline) {
			deadline = deadlineFromContext
		}
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}
	return conn.HandshakeContext(ctx)
}

func muxCandidateNextProtos(existing []string, protocol string) []string {
	for _, proto := range existing {
		if proto == protocol {
			return append([]string(nil), existing...)
		}
	}
	next := append([]string{protocol}, existing...)
	return next
}

func normalizeExperimentalMuxCandidateProtocol(protocol string) string {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		return experimentalMuxCandidateDefaultProtocol
	}
	return protocol
}

func normalizeExperimentalMuxCandidatePayloadCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "gzip":
		return "gzip"
	default:
		return "identity"
	}
}

func normalizeExperimentalMuxCandidateFrameCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "binary":
		return "binary"
	default:
		return "json"
	}
}

func muxCandidateTLSConfigured(cfg security.TLSConfig, role string) bool {
	if role == "server" {
		return cfg.Enabled()
	}
	return cfg.Enabled() || cfg.CAFile != "" || cfg.ServerName != "" || cfg.InsecureSkipVerify
}

func muxCandidateMutualTLSConfigured(cfg security.TLSConfig, role string) bool {
	if role == "server" {
		return cfg.MutualEnabled()
	}
	return cfg.Enabled()
}
