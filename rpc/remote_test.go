package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/gofly/gofly/core/metadata"
)

func TestFramedTransportSendReceive(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewFramedTransport(clientConn)
	server := NewFramedTransport(serverConn)
	recv := make(chan struct {
		msg Message
		err error
	}, 1)
	go func() {
		msg, err := server.Receive(context.Background())
		recv <- struct {
			msg Message
			err error
		}{msg: msg, err: err}
	}()

	meta := metadata.MD{"trace": "abc"}
	if err := client.Send(context.Background(), Message{Service: "greeter", Method: "SayHello", Payload: []byte("hello"), Meta: meta}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := <-recv
	if got.err != nil {
		t.Fatalf("Receive: %v", got.err)
	}
	if got.msg.Service != "greeter" || got.msg.Method != "SayHello" || string(got.msg.Payload) != "hello" || got.msg.Codec != "identity" {
		t.Fatalf("message = %#v, want decoded identity greeter message", got.msg)
	}
	if !reflect.DeepEqual(got.msg.Meta, meta) {
		t.Fatalf("metadata = %#v, want %#v", got.msg.Meta, meta)
	}
	if stats := client.Snapshot(); stats.FramesOut != 1 || stats.BytesOut == 0 {
		t.Fatalf("client stats = %#v, want one outbound frame", stats)
	}
	if stats := server.Snapshot(); stats.FramesIn != 1 || stats.BytesIn == 0 {
		t.Fatalf("server stats = %#v, want one inbound frame", stats)
	}
}

func TestFramedTransportGzipPayload(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewFramedTransport(clientConn, WithPayloadCodec(GzipPayloadCodec{}))
	server := NewFramedTransport(serverConn, WithPayloadCodec(GzipPayloadCodec{}))
	recv := make(chan struct {
		msg Message
		err error
	}, 1)
	go func() {
		msg, err := server.Receive(context.Background())
		recv <- struct {
			msg Message
			err error
		}{msg: msg, err: err}
	}()

	payload := []byte("gofly-gzip-payload-gofly-gzip-payload")
	if err := client.Send(context.Background(), Message{Payload: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := <-recv
	if got.err != nil {
		t.Fatalf("Receive: %v", got.err)
	}
	if got.msg.Codec != "gzip" || string(got.msg.Payload) != string(payload) {
		t.Fatalf("message = %#v, want gzip decoded payload %q", got.msg, payload)
	}
}

func TestFramedTransportBinaryFrames(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewFramedTransport(clientConn, WithBinaryFrames())
	server := NewFramedTransport(serverConn, WithBinaryFrames())
	recv := receiveAsync(server)

	meta := metadata.MD{"trace": "abc", "tenant": "gofly"}
	if err := client.Send(context.Background(), Message{Service: "greeter", Method: "SayHello", Payload: []byte("hello"), Meta: meta, Code: CodeOK}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := <-recv
	if got.err != nil {
		t.Fatalf("Receive: %v", got.err)
	}
	if got.msg.Service != "greeter" || got.msg.Method != "SayHello" || got.msg.Codec != "identity" || got.msg.Code != CodeOK || string(got.msg.Payload) != "hello" {
		t.Fatalf("message = %#v, want decoded binary greeter message", got.msg)
	}
	if !reflect.DeepEqual(got.msg.Meta, meta) {
		t.Fatalf("metadata = %#v, want %#v", got.msg.Meta, meta)
	}
}

func TestBinaryFrameCodecRejectsMalformedFrames(t *testing.T) {
	codec := BinaryFrameCodec{}
	if (JSONFrameCodec{}).Name() != "json" || codec.Name() != "binary" {
		t.Fatalf("codec names = %q/%q, want json/binary", (JSONFrameCodec{}).Name(), codec.Name())
	}
	if _, err := codec.Unmarshal([]byte{2}); err == nil {
		t.Fatal("unsupported version error is nil")
	}
	if _, err := codec.Unmarshal([]byte{1, 3, 'a'}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame error = %v, want unexpected EOF", err)
	}
}

func TestBinaryFrameCodecRejectsOverflowLengths(t *testing.T) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], ^uint64(0))
	if _, err := readFrameBytes(bytes.NewReader(buf[:n])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readFrameBytes overflow length error = %v, want unexpected EOF", err)
	}
	if _, err := readFrameMetadata(bytes.NewReader(buf[:n])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readFrameMetadata overflow count error = %v, want unexpected EOF", err)
	}
}

func TestFramedTransportFrameLimitsAndCancellation(t *testing.T) {
	t.Run("nil options fall back to defaults", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		transport := NewFramedTransport(clientConn, WithPayloadCodec(nil), WithFrameCodec(nil), WithMaxFrameBytes(-1))
		if transport.payload.Name() != "identity" || transport.frame.Name() != "json" || transport.maxFrame != DefaultMaxFrameBytes {
			t.Fatalf("transport defaults = payload %q frame %q max %d", transport.payload.Name(), transport.frame.Name(), transport.maxFrame)
		}
	})

	t.Run("send rejects oversized encoded frame", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		client := NewFramedTransport(clientConn, WithMaxFrameBytes(8))
		if err := client.Send(context.Background(), Message{Payload: []byte("too large")}); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("Send error = %v, want ErrFrameTooLarge", err)
		}
	})

	t.Run("receive rejects oversized declared frame", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		server := NewFramedTransport(serverConn, WithMaxFrameBytes(4))
		go func() {
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], 5)
			_, _ = clientConn.Write(header[:])
			_, _ = clientConn.Write([]byte("12345"))
		}()
		if _, err := server.Receive(context.Background()); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("Receive error = %v, want ErrFrameTooLarge", err)
		}
	})

	t.Run("canceled context fails before io", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		transport := NewFramedTransport(clientConn)
		if err := transport.Send(ctx, Message{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Send canceled error = %v, want context.Canceled", err)
		}
		if _, err := transport.Receive(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Receive canceled error = %v, want context.Canceled", err)
		}
	})
}

func TestServeFramedMiddlewareAndErrorEnvelope(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewFramedTransport(clientConn)
	server := NewFramedTransport(serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeFramed(ctx, server, func(ctx context.Context, msg Message) (Message, error) {
			if msg.Meta.Get("mw") != "on" {
				return Message{}, NewError(CodeInvalidArgument, "middleware missing")
			}
			return Message{}, NewError(CodeInvalidArgument, "bad framed request")
		}, func(next MessageHandler) MessageHandler {
			return func(ctx context.Context, msg Message) (Message, error) {
				if msg.Meta == nil {
					msg.Meta = metadata.MD{}
				}
				msg.Meta["mw"] = "on"
				return next(ctx, msg)
			}
		})
	}()

	if err := client.Send(context.Background(), Message{Payload: []byte("request")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := client.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if resp.Code != CodeInvalidArgument || resp.Error != "bad framed request" {
		t.Fatalf("response = %#v, want invalid argument error envelope", resp)
	}
	cancel()
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeFramed did not stop after context cancellation and close")
	}
}

func TestServeFramedNilHandlerAndTransportNilReceivers(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	if err := ServeFramed(context.Background(), NewFramedTransport(serverConn), nil); err == nil {
		t.Fatal("ServeFramed nil handler succeeded, want error")
	}
	var transport *FramedTransport
	if err := transport.Close(); err != nil {
		t.Fatalf("nil transport Close error = %v, want nil", err)
	}
	if stats := transport.Snapshot(); stats != (TransportStats{}) {
		t.Fatalf("nil transport stats = %#v, want zero", stats)
	}
}

func TestDialFramedSuccessAndError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
		close(accepted)
	}()

	transport, err := DialFramed(context.Background(), "tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("DialFramed success: %v", err)
	}
	defer transport.Close()
	if conn := <-accepted; conn != nil {
		defer conn.Close()
	} else {
		t.Fatal("listener did not accept DialFramed connection")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DialFramed(ctx, "tcp", listener.Addr().String(), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("DialFramed canceled error = %v, want context.Canceled", err)
	}
}

func TestFramedTransportClearsStickyDeadlines(t *testing.T) {
	t.Run("send clears expired write deadline when context has no deadline", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		client := NewFramedTransport(clientConn)
		server := NewFramedTransport(serverConn)
		first := receiveAsync(server)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(20*time.Millisecond))
		defer cancel()
		if err := client.Send(ctx, Message{Payload: []byte("first")}); err != nil {
			t.Fatalf("first Send: %v", err)
		}
		if got := <-first; got.err != nil || string(got.msg.Payload) != "first" {
			t.Fatalf("first Receive = %#v, want first payload", got)
		}
		time.Sleep(30 * time.Millisecond)

		second := receiveAsync(server)
		if err := client.Send(context.Background(), Message{Payload: []byte("second")}); err != nil {
			t.Fatalf("second Send after expired prior deadline = %v, want success", err)
		}
		if got := <-second; got.err != nil || string(got.msg.Payload) != "second" {
			t.Fatalf("second Receive = %#v, want second payload", got)
		}
	})

	t.Run("receive clears expired read deadline when context has no deadline", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()

		client := NewFramedTransport(clientConn)
		server := NewFramedTransport(serverConn)
		go func() { _ = client.Send(context.Background(), Message{Payload: []byte("first")}) }()
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(20*time.Millisecond))
		defer cancel()
		msg, err := server.Receive(ctx)
		if err != nil || string(msg.Payload) != "first" {
			t.Fatalf("first Receive = %#v, %v; want first payload", msg, err)
		}
		time.Sleep(30 * time.Millisecond)

		go func() { _ = client.Send(context.Background(), Message{Payload: []byte("second")}) }()
		msg, err = server.Receive(context.Background())
		if err != nil || string(msg.Payload) != "second" {
			t.Fatalf("second Receive after expired prior deadline = %#v, %v; want second payload", msg, err)
		}
	})
}

type receivedMessage struct {
	msg Message
	err error
}

func receiveAsync(t *FramedTransport) <-chan receivedMessage {
	ch := make(chan receivedMessage, 1)
	go func() {
		msg, err := t.Receive(context.Background())
		ch <- receivedMessage{msg: msg, err: err}
	}()
	return ch
}

func BenchmarkFrameCodecRoundTrip(b *testing.B) {
	msg := Message{
		Service: "greeter",
		Method:  "SayHello",
		Codec:   "identity",
		Payload: []byte(`{"name":"gofly","message":"hello framed transport"}`),
		Meta:    metadata.MD{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "tenant": "gofly"},
		Code:    CodeOK,
	}
	benchmarks := []struct {
		name  string
		codec FrameCodec
	}{
		{name: "json", codec: JSONFrameCodec{}},
		{name: "binary", codec: BinaryFrameCodec{}},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			var out Message
			for b.Loop() {
				data, err := bm.codec.Marshal(msg)
				if err != nil {
					b.Fatal(err)
				}
				out, err = bm.codec.Unmarshal(data)
				if err != nil {
					b.Fatal(err)
				}
			}
			if out.Service != msg.Service || out.Method != msg.Method || string(out.Payload) != string(msg.Payload) {
				b.Fatalf("round trip = %#v, want %s/%s", out, msg.Service, msg.Method)
			}
		})
	}
}
