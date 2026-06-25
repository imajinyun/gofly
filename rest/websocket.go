// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505 -- RFC 6455 requires SHA-1 for Sec-WebSocket-Accept; this is not password hashing or signature verification.
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// WebSocket message type constants.
const (
	WebSocketTextMessage   = 1
	WebSocketBinaryMessage = 2
	WebSocketCloseMessage  = 8
	WebSocketPingMessage   = 9
	WebSocketPongMessage   = 10

	webSocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

var (
	// ErrWebSocketUpgrade is returned when the HTTP upgrade fails.
	ErrWebSocketUpgrade = errors.New("websocket upgrade failed")
	// ErrWebSocketClosed is returned when writing to a closed connection.
	ErrWebSocketClosed = errors.New("websocket connection closed")

	// DefaultWebSocketManager is the global default WebSocket manager.
	DefaultWebSocketManager = NewWebSocketManager()
)

// WebSocketHandler handles an upgraded WebSocket connection.
type WebSocketHandler func(context.Context, *WebSocketConn)

type WebSocketOption func(*webSocketOptions)

type webSocketOptions struct {
	maxMessageBytes int64
	readTimeout     time.Duration
	writeTimeout    time.Duration
	manager         *WebSocketManager
}

type WebSocketStats struct {
	Accepted     int64 `json:"accepted"`
	Active       int64 `json:"active"`
	Closed       int64 `json:"closed"`
	MessagesIn   int64 `json:"messagesIn"`
	MessagesOut  int64 `json:"messagesOut"`
	BytesIn      int64 `json:"bytesIn"`
	BytesOut     int64 `json:"bytesOut"`
	ProtocolErrs int64 `json:"protocolErrors"`
}

type WebSocketManager struct {
	accepted     atomic.Int64
	active       atomic.Int64
	closed       atomic.Int64
	messagesIn   atomic.Int64
	messagesOut  atomic.Int64
	bytesIn      atomic.Int64
	bytesOut     atomic.Int64
	protocolErrs atomic.Int64
}

type WebSocketConn struct {
	conn            net.Conn
	rw              *bufio.ReadWriter
	maxMessageBytes int64
	writeTimeout    time.Duration
	manager         *WebSocketManager
	writeMu         sync.Mutex
	closeOnce       sync.Once
	closed          chan struct{}
}

func NewWebSocketManager() *WebSocketManager { return &WebSocketManager{} }

func WithWebSocketMaxMessageBytes(n int64) WebSocketOption {
	return func(opts *webSocketOptions) {
		if n > 0 {
			opts.maxMessageBytes = n
		}
	}
}

func WithWebSocketReadTimeout(timeout time.Duration) WebSocketOption {
	return func(opts *webSocketOptions) {
		if timeout > 0 {
			opts.readTimeout = timeout
		}
	}
}

func WithWebSocketWriteTimeout(timeout time.Duration) WebSocketOption {
	return func(opts *webSocketOptions) {
		if timeout > 0 {
			opts.writeTimeout = timeout
		}
	}
}

func WithWebSocketManager(manager *WebSocketManager) WebSocketOption {
	return func(opts *webSocketOptions) {
		if manager != nil {
			opts.manager = manager
		}
	}
}

func (m *WebSocketManager) Snapshot() WebSocketStats {
	if m == nil {
		return WebSocketStats{}
	}
	return WebSocketStats{
		Accepted:     m.accepted.Load(),
		Active:       m.active.Load(),
		Closed:       m.closed.Load(),
		MessagesIn:   m.messagesIn.Load(),
		MessagesOut:  m.messagesOut.Load(),
		BytesIn:      m.bytesIn.Load(),
		BytesOut:     m.bytesOut.Load(),
		ProtocolErrs: m.protocolErrs.Load(),
	}
}

func (c *Context) WebSocket(handler WebSocketHandler, opts ...WebSocketOption) error {
	if handler == nil {
		writeError(c.Response, http.StatusInternalServerError, coreerrors.CodeInternal, "websocket handler is nil")
		return ErrWebSocketUpgrade
	}
	options := webSocketOptions{maxMessageBytes: 1 << 20, readTimeout: 0, writeTimeout: 5 * time.Second, manager: DefaultWebSocketManager}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	conn, rw, err := upgradeWebSocket(c.Response, c.Request)
	if err != nil {
		writeError(c.Response, http.StatusBadRequest, coreerrors.CodeInvalidArgument, err.Error())
		return err
	}
	ws := &WebSocketConn{conn: conn, rw: rw, maxMessageBytes: options.maxMessageBytes, writeTimeout: options.writeTimeout, manager: options.manager, closed: make(chan struct{})}
	if options.readTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(options.readTimeout))
	}
	if ws.manager != nil {
		ws.manager.accepted.Add(1)
		ws.manager.active.Add(1)
	}
	go func() {
		defer ws.Close()
		handler(c.Request.Context(), ws)
	}()
	return nil
}

func (c *WebSocketConn) ReadMessage() (int, []byte, error) {
	if c == nil {
		return 0, nil, ErrWebSocketClosed
	}
	messageType, payload, err := c.readFrame()
	if err != nil {
		if c.manager != nil && !errors.Is(err, ErrWebSocketClosed) && !errors.Is(err, io.EOF) {
			c.manager.protocolErrs.Add(1)
		}
		return 0, nil, err
	}
	if messageType == WebSocketCloseMessage {
		return messageType, payload, ErrWebSocketClosed
	}
	if c.manager != nil {
		c.manager.messagesIn.Add(1)
		c.manager.bytesIn.Add(int64(len(payload)))
	}
	return messageType, payload, nil
}

func (c *WebSocketConn) WriteMessage(messageType int, payload []byte) error {
	if c == nil {
		return ErrWebSocketClosed
	}
	if messageType != WebSocketTextMessage && messageType != WebSocketBinaryMessage && messageType != WebSocketCloseMessage && messageType != WebSocketPingMessage && messageType != WebSocketPongMessage {
		return fmt.Errorf("unsupported websocket message type: %d", messageType)
	}
	if c.writeTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := writeWebSocketFrame(c.rw, messageType, payload); err != nil {
		return err
	}
	if c.manager != nil {
		c.manager.messagesOut.Add(1)
		c.manager.bytesOut.Add(int64(len(payload)))
	}
	return nil
}

func (c *WebSocketConn) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.manager != nil {
			c.manager.closed.Add(1)
			c.manager.active.Add(-1)
		}
		err = c.conn.Close()
	})
	return err
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if r.Method != http.MethodGet || !headerContains(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, nil, fmt.Errorf("%w: invalid upgrade headers", ErrWebSocketUpgrade)
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, nil, fmt.Errorf("%w: invalid websocket version or key", ErrWebSocketUpgrade)
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("%w: response writer does not support hijacking", ErrWebSocketUpgrade)
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, errors.Join(ErrWebSocketUpgrade, fmt.Errorf("hijack websocket connection: %w", err))
	}
	accept := webSocketAccept(key)
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		_ = conn.Close() // best-effort cleanup after upgrade failure
		return nil, nil, fmt.Errorf("write websocket upgrade response: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close() // best-effort cleanup after upgrade failure
		return nil, nil, fmt.Errorf("flush websocket upgrade response: %w", err)
	}
	return conn, rw, nil
}

func (c *WebSocketConn) readFrame() (int, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.rw, header); err != nil {
		return 0, nil, err
	}
	fin := header[0]&0x80 != 0
	opcode := int(header[0] & 0x0f)
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	if !fin || !masked {
		return 0, nil, errors.New("invalid websocket frame flags")
	}
	switch length {
	case 126:
		var buf [2]byte
		if _, err := io.ReadFull(c.rw, buf[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(buf[:]))
	case 127:
		var buf [8]byte
		if _, err := io.ReadFull(c.rw, buf[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(buf[:])
	}
	if c.maxMessageBytes <= 0 || length > uint64(math.MaxInt64) || length > uint64(math.MaxInt) || int64(length) > c.maxMessageBytes {
		return 0, nil, errors.New("websocket message exceeds maximum size")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.rw, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, payload, nil
}

func writeWebSocketFrame(w *bufio.ReadWriter, messageType int, payload []byte) error {
	if messageType < 0 || messageType > math.MaxUint8 {
		return fmt.Errorf("unsupported websocket message type: %d", messageType)
	}
	if err := w.WriteByte(0x80 | byte(messageType)); err != nil {
		return err
	}
	length := len(payload)
	switch {
	case length < 126:
		if err := w.WriteByte(byte(length)); err != nil {
			return err
		}
	case length <= 65535:
		if err := w.WriteByte(126); err != nil {
			return err
		}
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(length))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	default:
		if err := w.WriteByte(127); err != nil {
			return err
		}
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(length))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func headerContains(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func webSocketAccept(key string) string {
	// #nosec G401 -- RFC 6455 mandates SHA-1 here for protocol compatibility, not cryptographic trust.
	sum := sha1.Sum([]byte(key + webSocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}
