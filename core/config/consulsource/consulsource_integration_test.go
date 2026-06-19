//go:build integration

package consulsource

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gofly/gofly/core/config"
)

func TestConsulSourceIntegration_GetAndWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	addr := startConsul(t, ctx)

	src, err := New(Config{Address: addr, Key: "cfg/app", WaitTime: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	// Initial Get should return empty because no key is set yet.
	_, err = src.Get(ctx)
	if err == nil {
		t.Fatal("expected error for missing key")
	}

	// Use the internal client to write a value (we don't have a direct Put API).
	// Instead, create a second source pointing at the same key after writing via HTTP.
	if err := putConsulKey(ctx, addr, "cfg/app", `{"version":1}`); err != nil {
		t.Fatalf("put consul key: %v", err)
	}

	// Give consul a moment to persist.
	time.Sleep(100 * time.Millisecond)

	got, err := src.Get(ctx)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.Key != "cfg/app" || string(got.Data) != `{"version":1}` {
		t.Fatalf("Get = %#v, want key=cfg/app data={\"version\":1}", got)
	}

	// Watch for changes.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	changes := make(chan config.RemoteValue, 1)
	done := make(chan error, 1)
	go func() { done <- src.Watch(watchCtx, func(v config.RemoteValue) { changes <- v }) }()

	time.Sleep(300 * time.Millisecond)
	if err := putConsulKey(ctx, addr, "cfg/app", `{"version":2}`); err != nil {
		t.Fatalf("put updated key: %v", err)
	}

	select {
	case change := <-changes:
		if change.Key != "cfg/app" || string(change.Data) != `{"version":2}` {
			t.Fatalf("change = %#v, want updated value", change)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for consul watch update")
	}

	stopWatch()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Watch error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch shutdown")
	}
}

func startConsul(t *testing.T, ctx context.Context) string {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "hashicorp/consul:1.16",
		ExposedPorts: []string{"8500/tcp"},
		Cmd:          []string{"consul", "agent", "-dev", "-client", "0.0.0.0", "-bind", "0.0.0.0"},
		WaitingFor:   wait.ForListeningPort("8500/tcp").WithStartupTimeout(time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start consul container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("consul host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8500/tcp")
	if err != nil {
		t.Fatalf("consul port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

func putConsulKey(ctx context.Context, addr, key, value string) error {
	url := fmt.Sprintf("%s/v1/kv/%s", addr, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(value))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
