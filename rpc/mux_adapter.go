package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/imajinyun/gofly/core"
	coreruntime "github.com/imajinyun/gofly/core/runtime"
)

const experimentalMuxRouteService = "_gofly_mux"

// ExperimentalMuxStreamHandler handles a stream accepted by the opt-in mux adapter.
type ExperimentalMuxStreamHandler func(context.Context, *ExperimentalMuxStream) error

// ExperimentalMuxAdapterSnapshot reports opt-in mux adapter state.
type ExperimentalMuxAdapterSnapshot struct {
	Role            string                           `json:"role,omitempty"`
	AcceptedStreams int64                            `json:"acceptedStreams,omitempty"`
	RejectedStreams int64                            `json:"rejectedStreams,omitempty"`
	HandlerErrors   int64                            `json:"handlerErrors,omitempty"`
	LastMethod      string                           `json:"lastMethod,omitempty"`
	LastError       string                           `json:"lastError,omitempty"`
	LastHandledAt   time.Time                        `json:"lastHandledAt,omitempty"`
	Transport       ExperimentalMuxTransportSnapshot `json:"transport"`
}

// ExperimentalMuxClientAdapter opens streams over an explicit mux transport.
type ExperimentalMuxClientAdapter struct {
	transport *ExperimentalMuxTransport
}

// ExperimentalMuxServerAdapter dispatches opt-in mux streams by method.
type ExperimentalMuxServerAdapter struct {
	transport *ExperimentalMuxTransport

	mu       sync.RWMutex
	handlers map[string]ExperimentalMuxStreamHandler

	acceptedStreams atomic.Int64
	rejectedStreams atomic.Int64
	handlerErrors   atomic.Int64
	lastHandledAt   atomic.Int64
	lastMethod      atomic.Value
	lastError       atomic.Value
}

// NewExperimentalMuxClientAdapter creates an opt-in mux client over conn.
func NewExperimentalMuxClientAdapter(conn net.Conn, opts ...ExperimentalMuxTransportOption) *ExperimentalMuxClientAdapter {
	return &ExperimentalMuxClientAdapter{
		transport: NewExperimentalMuxTransport(conn, opts...),
	}
}

// NewExperimentalMuxServerAdapter creates an opt-in mux server over conn.
func NewExperimentalMuxServerAdapter(conn net.Conn, opts ...ExperimentalMuxTransportOption) *ExperimentalMuxServerAdapter {
	serverOpts := append([]ExperimentalMuxTransportOption{WithExperimentalMuxServerRole()}, opts...)
	adapter := &ExperimentalMuxServerAdapter{
		transport: NewExperimentalMuxTransport(conn, serverOpts...),
		handlers:  make(map[string]ExperimentalMuxStreamHandler),
	}
	adapter.lastMethod.Store("")
	adapter.lastError.Store("")
	return adapter
}

// OpenStream opens a mux stream and sends an adapter routing frame before user messages.
func (a *ExperimentalMuxClientAdapter) OpenStream(ctx context.Context, method string) (*ExperimentalMuxStream, error) {
	if a == nil || a.transport == nil {
		return nil, ErrExperimentalMuxTransportClosed
	}
	method = normalizeExperimentalMuxMethod(method)
	if method == "" {
		return nil, NewError(CodeInvalidArgument, "mux stream method is required")
	}
	stream, err := a.transport.OpenStream(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(ctx, Message{Service: experimentalMuxRouteService, Method: method}); err != nil {
		_ = stream.CloseWithCode(context.Background(), CodeCanceled, "mux_route_failed")
		return nil, err
	}
	return stream, nil
}

// Close closes the underlying mux transport.
func (a *ExperimentalMuxClientAdapter) Close() error {
	if a == nil || a.transport == nil {
		return nil
	}
	return a.transport.Close()
}

// Snapshot returns client-side mux adapter transport state.
func (a *ExperimentalMuxClientAdapter) Snapshot() ExperimentalMuxAdapterSnapshot {
	if a == nil || a.transport == nil {
		return ExperimentalMuxAdapterSnapshot{Role: "client", Transport: ExperimentalMuxTransportSnapshot{Closed: true}}
	}
	return ExperimentalMuxAdapterSnapshot{Role: "client", Transport: a.transport.Snapshot()}
}

// DiagnosisSnapshot returns client-side mux transport diagnosis.
func (a *ExperimentalMuxClientAdapter) DiagnosisSnapshot() RPCMuxTransportDiagnosis {
	return muxDiagnosisFromAdapterSnapshot(a.Snapshot())
}

// RuntimeComponentSnapshot returns an AI-readable runtime component snapshot.
func (a *ExperimentalMuxClientAdapter) RuntimeComponentSnapshot(ctx context.Context) coreruntime.ComponentSnapshot {
	if err := ctx.Err(); err != nil {
		return coreruntime.ComponentSnapshot{
			Name:   "rpc.mux.client",
			Kind:   "client",
			Owner:  "rpc",
			Status: "error",
			Error:  err.Error(),
		}
	}
	snapshot := a.Snapshot()
	return coreruntime.ComponentSnapshot{
		Name:    "rpc.mux.client",
		Kind:    "client",
		Owner:   "rpc",
		Status:  muxAdapterStatus(snapshot),
		Details: a.DiagnosisSnapshot(),
	}
}

// RegisterStream registers an opt-in mux stream handler.
func (a *ExperimentalMuxServerAdapter) RegisterStream(method string, handler ExperimentalMuxStreamHandler) error {
	if a == nil {
		return ErrExperimentalMuxTransportClosed
	}
	method = normalizeExperimentalMuxMethod(method)
	if method == "" {
		return NewError(CodeInvalidArgument, "mux stream method is required")
	}
	if handler == nil {
		return NewError(CodeInvalidArgument, "mux stream handler is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.handlers[method]; ok {
		return NewError(CodeAlreadyExists, "mux stream handler already registered")
	}
	a.handlers[method] = handler
	return nil
}

// Serve accepts mux streams until ctx is canceled or the underlying transport closes.
func (a *ExperimentalMuxServerAdapter) Serve(ctx context.Context) error {
	if a == nil || a.transport == nil {
		return ErrExperimentalMuxTransportClosed
	}
	ctx = core.Context(ctx)
	for {
		stream, err := a.transport.AcceptStream(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrExperimentalMuxTransportClosed) {
				return nil
			}
			return err
		}
		streamCtx, cancel := context.WithCancel(ctx)
		go func() {
			defer cancel()
			a.handleStream(streamCtx, stream)
		}()
	}
}

// Close closes the underlying mux transport.
func (a *ExperimentalMuxServerAdapter) Close() error {
	if a == nil || a.transport == nil {
		return nil
	}
	return a.transport.Close()
}

// Snapshot returns server-side mux adapter state.
func (a *ExperimentalMuxServerAdapter) Snapshot() ExperimentalMuxAdapterSnapshot {
	if a == nil || a.transport == nil {
		return ExperimentalMuxAdapterSnapshot{Role: "server", Transport: ExperimentalMuxTransportSnapshot{Closed: true}}
	}
	method, _ := a.lastMethod.Load().(string)
	lastError, _ := a.lastError.Load().(string)
	return ExperimentalMuxAdapterSnapshot{
		Role:            "server",
		AcceptedStreams: a.acceptedStreams.Load(),
		RejectedStreams: a.rejectedStreams.Load(),
		HandlerErrors:   a.handlerErrors.Load(),
		LastMethod:      method,
		LastError:       lastError,
		LastHandledAt:   unixNanoToTime(a.lastHandledAt.Load()),
		Transport:       a.transport.Snapshot(),
	}
}

// DiagnosisSnapshot returns server-side mux transport diagnosis.
func (a *ExperimentalMuxServerAdapter) DiagnosisSnapshot() RPCMuxTransportDiagnosis {
	return muxDiagnosisFromAdapterSnapshot(a.Snapshot())
}

// RuntimeComponentSnapshot returns an AI-readable runtime component snapshot.
func (a *ExperimentalMuxServerAdapter) RuntimeComponentSnapshot(ctx context.Context) coreruntime.ComponentSnapshot {
	if err := ctx.Err(); err != nil {
		return coreruntime.ComponentSnapshot{
			Name:   "rpc.mux.server",
			Kind:   "server",
			Owner:  "rpc",
			Status: "error",
			Error:  err.Error(),
		}
	}
	snapshot := a.Snapshot()
	return coreruntime.ComponentSnapshot{
		Name:    "rpc.mux.server",
		Kind:    "server",
		Owner:   "rpc",
		Status:  muxAdapterStatus(snapshot),
		Details: a.DiagnosisSnapshot(),
	}
}

func (a *ExperimentalMuxServerAdapter) handleStream(ctx context.Context, stream *ExperimentalMuxStream) {
	route, err := stream.Receive(ctx)
	if err != nil {
		a.rejectStream(stream, CodeInvalidArgument, "mux route receive failed")
		a.rememberError("", err)
		return
	}
	method := normalizeExperimentalMuxMethod(route.Method)
	if route.Service != experimentalMuxRouteService || method == "" {
		a.rejectStream(stream, CodeInvalidArgument, "invalid mux route")
		a.rememberError(method, NewError(CodeInvalidArgument, "invalid mux route"))
		return
	}
	handler := a.lookup(method)
	if handler == nil {
		a.rejectStream(stream, CodeNotFound, "mux stream handler not found")
		a.rememberError(method, NewError(CodeNotFound, "mux stream handler not found"))
		return
	}
	a.acceptedStreams.Add(1)
	a.lastMethod.Store(method)
	a.lastHandledAt.Store(time.Now().UnixNano())
	if err := handler(ctx, stream); err != nil && !errors.Is(err, io.EOF) {
		a.handlerErrors.Add(1)
		a.rememberError(method, err)
		_ = stream.CloseWithCode(context.Background(), CodeOf(err), textOf(err))
	}
}

func (a *ExperimentalMuxServerAdapter) lookup(method string) ExperimentalMuxStreamHandler {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.handlers[method]
}

func (a *ExperimentalMuxServerAdapter) rejectStream(stream *ExperimentalMuxStream, code Code, reason string) {
	a.rejectedStreams.Add(1)
	if stream != nil {
		_ = stream.CloseWithCode(context.Background(), code, reason)
	}
}

func (a *ExperimentalMuxServerAdapter) rememberError(method string, err error) {
	if method != "" {
		a.lastMethod.Store(method)
	}
	if err != nil {
		a.lastError.Store(err.Error())
	}
	a.lastHandledAt.Store(time.Now().UnixNano())
}

func normalizeExperimentalMuxMethod(method string) string {
	return strings.Trim(strings.TrimSpace(method), "/")
}

func muxDiagnosisFromAdapterSnapshot(snapshot ExperimentalMuxAdapterSnapshot) RPCMuxTransportDiagnosis {
	transport := snapshot.Transport
	return RPCMuxTransportDiagnosis{
		Enabled:   !transport.Closed,
		Mode:      "experimental_mux",
		Adapter:   snapshot,
		Transport: transport,
		FlowControl: RPCMuxFlowControlDiagnosis{
			ReceiveQueueSize:      transport.ReceiveQueueSize,
			ConnectionWindow:      transport.ConnectionWindow,
			ConnectionCreditWaits: transport.ConnectionCreditWaits,
			StreamCreditWaits:     transport.CreditWaits,
			WindowFramesIn:        transport.WindowFramesIn,
			WindowFramesOut:       transport.WindowFramesOut,
			ConnectionWindowIn:    transport.ConnectionWindowFramesIn,
			ConnectionWindowOut:   transport.ConnectionWindowFramesOut,
			BackpressureEvents:    transport.BackpressureEvents,
		},
		Keepalive: RPCMuxKeepaliveDiagnosis{
			Liveness:           transport.Liveness,
			Interval:           transport.KeepaliveInterval,
			Idle:               transport.KeepaliveIdle,
			PingFramesIn:       transport.PingFramesIn,
			PingFramesOut:      transport.PingFramesOut,
			PongFramesIn:       transport.PongFramesIn,
			PongFramesOut:      transport.PongFramesOut,
			IdleTimeouts:       transport.IdleTimeouts,
			LastPingAt:         transport.LastPingAt,
			LastPongAt:         transport.LastPongAt,
			LastFrameReadAt:    transport.LastFrameReadAt,
			LastFrameWrittenAt: transport.LastFrameWrittenAt,
		},
		Drain: RPCMuxDrainDiagnosis{
			Draining:          transport.Draining,
			RemoteDraining:    transport.RemoteDraining,
			DrainReason:       transport.DrainReason,
			RemoteDrainReason: transport.RemoteDrainReason,
			GoAwayFramesIn:    transport.GoAwayFramesIn,
			GoAwayFramesOut:   transport.GoAwayFramesOut,
			DrainRejects:      transport.DrainRejects,
		},
	}
}

func muxAdapterStatus(snapshot ExperimentalMuxAdapterSnapshot) string {
	if snapshot.Transport.Closed {
		return "closed"
	}
	if snapshot.Transport.Liveness != "" {
		return snapshot.Transport.Liveness
	}
	return "ok"
}
