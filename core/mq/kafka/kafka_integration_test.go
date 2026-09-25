//go:build integration

package kafka

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	segmentkafka "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/imajinyun/gofly/core/mq"
)

func TestKafkaIntegrationPublishSubscribe(t *testing.T) {
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
	createKafkaTopics(t, ctx, brokerAddr, topic)

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

func TestKafkaIntegrationDeadLetterSuccess(t *testing.T) {
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
	createKafkaTopics(t, ctx, brokerAddr, topic, dlqTopic)

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

func TestKafkaIntegrationRetryThenSuccess(t *testing.T) {
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
	createKafkaTopics(t, ctx, brokerAddr, topic)

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

	kafkaContainer, err := tckafka.Run(ctx, "confluentinc/confluent-local@sha256:8e391de42cfcd3498e7317dcf159790f1f1cc3f3ffce900b30d7da23888687fd")
	testcontainers.CleanupContainer(t, kafkaContainer)
	if err != nil {
		t.Fatalf("start kafka container: %v", err)
	}
	brokers, err := kafkaContainer.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	if len(brokers) != 1 {
		t.Fatalf("kafka brokers = %v, want one broker", brokers)
	}
	return brokers[0]
}

func createKafkaTopics(t *testing.T, ctx context.Context, brokerAddr string, topics ...string) {
	t.Helper()

	host, port, err := net.SplitHostPort(brokerAddr)
	if err != nil {
		t.Fatalf("parse kafka broker address %q: %v", brokerAddr, err)
	}
	configs := make([]segmentkafka.TopicConfig, 0, len(topics))
	for _, topic := range topics {
		configs = append(configs, segmentkafka.TopicConfig{
			Topic:             topic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		})
	}
	client := &segmentkafka.Client{Addr: segmentkafka.TCP(net.JoinHostPort(host, port))}
	response, err := client.CreateTopics(ctx, &segmentkafka.CreateTopicsRequest{Topics: configs})
	if err != nil {
		t.Fatalf("create kafka topics %v: %v", topics, err)
	}
	for topic, topicErr := range response.Errors {
		if topicErr != nil {
			t.Fatalf("create kafka topic %q: %v", topic, topicErr)
		}
	}
}
