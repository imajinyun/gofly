//go:build integration

package etcdv3

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/imajinyun/gofly/core/discovery"
)

func TestEtcdRegistryIntegrationRegisterResolveDeregister(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := startEtcdClient(t, ctx)
	registry, err := NewWithClient(client, Config{Prefix: "/it/services", TTL: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	defer registry.Close(context.Background())

	instance := discovery.Instance{
		ID:       "users-1",
		Service:  "users",
		Endpoint: "127.0.0.1:8080",
		Version:  "v1",
		Zone:     "az-a",
		Tags:     map[string]string{"canary": "true"},
		Metadata: map[string]string{"owner": "team-a"},
	}
	lease, err := registry.Register(ctx, instance)
	if err != nil {
		t.Fatalf("Register error = %v", err)
	}
	defer lease.Close(context.Background())

	resolved, err := registry.Resolve(ctx, "users", discovery.WithVersion("v1"), discovery.WithZone("az-a"), discovery.WithTag("canary", "true"))
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("Resolve got %d instances, want 1: %#v", len(resolved), resolved)
	}
	got := resolved[0]
	if got.ID != instance.ID || got.Endpoint != instance.Endpoint || got.Metadata["owner"] != "team-a" {
		t.Fatalf("resolved instance = %#v, want registered instance", got)
	}

	if err := registry.Deregister(ctx, instance); err != nil {
		t.Fatalf("Deregister error = %v", err)
	}
	resolved, err = registry.Resolve(ctx, "users")
	if !errors.Is(err, discovery.ErrNoInstances) {
		t.Fatalf("Resolve after deregister error = %v, want ErrNoInstances", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("Resolve after deregister got %d instances, want 0", len(resolved))
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
