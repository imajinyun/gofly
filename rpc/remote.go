// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync/atomic"
	"time"

	core "github.com/gofly/gofly/core"
	"github.com/gofly/gofly/core/metadata"
)

const DefaultMaxFrameBytes int64 = 4 << 20

var ErrFrameTooLarge = errors.New("rpc frame exceeds maximum size")

type Message struct {
	Service string      `json:"service,omitempty"`
	Method  string      `json:"method,omitempty"`
	Codec   string      `json:"codec,omitempty"`
	Payload []byte      `json:"payload,omitempty"`
	Meta    metadata.MD `json:"metadata,omitempty"`
	Code    Code        `json:"code,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type MessageHandler func(context.Context, Message) (Message, error)

type MessageMiddleware func(MessageHandler) MessageHandler

type PayloadCodec interface {
	Name() string
	Encode([]byte) ([]byte, error)
	Decode([]byte) ([]byte, error)
}

type FrameCodec interface {
	Name() string
	Marshal(Message) ([]byte, error)
	Unmarshal([]byte) (Message, error)
}

type JSONFrameCodec struct{}

func (JSONFrameCodec) Name() string { return "json" }

func (JSONFrameCodec) Marshal(msg Message) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc frame: %w", err)
	}
	return data, nil
}

func (JSONFrameCodec) Unmarshal(data []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, fmt.Errorf("unmarshal rpc frame: %w", err)
	}
	return msg, nil
}

type BinaryFrameCodec struct{}

func (BinaryFrameCodec) Name() string { return "binary" }

func (BinaryFrameCodec) Marshal(msg Message) ([]byte, error) {
	var b bytes.Buffer
	b.Grow(binaryFrameSizeHint(msg))
	b.WriteByte(1)
	writeFrameString(&b, msg.Service)
	writeFrameString(&b, msg.Method)
	writeFrameString(&b, msg.Codec)
	writeFrameBytes(&b, msg.Payload)
	writeFrameMetadata(&b, msg.Meta)
	writeFrameString(&b, string(msg.Code))
	writeFrameString(&b, msg.Error)
	return b.Bytes(), nil
}

func (BinaryFrameCodec) Unmarshal(data []byte) (Message, error) {
	r := bytes.NewReader(data)
	version, err := r.ReadByte()
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc frame version: %w", err)
	}
	if version != 1 {
		return Message{}, fmt.Errorf("unsupported binary rpc frame version %d", version)
	}
	service, err := readFrameString(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc service: %w", err)
	}
	method, err := readFrameString(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc method: %w", err)
	}
	codec, err := readFrameString(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc codec: %w", err)
	}
	payload, err := readFrameBytes(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc payload: %w", err)
	}
	meta, err := readFrameMetadata(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc metadata: %w", err)
	}
	code, err := readFrameString(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc code: %w", err)
	}
	message, err := readFrameString(r)
	if err != nil {
		return Message{}, fmt.Errorf("read binary rpc error: %w", err)
	}
	if r.Len() != 0 {
		return Message{}, fmt.Errorf("binary rpc frame has %d trailing bytes", r.Len())
	}
	return Message{Service: service, Method: method, Codec: codec, Payload: payload, Meta: meta, Code: Code(code), Error: message}, nil
}

type NoopPayloadCodec struct{}

func (NoopPayloadCodec) Name() string { return "identity" }
func (NoopPayloadCodec) Encode(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}
func (NoopPayloadCodec) Decode(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

type GzipPayloadCodec struct{}

func (GzipPayloadCodec) Name() string { return "gzip" }
func (GzipPayloadCodec) Encode(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		_ = w.Close() // best-effort cleanup after write failure
		return nil, fmt.Errorf("gzip encode payload: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip close encoder: %w", err)
	}
	return b.Bytes(), nil
}
func (GzipPayloadCodec) Decode(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip decode payload: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read gzip payload: %w", err)
	}
	return out, nil
}

func binaryFrameSizeHint(msg Message) int {
	size := 1 + len(msg.Service) + len(msg.Method) + len(msg.Codec) + len(msg.Payload) + len(msg.Code) + len(msg.Error) + 32
	for key, value := range msg.Meta {
		size += len(key) + len(value) + 2
	}
	return size
}

func writeFrameString(b *bytes.Buffer, value string) {
	writeFrameBytes(b, []byte(value))
}

func writeFrameBytes(b *bytes.Buffer, value []byte) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(value)))
	b.Write(buf[:n])
	b.Write(value)
}

func writeFrameMetadata(b *bytes.Buffer, md metadata.MD) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(md)))
	b.Write(buf[:n])
	for key, value := range md {
		writeFrameString(b, key)
		writeFrameString(b, value)
	}
}

func readFrameString(r *bytes.Reader) (string, error) {
	value, err := readFrameBytes(r)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func readFrameBytes(r *bytes.Reader) ([]byte, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if length > uint64(math.MaxInt) || int(length) > r.Len() {
		return nil, io.ErrUnexpectedEOF
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, err
	}
	return value, nil
}

func readFrameMetadata(r *bytes.Reader) (metadata.MD, error) {
	count, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	if count > uint64(math.MaxInt) || int(count) > r.Len()/2+1 {
		return nil, io.ErrUnexpectedEOF
	}
	md := make(metadata.MD, int(count))
	for i := uint64(0); i < count; i++ {
		key, err := readFrameString(r)
		if err != nil {
			return nil, err
		}
		value, err := readFrameString(r)
		if err != nil {
			return nil, err
		}
		md[key] = value
	}
	return md, nil
}

type FramedTransport struct {
	conn       net.Conn
	maxFrame   int64
	payload    PayloadCodec
	frame      FrameCodec
	readCount  atomic.Int64
	writeCount atomic.Int64
	bytesIn    atomic.Int64
	bytesOut   atomic.Int64
}

type TransportStats struct {
	FramesIn  int64 `json:"framesIn"`
	FramesOut int64 `json:"framesOut"`
	BytesIn   int64 `json:"bytesIn"`
	BytesOut  int64 `json:"bytesOut"`
}

type FramedTransportOption func(*FramedTransport)

func NewFramedTransport(conn net.Conn, opts ...FramedTransportOption) *FramedTransport {
	t := &FramedTransport{conn: conn, maxFrame: DefaultMaxFrameBytes, payload: NoopPayloadCodec{}, frame: JSONFrameCodec{}}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.payload == nil {
		t.payload = NoopPayloadCodec{}
	}
	if t.frame == nil {
		t.frame = JSONFrameCodec{}
	}
	return t
}

func WithMaxFrameBytes(max int64) FramedTransportOption {
	return func(t *FramedTransport) {
		if max > 0 {
			t.maxFrame = max
		}
	}
}

func WithPayloadCodec(codec PayloadCodec) FramedTransportOption {
	return func(t *FramedTransport) {
		if codec != nil {
			t.payload = codec
		}
	}
}

func WithFrameCodec(codec FrameCodec) FramedTransportOption {
	return func(t *FramedTransport) {
		if codec != nil {
			t.frame = codec
		}
	}
}

func WithBinaryFrames() FramedTransportOption {
	return WithFrameCodec(BinaryFrameCodec{})
}

func (t *FramedTransport) Send(ctx context.Context, msg Message) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := t.payload.Encode(msg.Payload)
	if err != nil {
		return err
	}
	msg.Payload = encoded
	if msg.Codec == "" {
		msg.Codec = t.payload.Name()
	}
	data, err := t.frame.Marshal(msg)
	if err != nil {
		return err
	}
	if int64(len(data)) > t.maxFrame || len(data) > math.MaxUint32 {
		return ErrFrameTooLarge
	}
	var header [4]byte
	// #nosec G115 -- len(data) is checked against math.MaxUint32 immediately above.
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetWriteDeadline(deadline)
	} else {
		_ = t.conn.SetWriteDeadline(time.Time{})
	}
	if _, err := t.conn.Write(header[:]); err != nil {
		return fmt.Errorf("write rpc frame header: %w", err)
	}
	if _, err := t.conn.Write(data); err != nil {
		return fmt.Errorf("write rpc frame body: %w", err)
	}
	t.writeCount.Add(1)
	t.bytesOut.Add(int64(4 + len(data)))
	return nil
}

func (t *FramedTransport) Receive(ctx context.Context) (Message, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetReadDeadline(deadline)
	} else {
		_ = t.conn.SetReadDeadline(time.Time{})
	}
	var header [4]byte
	if _, err := io.ReadFull(t.conn, header[:]); err != nil {
		return Message{}, fmt.Errorf("read rpc frame header: %w", err)
	}
	length := int64(binary.BigEndian.Uint32(header[:]))
	if length <= 0 || length > t.maxFrame {
		return Message{}, ErrFrameTooLarge
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(t.conn, data); err != nil {
		return Message{}, fmt.Errorf("read rpc frame body: %w", err)
	}
	msg, err := t.frame.Unmarshal(data)
	if err != nil {
		return Message{}, err
	}
	if msg.Codec == t.payload.Name() {
		decoded, err := t.payload.Decode(msg.Payload)
		if err != nil {
			return Message{}, err
		}
		msg.Payload = decoded
	}
	t.readCount.Add(1)
	t.bytesIn.Add(4 + length)
	return msg, nil
}

func (t *FramedTransport) Close() error {
	if t == nil || t.conn == nil {
		return nil
	}
	return t.conn.Close()
}

func (t *FramedTransport) Snapshot() TransportStats {
	if t == nil {
		return TransportStats{}
	}
	return TransportStats{FramesIn: t.readCount.Load(), FramesOut: t.writeCount.Load(), BytesIn: t.bytesIn.Load(), BytesOut: t.bytesOut.Load()}
}

func ChainMessageMiddleware(mws ...MessageMiddleware) MessageMiddleware {
	return func(next MessageHandler) MessageHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

func ServeFramed(ctx context.Context, transport *FramedTransport, handler MessageHandler, mws ...MessageMiddleware) error {
	ctx = core.Context(ctx)
	if handler == nil {
		return errors.New("rpc message handler is nil")
	}
	h := ChainMessageMiddleware(mws...)(handler)
	for {
		msg, err := transport.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		resp, err := h(ctx, msg)
		if err != nil {
			resp = Message{Code: CodeOf(err), Error: textOf(err)}
		}
		if err := transport.Send(ctx, resp); err != nil {
			return err
		}
	}
}

func DialFramed(ctx context.Context, network, address string, timeout time.Duration, opts ...FramedTransportOption) (*FramedTransport, error) {
	ctx = core.Context(ctx)
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("dial framed rpc transport: %w", err)
	}
	return NewFramedTransport(conn, opts...), nil
}
