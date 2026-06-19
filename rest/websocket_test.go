package rest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
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
