// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/gofly/gofly/core/metadata"
)

// Codec marshals and unmarshals RPC payloads.
type Codec interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONCodec marshals values as JSON.
type JSONCodec struct{}

// Name returns "json".
func (JSONCodec) Name() string { return "json" }

// Marshal encodes v as JSON.
func (JSONCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON into v.
func (JSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// ProtoCodec marshals values as Protocol Buffers.
type ProtoCodec struct{}

// Name returns "proto".
func (ProtoCodec) Name() string { return "proto" }

// Marshal encodes v as protobuf.
func (ProtoCodec) Marshal(v any) ([]byte, error) {
	msg, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("proto codec requires proto.Message, got %T", v)
	}
	return proto.Marshal(msg)
}

// Unmarshal decodes protobuf into v.
func (ProtoCodec) Unmarshal(data []byte, v any) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("proto codec requires proto.Message, got %T", v)
	}
	return proto.Unmarshal(data, msg)
}

type requestEnvelope struct {
	Payload      json.RawMessage `json:"payload,omitempty"`
	PayloadBytes []byte          `json:"payloadBytes,omitempty"`
	Codec        string          `json:"codec,omitempty"`
	Metadata     metadata.MD     `json:"metadata,omitempty"`
}

type responseEnvelope struct {
	Payload      any         `json:"payload,omitempty"`
	PayloadBytes []byte      `json:"payloadBytes,omitempty"`
	Codec        string      `json:"codec,omitempty"`
	Metadata     metadata.MD `json:"metadata,omitempty"`
	Code         Code        `json:"code,omitempty"`
	Error        string      `json:"error,omitempty"`
}
