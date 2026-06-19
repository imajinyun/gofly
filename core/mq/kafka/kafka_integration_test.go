//go:build integration

package kafka

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gofly/gofly/core/mq"
)

func TestKafkaIntegration_PublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	brokerAddr := startKafka(t, ctx)

	broker, err := New(Options{Brokers: []string{brokerAddr}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	defer broker.Close(context.Background())

	const topic = "gofly.test.events"
	const group = "gofly-test-group"

	// Publish a message.
	msg := mq.Message{Topic: topic, Key: "k1", Body: []byte("hello kafka")}
	if err := broker.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	// Subscribe and consume.
	received := make(chan mq.Message, 1)
	sub, err := broker.Subscribe(ctx, topic, group, func(_ context.Context, m mq.Message) error {
		received <- m
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe error = %v", err)
	}
	defer sub.Stop(context.Background())

	select {
	case m := <-received:
		if string(m.Body) != "hello kafka" || m.Key != "k1" {
			t.Fatalf("message = %#v, want key=k1 body=hello kafka", m)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for kafka message")
	}
}

func TestKafkaIntegration_DeadLetterSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	brokerAddr := startKafka(t, ctx)

	broker, err := New(Options{Brokers: []string{brokerAddr}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	defer broker.Close(context.Background())

	const topic = "gofly.test.dlq.events"
	const dlqTopic = "gofly.test.dlq.events.dlq"
	const group = "gofly-test-dlq-group"

	dlqReceived := make(chan mq.Message, 1)
	dlqSub, err := broker.Subscribe(ctx, dlqTopic, "dlq-group", func(_ context.Context, m mq.Message) error {
		dlqReceived <- m
		return nil
	})
	if err != nil {
		t.Fatalf("DLQ Subscribe error = %v", err)
	}
	defer dlqSub.Stop(context.Background())

	received := make(chan mq.Message, 1)
	sub, err := broker.Subscribe(ctx, topic, group, func(_ context.Context, m mq.Message) error {
		received <- m
		return errors.New("always fail")
	}, mq.WithMaxAttempts(1), mq.WithDeadLetterTopic(dlqTopic))
	if err != nil {
		t.Fatalf("Subscribe error = %v", err)
	}
	defer sub.Stop(context.Background())

	msg := mq.Message{Topic: topic, Key: "k-dlq", Body: []byte("fail me")}
	if err := broker.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	select {
	case <-received:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for original message")
	}

	select {
	case m := <-dlqReceived:
		if string(m.Body) != "fail me" || m.Key != "k-dlq" {
			t.Fatalf("dlq message = %#v, want key=k-dlq body=fail me", m)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for dead-letter message")
	}
}

func TestKafkaIntegration_RetryThenSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	brokerAddr := startKafka(t, ctx)

	broker, err := New(Options{Brokers: []string{brokerAddr}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	defer broker.Close(context.Background())

	const topic = "gofly.test.retry.events"
	const group = "gofly-test-retry-group"

	attempts := make([]int, 0, 2)
	received := make(chan mq.Message, 1)
	sub, err := broker.Subscribe(ctx, topic, group, func(_ context.Context, m mq.Message) error {
		attempts = append(attempts, m.Attempts)
		if m.Attempts == 1 {
			return errors.New("retry me")
		}
		received <- m
		return nil
	}, mq.WithMaxAttempts(2), mq.WithDeadLetterTopic(""))
	if err != nil {
		t.Fatalf("Subscribe error = %v", err)
	}
	defer sub.Stop(context.Background())

	msg := mq.Message{Topic: topic, Key: "k-retry", Body: []byte("retry me")}
	if err := broker.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	select {
	case m := <-received:
		if string(m.Body) != "retry me" {
			t.Fatalf("message = %#v, want body=retry me", m)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for retried message")
	}

	if !reflect.DeepEqual(attempts, []int{1, 2}) {
		t.Fatalf("attempts = %v, want [1 2]", attempts)
	}
}

func startKafka(t *testing.T, ctx context.Context) string {
	t.Helper()

	// Start Zookeeper (required for Kafka testcontainer).
	zkReq := testcontainers.ContainerRequest{
		Image:        "confluentinc/cp-zookeeper:7.5.0",
		ExposedPorts: []string{"2181/tcp"},
		Env: map[string]string{
			"ZOOKEEPER_CLIENT_PORT": "2181",
		},
		WaitingFor: wait.ForListeningPort("2181/tcp").WithStartupTimeout(2 * time.Minute),
	}
	zkContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: zkReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start zookeeper container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(zkContainer) })

	zkHost, err := zkContainer.Host(ctx)
	if err != nil {
		t.Fatalf("zk host: %v", err)
	}
	zkPort, err := zkContainer.MappedPort(ctx, "2181/tcp")
	if err != nil {
		t.Fatalf("zk port: %v", err)
	}
	zkAddr := fmt.Sprintf("%s:%s", zkHost, zkPort.Port())

	// Start Kafka.
	kafkaReq := testcontainers.ContainerRequest{
		Image:        "confluentinc/cp-kafka:7.5.0",
		ExposedPorts: []string{"9092/tcp", "9093/tcp"},
		Env: map[string]string{
			"KAFKA_BROKER_ID":                        "1",
			"KAFKA_ZOOKEEPER_CONNECT":                zkAddr,
			"KAFKA_LISTENERS":                        "PLAINTEXT://0.0.0.0:9092,BROKER://0.0.0.0:9093",
			"KAFKA_ADVERTISED_LISTENERS":             "PLAINTEXT://localhost:9092",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":   "PLAINTEXT:PLAINTEXT,BROKER:PLAINTEXT",
			"KAFKA_INTER_BROKER_LISTENER_NAME":       "BROKER",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR": "1",
		},
		WaitingFor: wait.ForListeningPort("9092/tcp").WithStartupTimeout(2 * time.Minute),
	}
	kafkaContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: kafkaReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start kafka container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(kafkaContainer) })

	host, err := kafkaContainer.Host(ctx)
	if err != nil {
		t.Fatalf("kafka host: %v", err)
	}
	port, err := kafkaContainer.MappedPort(ctx, "9092/tcp")
	if err != nil {
		t.Fatalf("kafka port: %v", err)
	}
	return fmt.Sprintf("%s:%s", host, port.Port())
}
