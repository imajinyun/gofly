package consulsource

import (
	"context"
	"encoding/base64"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/gofly/gofly/core/config"
)

func TestNewRequiresKey(t *testing.T) {
	_, err := New(Config{})
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("New error = %v, want key required", err)
	}
}

func TestNewConfiguresClientAndReadsWithNilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/cfg/app" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Consul-Token"); got != "secret" {
			t.Fatalf("X-Consul-Token = %q, want secret", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"source":"new"}`)) + `","ModifyIndex":13}]`))
	}))
	defer server.Close()

	src, err := New(Config{Address: server.URL, Token: "secret", Key: "cfg/app", WaitTime: 2 * time.Millisecond})
	if err != nil {
		t.Fatalf("New valid config error = %v", err)
	}
	if src.key != "cfg/app" {
		t.Fatalf("key = %q, want cfg/app", src.key)
	}
	if src.waitTime != 2*time.Millisecond {
		t.Fatalf("waitTime = %s, want 2ms", src.waitTime)
	}

	var nilCtx context.Context
	got, err := src.Get(nilCtx)
	if err != nil {
		t.Fatalf("Get(nil) error = %v", err)
	}
	if got.Key != "cfg/app" || string(got.Data) != `{"source":"new"}` || got.Version != 13 {
		t.Fatalf("Get(nil) = %#v, want key/data/version from New client", got)
	}
}

func TestNewWithClientValidationAndDefaults(t *testing.T) {
	if _, err := NewWithClient(nil, "cfg/app"); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("NewWithClient(nil) error = %v, want client is nil", err)
	}

	client, err := consulapi.NewClient(consulapi.DefaultConfig())
	if err != nil {
		t.Fatalf("new consul client: %v", err)
	}
	if _, err := NewWithClient(client, ""); err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("NewWithClient(empty key) error = %v, want key required", err)
	}

	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient valid error = %v", err)
	}
	if src.key != "cfg/app" {
		t.Fatalf("key = %q, want cfg/app", src.key)
	}
	if src.waitTime != 30*time.Second {
		t.Fatalf("waitTime = %s, want 30s", src.waitTime)
	}
}

func TestGetReadsConsulKVValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/cfg/app" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"name":"api"}`)) + `","ModifyIndex":7}]`))
	}))
	defer server.Close()

	src := newConsulTestSource(t, server.URL, "cfg/app")
	got, err := src.Get(context.Background())
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.Key != "cfg/app" || string(got.Data) != `{"name":"api"}` || got.Version != 7 {
		t.Fatalf("Get = %#v, want key/data/version from KV response", got)
	}
}

func TestGetReturnsNotFoundForMissingKey(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	src := newConsulTestSource(t, server.URL, "cfg/missing")
	_, err := src.Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Get missing key error = %v, want not found", err)
	}
}

func TestGetErrorBoundaries(t *testing.T) {
	t.Run("wraps consul kv error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		src := newConsulTestSource(t, server.URL, "cfg/app")
		_, err := src.Get(context.Background())
		if err == nil || !strings.Contains(err.Error(), `get "cfg/app"`) {
			t.Fatalf("Get server error = %v, want wrapped key error", err)
		}
	})

	t.Run("rejects modify index overflow", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"overflow":true}`)) + `","ModifyIndex":9223372036854775808}]`))
		}))
		defer server.Close()

		src := newConsulTestSource(t, server.URL, "cfg/app")
		_, err := src.Get(context.Background())
		if err == nil || !strings.Contains(err.Error(), "overflows int64") {
			t.Fatalf("Get overflow error = %v, want int64 overflow rejection", err)
		}
	})
}

func TestConsulModifyIndexVersionRejectsOverflow(t *testing.T) {
	if got, err := consulModifyIndexVersion(uint64(math.MaxInt64)); err != nil || got != math.MaxInt64 {
		t.Fatalf("consulModifyIndexVersion(MaxInt64) = %d, %v; want MaxInt64, nil", got, err)
	}
	if _, err := consulModifyIndexVersion(uint64(math.MaxInt64) + 1); err == nil || !strings.Contains(err.Error(), "overflows int64") {
		t.Fatalf("consulModifyIndexVersion overflow error = %v, want overflow rejection", err)
	}
}

func TestWatchPublishesChangedValueAndStopsOnCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/cfg/app" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", "11")
		_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"version":2}`)) + `","ModifyIndex":11}]`))
	}))
	defer server.Close()

	src := newConsulTestSource(t, server.URL, "cfg/app")
	src.waitTime = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes := make(chan string, 1)
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- src.Watch(ctx, func(v config.RemoteValue) {
			changes <- string(v.Data)
			cancel()
		})
	}()

	select {
	case got := <-changes:
		if got != `{"version":2}` {
			t.Fatalf("watch data = %q, want version payload", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch change")
	}
	select {
	case err := <-watchErr:
		if err != context.Canceled {
			t.Fatalf("Watch error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Watch shutdown")
	}
}

func TestWatchSkipsNoChangeAndDeletedKeyThenReturnsError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/cfg/app" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			w.Header().Set("X-Consul-Index", "5")
			_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"first":true}`)) + `","ModifyIndex":5}]`))
		case 2:
			w.Header().Set("X-Consul-Index", "5")
			_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"same":true}`)) + `","ModifyIndex":5}]`))
		case 3:
			w.Header().Set("X-Consul-Index", "6")
			_, _ = w.Write([]byte(`[]`))
		default:
			http.Error(w, "watch failed", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	src := newConsulTestSource(t, server.URL, "cfg/app")
	err := src.Watch(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), `watch "cfg/app"`) {
		t.Fatalf("Watch error = %v, want wrapped watch error after skipped events", err)
	}
	if got := calls.Load(); got < 4 {
		t.Fatalf("watch calls = %d, want at least 4 to cover no-change and deleted-key paths", got)
	}
}

func TestWatchRejectsOverflowVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/cfg/app" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", "9")
		_, _ = w.Write([]byte(`[{"Key":"cfg/app","Value":"` + base64.StdEncoding.EncodeToString([]byte(`{"overflow":true}`)) + `","ModifyIndex":9223372036854775808}]`))
	}))
	defer server.Close()

	src := newConsulTestSource(t, server.URL, "cfg/app")
	err := src.Watch(context.Background(), func(config.RemoteValue) {
		t.Fatal("onChange should not be called for overflowing modify index")
	})
	if err == nil || !strings.Contains(err.Error(), "overflows int64") {
		t.Fatalf("Watch overflow error = %v, want int64 overflow rejection", err)
	}
}

func TestSourceCloseNoop(t *testing.T) {
	if err := (*Source)(nil).Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

func newConsulTestSource(t *testing.T, address, key string) *Source {
	t.Helper()
	client, err := consulapi.NewClient(&consulapi.Config{Address: address})
	if err != nil {
		t.Fatalf("new consul test client: %v", err)
	}
	return newWithClient(client, key, time.Millisecond)
}
