//go:build integration

package nacossource

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gofly/gofly/core/config"
)

func TestNacosSourceIntegration_GetAndWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	addr := startNacos(t, ctx)

	src, err := New(Config{
		Servers: []ServerConfig{{IPAddr: addr, Port: 8848}},
		DataID:  "gofly-test",
		Group:   "DEFAULT_GROUP",
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	// Publish initial config via Nacos OpenAPI.
	if err := publishNacosConfig(ctx, addr, "gofly-test", "DEFAULT_GROUP", `{"version":1}`); err != nil {
		t.Fatalf("publish initial config: %v", err)
	}

	// Allow Nacos to persist.
	time.Sleep(500 * time.Millisecond)

	got, err := src.Get(ctx)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.Key != "gofly-test" || string(got.Data) != `{"version":1}` {
		t.Fatalf("Get = %#v, want key=gofly-test data={\"version\":1}", got)
	}

	// Watch for changes.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	changes := make(chan config.RemoteValue, 1)
	done := make(chan error, 1)
	go func() { done <- src.Watch(watchCtx, func(v config.RemoteValue) { changes <- v }) }()

	time.Sleep(300 * time.Millisecond)
	if err := publishNacosConfig(ctx, addr, "gofly-test", "DEFAULT_GROUP", `{"version":2}`); err != nil {
		t.Fatalf("publish updated config: %v", err)
	}

	select {
	case change := <-changes:
		if change.Key != "gofly-test" || string(change.Data) != `{"version":2}` {
			t.Fatalf("change = %#v, want updated value", change)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for nacos watch update")
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

func startNacos(t *testing.T, ctx context.Context) string {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "nacos/nacos-server:v2.2.3",
		ExposedPorts: []string{"8848/tcp"},
		Env: map[string]string{
			"MODE": "standalone",
		},
		WaitingFor: wait.ForListeningPort("8848/tcp").WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start nacos container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("nacos host: %v", err)
	}
	return host
}

func publishNacosConfig(ctx context.Context, host, dataID, group, content string) error {
	url := fmt.Sprintf("http://%s:8848/nacos/v1/cs/configs?dataId=%s&group=%s&content=%s", host, dataID, group, content)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
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
