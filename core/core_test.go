package core

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestContextNilFallsBackToTODO(t *testing.T) {
	var nilCtx context.Context
	ctx := Context(nilCtx)
	if ctx == nil {
		t.Fatal("Context(nil) returned nil, want non-nil fallback")
	}
	// context.TODO() is non-nil but has no deadline or values.
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("fallback context should have no deadline")
	}
}

func TestContextNonNilPassedThrough(t *testing.T) {
	bg := context.Background()
	if got := Context(bg); got != bg {
		t.Fatal("Context(bg) should return the same context")
	}
}

func TestDefaultHTTPClient(t *testing.T) {
	c := DefaultHTTPClient()
	if c == nil {
		t.Fatal("DefaultHTTPClient returned nil")
	}
	if c.Timeout != DefaultHTTPClientTimeout {
		t.Fatalf("timeout = %v, want %v", c.Timeout, DefaultHTTPClientTimeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport is not *http.Transport")
	}
	if tr.MaxIdleConns != 100 {
		t.Fatalf("MaxIdleConns = %d, want 100", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 10 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 10", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Fatalf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want 10s", tr.TLSHandshakeTimeout)
	}
	if tr.TLSClientConfig != nil {
		t.Fatal("TLSClientConfig should be nil for default client")
	}
}

func TestNewHTTPClientWithOverrides(t *testing.T) {
	proxy := func(*http.Request) (*url.URL, error) { return nil, nil }
	tlsConf := &tls.Config{InsecureSkipVerify: true}
	c := NewHTTPClient(HTTPClientConfig{
		Timeout:             5 * time.Second,
		TLSHandshakeTimeout: 3 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 5,
		Proxy:               proxy,
		TLSClientConfig:     tlsConf,
	})
	if c.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", c.Timeout)
	}
	tr := c.Transport.(*http.Transport)
	if tr.MaxIdleConns != 50 {
		t.Fatalf("MaxIdleConns = %d, want 50", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 5 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 5", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 30*time.Second {
		t.Fatalf("IdleConnTimeout = %v, want 30s", tr.IdleConnTimeout)
	}
	if tr.TLSHandshakeTimeout != 3*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want 3s", tr.TLSHandshakeTimeout)
	}
	if tr.TLSClientConfig != tlsConf {
		t.Fatal("TLSClientConfig not passed through")
	}
}
