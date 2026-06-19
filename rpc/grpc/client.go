// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/gofly/gofly/core/security"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type ClientConn struct {
	conn *stdgrpc.ClientConn
}

type ClientOption func(*clientOptions)

type clientOptions struct {
	dialOptions []stdgrpc.DialOption
	timeout     time.Duration
	tls         *security.TLSConfig
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
			o.dialOptions = append(o.dialOptions, stdgrpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
		}
	}
	if len(o.dialOptions) == 0 {
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
	return &ClientConn{conn: conn}, nil
}

func WithDialOptions(opts ...stdgrpc.DialOption) ClientOption {
	return func(o *clientOptions) { o.dialOptions = append(o.dialOptions, opts...) }
}

func WithInsecure() ClientOption {
	return WithDialOptions(stdgrpc.WithTransportCredentials(insecure.NewCredentials()))
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
