// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/observability/metrics"
	coreruntime "github.com/imajinyun/gofly/core/runtime"
	"github.com/imajinyun/gofly/core/security"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

type Server struct {
	mu               sync.RWMutex
	addr             string
	actualAddr       string
	server           *stdgrpc.Server
	health           *health.Server
	enableHealth     bool
	enableReflection bool
	stopTimeout      time.Duration

	adminAddr       string
	adminActualAddr string
	adminServer     *http.Server
	rules           *governance.RuleSet
	registry        *metrics.Registry
	tlsErr          error
	runtime         *coreruntime.Registry
	unaryNames      []string
	streamNames     []string
	discovery       discovery.Registrar
	discoveryLease  discovery.Lease
	discoveryOpts   []discovery.RegisterOption
	discoveryEntry  discovery.Instance
	healthServices  []string
	ready           bool
}

type ServerOption func(*serverOptions)

type serverOptions struct {
	addr               string
	serverOptions      []stdgrpc.ServerOption
	unaryInterceptors  []stdgrpc.UnaryServerInterceptor
	streamInterceptors []stdgrpc.StreamServerInterceptor
	enableHealth       bool
	enableReflection   bool
	stopTimeout        time.Duration

	adminAddr      string
	rules          *governance.RuleSet
	registry       *metrics.Registry
	tls            security.TLSConfig
	unaryNames     []string
	streamNames    []string
	discovery      discovery.Registrar
	discoveryOpts  []discovery.RegisterOption
	discoveryEntry discovery.Instance
	healthServices []string
}

func NewServer(opts ...ServerOption) *Server {
	o := serverOptions{addr: ":8082", enableHealth: true, stopTimeout: 15 * time.Second}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	serverOptions := append([]stdgrpc.ServerOption(nil), o.serverOptions...)
	var tlsErr error
	if tlsCfg, err := o.tls.ServerTLSConfig(); err != nil {
		tlsErr = err
	} else if tlsCfg != nil {
		serverOptions = append(serverOptions, stdgrpc.Creds(credentials.NewTLS(tlsCfg)))
	}
	if len(o.unaryInterceptors) > 0 {
		serverOptions = append(serverOptions, stdgrpc.ChainUnaryInterceptor(o.unaryInterceptors...))
	}
	if len(o.streamInterceptors) > 0 {
		serverOptions = append(serverOptions, stdgrpc.ChainStreamInterceptor(o.streamInterceptors...))
	}
	s := &Server{
		addr:             o.addr,
		server:           stdgrpc.NewServer(serverOptions...),
		health:           health.NewServer(),
		enableHealth:     o.enableHealth,
		enableReflection: o.enableReflection,
		stopTimeout:      o.stopTimeout,
		adminAddr:        o.adminAddr,
		rules:            o.rules,
		registry:         o.registry,
		tlsErr:           tlsErr,
		runtime:          coreruntime.NewRegistry(),
		unaryNames:       append([]string(nil), o.unaryNames...),
		streamNames:      append([]string(nil), o.streamNames...),
		discovery:        o.discovery,
		discoveryOpts:    append([]discovery.RegisterOption(nil), o.discoveryOpts...),
		discoveryEntry:   cloneDiscoveryInstance(o.discoveryEntry),
		healthServices:   normalizedHealthServices(o.healthServices),
	}
	s.registerRuntime()
	if s.enableHealth {
		healthpb.RegisterHealthServer(s.server, s.health)
		s.setHealthStatus(healthpb.HealthCheckResponse_NOT_SERVING)
	}
	if s.enableReflection {
		reflection.Register(s.server)
	}
	return s
}

func NewDefaultServer(addr, serviceName string, rules *governance.RuleSet, registry *metrics.Registry, opts ...ServerOption) *Server {
	if registry == nil {
		registry = metrics.Default
	}
	unary := []stdgrpc.UnaryServerInterceptor{
		RecoveryUnaryServerInterceptor(nil),
		ObservabilityUnaryServerInterceptor(serviceName, registry, nil),
		OTelUnaryServerInterceptor(),
	}
	unaryNames := []string{"recover", "observability", "otel_trace"}
	stream := []stdgrpc.StreamServerInterceptor{
		RecoveryStreamServerInterceptor(nil),
		ObservabilityStreamServerInterceptor(serviceName, registry, nil),
		OTelStreamServerInterceptor(),
	}
	streamNames := []string{"recover", "observability", "otel_trace"}
	if rules != nil {
		unary = append(unary, GovernanceUnaryServerInterceptor(rules))
		stream = append(stream, GovernanceStreamServerInterceptor(rules))
		unaryNames = append(unaryNames, "governance")
		streamNames = append(streamNames, "governance")
	}
	defaults := []ServerOption{
		WithAddress(addr),
		WithHealthServices(serviceName),
		WithRules(rules),
		WithMetricsRegistry(registry),
		withNamedUnaryServerInterceptors(unaryNames, unary...),
		withNamedStreamServerInterceptors(streamNames, stream...),
	}
	return NewServer(append(defaults, opts...)...)
}

func WithAddress(addr string) ServerOption {
	return func(o *serverOptions) {
		if addr != "" {
			o.addr = addr
		}
	}
}

func WithServerOptions(opts ...stdgrpc.ServerOption) ServerOption {
	return func(o *serverOptions) { o.serverOptions = append(o.serverOptions, opts...) }
}

// WithServerKeepalive configures server keepalive parameters and enforcement.
func WithServerKeepalive(params keepalive.ServerParameters, policy keepalive.EnforcementPolicy) ServerOption {
	return WithServerOptions(stdgrpc.KeepaliveParams(params), stdgrpc.KeepaliveEnforcementPolicy(policy))
}

func WithUnaryServerInterceptors(interceptors ...stdgrpc.UnaryServerInterceptor) ServerOption {
	return func(o *serverOptions) {
		o.unaryInterceptors = append(o.unaryInterceptors, interceptors...)
		for range interceptors {
			o.unaryNames = append(o.unaryNames, "custom_unary_interceptor")
		}
	}
}

func WithStreamServerInterceptors(interceptors ...stdgrpc.StreamServerInterceptor) ServerOption {
	return func(o *serverOptions) {
		o.streamInterceptors = append(o.streamInterceptors, interceptors...)
		for range interceptors {
			o.streamNames = append(o.streamNames, "custom_stream_interceptor")
		}
	}
}

func withNamedUnaryServerInterceptors(names []string, interceptors ...stdgrpc.UnaryServerInterceptor) ServerOption {
	return func(o *serverOptions) {
		o.unaryInterceptors = append(o.unaryInterceptors, interceptors...)
		o.unaryNames = append(o.unaryNames, names...)
	}
}

func withNamedStreamServerInterceptors(names []string, interceptors ...stdgrpc.StreamServerInterceptor) ServerOption {
	return func(o *serverOptions) {
		o.streamInterceptors = append(o.streamInterceptors, interceptors...)
		o.streamNames = append(o.streamNames, names...)
	}
}

func WithHealth(enabled bool) ServerOption {
	return func(o *serverOptions) { o.enableHealth = enabled }
}

func WithReflection(enabled bool) ServerOption {
	return func(o *serverOptions) { o.enableReflection = enabled }
}

func WithStopTimeout(timeout time.Duration) ServerOption {
	return func(o *serverOptions) {
		if timeout > 0 {
			o.stopTimeout = timeout
		}
	}
}

func WithAdminAddr(addr string) ServerOption {
	return func(o *serverOptions) {
		if addr != "" {
			o.adminAddr = addr
		}
	}
}

func WithRules(rules *governance.RuleSet) ServerOption {
	return func(o *serverOptions) {
		if rules != nil {
			o.rules = rules
		}
	}
}

func WithMetricsRegistry(registry *metrics.Registry) ServerOption {
	return func(o *serverOptions) {
		if registry != nil {
			o.registry = registry
		}
	}
}

// WithDiscovery registers this gRPC server through a backend-neutral discovery
// registrar after the listener is created. The returned lease is owned by the
// server and closed during shutdown. When instance.Endpoint is empty, the
// listener address is advertised; wildcard listeners prefer POD_IP.
func WithDiscovery(registrar discovery.Registrar, instance discovery.Instance, opts ...discovery.RegisterOption) ServerOption {
	return func(o *serverOptions) {
		o.discovery = registrar
		o.discoveryEntry = cloneDiscoveryInstance(instance)
		o.discoveryOpts = append([]discovery.RegisterOption(nil), opts...)
	}
}

// WithHealthServices adds service names whose gRPC health status follows the
// server lifecycle in addition to the standard aggregate service name.
func WithHealthServices(services ...string) ServerOption {
	return func(o *serverOptions) {
		o.healthServices = append(o.healthServices, services...)
	}
}

// WithServerTLS configures TLS or mutual TLS for the gRPC server. When the
// config has a cert/key pair the server speaks TLS; when ClientCAFile is also
// set it requires and verifies client certificates (mTLS).
func WithServerTLS(cfg security.TLSConfig) ServerOption {
	return func(o *serverOptions) { o.tls = cfg }
}

func (s *Server) GRPCServer() *stdgrpc.Server { return s.server }

func (s *Server) Health() *health.Server { return s.health }

func (s *Server) Address() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.actualAddr != "" {
		return s.actualAddr
	}
	return s.addr
}

func (s *Server) AdminAddress() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.adminActualAddr != "" {
		return s.adminActualAddr
	}
	return ""
}

// Ready reports whether the listener and optional discovery registration are ready.
func (s *Server) Ready() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func (s *Server) RegisterService(desc *stdgrpc.ServiceDesc, impl any) {
	s.server.RegisterService(desc, impl)
	if desc != nil {
		s.mu.Lock()
		s.healthServices = normalizedHealthServices(append(s.healthServices, desc.ServiceName))
		ready := s.ready
		s.mu.Unlock()
		if s.enableHealth {
			status := healthpb.HealthCheckResponse_NOT_SERVING
			if ready {
				status = healthpb.HealthCheckResponse_SERVING
			}
			s.health.SetServingStatus(desc.ServiceName, status)
		}
	}
}

func (s *Server) RuntimeSnapshot(ctx context.Context) coreruntime.Snapshot {
	if s == nil || s.runtime == nil {
		return coreruntime.Snapshot{}
	}
	return s.runtime.Snapshot(ctx)
}

func (s *Server) Start() error {
	if s.tlsErr != nil {
		return fmt.Errorf("configure grpc tls: %w", s.tlsErr)
	}
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}
	s.setAddress(listener.Addr().String())
	if err := s.registerDiscovery(context.Background(), listener.Addr()); err != nil {
		_ = listener.Close()
		return err
	}

	var adminListener net.Listener
	if s.adminAddr != "" {
		var err error
		adminListener, err = net.Listen("tcp", s.adminAddr)
		if err != nil {
			_ = listener.Close()
			_ = s.closeDiscovery(context.Background())
			return fmt.Errorf("listen grpc admin: %w", err)
		}
		s.setAdminAddress(adminListener.Addr().String())
		s.setAdminServer(newAdminServer(adminListener.Addr().String(), s.rules, s.registry, s))
		go func(hs *http.Server) {
			if err := hs.Serve(adminListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				_ = hs.Close()
			}
		}(s.adminServer)
	}
	s.setReady(true)
	if err := s.server.Serve(listener); err != nil && !errors.Is(err, stdgrpc.ErrServerStopped) {
		s.setReady(false)
		_ = s.shutdownAdmin(context.Background())
		_ = s.closeDiscovery(context.Background())
		return fmt.Errorf("serve grpc: %w", err)
	}
	s.setReady(false)
	_ = s.closeDiscovery(context.Background())
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok && s.stopTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.stopTimeout)
		defer cancel()
	}
	s.setReady(false)
	var shutdownErr error
	if err := s.closeDiscovery(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("deregister grpc service: %w", err))
	}
	if err := s.shutdownAdmin(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	done := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return shutdownErr
	case <-ctx.Done():
		s.server.Stop()
		<-done
		return errors.Join(shutdownErr, ctx.Err())
	}
}

func (s *Server) registerDiscovery(ctx context.Context, listener net.Addr) error {
	if s.discovery == nil {
		return nil
	}
	instance := cloneDiscoveryInstance(s.discoveryEntry)
	instance.Service = strings.TrimSpace(instance.Service)
	if instance.Service == "" {
		return errors.New("grpc discovery service is required")
	}
	if strings.TrimSpace(instance.Endpoint) == "" {
		instance.Endpoint = grpcAdvertiseAddress(listener)
	}
	lease, err := s.discovery.Register(ctx, instance, s.discoveryOpts...)
	if err != nil {
		return fmt.Errorf("register grpc service %s: %w", instance.Service, err)
	}
	if lease == nil {
		return fmt.Errorf("register grpc service %s: discovery registrar returned a nil lease", instance.Service)
	}
	s.mu.Lock()
	s.discoveryEntry = cloneDiscoveryInstance(lease.Instance())
	s.discoveryLease = lease
	s.mu.Unlock()
	return nil
}

func (s *Server) closeDiscovery(ctx context.Context) error {
	s.mu.Lock()
	lease := s.discoveryLease
	s.discoveryLease = nil
	s.mu.Unlock()
	if lease == nil {
		return nil
	}
	return lease.Close(ctx)
}

func (s *Server) setReady(ready bool) {
	s.mu.Lock()
	s.ready = ready
	s.mu.Unlock()
	if !s.enableHealth {
		return
	}
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if ready {
		status = healthpb.HealthCheckResponse_SERVING
	}
	s.setHealthStatus(status)
}

func (s *Server) setHealthStatus(status healthpb.HealthCheckResponse_ServingStatus) {
	s.health.SetServingStatus("", status)
	s.mu.RLock()
	services := append([]string(nil), s.healthServices...)
	s.mu.RUnlock()
	for _, service := range services {
		s.health.SetServingStatus(service, status)
	}
}

func grpcAdvertiseAddress(listener net.Addr) string {
	if listener == nil {
		return ""
	}
	host, port, err := net.SplitHostPort(listener.String())
	if err != nil {
		return listener.String()
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = strings.TrimSpace(os.Getenv("POD_IP"))
		if host == "" {
			host = "127.0.0.1"
		}
	}
	return net.JoinHostPort(host, port)
}

func normalizedHealthServices(services []string) []string {
	seen := make(map[string]struct{}, len(services))
	out := make([]string, 0, len(services))
	for _, service := range services {
		service = strings.TrimSpace(service)
		if service == "" {
			continue
		}
		if _, ok := seen[service]; ok {
			continue
		}
		seen[service] = struct{}{}
		out = append(out, service)
	}
	sort.Strings(out)
	return out
}

func cloneDiscoveryInstance(instance discovery.Instance) discovery.Instance {
	instance.Tags = cloneDiscoveryMap(instance.Tags)
	instance.Metadata = cloneDiscoveryMap(instance.Metadata)
	return instance
}

func cloneDiscoveryMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (s *Server) setAddress(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actualAddr = addr
}

func (s *Server) setAdminAddress(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adminActualAddr = addr
}

func (s *Server) setAdminServer(server *http.Server) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adminServer = server
}

func (s *Server) shutdownAdmin(ctx context.Context) error {
	s.mu.RLock()
	admin := s.adminServer
	s.mu.RUnlock()
	if admin == nil {
		return nil
	}
	if err := admin.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown grpc admin: %w", err)
	}
	s.mu.Lock()
	if s.adminServer == admin {
		s.adminServer = nil
		s.adminActualAddr = ""
	}
	s.mu.Unlock()
	return nil
}

func (s *Server) registerRuntime() {
	if s == nil || s.runtime == nil {
		return
	}
	s.runtime.Register("rpc.grpc.server", "server", func(ctx context.Context) coreruntime.ComponentSnapshot {
		if err := ctx.Err(); err != nil {
			return coreruntime.ComponentSnapshot{
				Name:   "rpc.grpc.server",
				Kind:   "server",
				Owner:  "rpc",
				Target: s.Address(),
				Status: "error",
				Error:  err.Error(),
			}
		}
		status := "initialized"
		if s.Address() != "" && s.Address() != s.addr {
			status = "running"
		}
		return coreruntime.ComponentSnapshot{
			Name:   "rpc.grpc.server",
			Kind:   "server",
			Owner:  "rpc",
			Target: s.Address(),
			Status: status,
			Middleware: &coreruntime.MiddlewareSnapshot{
				Unary:  grpcRuntimeMiddlewareLayers(s.unaryNames, "unary"),
				Stream: grpcRuntimeMiddlewareLayers(s.streamNames, "stream"),
			},
			Governance: map[string]any{
				"rules": s.ruleCount(),
			},
			Details: map[string]any{
				"health":     s.enableHealth,
				"reflection": s.enableReflection,
				"adminAddr":  s.AdminAddress(),
			},
		}
	}, coreruntime.WithOwner("rpc"))
}

func (s *Server) ruleCount() int {
	if s == nil || s.rules == nil {
		return 0
	}
	return len(s.rules.Snapshot())
}

func grpcRuntimeMiddlewareLayers(names []string, mode string) []coreruntime.MiddlewareLayer {
	if len(names) == 0 {
		return nil
	}
	layers := make([]coreruntime.MiddlewareLayer, 0, len(names))
	for i, name := range names {
		layers = append(layers, coreruntime.MiddlewareLayer{
			Name:   name,
			Source: "preset",
			Order:  i,
			Reason: mode + " interceptor chain",
		})
	}
	return layers
}

func newAdminServer(addr string, rules *governance.RuleSet, registry *metrics.Registry, runtimeSource interface {
	RuntimeSnapshot(context.Context) coreruntime.Snapshot
}) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready, ok := runtimeSource.(interface{ Ready() bool }); ok && !ready.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/startupz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if registry != nil {
			_ = registry.WritePrometheus(w)
			return
		}
		_ = metrics.Default.WritePrometheus(w)
	})
	mux.HandleFunc("/runtime", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if runtimeSource == nil {
			_ = json.NewEncoder(w).Encode(coreruntime.Snapshot{})
			return
		}
		_ = json.NewEncoder(w).Encode(runtimeSource.RuntimeSnapshot(r.Context()))
	})
	if rules != nil {
		admin := governance.NewAdmin(rules, nil)
		mux.Handle("/governance/", http.StripPrefix("/governance", admin))
	}
	return &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
}
