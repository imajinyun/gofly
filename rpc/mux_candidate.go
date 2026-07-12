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
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/security"
)

const (
	experimentalMuxCandidateDefaultProtocol = "gofly-mux/experimental-v1"
	experimentalMuxCandidatePrefacePrefix   = "GOFLY-MUX/1 "
	experimentalMuxCandidateMaxPrefaceBytes = 512
)

const (
	experimentalMuxCandidateFailureTLS         = "tls"
	experimentalMuxCandidateFailureALPN        = "alpn"
	experimentalMuxCandidateFailurePreface     = "preface"
	experimentalMuxCandidateFailureProtocol    = "protocol_mismatch"
	experimentalMuxCandidateFailureFramePolicy = "frame_policy_mismatch"
	experimentalMuxCandidateFailurePolicyRisk  = "fragment_window_policy_risk"
)

var (
	rpcMuxCandidateNegotiationFailures *metrics.Counter
	rpcMuxCandidateDowngrades          *metrics.Counter
	rpcMuxCandidateConnections         *metrics.Gauge
	rpcMuxCandidateDrains              *metrics.Counter
	rpcMuxCandidateActiveStreams       *metrics.Gauge
	rpcMuxCandidateDrainTimeouts       *metrics.Counter
	rpcMuxCandidateForcedCloses        *metrics.Counter
	rpcMuxCandidateFlowControlEvents   *metrics.Counter
)

func init() {
	registerExperimentalMuxCandidateMetrics(metrics.Default)
}

func registerExperimentalMuxCandidateMetrics(registry *metrics.Registry) {
	if registry == nil {
		registry = metrics.Default
	}
	rpcMuxCandidateNegotiationFailures = registry.Counter(
		"gofly_rpc_mux_candidate_negotiation_failures_total",
		"Total mux candidate negotiation failures by low-cardinality phase and peer protocol.",
		"phase",
		"peer_protocol",
	)
	rpcMuxCandidateDowngrades = registry.Counter(
		"gofly_rpc_mux_candidate_downgrades_total",
		"Total mux candidate legacy downgrades by failure phase and peer protocol.",
		"phase",
		"peer_protocol",
	)
	rpcMuxCandidateConnections = registry.Gauge(
		"gofly_rpc_mux_candidate_connections",
		"Current mux candidate connection policy signals by codec and downgrade state.",
		"frame_codec",
		"payload_codec",
		"downgraded",
	)
	rpcMuxCandidateDrains = registry.Counter(
		"gofly_rpc_mux_candidate_drain_total",
		"Total mux candidate GOAWAY drain events by reason and direction.",
		"drain_reason",
		"direction",
	)
	rpcMuxCandidateActiveStreams = registry.Gauge(
		"gofly_rpc_mux_candidate_active_streams",
		"Current active streams observed during mux candidate drain lifecycle.",
		"drain_reason",
		"state",
	)
	rpcMuxCandidateDrainTimeouts = registry.Counter(
		"gofly_rpc_mux_candidate_drain_timeout_total",
		"Total mux candidate graceful drain timeouts by reason.",
		"drain_reason",
	)
	rpcMuxCandidateForcedCloses = registry.Counter(
		"gofly_rpc_mux_candidate_forced_close_total",
		"Total mux candidate forced closes after drain timeout by reason.",
		"drain_reason",
	)
	rpcMuxCandidateFlowControlEvents = registry.Counter(
		"gofly_rpc_mux_candidate_flow_control_events_total",
		"Total mux candidate flow-control and write timeout events.",
		"event",
	)
}

// ExperimentalMuxCandidateConfig describes the opt-in production-candidate
// adapter surface for the experimental mux transport. It keeps the default HTTP
// RPC transport untouched while making the mux path configurable enough for
// real TCP/TLS smoke and diagnosis.
type ExperimentalMuxCandidateConfig struct {
	Protocol                             string             `json:"protocol,omitempty"`
	TLS                                  security.TLSConfig `json:"tls,omitempty"`
	DialTimeout                          time.Duration      `json:"dialTimeout,omitempty"`
	KeepAlive                            time.Duration      `json:"keepAlive,omitempty"`
	HandshakeTimeout                     time.Duration      `json:"handshakeTimeout,omitempty"`
	KeepaliveInterval                    time.Duration      `json:"keepaliveInterval,omitempty"`
	KeepaliveIdle                        time.Duration      `json:"keepaliveIdle,omitempty"`
	WriteTimeout                         time.Duration      `json:"writeTimeout,omitempty"`
	CreditWaitTimeout                    time.Duration      `json:"creditWaitTimeout,omitempty"`
	MaxFrameBytes                        int64              `json:"maxFrameBytes,omitempty"`
	MaxMessageBytes                      int64              `json:"maxMessageBytes,omitempty"`
	MaxConcurrentStreams                 int                `json:"maxConcurrentStreams,omitempty"`
	ReceiveQueueSize                     int                `json:"receiveQueueSize,omitempty"`
	ConnectionWindow                     int                `json:"connectionWindow,omitempty"`
	FragmentStreamWindowUpdatePolicy     string             `json:"fragmentStreamWindowUpdatePolicy,omitempty"`
	FragmentConnectionWindowUpdatePolicy string             `json:"fragmentConnectionWindowUpdatePolicy,omitempty"`
	FragmentStreamWindowRefillRatio      float64            `json:"fragmentStreamWindowRefillRatio,omitempty"`
	FragmentConnectionWindowRefillRatio  float64            `json:"fragmentConnectionWindowRefillRatio,omitempty"`
	FragmentMaxDeferredFragments         int                `json:"fragmentMaxDeferredFragments,omitempty"`
	FragmentWindowPolicyRiskMode         string             `json:"fragmentWindowPolicyRiskMode,omitempty"`
	PayloadCodec                         string             `json:"payloadCodec,omitempty"`
	FrameCodec                           string             `json:"frameCodec,omitempty"`
	DrainGrace                           time.Duration      `json:"drainGrace,omitempty"`
	AllowLegacyDowngrade                 bool               `json:"allowLegacyDowngrade,omitempty"`
}

// ExperimentalMuxCandidateSnapshot is intentionally path-free so runtime
// diagnosis can report the active transport policy without leaking cert paths.
type ExperimentalMuxCandidateSnapshot struct {
	Enabled                              bool             `json:"enabled"`
	Protocol                             string           `json:"protocol,omitempty"`
	PeerProtocol                         string           `json:"peerProtocol,omitempty"`
	NegotiatedProtocol                   string           `json:"negotiatedProtocol,omitempty"`
	TLS                                  bool             `json:"tls"`
	MutualTLS                            bool             `json:"mutualTLS"`
	NegotiationFailures                  int64            `json:"negotiationFailures,omitempty"`
	NegotiationFailureEvents             map[string]int64 `json:"negotiationFailureEvents,omitempty"`
	LastNegotiationError                 string           `json:"lastNegotiationError,omitempty"`
	LastNegotiationPhase                 string           `json:"lastNegotiationPhase,omitempty"`
	DowngradeAllowed                     bool             `json:"downgradeAllowed,omitempty"`
	Downgrades                           int64            `json:"downgrades,omitempty"`
	Downgraded                           bool             `json:"downgraded,omitempty"`
	DowngradeReason                      string           `json:"downgradeReason,omitempty"`
	DialTimeout                          time.Duration    `json:"dialTimeout,omitempty"`
	KeepAlive                            time.Duration    `json:"keepAlive,omitempty"`
	HandshakeTimeout                     time.Duration    `json:"handshakeTimeout,omitempty"`
	KeepaliveInterval                    time.Duration    `json:"keepaliveInterval,omitempty"`
	KeepaliveIdle                        time.Duration    `json:"keepaliveIdle,omitempty"`
	WriteTimeout                         time.Duration    `json:"writeTimeout,omitempty"`
	CreditWaitTimeout                    time.Duration    `json:"creditWaitTimeout,omitempty"`
	MaxFrameBytes                        int64            `json:"maxFrameBytes,omitempty"`
	MaxMessageBytes                      int64            `json:"maxMessageBytes,omitempty"`
	MaxConcurrentStreams                 int              `json:"maxConcurrentStreams,omitempty"`
	ReceiveQueueSize                     int              `json:"receiveQueueSize,omitempty"`
	ConnectionWindow                     int              `json:"connectionWindow,omitempty"`
	FragmentStreamWindowUpdatePolicy     string           `json:"fragmentStreamWindowUpdatePolicy,omitempty"`
	FragmentConnectionWindowUpdatePolicy string           `json:"fragmentConnectionWindowUpdatePolicy,omitempty"`
	FragmentStreamWindowRefillRatio      float64          `json:"fragmentStreamWindowRefillRatio,omitempty"`
	FragmentConnectionWindowRefillRatio  float64          `json:"fragmentConnectionWindowRefillRatio,omitempty"`
	FragmentMaxDeferredFragments         int              `json:"fragmentMaxDeferredFragments,omitempty"`
	FragmentWindowPolicyRisk             bool             `json:"fragmentWindowPolicyRisk,omitempty"`
	FragmentWindowPolicyRiskReason       string           `json:"fragmentWindowPolicyRiskReason,omitempty"`
	FragmentWindowPolicyRiskMode         string           `json:"fragmentWindowPolicyRiskMode,omitempty"`
	FragmentWindowPolicyRiskWarning      bool             `json:"fragmentWindowPolicyRiskWarning,omitempty"`
	FragmentWindowPolicyRiskRejected     bool             `json:"fragmentWindowPolicyRiskRejected,omitempty"`
	FragmentEstimatedMaxFragments        int              `json:"fragmentEstimatedMaxFragments,omitempty"`
	PayloadCodec                         string           `json:"payloadCodec,omitempty"`
	FrameCodec                           string           `json:"frameCodec,omitempty"`
	DrainGrace                           time.Duration    `json:"drainGrace,omitempty"`
}

type ExperimentalMuxCandidateFailure struct {
	Phase        string
	PeerProtocol string
	Err          error
}

func (e *ExperimentalMuxCandidateFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return "mux candidate negotiation failed"
	}
	if e.Phase == "" {
		return e.Err.Error()
	}
	return "mux candidate " + e.Phase + ": " + e.Err.Error()
}

func (e *ExperimentalMuxCandidateFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type experimentalMuxCandidatePeer struct {
	Protocol        string
	FrameCodec      string
	PayloadCodec    string
	MaxFrameBytes   int64
	MaxMessageBytes int64
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
	c.FragmentWindowPolicyRiskMode = normalizeExperimentalMuxFragmentPolicyRiskMode(c.FragmentWindowPolicyRiskMode)
	return c
}

// Validate checks candidate mux transport policies that can be rejected before
// opening a network connection.
func (c ExperimentalMuxCandidateConfig) Validate() error {
	if rawMode := strings.TrimSpace(c.FragmentWindowPolicyRiskMode); rawMode != "" &&
		normalizeExperimentalMuxFragmentPolicyRiskMode(rawMode) == "" {
		return NewError(CodeInvalidArgument, "mux candidate fragment window policy risk mode must be diagnose, warn, or reject")
	}
	if !isValidExperimentalMuxWindowRefillRatio(c.FragmentStreamWindowRefillRatio) {
		return NewError(CodeInvalidArgument, "mux candidate fragment stream window refill ratio must be between 0 and 1")
	}
	if !isValidExperimentalMuxWindowRefillRatio(c.FragmentConnectionWindowRefillRatio) {
		return NewError(CodeInvalidArgument, "mux candidate fragment connection window refill ratio must be between 0 and 1")
	}
	if c.FragmentMaxDeferredFragments < 0 {
		return NewError(CodeInvalidArgument, "mux candidate fragment max deferred fragments must be non-negative")
	}
	_, err := c.validateFragmentWindowPolicyRisk("")
	return err
}

func (c ExperimentalMuxCandidateConfig) transportOptions() []ExperimentalMuxTransportOption {
	c = c.normalized()
	opts := make([]ExperimentalMuxTransportOption, 0, 8)
	if c.KeepaliveInterval > 0 || c.KeepaliveIdle > 0 {
		opts = append(opts, WithExperimentalMuxKeepalive(c.KeepaliveInterval, c.KeepaliveIdle))
	}
	if c.WriteTimeout > 0 {
		opts = append(opts, WithExperimentalMuxWriteTimeout(c.WriteTimeout))
	}
	if c.CreditWaitTimeout > 0 {
		opts = append(opts, WithExperimentalMuxCreditWaitTimeout(c.CreditWaitTimeout))
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
	opts = append(opts, WithExperimentalMuxFragmentWindowUpdatePolicy(
		c.FragmentStreamWindowUpdatePolicy,
		c.FragmentConnectionWindowUpdatePolicy,
	))
	opts = append(opts, WithExperimentalMuxFragmentWindowRefillPolicy(
		c.FragmentStreamWindowRefillRatio,
		c.FragmentConnectionWindowRefillRatio,
		c.FragmentMaxDeferredFragments,
	))
	opts = append(opts, WithExperimentalMuxFragmentWindowPolicyRiskMode(c.FragmentWindowPolicyRiskMode))
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
	fragmentPolicyRisk, fragmentPolicyRiskReason, fragmentEstimatedMaxFragments := experimentalMuxFragmentWindowPolicyRisk(
		c.MaxFrameBytes,
		c.MaxMessageBytes,
		c.ReceiveQueueSize,
		c.ConnectionWindow,
		c.FragmentStreamWindowUpdatePolicy,
		c.FragmentConnectionWindowUpdatePolicy,
		c.FragmentMaxDeferredFragments,
	)
	return ExperimentalMuxCandidateSnapshot{
		Enabled:                              true,
		Protocol:                             c.Protocol,
		NegotiatedProtocol:                   negotiated,
		TLS:                                  muxCandidateTLSConfigured(c.TLS, role),
		MutualTLS:                            muxCandidateMutualTLSConfigured(c.TLS, role),
		DialTimeout:                          c.DialTimeout,
		KeepAlive:                            c.KeepAlive,
		HandshakeTimeout:                     c.HandshakeTimeout,
		KeepaliveInterval:                    c.KeepaliveInterval,
		KeepaliveIdle:                        c.KeepaliveIdle,
		WriteTimeout:                         c.WriteTimeout,
		CreditWaitTimeout:                    c.CreditWaitTimeout,
		MaxFrameBytes:                        c.MaxFrameBytes,
		MaxMessageBytes:                      c.MaxMessageBytes,
		MaxConcurrentStreams:                 c.MaxConcurrentStreams,
		ReceiveQueueSize:                     c.ReceiveQueueSize,
		ConnectionWindow:                     c.ConnectionWindow,
		FragmentStreamWindowUpdatePolicy:     normalizeExperimentalMuxWindowUpdatePolicy(c.FragmentStreamWindowUpdatePolicy),
		FragmentConnectionWindowUpdatePolicy: normalizeExperimentalMuxWindowUpdatePolicy(c.FragmentConnectionWindowUpdatePolicy),
		FragmentStreamWindowRefillRatio:      normalizeExperimentalMuxWindowRefillRatio(c.FragmentStreamWindowRefillRatio),
		FragmentConnectionWindowRefillRatio:  normalizeExperimentalMuxWindowRefillRatio(c.FragmentConnectionWindowRefillRatio),
		FragmentMaxDeferredFragments:         c.FragmentMaxDeferredFragments,
		FragmentWindowPolicyRisk:             fragmentPolicyRisk,
		FragmentWindowPolicyRiskReason:       fragmentPolicyRiskReason,
		FragmentWindowPolicyRiskMode:         c.FragmentWindowPolicyRiskMode,
		FragmentWindowPolicyRiskWarning:      fragmentPolicyRisk && c.FragmentWindowPolicyRiskMode == experimentalMuxFragmentPolicyRiskModeWarn,
		FragmentWindowPolicyRiskRejected:     fragmentPolicyRisk && c.FragmentWindowPolicyRiskMode == experimentalMuxFragmentPolicyRiskModeReject,
		FragmentEstimatedMaxFragments:        fragmentEstimatedMaxFragments,
		PayloadCodec:                         c.PayloadCodec,
		FrameCodec:                           c.FrameCodec,
		DrainGrace:                           c.DrainGrace,
		DowngradeAllowed:                     c.AllowLegacyDowngrade,
	}
}

func (c ExperimentalMuxCandidateConfig) validateFragmentWindowPolicyRisk(role string) (ExperimentalMuxCandidateSnapshot, error) {
	snapshot := c.snapshot(role, "")
	if !snapshot.FragmentWindowPolicyRisk || snapshot.FragmentWindowPolicyRiskMode != experimentalMuxFragmentPolicyRiskModeReject {
		return snapshot, nil
	}
	return snapshot, newExperimentalMuxCandidateFailure(
		experimentalMuxCandidateFailurePolicyRisk,
		snapshot.Protocol,
		NewError(CodeInvalidArgument, "mux candidate fragment window policy risk rejected: "+snapshot.FragmentWindowPolicyRiskReason),
	)
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
	if snapshot, err := cfg.validateFragmentWindowPolicyRisk("client"); err != nil {
		return nil, snapshot, err
	}
	dialer := net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: cfg.KeepAlive}
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	var negotiated string
	if tlsCfg, err := cfg.clientTLSConfig(); err != nil {
		_ = conn.Close()
		return nil, ExperimentalMuxCandidateSnapshot{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureTLS, "", err)
	} else if tlsCfg != nil {
		tlsConn := tls.Client(conn, tlsCfg)
		if err := muxCandidateHandshake(ctx, tlsConn, cfg.HandshakeTimeout); err != nil {
			_ = tlsConn.Close()
			return nil, ExperimentalMuxCandidateSnapshot{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureTLS, "", err)
		}
		state := tlsConn.ConnectionState()
		negotiated = state.NegotiatedProtocol
		if negotiated != cfg.Protocol {
			_ = tlsConn.Close()
			return nil, ExperimentalMuxCandidateSnapshot{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureALPN, negotiated, fmt.Errorf("negotiated protocol %q, want %q", negotiated, cfg.Protocol))
		}
		conn = tlsConn
	}
	if negotiated == "" {
		negotiated = cfg.Protocol
	}
	peer, err := exchangeExperimentalMuxCandidateProtocol(ctx, conn, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	snapshot := cfg.snapshot("client", negotiated)
	snapshot.PeerProtocol = peer.Protocol
	recordExperimentalMuxCandidateConnectionMetric(snapshot)
	return conn, snapshot, nil
}

func acceptExperimentalMuxCandidateConn(ctx context.Context, conn net.Conn, cfg ExperimentalMuxCandidateConfig) (net.Conn, ExperimentalMuxCandidateSnapshot, error) {
	ctx = core.Context(ctx)
	cfg = cfg.normalized()
	if snapshot, err := cfg.validateFragmentWindowPolicyRisk("server"); err != nil {
		return nil, snapshot, err
	}
	var negotiated string
	if tlsCfg, err := cfg.serverTLSConfig(); err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureTLS, "", err)
	} else if tlsCfg != nil {
		tlsConn := tls.Server(conn, tlsCfg)
		if err := muxCandidateHandshake(ctx, tlsConn, cfg.HandshakeTimeout); err != nil {
			_ = tlsConn.Close()
			return nil, ExperimentalMuxCandidateSnapshot{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureTLS, "", err)
		}
		state := tlsConn.ConnectionState()
		negotiated = state.NegotiatedProtocol
		if negotiated != cfg.Protocol {
			_ = tlsConn.Close()
			return nil, ExperimentalMuxCandidateSnapshot{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureALPN, negotiated, fmt.Errorf("negotiated protocol %q, want %q", negotiated, cfg.Protocol))
		}
		conn = tlsConn
	}
	if negotiated == "" {
		negotiated = cfg.Protocol
	}
	peer, err := exchangeExperimentalMuxCandidateProtocol(ctx, conn, cfg)
	if err != nil {
		return nil, ExperimentalMuxCandidateSnapshot{}, err
	}
	snapshot := cfg.snapshot("server", negotiated)
	snapshot.PeerProtocol = peer.Protocol
	recordExperimentalMuxCandidateConnectionMetric(snapshot)
	return conn, snapshot, nil
}

func exchangeExperimentalMuxCandidateProtocol(ctx context.Context, conn net.Conn, cfg ExperimentalMuxCandidateConfig) (experimentalMuxCandidatePeer, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return experimentalMuxCandidatePeer{}, err
	}
	deadline := time.Now().Add(cfg.HandshakeTimeout)
	if deadlineFromContext, ok := ctx.Deadline(); ok && deadlineFromContext.Before(deadline) {
		deadline = deadlineFromContext
	}
	if !deadline.IsZero() {
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}
	preface := []byte(formatExperimentalMuxCandidatePreface(cfg))
	if len(preface) > experimentalMuxCandidateMaxPrefaceBytes {
		return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, "", NewError(CodeInvalidArgument, "mux candidate protocol preface too large"))
	}
	if _, err := conn.Write(preface); err != nil {
		return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(muxCandidateIOFailurePhase(conn), "", fmt.Errorf("write mux candidate protocol preface: %w", err))
	}
	peer, err := readExperimentalMuxCandidatePreface(conn)
	if err != nil {
		return peer, err
	}
	if peer.Protocol != cfg.Protocol {
		return peer, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureProtocol, peer.Protocol, NewError(CodeUnavailable, "mux candidate protocol mismatch"))
	}
	if err := validateExperimentalMuxCandidatePolicy(cfg, peer); err != nil {
		return peer, err
	}
	return peer, nil
}

func readExperimentalMuxCandidatePreface(conn net.Conn) (experimentalMuxCandidatePeer, error) {
	buf := make([]byte, 0, experimentalMuxCandidateMaxPrefaceBytes)
	var one [1]byte
	for len(buf) < experimentalMuxCandidateMaxPrefaceBytes {
		n, err := conn.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				line := string(buf)
				if !strings.HasPrefix(line, experimentalMuxCandidatePrefacePrefix) {
					return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, "", NewError(CodeUnavailable, "mux candidate protocol preface mismatch"))
				}
				return parseExperimentalMuxCandidatePreface(strings.TrimPrefix(line, experimentalMuxCandidatePrefacePrefix))
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, "", ErrExperimentalMuxTransportClosed)
			}
			return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(muxCandidateIOFailurePhase(conn), "", fmt.Errorf("read mux candidate protocol preface: %w", err))
		}
	}
	return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, "", NewError(CodeUnavailable, "mux candidate protocol preface too large"))
}

func formatExperimentalMuxCandidatePreface(cfg ExperimentalMuxCandidateConfig) string {
	policy := effectiveExperimentalMuxCandidatePolicy(cfg)
	return fmt.Sprintf(
		"%s%s frame=%s payload=%s maxFrame=%d maxMessage=%d\n",
		experimentalMuxCandidatePrefacePrefix,
		policy.Protocol,
		policy.FrameCodec,
		policy.PayloadCodec,
		policy.MaxFrameBytes,
		policy.MaxMessageBytes,
	)
}

func parseExperimentalMuxCandidatePreface(line string) (experimentalMuxCandidatePeer, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return experimentalMuxCandidatePeer{}, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, "", NewError(CodeUnavailable, "mux candidate protocol preface missing protocol"))
	}
	peer := experimentalMuxCandidatePeer{Protocol: fields[0]}
	for _, field := range fields[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "frame":
			peer.FrameCodec = value
		case "payload":
			peer.PayloadCodec = value
		case "maxFrame":
			if _, err := fmt.Sscan(value, &peer.MaxFrameBytes); err != nil {
				return peer, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, peer.Protocol, fmt.Errorf("parse mux candidate maxFrame: %w", err))
			}
		case "maxMessage":
			if _, err := fmt.Sscan(value, &peer.MaxMessageBytes); err != nil {
				return peer, newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailurePreface, peer.Protocol, fmt.Errorf("parse mux candidate maxMessage: %w", err))
			}
		}
	}
	return peer, nil
}

func validateExperimentalMuxCandidatePolicy(cfg ExperimentalMuxCandidateConfig, peer experimentalMuxCandidatePeer) error {
	policy := effectiveExperimentalMuxCandidatePolicy(cfg)
	if peer.FrameCodec != "" && peer.FrameCodec != policy.FrameCodec {
		return newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureFramePolicy, peer.Protocol, fmt.Errorf("peer frame codec %q, want %q", peer.FrameCodec, policy.FrameCodec))
	}
	if peer.PayloadCodec != "" && peer.PayloadCodec != policy.PayloadCodec {
		return newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureFramePolicy, peer.Protocol, fmt.Errorf("peer payload codec %q, want %q", peer.PayloadCodec, policy.PayloadCodec))
	}
	if peer.MaxFrameBytes > 0 && peer.MaxFrameBytes != policy.MaxFrameBytes {
		return newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureFramePolicy, peer.Protocol, fmt.Errorf("peer max frame %d, want %d", peer.MaxFrameBytes, policy.MaxFrameBytes))
	}
	if peer.MaxMessageBytes > 0 && peer.MaxMessageBytes != policy.MaxMessageBytes {
		return newExperimentalMuxCandidateFailure(experimentalMuxCandidateFailureFramePolicy, peer.Protocol, fmt.Errorf("peer max message %d, want %d", peer.MaxMessageBytes, policy.MaxMessageBytes))
	}
	return nil
}

func effectiveExperimentalMuxCandidatePolicy(cfg ExperimentalMuxCandidateConfig) experimentalMuxCandidatePeer {
	cfg = cfg.normalized()
	maxFrame := cfg.MaxFrameBytes
	if maxFrame <= 0 {
		maxFrame = DefaultMaxFrameBytes
	}
	maxMessage := cfg.MaxMessageBytes
	if maxMessage <= 0 {
		maxMessage = maxFrame * 16
	}
	return experimentalMuxCandidatePeer{
		Protocol:        cfg.Protocol,
		FrameCodec:      cfg.FrameCodec,
		PayloadCodec:    cfg.PayloadCodec,
		MaxFrameBytes:   maxFrame,
		MaxMessageBytes: maxMessage,
	}
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

func recordExperimentalMuxCandidateNegotiationFailureMetric(err error) {
	phase, peerProtocol, _ := experimentalMuxCandidateFailureInfo(err)
	rpcMuxCandidateNegotiationFailures.Inc(
		normalizeExperimentalMuxCandidateMetricLabel(phase, "unknown"),
		normalizeExperimentalMuxCandidateMetricLabel(peerProtocol, "unknown"),
	)
}

func recordExperimentalMuxCandidateDowngradeMetric(err error) {
	phase, peerProtocol, _ := experimentalMuxCandidateFailureInfo(err)
	rpcMuxCandidateDowngrades.Inc(
		normalizeExperimentalMuxCandidateMetricLabel(phase, "unknown"),
		normalizeExperimentalMuxCandidateMetricLabel(peerProtocol, "unknown"),
	)
}

func recordExperimentalMuxCandidateConnectionMetric(snapshot ExperimentalMuxCandidateSnapshot) {
	if !snapshot.Enabled {
		return
	}
	downgraded := "false"
	if snapshot.Downgraded {
		downgraded = "true"
	}
	rpcMuxCandidateConnections.Set(1,
		normalizeExperimentalMuxCandidateMetricLabel(snapshot.FrameCodec, "unknown"),
		normalizeExperimentalMuxCandidateMetricLabel(snapshot.PayloadCodec, "unknown"),
		downgraded,
	)
}

func recordExperimentalMuxCandidateDrainMetric(reason string, direction string, activeStreams int) {
	reason = normalizeExperimentalMuxCandidateMetricLabel(reason, "unknown")
	direction = normalizeExperimentalMuxCandidateMetricLabel(direction, "unknown")
	rpcMuxCandidateDrains.Inc(reason, direction)
	rpcMuxCandidateActiveStreams.Set(float64(activeStreams), reason, "draining")
}

func recordExperimentalMuxCandidateForcedCloseMetric(reason string) {
	reason = normalizeExperimentalMuxCandidateMetricLabel(reason, "unknown")
	rpcMuxCandidateDrainTimeouts.Inc(reason)
	rpcMuxCandidateForcedCloses.Inc(reason)
}

func recordExperimentalMuxCandidateFlowControlMetric(event string, count int64) {
	if count <= 0 {
		return
	}
	rpcMuxCandidateFlowControlEvents.Add(float64(count), normalizeExperimentalMuxCandidateMetricLabel(event, "unknown"))
}

func normalizeExperimentalMuxCandidateMetricLabel(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	if len(value) > 64 {
		value = value[:64]
	}
	var b strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == '_' || ch == '-' || ch == '/' || ch == '.':
			b.WriteRune(ch)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return fallback
	}
	return out
}

func muxCandidateIOFailurePhase(conn net.Conn) string {
	if _, ok := conn.(*tls.Conn); ok {
		return experimentalMuxCandidateFailureTLS
	}
	return experimentalMuxCandidateFailurePreface
}

func newExperimentalMuxCandidateFailure(phase string, peerProtocol string, err error) error {
	if err == nil {
		err = errors.New("mux candidate negotiation failed")
	}
	return &ExperimentalMuxCandidateFailure{Phase: phase, PeerProtocol: peerProtocol, Err: err}
}

func experimentalMuxCandidateFailureInfo(err error) (phase string, peerProtocol string, ok bool) {
	var failure *ExperimentalMuxCandidateFailure
	if errors.As(err, &failure) && failure != nil {
		return failure.Phase, failure.PeerProtocol, true
	}
	return "", "", false
}

func isExperimentalMuxCandidatePolicyRiskFailure(err error) bool {
	phase, _, ok := experimentalMuxCandidateFailureInfo(err)
	return ok && phase == experimentalMuxCandidateFailurePolicyRisk
}
