// Package outbox implements the transactional outbox pattern: business writes
// and the messages they emit are persisted atomically in the same store, then a
// background relay reliably forwards persisted messages to a message broker.
//
// This decouples "commit my state" from "publish my event" without distributed
// transactions: the enqueue happens inside the caller's transaction, and a relay
// polls undelivered records and publishes them with at-least-once semantics,
// visibility timeouts, bounded retries and dead-lettering.
package outbox

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrClosed is returned by a store once it has been closed.
	ErrClosed = errors.New("outbox: store closed")
	// ErrEmptyTopic is returned when enqueuing a message without a topic.
	ErrEmptyTopic = errors.New("outbox: message topic is empty")
)

// Status is the lifecycle state of an outbox record.
type Status string

const (
	// StatusPending marks a record awaiting delivery.
	StatusPending Status = "pending"
	// StatusDelivered marks a record successfully published.
	StatusDelivered Status = "delivered"
	// StatusDead marks a record that exhausted its delivery attempts.
	StatusDead Status = "dead"
)

// Message is the payload enqueued into the outbox. It mirrors the fields a
// broker needs so the relay can forward it without transformation.
type Message struct {
	Topic   string
	Key     string
	Body    []byte
	Headers map[string]string
}

// Record is a stored outbox entry tracking delivery state for a Message.
type Record struct {
	ID          string
	Message     Message
	Status      Status
	Attempts    int
	CreatedAt   time.Time
	AvailableAt time.Time
	DeliveredAt time.Time
	LastError   string
}

// Store persists outbox records. Implementations must make Enqueue participate
// in the caller's transaction (see SQLStore) so business state and the emitted
// message commit atomically.
type Store interface {
	// Fetch claims up to limit pending records that are due for delivery,
	// leasing them for the given visibility timeout so concurrent relays do not
	// pick the same records. Returned records have their Attempts incremented.
	Fetch(ctx context.Context, limit int, visibility time.Duration) ([]Record, error)
	// MarkDelivered marks a leased record as successfully delivered.
	MarkDelivered(ctx context.Context, id string) error
	// Retry reschedules a leased record for a later attempt with the given error.
	Retry(ctx context.Context, id string, availableAt time.Time, lastErr string) error
	// MarkDead moves a leased record to the dead state after exhausting retries.
	MarkDead(ctx context.Context, id string, lastErr string) error
}

func validateMessage(msg Message) error {
	if msg.Topic == "" {
		return ErrEmptyTopic
	}
	return nil
}

func cloneMessage(msg Message) Message {
	msg.Body = append([]byte(nil), msg.Body...)
	if len(msg.Headers) > 0 {
		headers := make(map[string]string, len(msg.Headers))
		for k, v := range msg.Headers {
			headers[k] = v
		}
		msg.Headers = headers
	}
	return msg
}
