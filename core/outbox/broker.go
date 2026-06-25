// Package outbox implements the transactional outbox pattern with pluggable
// stores (memory, SQL) and a relay that forwards messages to a Publisher.
package outbox

import (
	"context"

	"github.com/imajinyun/gofly/core/mq"
)

// brokerPublisher adapts a core/mq.Broker to the Publisher interface so the
// relay can forward outbox messages to any configured broker transport.
type brokerPublisher struct {
	broker mq.Broker
}

// BrokerPublisher wraps a core/mq.Broker as an outbox Publisher.
func BrokerPublisher(broker mq.Broker) Publisher {
	return brokerPublisher{broker: broker}
}

func (p brokerPublisher) Publish(ctx context.Context, msg Message) error {
	return p.broker.Publish(ctx, mq.Message{
		Topic:   msg.Topic,
		Key:     msg.Key,
		Body:    msg.Body,
		Headers: msg.Headers,
	})
}
