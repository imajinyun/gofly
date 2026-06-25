// Package rpc provides a lightweight RPC server and client over HTTP for gofly
// services. It supports service registration, method and streaming handlers,
// governance (rate limiting, circuit breaking, concurrency limiting), metadata
// propagation, and TLS.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

// HTTPServer is an HTTP-based RPC server with governance and service registration.
type HTTPServer struct {
	mu       sync.RWMutex
	methods  map[string]MethodDesc
	streams  map[string]StreamDesc
	services map[string]ServiceDesc
	opts     serverOptions
	server   *http.Server
	state    atomic.Int32
	since    atomic.Int64
	runtime  *ruleRuntime
}

type ruleRuntime struct {
	mu          sync.Mutex
	rateLimits  map[string]*cachedRateLimiter
	concurrency map[string]*cachedConcurrencyLimiter
	breakers    map[string]*cachedBreaker
	balancers   map[string]*cachedPolicyBalancer
}

type cachedRateLimiter struct {
	rate    int
	burst   int
	limiter *limit.Limiter
}

type cachedConcurrencyLimiter struct {
	limit   int
	limiter *limit.ConcurrencyLimiter
}

type cachedBreaker struct {
	policy  governance.BreakerPolicy
	breaker *breaker.AdaptiveBreaker
}

type cachedPolicyBalancer struct {
	signature string
	balancer  Balancer
}

// NewServer creates an HTTPServer with the given options.
func NewServer(opts ...ServerOption) *HTTPServer {
	o := serverOptions{addr: ":8081", codec: JSONCodec{}, governance: governance.NewRegistry()}
	for _, opt := range opts {
		opt(&o)
	}
	s := &HTTPServer{methods: make(map[string]MethodDesc), streams: make(map[string]StreamDesc), services: make(map[string]ServiceDesc), opts: o, runtime: newRuleRuntime()}
	s.setState(serverStateInitialized)
	return s
}

// RegisterService registers a service descriptor and its implementation.
func (s *HTTPServer) RegisterService(desc ServiceDesc, impl any) error {
	if err := desc.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.services[desc.Name]; ok {
		return fmt.Errorf("service %s already registered", desc.Name)
	}
	methods := make(map[string]MethodDesc, len(desc.Methods))
	for _, method := range desc.Methods {
		if method.Handler == nil {
			return fmt.Errorf("rpc service %s method %s handler is required", desc.Name, method.Name)
		}
		key := desc.Name + "/" + method.Name
		if _, ok := s.methods[key]; ok {
			return fmt.Errorf("method %s already registered", key)
		}
		methods[key] = cloneMethodDesc(method)
	}
	streams := make(map[string]StreamDesc, len(desc.Streams))
	for _, stream := range desc.Streams {
		if stream.Handler == nil {
			return fmt.Errorf("rpc service %s stream %s handler is required", desc.Name, stream.Name)
		}
		key := desc.Name + "/" + stream.Name
		if _, ok := s.streams[key]; ok {
			return fmt.Errorf("stream %s already registered", key)
		}
		streams[key] = cloneStreamDesc(stream)
	}
	for key, method := range methods {
		s.methods[key] = method
	}
	for key, stream := range streams {
		s.streams[key] = stream
	}
	s.services[desc.Name] = cloneServiceDesc(desc)
	return nil
}

// GetServiceInfos returns a snapshot of registered service descriptors.
func (s *HTTPServer) GetServiceInfos() map[string]ServiceDesc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ServiceDesc, len(s.services))
	for k, v := range s.services {
		out[k] = cloneServiceDesc(v)
	}
	return out
}

// GetServiceDescriptors returns descriptors for all registered services.
func (s *HTTPServer) GetServiceDescriptors() map[string]Descriptor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Descriptor, len(s.services))
	for k, v := range s.services {
		out[k] = v.Descriptor()
	}
	return out
}

// GetServiceDescriptor returns the descriptor for a named service.
func (s *HTTPServer) GetServiceDescriptor(name string) (Descriptor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	desc, ok := s.services[name]
	if !ok {
		return Descriptor{}, false
	}
	return desc.Descriptor(), true
}

func cloneServiceDesc(desc ServiceDesc) ServiceDesc {
	desc.Metadata = cloneStringMap(desc.Metadata)
	if len(desc.Methods) > 0 {
		methods := make([]MethodDesc, len(desc.Methods))
		for i, method := range desc.Methods {
			methods[i] = cloneMethodDesc(method)
		}
		desc.Methods = methods
	}
	if len(desc.Streams) > 0 {
		streams := make([]StreamDesc, len(desc.Streams))
		for i, stream := range desc.Streams {
			streams[i] = cloneStreamDesc(stream)
		}
		desc.Streams = streams
	}
	return desc
}

func cloneMethodDesc(desc MethodDesc) MethodDesc {
	desc.Metadata = cloneStringMap(desc.Metadata)
	if len(desc.Middlewares) > 0 {
		desc.Middlewares = append([]endpoint.Middleware(nil), desc.Middlewares...)
	}
	return desc
}

func cloneStreamDesc(desc StreamDesc) StreamDesc {
	desc.Metadata = cloneStringMap(desc.Metadata)
	if len(desc.Middlewares) > 0 {
		desc.Middlewares = append([]StreamMiddleware(nil), desc.Middlewares...)
	}
	return desc
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendEndpointMiddlewares(base []endpoint.Middleware, extra []endpoint.Middleware) []endpoint.Middleware {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make([]endpoint.Middleware, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

func appendStreamMiddlewares(base []StreamMiddleware, extra []StreamMiddleware) []StreamMiddleware {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make([]StreamMiddleware, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

// Start begins serving RPC requests. It is an alias for Run.
func (s *HTTPServer) Start() error { return s.Run() }

// Shutdown gracefully stops the server. It is an alias for Stop.
func (s *HTTPServer) Shutdown(ctx context.Context) error { return s.Stop(ctx) }

// Run listens on the configured address and serves RPC over HTTP.
func (s *HTTPServer) Run() error {
	s.setState(serverStateStarting)
	listener, err := net.Listen("tcp", s.opts.addr)
	if err != nil {
		s.setState(serverStateStopped)
		return fmt.Errorf("listen rpc: %w", err)
	}
	s.server = &http.Server{Addr: s.opts.addr, Handler: s, ReadHeaderTimeout: s.readHeaderTimeout()}
	if err := s.register(context.Background(), listener.Addr().String()); err != nil {
		_ = listener.Close()
		s.setState(serverStateStopped)
		return err
	}
	keepaliveCtx, stopKeepalive := context.WithCancel(context.Background())
	s.startRegistryKeepalive(keepaliveCtx)
	defer s.deregister(context.Background())
	defer stopKeepalive()
	s.setState(serverStateRunning)
	if s.opts.tls.Enabled() {
		if s.opts.tls.MutualEnabled() {
			tlsCfg, tlsErr := s.opts.tls.ServerTLSConfig()
			if tlsErr != nil {
				s.setState(serverStateStopped)
				return fmt.Errorf("configure rpc tls: %w", tlsErr)
			}
			s.server.TLSConfig = tlsCfg
			err = s.server.ServeTLS(listener, "", "")
		} else {
			err = s.server.ServeTLS(listener, s.opts.tls.CertFile, s.opts.tls.KeyFile)
		}
	} else {
		err = s.server.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	s.setState(serverStateStopped)
	return err
}

func (s *HTTPServer) readHeaderTimeout() time.Duration {
	if s.opts.readHeaderTimeout > 0 {
		return s.opts.readHeaderTimeout
	}
	return 3 * time.Second
}

// Stop shuts down the server gracefully.
func (s *HTTPServer) Stop(ctx context.Context) error {
	s.setState(serverStateStopping)
	if s.server == nil {
		s.setState(serverStateStopped)
		return nil
	}
	err := s.server.Shutdown(ctx)
	s.setState(serverStateStopped)
	return err
}

func (s *HTTPServer) register(ctx context.Context, listenerAddr string) error {
	if s.opts.registrar == nil {
		return nil
	}
	endpoint := s.opts.advertiseEndpoint
	if endpoint == "" {
		endpoint = "http://" + strings.TrimRight(listenerAddr, "/")
	}
	services := s.serviceNames()
	for _, service := range services {
		if err := s.registerOne(ctx, service, endpoint); err != nil {
			return fmt.Errorf("register rpc service %s: %w", service, err)
		}
	}
	s.opts.advertiseEndpoint = endpoint
	return nil
}

func (s *HTTPServer) registerOne(ctx context.Context, service string, endpoint string) error {
	if s.opts.registryTTL > 0 {
		if registrar, ok := s.opts.registrar.(interface {
			RegisterServiceWithOptions(context.Context, string, string, ...RegisterOption) error
		}); ok {
			return registrar.RegisterServiceWithOptions(ctx, service, endpoint, WithRegisterTTL(s.opts.registryTTL))
		}
	}
	return s.opts.registrar.RegisterService(ctx, service, endpoint)
}

func (s *HTTPServer) startRegistryKeepalive(ctx context.Context) {
	if s.opts.registrar == nil || s.opts.registryTTL <= 0 || s.opts.advertiseEndpoint == "" {
		return
	}
	interval := s.opts.registryRefresh
	if interval <= 0 {
		interval = s.opts.registryTTL / 2
	}
	if interval <= 0 {
		interval = s.opts.registryTTL
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, service := range s.serviceNames() {
					if err := s.registerOne(ctx, service, s.opts.advertiseEndpoint); err != nil && ctx.Err() == nil {
						slog.Warn("refresh rpc service registration", "service", service, "endpoint", s.opts.advertiseEndpoint, "error", err)
					}
				}
			}
		}
	}()
}

func (s *HTTPServer) deregister(ctx context.Context) {
	if s.opts.registrar == nil || s.opts.advertiseEndpoint == "" {
		return
	}
	for _, service := range s.serviceNames() {
		if err := s.opts.registrar.DeregisterService(ctx, service, s.opts.advertiseEndpoint); err != nil {
			slog.Warn("deregister rpc service", "service", service, "endpoint", s.opts.advertiseEndpoint, "error", err)
		}
	}
}

func (s *HTTPServer) serviceNames() []string {
	if s.opts.serviceName != "" {
		return []string{s.opts.serviceName}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.services))
	for name := range s.services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ServeHTTP implements http.Handler and dispatches RPC, stream, and admin requests.
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/rpc/admin/") {
		s.serveAdmin(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/rpc/stream/") {
		s.serveStream(w, r)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/rpc/") {
		http.NotFound(w, r)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/rpc/")
	s.mu.RLock()
	method, ok := s.methods[key]
	s.mu.RUnlock()
	if !ok {
		writeRPCError(w, http.StatusNotFound, CodeNotFound, "method not found")
		return
	}
	service, rpcMethod := splitRPCMethod(key)
	governanceReq := governance.Request{
		Transport: governance.TransportRPC,
		Service:   service,
		Method:    rpcMethod,
		Path:      "/" + strings.TrimPrefix(key, "/"),
		Tags:      s.rpcTags(service, rpcMethod, method.Metadata),
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
			writeRPCError(w, http.StatusServiceUnavailable, CodeUnavailable, "too many concurrent requests")
			return
		}
		defer limiter.Release()
	}
	if r.Body == nil {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "request body is required")
		return
	}
	if policy.MaxBodyBytes > 0 {
		if r.ContentLength > policy.MaxBodyBytes {
			writeRPCError(w, http.StatusRequestEntityTooLarge, CodeResourceExhausted, http.StatusText(http.StatusRequestEntityTooLarge))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, policy.MaxBodyBytes)
	}
	defer r.Body.Close()
	var env requestEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "decode request: "+err.Error())
		return
	}
	req := method.NewRequest()
	payload := []byte(env.Payload)
	if len(env.PayloadBytes) > 0 {
		payload = env.PayloadBytes
	}
	if err := s.opts.codec.Unmarshal(payload, req); err != nil {
		writeRPCError(w, http.StatusBadRequest, CodeInvalidArgument, "decode payload: "+err.Error())
		return
	}
	ctx := r.Context()
	if len(env.Metadata) > 0 {
		ctx = metadata.NewContext(ctx, env.Metadata)
	}
	ctx = applyGovernanceMetadata(ctx, s.serviceMetadata(service))
	ctx = applyGovernanceMetadata(ctx, method.Metadata)
	ctx = applyGovernanceMetadata(ctx, canaryMetadata(policy.Canary, governanceReq))
	ctx = applyGovernanceMetadata(ctx, policy.Metadata)
	ctx, cancel := withPolicyTimeout(ctx, effectiveTimeout(policy.Timeout, method.Timeout))
	defer cancel()
	callCtx := ctx
	ep := endpoint.Endpoint(func(ctx context.Context, in any) (resp any, err error) {
		callCtx = ctx
		defer func() {
			if v := recover(); v != nil {
				slog.Error("rpc panic recovered", "panic", v, "stack", string(debug.Stack()))
				err = fmt.Errorf("panic recovered: %v", v)
			}
		}()
		return method.Handler(ctx, in)
	})
	ep = endpoint.Chain(appendEndpointMiddlewares(s.opts.middlewares, method.Middlewares)...)(ep)
	var resp any
	var err error
	if brk := s.ruleBreaker(runtimeKey, policy.Breaker); brk != nil {
		err = brk.Allow()
		if err == nil {
			resp, err = ep(ctx, req)
			err = normalizeContextError(ctx, err)
			if err != nil {
				brk.MarkFailure()
			} else {
				brk.MarkSuccess()
			}
		}
	} else {
		resp, err = ep(ctx, req)
		err = normalizeContextError(ctx, err)
	}
	if err != nil {
		code := CodeOf(err)
		if errors.Is(err, breaker.ErrOpen) {
			code = CodeUnavailable
		}
		writeRPCError(w, httpStatusFromCode(code), code, textOf(err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	respMD, _ := metadata.FromContext(callCtx)
	envResp := responseEnvelope{Metadata: respMD, Code: CodeOK, Codec: s.opts.codec.Name()}
	if s.opts.codec.Name() == "json" {
		envResp.Payload = resp
	} else {
		payload, err := s.opts.codec.Marshal(resp)
		if err != nil {
			writeRPCError(w, http.StatusInternalServerError, CodeInternal, "encode payload: "+err.Error())
			return
		}
		envResp.PayloadBytes = payload
	}
	_ = json.NewEncoder(w).Encode(envResp)
}

func newRuleRuntime() *ruleRuntime {
	return &ruleRuntime{
		rateLimits:  make(map[string]*cachedRateLimiter),
		concurrency: make(map[string]*cachedConcurrencyLimiter),
		breakers:    make(map[string]*cachedBreaker),
		balancers:   make(map[string]*cachedPolicyBalancer),
	}
}

func governanceRuntimeKey(decision governance.Decision, fallback string) string {
	if decision.RuleKey != "" {
		return decision.RuleKey
	}
	if decision.RuleName != "" {
		return "name:" + decision.RuleName
	}
	return fallback
}

func (s *HTTPServer) governanceDecision(req governance.Request) governance.Decision {
	return s.governanceDecisionContext(context.Background(), req)
}

func (s *HTTPServer) governanceDecisionContext(ctx context.Context, req governance.Request) governance.Decision {
	if s == nil || s.opts.rules == nil {
		return governance.Decision{}
	}
	if s.opts.manager != nil {
		return s.opts.manager.MatchContext(ctx, req)
	}
	return s.opts.rules.Match(req)
}

func (s *HTTPServer) rpcTags(service, method string, methodMetadata map[string]string) map[string]string {
	tags := s.serviceMetadata(service)
	for k, v := range methodMetadata {
		if tags == nil {
			tags = make(map[string]string)
		}
		tags[k] = v
	}
	if tags == nil {
		tags = make(map[string]string, 2)
	}
	tags["rpc.service"] = service
	tags["rpc.method"] = method
	return tags
}

func (s *HTTPServer) serviceMetadata(service string) map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	desc, ok := s.services[service]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	metadata := cloneStringMap(desc.Metadata)
	if desc.Version != "" {
		if metadata == nil {
			metadata = make(map[string]string, 1)
		}
		metadata["rpc.service.version"] = desc.Version
	}
	return metadata
}

func (s *HTTPServer) ruleRateLimiter(key string, policy governance.RateLimitPolicy) *limit.Limiter {
	if s == nil || s.runtime == nil || policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	s.runtime.mu.Lock()
	defer s.runtime.mu.Unlock()
	cached := s.runtime.rateLimits[key]
	if cached != nil && cached.rate == rate && cached.burst == burst {
		return cached.limiter
	}
	limiter := limit.New(rate, burst)
	s.runtime.rateLimits[key] = &cachedRateLimiter{rate: rate, burst: burst, limiter: limiter}
	return limiter
}

func (s *HTTPServer) ruleConcurrencyLimiter(key string, policy governance.ConcurrencyPolicy) *limit.ConcurrencyLimiter {
	if s == nil || s.runtime == nil || policy.Limit <= 0 {
		return nil
	}
	s.runtime.mu.Lock()
	defer s.runtime.mu.Unlock()
	cached := s.runtime.concurrency[key]
	if cached != nil && cached.limit == policy.Limit {
		return cached.limiter
	}
	limiter := limit.NewConcurrency(policy.Limit)
	s.runtime.concurrency[key] = &cachedConcurrencyLimiter{limit: policy.Limit, limiter: limiter}
	return limiter
}

func (s *HTTPServer) ruleBreaker(key string, policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	if s == nil || s.runtime == nil || !policy.Enabled {
		return nil
	}
	s.runtime.mu.Lock()
	defer s.runtime.mu.Unlock()
	cached := s.runtime.breakers[key]
	if cached != nil && cached.policy == policy {
		return cached.breaker
	}
	brk := adaptiveBreakerFromPolicy(policy)
	s.runtime.breakers[key] = &cachedBreaker{policy: policy, breaker: brk}
	return brk
}

func adaptiveBreakerFromPolicy(policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	opts := make([]breaker.AdaptiveOption, 0, 5)
	if policy.OpenTimeout > 0 {
		opts = append(opts, breaker.WithAdaptiveOpenTimeout(policy.OpenTimeout))
	}
	if policy.Window > 0 {
		opts = append(opts, breaker.WithAdaptiveWindow(policy.Window))
	}
	if policy.Buckets > 0 {
		opts = append(opts, breaker.WithAdaptiveBuckets(policy.Buckets))
	}
	if policy.MinRequests > 0 {
		opts = append(opts, breaker.WithAdaptiveMinRequests(policy.MinRequests))
	}
	if policy.FailureRatio > 0 {
		opts = append(opts, breaker.WithAdaptiveFailureRatio(policy.FailureRatio))
	}
	return breaker.NewAdaptive(opts...)
}

func withPolicyTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func effectiveTimeout(policyTimeout, methodTimeout time.Duration) time.Duration {
	if policyTimeout > 0 {
		return policyTimeout
	}
	return methodTimeout
}

func normalizeContextError(ctx context.Context, err error) error {
	if ctx == nil || ctx.Err() == nil {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return NewError(CodeDeadlineExceeded, ctx.Err().Error())
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return NewError(CodeCanceled, ctx.Err().Error())
	}
	return err
}

func canaryMetadata(policy governance.CanaryPolicy, req governance.Request) map[string]string {
	decision := governance.SelectCanary(policy, req)
	if !decision.Selected {
		return nil
	}
	md := map[string]string{governance.HeaderCanary: "true"}
	if decision.Service != "" {
		md[governance.HeaderCanaryService] = decision.Service
	}
	for key, value := range decision.Headers {
		md[key] = value
	}
	return md
}

func headerMap(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string]string, len(header))
	for key, values := range header {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

func writeRPCError(w http.ResponseWriter, status int, code Code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(responseEnvelope{Code: code, Error: msg})
}
