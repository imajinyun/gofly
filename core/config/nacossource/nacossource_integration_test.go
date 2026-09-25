//go:build integration

package nacossource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/imajinyun/gofly/core/config"
)

func TestNacosSourceIntegrationGetAndWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	host, port, grpcPort := startNacos(t, ctx)
	addr := fmt.Sprintf("%s:%d", host, port)

	src, err := New(Config{
		Servers: []ServerConfig{{IPAddr: host, Port: port, GrpcPort: grpcPort}},
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

func startNacos(t *testing.T, ctx context.Context) (string, uint64, uint64) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "nacos/nacos-server:v2.5.1@sha256:8987908cb94ed5f9d30522a64493d35732a6c05f216d667a7addb022f3d92e80",
		ExposedPorts: []string{"8848/tcp", "9848/tcp"},
		Env: map[string]string{
			"MODE": "standalone",
		},
		WaitingFor: wait.ForHTTP("/nacos/v1/console/health/readiness").
			WithPort("8848/tcp").
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
			WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start nacos container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("nacos host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8848/tcp")
	if err != nil {
		t.Fatalf("nacos port: %v", err)
	}
	mappedPort, err := strconv.ParseUint(port.Port(), 10, 64)
	if err != nil {
		t.Fatalf("parse nacos port %q: %v", port.Port(), err)
	}
	grpcPort, err := container.MappedPort(ctx, "9848/tcp")
	if err != nil {
		t.Fatalf("nacos grpc port: %v", err)
	}
	mappedGrpcPort, err := strconv.ParseUint(grpcPort.Port(), 10, 64)
	if err != nil {
		t.Fatalf("parse nacos grpc port %q: %v", grpcPort.Port(), err)
	}
	return host, mappedPort, mappedGrpcPort
}

func publishNacosConfig(ctx context.Context, address, dataID, group, content string) error {
	form := url.Values{
		"content": {content},
		"dataId":  {dataID},
		"group":   {group},
	}
	endpoint := fmt.Sprintf("http://%s/nacos/v1/cs/configs", address)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
