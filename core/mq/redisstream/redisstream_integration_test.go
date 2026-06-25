//go:build integration

package redisstream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/imajinyun/gofly/core/kv/redis"
	"github.com/imajinyun/gofly/core/mq"
)

func TestRedisStreamBrokerIntegration_PublishSubscribeAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := startRedisClient(t, ctx)
	defer client.Close()

	broker, err := New(client, Options{
		Consumer:      "consumer-1",
		BlockInterval: 100 * time.Millisecond,
		ReadCount:     4,
	})
	if err != nil {
		t.Fatalf("New broker error = %v", err)
	}
	defer broker.Close(context.Background())

	received := make(chan mq.Message, 1)
	sub, err := broker.Subscribe(ctx, "orders", "workers", func(_ context.Context, msg mq.Message) error {
		received <- msg
		return nil
	}, mq.WithMaxAttempts(1), mq.WithDeadLetterTopic(""))
	if err != nil {
		t.Fatalf("Subscribe error = %v", err)
	}
	defer sub.Stop(context.Background())

	msg := mq.Message{
		Topic:       "orders",
		ID:          "order-1",
		Key:         "user-1",
		Body:        []byte("created"),
		Headers:     map[string]string{"trace": "abc"},
		PublishedAt: time.Unix(100, 200),
	}
	if err := broker.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	select {
	case got := <-received:
		if got.ID != msg.ID || got.Key != msg.Key || string(got.Body) != string(msg.Body) {
			t.Fatalf("received = %#v, want message core fields %#v", got, msg)
		}
		if got.Headers["trace"] != "abc" {
			t.Fatalf("headers = %#v, want trace header", got.Headers)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Redis Stream message")
	}
}

func startRedisClient(t *testing.T, ctx context.Context) *redis.Client {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	t.Cleanup(func() {
		_ = testcontainers.TerminateContainer(container)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("redis host: %v", err)
	}
	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("redis mapped port: %v", err)
	}

	client := redis.New(redis.Config{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	return client
}
