// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/security"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

func stdgrpcServiceConfigOption(name string) stdgrpc.DialOption {
	return stdgrpc.WithDefaultServiceConfig(serviceConfigForBalancer(name))
}

func serviceConfigForBalancer(name string) string {
	return fmt.Sprintf(`{"loadBalancingConfig":[{%q:{}}]}`, name)
}

type ClientConn struct {
	conn *stdgrpc.ClientConn
}

type ClientOption func(*clientOptions)

type clientOptions struct {
	dialOptions         []stdgrpc.DialOption
	timeout             time.Duration
	tls                 *security.TLSConfig
	waitForReady        bool
	credentialsProvided bool
	rules               *governance.RuleSet
}

func Dial(ctx context.Context, target string, opts ...ClientOption) (*ClientConn, error) {
	if target == "" {
		return nil, fmt.Errorf("grpc target is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	o := clientOptions{timeout: 5 * time.Second}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.tls != nil {
		tlsCfg, err := o.tls.ClientTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("configure grpc tls: %w", err)
		}
		if tlsCfg != nil {
			WithTransportCredentials(credentials.NewTLS(tlsCfg))(&o)
		}
	}
	if !o.credentialsProvided {
		o.dialOptions = append(o.dialOptions, stdgrpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("grpc client context: %w", err)
	}
	conn, err := stdgrpc.NewClient(target, o.dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}
	if o.waitForReady {
		conn.Connect()
		for state := conn.GetState(); state != connectivity.Ready; state = conn.GetState() {
			if state == connectivity.Shutdown {
				_ = conn.Close()
				return nil, fmt.Errorf("connect grpc client: connection shut down")
			}
			if !conn.WaitForStateChange(ctx, state) {
				_ = conn.Close()
				return nil, fmt.Errorf("connect grpc client: %w", ctx.Err())
			}
		}
	}
	return &ClientConn{conn: conn}, nil
}

// NewDefaultClient creates a gRPC client with gofly's default observability and
// tracing interceptors. When rules is non-nil, the same live RuleSet used by
// servers is evaluated for client-side timeout, retry, breaker, rate-limit and
// concurrency policies. Additional options are applied after these defaults.
func NewDefaultClient(ctx context.Context, target, service string, rules *governance.RuleSet, registry *metrics.Registry, opts ...ClientOption) (*ClientConn, error) {
	configured := clientOptions{timeout: 5 * time.Second, rules: rules}
	for _, opt := range opts {
		if opt != nil {
			opt(&configured)
		}
	}
	rules = configured.rules
	if registry == nil {
		registry = metrics.Default
	}
	unary := []stdgrpc.UnaryClientInterceptor{
		ObservabilityUnaryClientInterceptor(service, registry, nil),
		OTelUnaryClientInterceptor(),
	}
	stream := []stdgrpc.StreamClientInterceptor{
		ObservabilityStreamClientInterceptor(service, registry, nil),
		OTelStreamClientInterceptor(),
	}
	if rules != nil {
		unary = append(unary, GovernanceUnaryClientInterceptor(rules))
		stream = append(stream, GovernanceStreamClientInterceptor(rules))
	}
	configured.dialOptions = append([]stdgrpc.DialOption{
		stdgrpc.WithChainUnaryInterceptor(unary...),
		stdgrpc.WithChainStreamInterceptor(stream...),
	}, configured.dialOptions...)
	return Dial(ctx, target, func(o *clientOptions) { *o = configured })
}

func WithClientRules(rules *governance.RuleSet) ClientOption {
	return func(o *clientOptions) { o.rules = rules }
}

// WithDialOptions appends raw gRPC options. Because grpc.DialOption is opaque,
// callers using this escape hatch own transport credential configuration. Use
// the typed gofly options when default insecure credentials are desired.
func WithDialOptions(opts ...stdgrpc.DialOption) ClientOption {
	return func(o *clientOptions) {
		o.dialOptions = append(o.dialOptions, opts...)
		o.credentialsProvided = true
	}
}

func WithUnaryClientInterceptors(interceptors ...stdgrpc.UnaryClientInterceptor) ClientOption {
	return withClientDialOptions(stdgrpc.WithChainUnaryInterceptor(interceptors...))
}

func WithStreamClientInterceptors(interceptors ...stdgrpc.StreamClientInterceptor) ClientOption {
	return withClientDialOptions(stdgrpc.WithChainStreamInterceptor(interceptors...))
}

// WithTransportCredentials configures client transport credentials without
// requiring callers to use the raw WithDialOptions escape hatch.
func WithTransportCredentials(creds credentials.TransportCredentials) ClientOption {
	return func(o *clientOptions) {
		o.dialOptions = append(o.dialOptions, stdgrpc.WithTransportCredentials(creds))
		o.credentialsProvided = true
	}
}

func WithInsecure() ClientOption {
	return WithTransportCredentials(insecure.NewCredentials())
}

// WithClientTLS configures TLS or mutual TLS for the gRPC client. Provide
// CAFile to verify the server and CertFile/KeyFile to present a client identity
// (mTLS).
func WithClientTLS(cfg security.TLSConfig) ClientOption {
	return func(o *clientOptions) {
		c := cfg
		o.tls = &c
	}
}

func WithDialTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) { o.timeout = timeout }
}

// WithWaitForReady makes Dial wait until the channel reaches READY. It is
// opt-in because grpc.NewClient is intentionally lazy and non-blocking.
func WithWaitForReady() ClientOption {
	return func(o *clientOptions) { o.waitForReady = true }
}

// WithClientKeepalive configures HTTP/2 keepalive pings for the gRPC client.
// Callers should prefer conservative intervals accepted by their servers.
func WithClientKeepalive(params keepalive.ClientParameters) ClientOption {
	return withClientDialOptions(stdgrpc.WithKeepaliveParams(params))
}

func withClientDialOptions(opts ...stdgrpc.DialOption) ClientOption {
	return func(o *clientOptions) { o.dialOptions = append(o.dialOptions, opts...) }
}

func (c *ClientConn) Conn() *stdgrpc.ClientConn { return c.conn }

func (c *ClientConn) Invoke(ctx context.Context, method string, args any, reply any, opts ...stdgrpc.CallOption) error {
	return c.conn.Invoke(ctx, method, args, reply, opts...)
}

func (c *ClientConn) NewStream(ctx context.Context, desc *stdgrpc.StreamDesc, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
	return c.conn.NewStream(ctx, desc, method, opts...)
}

func (c *ClientConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
