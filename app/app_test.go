package app

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeServer struct {
	started  atomic.Bool
	shutdown atomic.Bool
	stop     chan struct{}
}

type contextValueServer struct {
	key   any
	value any
	seen  atomic.Bool
}

func (s *fakeServer) Start() error {
	s.started.Store(true)
	if s.stop != nil {
		<-s.stop
		return nil
	}
	<-time.After(10 * time.Millisecond)
	return nil
}

func (s *fakeServer) Shutdown(ctx context.Context) error {
	if s.shutdown.CompareAndSwap(false, true) && s.stop != nil {
		close(s.stop)
	}
	return nil
}

func (s *contextValueServer) Start() error { return nil }

func (s *contextValueServer) Shutdown(ctx context.Context) error {
	if ctx.Value(s.key) == s.value {
		s.seen.Store(true)
	}
	return nil
}

func TestRunStartsAndShutsDownServers(t *testing.T) {
	server := &fakeServer{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, []Server{server}); err != nil {
		t.Fatal(err)
	}
	if !server.shutdown.Load() {
		t.Fatal("server was not shut down")
	}
}

func TestRunLifecycleHooks(t *testing.T) {
	server := &fakeServer{stop: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var got []string
	appendStep := func(step string) Hook {
		return func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, step)
			if step == "after_start" {
				cancel()
			}
			return nil
		}
	}

	if err := Run(ctx, []Server{server},
		BeforeStart(appendStep("before_start")),
		AfterStart(appendStep("after_start")),
		BeforeShutdown(appendStep("before_shutdown")),
		AfterShutdown(appendStep("after_shutdown")),
	); err != nil {
		t.Fatal(err)
	}
	want := []string{"before_start", "after_start", "before_shutdown", "after_shutdown"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hooks = %v, want %v", got, want)
	}
	if !server.started.Load() || !server.shutdown.Load() {
		t.Fatalf("started/shutdown = %v/%v, want true/true", server.started.Load(), server.shutdown.Load())
	}
}

func TestRunShutdownContextKeepsValuesAfterCancel(t *testing.T) {
	key := struct{ name string }{name: "request-id"}
	server := &contextValueServer{key: key, value: "run-123"}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, server.value))
	cancel()

	if err := Run(ctx, []Server{server}); err != nil {
		t.Fatal(err)
	}
	if !server.seen.Load() {
		t.Fatal("shutdown context did not preserve parent context values")
	}
}

func TestRunAcceptsNilContext(t *testing.T) {
	server := &fakeServer{stop: make(chan struct{})}
	var ctx context.Context
	if err := Run(ctx, []Server{server}, AfterStart(func(context.Context) error {
		return context.Canceled
	})); err == nil {
		t.Fatal("Run succeeded, want after-start error")
	}
	if !server.shutdown.Load() {
		t.Fatal("server was not shut down")
	}
}
