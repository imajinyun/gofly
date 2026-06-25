// Command outbox-mq demonstrates the transactional outbox pattern for
// reliable message publishing.
package main

import (
	"context"
	"fmt"

	"github.com/imajinyun/gofly/core/outbox"
)

func main() {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	id, err := store.Enqueue(ctx, outbox.Message{Topic: "hello.jobs", Key: "verify", Body: []byte("hello")})
	if err != nil {
		panic(err)
	}
	publisher := outbox.PublisherFunc(func(ctx context.Context, msg outbox.Message) error {
		fmt.Printf("published topic=%s key=%s body=%s\n", msg.Topic, msg.Key, string(msg.Body))
		return nil
	})
	relay := outbox.NewRelay(store, publisher, outbox.RelayConfig{BatchSize: 10})
	processed, err := relay.ProcessBatch(ctx)
	if err != nil {
		panic(err)
	}
	record, _ := store.Get(id)
	fmt.Printf("outbox processed=%d status=%s\n", processed, record.Status)
}
