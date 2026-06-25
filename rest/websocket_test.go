package rest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWebSocketHandshakeEchoAndStats(t *testing.T) {
	manager := NewWebSocketManager()
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ws", Handler: func(ctx *Context) {
		_ = ctx.WebSocket(func(_ context.Context, conn *WebSocketConn) {
			for {
				messageType, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				_ = conn.WriteMessage(messageType, payload)
			}
		}, WithWebSocketManager(manager))
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	conn, rw := dialWebSocket(t, ts.URL, "/ws")
	defer conn.Close()
	writeClientFrame(t, rw, WebSocketTextMessage, []byte("hello"))
	messageType, payload := readServerFrame(t, rw)
	if messageType != WebSocketTextMessage || string(payload) != "hello" {
		t.Fatalf("echo frame = type %d payload %q", messageType, payload)
	}
	writeClientFrame(t, rw, WebSocketCloseMessage, nil)
	_ = conn.Close()
	waitWebSocket(t, time.Second, func() bool { return manager.Snapshot().Active == 0 })
	snapshot := manager.Snapshot()
	if snapshot.Accepted != 1 || snapshot.Closed != 1 || snapshot.MessagesIn != 1 || snapshot.MessagesOut != 1 || snapshot.BytesIn != 5 || snapshot.BytesOut != 5 {
		t.Fatalf("unexpected websocket snapshot: %+v", snapshot)
	}
}

func TestWebSocketRejectsInvalidHandshake(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ws", Handler: func(ctx *Context) {
		_ = ctx.WebSocket(func(_ context.Context, conn *WebSocketConn) {})
	}})
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWebSocketMaxMessageBytesClosesConnection(t *testing.T) {
	manager := NewWebSocketManager()
	done := make(chan struct{})
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ws", Handler: func(ctx *Context) {
		_ = ctx.WebSocket(func(_ context.Context, conn *WebSocketConn) {
			_, _, _ = conn.ReadMessage()
			close(done)
		}, WithWebSocketManager(manager), WithWebSocketMaxMessageBytes(4))
	}})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	conn, rw := dialWebSocket(t, ts.URL, "/ws")
	defer conn.Close()
	writeClientFrame(t, rw, WebSocketTextMessage, []byte("too-large"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for size-limit handler exit")
	}
	waitWebSocket(t, time.Second, func() bool { return manager.Snapshot().ProtocolErrs == 1 && manager.Snapshot().Active == 0 })
}

func TestWebSocketReadFrameRejectsOverflowLength(t *testing.T) {
	var frame bytes.Buffer
	frame.WriteByte(0x80 | WebSocketTextMessage)
	frame.WriteByte(0x80 | 127)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(math.MaxInt)+1)
	frame.Write(length[:])

	conn := &WebSocketConn{
		rw:              bufio.NewReadWriter(bufio.NewReader(&frame), bufio.NewWriter(io.Discard)),
		maxMessageBytes: math.MaxInt64,
	}
	_, _, err := conn.readFrame()
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("readFrame overflow length error = %v, want size rejection", err)
	}
}

func TestWebSocketReadFrameBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		messageType int
		payload     []byte
	}{
		{name: "small", messageType: WebSocketTextMessage, payload: []byte("hello")},
		{name: "uint16 length", messageType: WebSocketBinaryMessage, payload: bytes.Repeat([]byte("a"), 126)},
		{name: "uint64 length", messageType: WebSocketTextMessage, payload: bytes.Repeat([]byte("b"), 66000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := maskedClientFrame(tt.messageType, tt.payload)
			conn := &WebSocketConn{rw: bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(frame)), bufio.NewWriter(io.Discard)), maxMessageBytes: int64(len(tt.payload) + 1)}
			messageType, payload, err := conn.readFrame()
			if err != nil {
				t.Fatalf("readFrame error = %v", err)
			}
			if messageType != tt.messageType || !bytes.Equal(payload, tt.payload) {
				t.Fatalf("readFrame = type %d len %d, want type %d len %d", messageType, len(payload), tt.messageType, len(tt.payload))
			}
		})
	}
}

func TestWebSocketReadFrameRejectsInvalidAndTruncatedFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame []byte
		max   int64
		want  string
	}{
		{name: "truncated header", frame: []byte{0x81}, max: 2, want: "EOF"},
		{name: "fragmented frame", frame: []byte{WebSocketTextMessage, 0x80, 1, 2, 3, 4}, max: 2, want: "invalid websocket frame flags"},
		{name: "unmasked frame", frame: []byte{0x80 | WebSocketTextMessage, 0}, max: 2, want: "invalid websocket frame flags"},
		{name: "truncated uint16 length", frame: []byte{0x80 | WebSocketTextMessage, 0x80 | 126, 0}, max: 2, want: "EOF"},
		{name: "truncated uint64 length", frame: []byte{0x80 | WebSocketTextMessage, 0x80 | 127, 0, 0}, max: 2, want: "EOF"},
		{name: "max message disabled", frame: maskedClientFrame(WebSocketTextMessage, []byte("x")), max: 0, want: "exceeds maximum size"},
		{name: "truncated mask", frame: []byte{0x80 | WebSocketTextMessage, 0x80 | 1, 1, 2}, max: 2, want: "EOF"},
		{name: "truncated payload", frame: []byte{0x80 | WebSocketTextMessage, 0x80 | 3, 1, 2, 3, 4, 'a'}, max: 4, want: "EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &WebSocketConn{rw: bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(tt.frame)), bufio.NewWriter(io.Discard)), maxMessageBytes: tt.max}
			_, _, err := conn.readFrame()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("readFrame error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

type failingWebSocketWriter struct {
	err error
}

func (w failingWebSocketWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteWebSocketFrameBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		prefix  []byte
	}{
		{name: "small", payload: []byte("hello"), prefix: []byte{0x80 | WebSocketTextMessage, 5}},
		{name: "uint16 length", payload: bytes.Repeat([]byte("a"), 126), prefix: []byte{0x80 | WebSocketTextMessage, 126, 0, 126}},
		{name: "uint64 length", payload: bytes.Repeat([]byte("b"), 66000), prefix: []byte{0x80 | WebSocketTextMessage, 127, 0, 0, 0, 0, 0, 1, 1, 208}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			rw := bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriter(&out))
			if err := writeWebSocketFrame(rw, WebSocketTextMessage, tt.payload); err != nil {
				t.Fatalf("writeWebSocketFrame error = %v", err)
			}
			got := out.Bytes()
			if !bytes.HasPrefix(got, tt.prefix) || !bytes.Equal(got[len(got)-len(tt.payload):], tt.payload) {
				t.Fatalf("frame prefix/payload mismatch: prefix=%v len=%d", got[:min(len(got), len(tt.prefix))], len(got))
			}
		})
	}

	if err := writeWebSocketFrame(bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriter(io.Discard)), math.MaxUint8+1, nil); err == nil {
		t.Fatal("writeWebSocketFrame accepted invalid message type")
	}
	wantErr := errors.New("flush failed")
	err := writeWebSocketFrame(bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriterSize(failingWebSocketWriter{err: wantErr}, 1)), WebSocketTextMessage, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeWebSocketFrame writer error = %v, want flush error", err)
	}
}

func maskedClientFrame(messageType int, payload []byte) []byte {
	var frame bytes.Buffer
	frame.WriteByte(0x80 | byte(messageType))
	length := len(payload)
	switch {
	case length < 126:
		frame.WriteByte(0x80 | byte(length))
	case length <= 65535:
		frame.WriteByte(0x80 | 126)
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(length))
		frame.Write(buf[:])
	default:
		frame.WriteByte(0x80 | 127)
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(length))
		frame.Write(buf[:])
	}
	mask := [4]byte{1, 2, 3, 4}
	frame.Write(mask[:])
	for i, b := range payload {
		frame.WriteByte(b ^ mask[i%4])
	}
	return frame.Bytes()
}

func dialWebSocket(t *testing.T, serverURL, path string) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	key := base64.StdEncoding.EncodeToString([]byte("gofly-websocket1"))
	_, _ = rw.WriteString("GET " + path + " HTTP/1.1\r\n")
	_, _ = rw.WriteString("Host: " + u.Host + "\r\n")
	_, _ = rw.WriteString("Upgrade: websocket\r\n")
	_, _ = rw.WriteString("Connection: Upgrade\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Version: 13\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Key: " + key + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
	status, err := rw.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status = %q", status)
	}
	wantAccept := testWebSocketAccept(key)
	foundAccept := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		if strings.EqualFold(strings.TrimSpace(line), "Sec-WebSocket-Accept: "+wantAccept) {
			foundAccept = true
		}
	}
	if !foundAccept {
		t.Fatal("missing Sec-WebSocket-Accept header")
	}
	return conn, rw
}

func writeClientFrame(t *testing.T, rw *bufio.ReadWriter, messageType int, payload []byte) {
	t.Helper()
	if err := rw.WriteByte(0x80 | byte(messageType)); err != nil {
		t.Fatal(err)
	}
	mask := [4]byte{1, 2, 3, 4}
	length := len(payload)
	switch {
	case length < 126:
		_ = rw.WriteByte(0x80 | byte(length))
	case length <= 65535:
		_ = rw.WriteByte(0x80 | 126)
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(length))
		_, _ = rw.Write(buf[:])
	default:
		t.Fatal("test frame too large")
	}
	_, _ = rw.Write(mask[:])
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	_, _ = rw.Write(masked)
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
}

func readServerFrame(t *testing.T, rw *bufio.ReadWriter) (int, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(rw, header); err != nil {
		t.Fatal(err)
	}
	messageType := int(header[0] & 0x0f)
	length := int(header[1] & 0x7f)
	if length == 126 {
		var buf [2]byte
		_, _ = io.ReadFull(rw, buf[:])
		length = int(binary.BigEndian.Uint16(buf[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(rw, payload); err != nil {
		t.Fatal(err)
	}
	return messageType, payload
}

func testWebSocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + webSocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func waitWebSocket(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
