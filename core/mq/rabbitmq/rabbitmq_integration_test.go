//go:build integration

package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/imajinyun/gofly/core/mq"
)

func TestRabbitMQBrokerIntegrationPublishSubscribeAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	url := startRabbitMQ(t, ctx)
	broker, err := New(Options{URL: url, ExchangePrefix: "it", Prefetch: 1})
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
		Body:        []byte("created"),
		Headers:     map[string]string{"trace": "abc"},
		PublishedAt: time.Unix(100, 200),
	}
	if err := broker.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	select {
	case got := <-received:
		if got.ID != msg.ID || string(got.Body) != string(msg.Body) {
			t.Fatalf("received = %#v, want message core fields %#v", got, msg)
		}
		if got.Headers["trace"] != "abc" {
			t.Fatalf("headers = %#v, want trace header", got.Headers)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for RabbitMQ message")
	}
}

func TestRabbitMQBrokerIntegrationCloseConsumerChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	url := startRabbitMQ(t, ctx)
	broker, err := New(Options{URL: url})
	if err != nil {
		t.Fatalf("New broker error = %v", err)
	}
	defer broker.Close(context.Background())

	if _, err := broker.pubCh.QueueDeclare("orders.workers", false, true, false, false, nil); err != nil {
		t.Fatalf("QueueDeclare error = %v", err)
	}

	_, err = broker.Subscribe(ctx, "orders", "workers", func(context.Context, mq.Message) error { return nil })
	if err == nil {
		t.Fatal("Subscribe with conflicting queue error = nil, want error")
	}
	if !contains(err.Error(), "rabbitmq: declare queue:") && !contains(err.Error(), "rabbitmq: close consumer channel:") {
		t.Fatalf("Subscribe error = %v, want queue declare or close consumer channel error", err)
	}
}

func TestRabbitMQBrokerIntegrationDeadLetterSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	url := startRabbitMQ(t, ctx)
	broker, err := New(Options{URL: url, ExchangePrefix: "it"})
	if err != nil {
		t.Fatalf("New broker error = %v", err)
	}
	defer broker.Close(context.Background())

	dlqReceived := make(chan mq.Message, 1)
	dlqSub, err := broker.Subscribe(ctx, "orders.dlq", "dlq-workers", func(_ context.Context, msg mq.Message) error {
		dlqReceived <- msg
		return nil
	}, mq.WithMaxAttempts(1), mq.WithDeadLetterTopic(""))
	if err != nil {
		t.Fatalf("DLQ Subscribe error = %v", err)
	}
	defer dlqSub.Stop(context.Background())

	received := make(chan mq.Message, 1)
	sub, err := broker.Subscribe(ctx, "orders", "workers", func(_ context.Context, msg mq.Message) error {
		received <- msg
		return errors.New("always fail")
	}, mq.WithMaxAttempts(1), mq.WithDeadLetterTopic("orders.dlq"))
	if err != nil {
		t.Fatalf("Subscribe error = %v", err)
	}
	defer sub.Stop(context.Background())

	msg := mq.Message{Topic: "orders", ID: "order-dlq", Body: []byte("fail")}
	if err := broker.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	select {
	case <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for original message")
	}

	select {
	case got := <-dlqReceived:
		if got.ID != msg.ID || string(got.Body) != string(msg.Body) {
			t.Fatalf("dlq received = %#v, want %#v", got, msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for dead-letter message")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func startRabbitMQ(t *testing.T, ctx context.Context) string {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "rabbitmq:3.13-alpine",
		ExposedPorts: []string{"5672/tcp"},
		WaitingFor:   wait.ForListeningPort("5672/tcp").WithStartupTimeout(time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start rabbitmq container: %v", err)
	}
	t.Cleanup(func() {
		_ = testcontainers.TerminateContainer(container)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("rabbitmq host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5672/tcp")
	if err != nil {
		t.Fatalf("rabbitmq mapped port: %v", err)
	}
	return fmt.Sprintf("amqp://guest:guest@%s:%s/", host, port.Port())
}
