// Command mq-worker demonstrates the core/mq broker contract with an in-memory
// broker, retrying consumer, and observable delivery statistics.
//
// Run it:
//
//	go run ./examples/mq-worker
//
// Expected output shows one transient handler failure followed by a successful
// message acknowledgement and a broker stats snapshot.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imajinyun/gofly/core/mq"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	broker := mq.NewMemoryBroker()
	defer broker.Close(context.Background())

	received := make(chan mq.Message, 1)
	attempts := 0
	sub, err := broker.Subscribe(ctx, "orders.created", "workers", func(ctx context.Context, msg mq.Message) error {
		attempts++
		if attempts == 1 {
			fmt.Printf("handler attempt=%d result=retry\n", attempts)
			return errors.New("transient downstream error")
		}
		fmt.Printf("handler attempt=%d topic=%s key=%s body=%s\n", attempts, msg.Topic, msg.Key, string(msg.Body))
		received <- msg
		return nil
	}, mq.WithMaxAttempts(2), mq.WithRetryBackoff(10*time.Millisecond))
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = sub.Stop(context.Background())
	}()

	err = broker.Publish(ctx, mq.Message{
		Topic:   "orders.created",
		Key:     "order-42",
		Body:    []byte(`{"id":"order-42","status":"created"}`),
		Headers: map[string]string{"trace_id": "demo-trace"},
	})
	if err != nil {
		panic(err)
	}

	select {
	case msg := <-received:
		fmt.Printf("received id=%s key=%s header.trace_id=%s\n", msg.ID, msg.Key, msg.Headers["trace_id"])
	case <-ctx.Done():
		panic(ctx.Err())
	}

	stats := broker.Snapshot()
	fmt.Printf("stats published=%d delivered=%d acked=%d retried=%d dead=%d\n",
		stats.Published, stats.Delivered, stats.Acked, stats.Retried, stats.Dead)
}
