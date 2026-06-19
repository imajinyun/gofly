//go:build integration

package etcdsource

import (
	"context"
	"fmt"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gofly/gofly/core/config"
)

func TestEtcdSourceIntegration_GetAndWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := startEtcdClient(t, ctx)
	defer client.Close()

	const key = "/gofly/config/app"
	if _, err := client.Put(ctx, key, `{"version":1}`); err != nil {
		t.Fatalf("put initial config: %v", err)
	}

	source, err := NewWithClient(client, key)
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	got, err := source.Get(ctx)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.Key != key || string(got.Data) != `{"version":1}` || got.Version == 0 {
		t.Fatalf("Get = %#v, want key/data/version from etcd", got)
	}

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	changes := make(chan config.RemoteValue, 1)
	done := make(chan error, 1)
	go func() { done <- source.Watch(watchCtx, func(v config.RemoteValue) { changes <- v }) }()

	// Give the watch goroutine a chance to establish its server-side watch
	// before publishing the update; otherwise a very fast Put can happen before
	// the subscription exists and no event is delivered.
	time.Sleep(300 * time.Millisecond)
	if _, err := client.Put(ctx, key, `{"version":2}`); err != nil {
		t.Fatalf("put updated config: %v", err)
	}
	select {
	case change := <-changes:
		if change.Key != key || string(change.Data) != `{"version":2}` || change.Version == 0 {
			t.Fatalf("change = %#v, want updated value", change)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for etcd watch update")
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

func startEtcdClient(t *testing.T, ctx context.Context) *clientv3.Client {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "quay.io/coreos/etcd:v3.5.15",
		ExposedPorts: []string{"2379/tcp"},
		Cmd: []string{
			"etcd",
			"--name", "default",
			"--listen-client-urls", "http://0.0.0.0:2379",
			"--advertise-client-urls", "http://127.0.0.1:2379",
			"--listen-peer-urls", "http://0.0.0.0:2380",
			"--initial-advertise-peer-urls", "http://127.0.0.1:2380",
			"--initial-cluster", "default=http://127.0.0.1:2380",
		},
		WaitingFor: wait.ForListeningPort("2379/tcp").WithStartupTimeout(time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start etcd container: %v", err)
	}
	t.Cleanup(func() {
		_ = testcontainers.TerminateContainer(container)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("etcd host: %v", err)
	}
	port, err := container.MappedPort(ctx, "2379/tcp")
	if err != nil {
		t.Fatalf("etcd mapped port: %v", err)
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%s", host, port.Port())},
		DialTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("new etcd client: %v", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.Status(pingCtx, client.Endpoints()[0]); err != nil {
		_ = client.Close()
		t.Fatalf("etcd status: %v", err)
	}
	return client
}
