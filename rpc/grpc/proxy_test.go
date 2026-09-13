package grpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"
)

func TestAppTokenProxyCachesAndBoundsConnections(t *testing.T) {
	proxy, err := NewAppTokenProxy("127.0.0.1:1",
		WithAppTokenProxyMaxEntries(1),
		WithAppTokenProxyTTL(time.Minute),
		WithAppTokenProxyClientOptions(WithInsecure()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	one := AppTokenCredentials{App: "orders", Token: "secret", AllowInsecure: true}
	first, err := proxy.TakeConn(context.Background(), one)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proxy.TakeConn(context.Background(), one)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("same credentials should reuse the cached connection")
	}
	if _, err := proxy.TakeConn(context.Background(), AppTokenCredentials{App: "billing", Token: "other", AllowInsecure: true}); err != nil {
		t.Fatal(err)
	}
	snapshot := proxy.Snapshot()
	if snapshot.Entries != 1 || snapshot.MaxEntries != 1 || snapshot.Closed {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if strings.Contains(appTokenProxyKey(one.App, one.Token), one.Token) {
		t.Fatal("proxy cache key contains the raw token")
	}
}

func TestAppTokenProxyExpirationAndLRU(t *testing.T) {
	proxy, err := NewAppTokenProxy("passthrough:///unused", nil, WithAppTokenProxyMaxEntries(2), WithAppTokenProxyTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	now := time.Unix(1000, 0)
	proxy.now = func() time.Time { return now }
	take := func(app string) *ClientConn {
		t.Helper()
		conn, err := proxy.TakeConn(t.Context(), AppTokenCredentials{App: app, Token: "test", AllowInsecure: true})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	first := take("first")
	now = now.Add(time.Second)
	second := take("second")
	now = now.Add(time.Second)
	if got := take("first"); got != first {
		t.Fatal("cache hit replaced first connection")
	}
	now = now.Add(time.Second)
	third := take("third")
	if second.Conn().GetState() != connectivity.Shutdown || first.Conn().GetState() == connectivity.Shutdown {
		t.Fatal("eviction did not preserve the recently accessed connection")
	}
	now = time.Unix(1060, 0)
	replacement := take("first")
	if replacement == first || first.Conn().GetState() != connectivity.Shutdown || third.Conn().GetState() == connectivity.Shutdown {
		t.Fatal("expiration did not close only the expired connection at its deadline")
	}
	if got := proxy.Snapshot(); got.Entries != 2 || got.TTL != time.Minute || got.Closed {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestAppTokenProxyConnectionFailures(t *testing.T) {
	t.Run("dial cancellation", func(t *testing.T) {
		proxy, err := NewAppTokenProxy("passthrough:///unused")
		if err != nil {
			t.Fatal(err)
		}
		defer proxy.Close()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		conn, err := proxy.TakeConn(ctx, AppTokenCredentials{App: "app", Token: "test", AllowInsecure: true})
		if conn != nil || !errors.Is(err, context.Canceled) || proxy.Snapshot().Entries != 0 {
			t.Fatalf("conn=%v err=%v snapshot=%+v", conn, err, proxy.Snapshot())
		}
	})
	t.Run("close while dialing", func(t *testing.T) {
		proxy, err := NewAppTokenProxy("passthrough:///unused")
		if err != nil {
			t.Fatal(err)
		}
		defer proxy.Close()
		WithAppTokenProxyClientOptions(func(*clientOptions) {
			if err := proxy.Close(); err != nil {
				t.Fatal(err)
			}
		})(proxy)
		conn, err := proxy.TakeConn(t.Context(), AppTokenCredentials{App: "app", Token: "test", AllowInsecure: true})
		if conn != nil || err == nil || !strings.Contains(err.Error(), "closed") || proxy.Snapshot().Entries != 0 {
			t.Fatalf("conn=%v err=%v snapshot=%+v", conn, err, proxy.Snapshot())
		}
	})
	for _, operation := range []string{"eviction", "close"} {
		t.Run(operation+" propagates close error", func(t *testing.T) {
			proxy, err := NewAppTokenProxy("passthrough:///unused", WithAppTokenProxyMaxEntries(1))
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			conn, err := proxy.TakeConn(t.Context(), AppTokenCredentials{App: "first", Token: "test", AllowInsecure: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if operation == "close" {
				err = proxy.Close()
			} else {
				_, err = proxy.TakeConn(t.Context(), AppTokenCredentials{App: "next", Token: "test", AllowInsecure: true})
			}
			if err == nil || proxy.Snapshot().Entries != 0 {
				t.Fatalf("err=%v snapshot=%+v", err, proxy.Snapshot())
			}
		})
	}
}

func TestAppTokenProxyValidationAndClose(t *testing.T) {
	if _, err := NewAppTokenProxy(""); err == nil {
		t.Fatal("empty target should fail")
	}
	proxy, err := NewAppTokenProxy("127.0.0.1:1", WithAppTokenProxyClientOptions(WithInsecure()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.TakeConn(context.Background(), AppTokenCredentials{}); err == nil {
		t.Fatal("empty credentials should fail")
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if !proxy.Snapshot().Closed {
		t.Fatal("proxy should report closed")
	}
	_, err = proxy.TakeConn(context.Background(), AppTokenCredentials{App: "orders", Token: "secret", AllowInsecure: true})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("TakeConn after Close = %v", err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	var nilProxy *AppTokenProxy
	if err := nilProxy.Close(); err != nil {
		t.Fatal(err)
	}
}
