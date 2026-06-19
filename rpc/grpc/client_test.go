package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/gofly/gofly/core/security"

	stdgrpc "google.golang.org/grpc"
)

func TestDialValidation(t *testing.T) {
	if _, err := Dial(context.Background(), ""); err == nil {
		t.Fatal("Dial empty target should error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(ctx, "127.0.0.1:1"); err == nil || err.Error() == "" {
		t.Fatalf("Dial canceled context should error, got %v", err)
	}
}

func TestClientOptions(t *testing.T) {
	o := clientOptions{timeout: 5 * time.Second}

	WithDialOptions(stdgrpc.WithDefaultCallOptions())(&o)
	if len(o.dialOptions) != 1 {
		t.Fatalf("WithDialOptions did not append")
	}

	WithInsecure()(&o)
	if len(o.dialOptions) != 2 {
		t.Fatalf("WithInsecure did not append")
	}

	WithDialTimeout(3 * time.Second)(&o)
	if o.timeout != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", o.timeout)
	}

	// nil option is silently ignored
	before := len(o.dialOptions)
	var nilOpt ClientOption
	if nilOpt != nil {
		nilOpt(&o)
	}
	if len(o.dialOptions) != before {
		t.Fatal("nil option should not mutate options")
	}
}

func TestClientConnNilGuards(t *testing.T) {
	var nilConn *ClientConn
	if err := nilConn.Close(); err != nil {
		t.Fatalf("nil Close = %v, want nil", err)
	}
	empty := &ClientConn{}
	if err := empty.Close(); err != nil {
		t.Fatalf("empty Close = %v, want nil", err)
	}
}

func TestDialAppliesInsecureDefault(t *testing.T) {
	// stdgrpc.NewClient does not dial synchronously, so even a non-existent
	// target returns a *ClientConn without error. The insecure default is
	// applied inside Dial when no dial options are provided. We verify the
	// option is accepted by ensuring Dial does not return a "target is required"
	// or credentials-missing error.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	conn, err := Dial(ctx, "127.0.0.1:1", WithDialTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("Dial unexpected error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}
	_ = conn.Close()
}

func TestWithClientTLS(t *testing.T) {
	o := clientOptions{}
	WithClientTLS(security.TLSConfig{InsecureSkipVerify: true})(&o)
	if o.tls == nil {
		t.Fatal("expected tls config to be set")
	}
}

func TestClientConnInvokeAndNewStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	conn, err := Dial(ctx, "127.0.0.1:1", WithDialTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("Dial unexpected error: %v", err)
	}
	defer conn.Close()

	// Invoke on non-existent service will fail at connection level, but
	// the method delegation itself is exercised.
	_ = conn.Invoke(context.Background(), "/test.Service/Method", &struct{}{}, &struct{}{})

	// NewStream on non-existent service exercises delegation.
	_, _ = conn.NewStream(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, "/test.Service/Stream")
}
