// Package redisstream implements the mq.Driver interface using Redis Streams.
package redisstream

import (
	"strconv"
	"time"

	"github.com/gofly/gofly/core/kv/redis"
	"github.com/gofly/gofly/core/mq"
)

// Field names used to serialize an mq.Message into a stream entry. Headers are
// stored with a "h:" prefix to keep them separate from the envelope fields.
const (
	fieldID          = "id"
	fieldKey         = "key"
	fieldBody        = "body"
	fieldAttempts    = "attempts"
	fieldPublishedAt = "ts"
	headerPrefix     = "h:"
)

// encode flattens a message into Redis stream fields.
func encode(msg mq.Message) map[string]string {
	fields := make(map[string]string, 5+len(msg.Headers))
	fields[fieldID] = msg.ID
	if msg.Key != "" {
		fields[fieldKey] = msg.Key
	}
	fields[fieldBody] = string(msg.Body)
	fields[fieldAttempts] = strconv.Itoa(msg.Attempts)
	if !msg.PublishedAt.IsZero() {
		fields[fieldPublishedAt] = strconv.FormatInt(msg.PublishedAt.UnixNano(), 10)
	}
	for k, v := range msg.Headers {
		fields[headerPrefix+k] = v
	}
	return fields
}

// decode reconstructs a message from a stream entry.
func decode(entry redis.StreamEntry) mq.Message {
	msg := mq.Message{ID: entry.Fields[fieldID], Key: entry.Fields[fieldKey]}
	if body, ok := entry.Fields[fieldBody]; ok {
		msg.Body = []byte(body)
	}
	if a, err := strconv.Atoi(entry.Fields[fieldAttempts]); err == nil {
		msg.Attempts = a
	}
	if ts, err := strconv.ParseInt(entry.Fields[fieldPublishedAt], 10, 64); err == nil {
		msg.PublishedAt = time.Unix(0, ts)
	}
	for k, v := range entry.Fields {
		if len(k) > len(headerPrefix) && k[:len(headerPrefix)] == headerPrefix {
			if msg.Headers == nil {
				msg.Headers = make(map[string]string)
			}
			msg.Headers[k[len(headerPrefix):]] = v
		}
	}
	if msg.ID == "" {
		msg.ID = entry.ID
	}
	return msg
}
