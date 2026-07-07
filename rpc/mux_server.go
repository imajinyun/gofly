package rpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreruntime "github.com/imajinyun/gofly/core/runtime"
)

type ExperimentalMuxServer struct {
	addr      string
	configure ExperimentalMuxServerConfigurer
	options   []ExperimentalMuxTransportOption

	mu       sync.Mutex
	listener net.Listener
	adapters map[*ExperimentalMuxServerAdapter]struct{}
	cancel   context.CancelFunc
	done     chan error
	closed   bool

	acceptedConns atomic.Int64
	closedConns   atomic.Int64
	lastError     atomic.Value
}

func NewExperimentalMuxServer(addr string, configure ExperimentalMuxServerConfigurer, opts ...ExperimentalMuxTransportOption) (*ExperimentalMuxServer, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, NewError(CodeInvalidArgument, "mux server address is required")
	}
	if configure == nil {
		return nil, NewError(CodeInvalidArgument, "mux server configurer is required")
	}
	s := &ExperimentalMuxServer{addr: addr, configure: configure, options: append([]ExperimentalMuxTransportOption(nil), opts...), adapters: make(map[*ExperimentalMuxServerAdapter]struct{}), done: make(chan error, 1)}
	s.lastError.Store("")
	return s, nil
}

func (s *ExperimentalMuxServer) Start() error {
	if s == nil {
		return ErrExperimentalMuxTransportClosed
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		_ = ln.Close()
		return ErrExperimentalMuxTransportClosed
	}
	s.listener = ln
	s.cancel = cancel
	s.mu.Unlock()
	go func() {
		s.done <- s.serve(ctx, ln)
	}()
	return nil
}

func (s *ExperimentalMuxServer) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	ln := s.listener
	adapters := make([]*ExperimentalMuxServerAdapter, 0, len(s.adapters))
	for adapter := range s.adapters {
		adapters = append(adapters, adapter)
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	for _, adapter := range adapters {
		_ = adapter.Close()
	}
	select {
	case err := <-s.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return nil
	}
}

func (s *ExperimentalMuxServer) Addr() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}

func (s *ExperimentalMuxServer) DiagnosisSnapshot() RPCMuxTransportDiagnosis {
	if s == nil {
		return RPCMuxTransportDiagnosis{Enabled: false, Mode: "experimental_mux_server"}
	}
	s.mu.Lock()
	adapters := make([]*ExperimentalMuxServerAdapter, 0, len(s.adapters))
	for adapter := range s.adapters {
		adapters = append(adapters, adapter)
	}
	closed := s.closed
	s.mu.Unlock()
	diag := RPCMuxTransportDiagnosis{Enabled: !closed, Mode: "experimental_mux_server"}
	diag.Adapter.Role = "server"
	diag.Adapter.AcceptedStreams = 0
	diag.Transport.Role = "server"
	for _, adapter := range adapters {
		snapshot := adapter.Snapshot()
		diag.Adapter.AcceptedStreams += snapshot.AcceptedStreams
		diag.Adapter.RejectedStreams += snapshot.RejectedStreams
		diag.Adapter.HandlerErrors += snapshot.HandlerErrors
		diag.Transport.AcceptedStreams += snapshot.Transport.AcceptedStreams
		diag.Transport.OpenedStreams += snapshot.Transport.OpenedStreams
		diag.Transport.ActiveStreams += snapshot.Transport.ActiveStreams
		diag.Transport.ClosedStreams += snapshot.Transport.ClosedStreams
		diag.Transport.GoAwayFramesOut += snapshot.Transport.GoAwayFramesOut
		diag.FlowControl.ConnectionWindow = snapshot.Transport.ConnectionWindow
		diag.Keepalive.Liveness = snapshot.Transport.Liveness
		diag.Drain.GoAwayFramesOut += snapshot.Transport.GoAwayFramesOut
	}
	return diag
}

func (s *ExperimentalMuxServer) RuntimeComponentSnapshot(ctx context.Context) coreruntime.ComponentSnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return coreruntime.ComponentSnapshot{Name: "rpc.mux.server", Kind: "server", Owner: "rpc", Target: s.Addr(), Status: "error", Error: err.Error()}
	}
	return coreruntime.ComponentSnapshot{Name: "rpc.mux.server", Kind: "server", Owner: "rpc", Target: s.Addr(), Status: muxServerStatus(s), Details: s.DiagnosisSnapshot()}
}

func (s *ExperimentalMuxServer) serve(ctx context.Context, ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.lastError.Store(err.Error())
			return err
		}
		adapter := NewExperimentalMuxServerAdapter(conn, s.options...)
		if err := s.configure(adapter); err != nil {
			s.lastError.Store(err.Error())
			_ = adapter.Close()
			return err
		}
		s.acceptedConns.Add(1)
		s.mu.Lock()
		if s.adapters == nil {
			s.adapters = make(map[*ExperimentalMuxServerAdapter]struct{})
		}
		s.adapters[adapter] = struct{}{}
		s.mu.Unlock()
		go func() {
			_ = adapter.Serve(ctx)
			_ = adapter.Close()
			s.closedConns.Add(1)
		}()
	}
}

func muxServerStatus(s *ExperimentalMuxServer) string {
	if s == nil {
		return "closed"
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "closed"
	}
	return "ok"
}
