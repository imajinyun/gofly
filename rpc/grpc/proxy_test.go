package grpc

import (
	"context"
	"strings"
	"testing"
	"time"
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
