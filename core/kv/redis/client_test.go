package redis

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockServer is a minimal RESP2 server supporting the subset of commands the
// client uses. It keeps a string/bit map in memory.
type mockServer struct {
	ln     net.Listener
	mu     sync.Mutex
	data   map[string][]byte
	pttl   map[string]int64
	groups map[string]bool
	closed bool
}

func newMockServer(t *testing.T) *mockServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockServer{ln: ln, data: make(map[string][]byte), pttl: make(map[string]int64), groups: make(map[string]bool)}
	go s.serve()
	return s
}

func (s *mockServer) addr() string { return s.ln.Addr().String() }

func (s *mockServer) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.ln.Close()
}

func (s *mockServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *mockServer) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	for {
		args, err := readRequest(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		s.dispatch(w, args)
		_ = w.Flush()
	}
}

func (s *mockServer) dispatch(w *bufio.Writer, args []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch strings.ToUpper(args[0]) {
	case "PING":
		w.WriteString("+PONG\r\n")
	case "AUTH", "SELECT":
		w.WriteString("+OK\r\n")
	case "SET":
		if hasArg(args, "NX") {
			if _, ok := s.data[args[1]]; ok {
				w.WriteString("$-1\r\n")
				return
			}
		}
		s.data[args[1]] = []byte(args[2])
		delete(s.pttl, args[1])
		if px := argAfter(args, "PX"); px != "" {
			ms, _ := strconv.ParseInt(px, 10, 64)
			s.pttl[args[1]] = ms
		}
		w.WriteString("+OK\r\n")
	case "GET":
		if v, ok := s.data[args[1]]; ok {
			writeBulk(w, v)
		} else {
			w.WriteString("$-1\r\n")
		}
	case "DEL":
		n := 0
		for _, k := range args[1:] {
			if _, ok := s.data[k]; ok {
				delete(s.data, k)
				delete(s.pttl, k)
				n++
			}
		}
		writeInt(w, int64(n))
	case "EXISTS":
		if _, ok := s.data[args[1]]; ok {
			writeInt(w, 1)
		} else {
			writeInt(w, 0)
		}
	case "INCR":
		n := s.incrBy(args[1], 1)
		writeInt(w, n)
	case "INCRBY":
		d, _ := strconv.ParseInt(args[2], 10, 64)
		writeInt(w, s.incrBy(args[1], d))
	case "EXPIRE", "PEXPIRE":
		if _, ok := s.data[args[1]]; ok {
			ms, _ := strconv.ParseInt(args[2], 10, 64)
			if strings.EqualFold(args[0], "EXPIRE") {
				ms *= 1000
			}
			s.pttl[args[1]] = ms
			writeInt(w, 1)
		} else {
			writeInt(w, 0)
		}
	case "PTTL":
		if _, ok := s.data[args[1]]; !ok {
			writeInt(w, -2)
		} else if ms, ok := s.pttl[args[1]]; ok {
			writeInt(w, ms)
		} else {
			writeInt(w, -1)
		}
	case "SETBIT":
		off, _ := strconv.ParseUint(args[2], 10, 64)
		prev := s.getBit(args[1], off)
		s.setBit(args[1], off, args[3] == "1")
		writeInt(w, int64(prev))
	case "GETBIT":
		off, _ := strconv.ParseUint(args[2], 10, 64)
		writeInt(w, int64(s.getBit(args[1], off)))
	case "EVAL":
		writeInt(w, 42)
	case "XADD":
		writeBulk(w, []byte("1700000000000-0"))
	case "XGROUP":
		groupKey := args[2] + ":" + args[3]
		if s.groups[groupKey] {
			w.WriteString("-BUSYGROUP Consumer Group name already exists\r\n")
			return
		}
		s.groups[groupKey] = true
		w.WriteString("+OK\r\n")
	case "XREADGROUP":
		w.WriteString("*1\r\n")
		w.WriteString("*2\r\n")
		writeBulk(w, []byte(args[len(args)-2]))
		w.WriteString("*1\r\n")
		w.WriteString("*2\r\n")
		writeBulk(w, []byte("1700000000000-0"))
		w.WriteString("*4\r\n")
		writeBulk(w, []byte("field"))
		writeBulk(w, []byte("value"))
		writeBulk(w, []byte("trace"))
		writeBulk(w, []byte("abc"))
	case "XACK":
		writeInt(w, int64(len(args)-3))
	default:
		w.WriteString("-ERR unknown command\r\n")
	}
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, want) {
			return true
		}
	}
	return false
}

func argAfter(args []string, want string) string {
	for i := 0; i+1 < len(args); i++ {
		if strings.EqualFold(args[i], want) {
			return args[i+1]
		}
	}
	return ""
}

func (s *mockServer) incrBy(key string, delta int64) int64 {
	cur := int64(0)
	if v, ok := s.data[key]; ok {
		cur, _ = strconv.ParseInt(string(v), 10, 64)
	}
	cur += delta
	s.data[key] = []byte(strconv.FormatInt(cur, 10))
	return cur
}

func (s *mockServer) setBit(key string, offset uint64, set bool) {
	byteIdx := offset / 8
	bitIdx := 7 - (offset % 8)
	buf := s.data[key]
	for uint64(len(buf)) <= byteIdx {
		buf = append(buf, 0)
	}
	if set {
		buf[byteIdx] |= 1 << bitIdx
	} else {
		buf[byteIdx] &^= 1 << bitIdx
	}
	s.data[key] = buf
}

func (s *mockServer) getBit(key string, offset uint64) int {
	byteIdx := offset / 8
	bitIdx := 7 - (offset % 8)
	buf := s.data[key]
	if uint64(len(buf)) <= byteIdx {
		return 0
	}
	return int((buf[byteIdx] >> bitIdx) & 1)
}

func writeBulk(w *bufio.Writer, v []byte) {
	w.WriteString("$" + strconv.Itoa(len(v)) + "\r\n")
	w.Write(v)
	w.WriteString("\r\n")
}

func writeInt(w *bufio.Writer, n int64) {
	w.WriteString(":" + strconv.FormatInt(n, 10) + "\r\n")
}

// readRequest parses one RESP2 array-of-bulk-strings request.
func readRequest(r *bufio.Reader) ([]string, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if prefix != '*' {
		return nil, nil
	}
	count, _ := strconv.Atoi(string(line))
	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if _, err := r.ReadByte(); err != nil { // '$'
			return nil, err
		}
		lenLine, err := readLine(r)
		if err != nil {
			return nil, err
		}
		n, _ := strconv.Atoi(string(lenLine))
		buf := make([]byte, n+2)
		if _, err := readFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:n]))
	}
	return args, nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func newTestClient(t *testing.T) (*Client, *mockServer) {
	t.Helper()
	srv := newMockServer(t)
	client := New(Config{Addr: srv.addr(), Timeout: time.Second})
	t.Cleanup(func() {
		_ = client.Close()
		srv.close()
	})
	return client, srv
}

func TestClientPing(t *testing.T) {
	client, _ := newTestClient(t)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClientSetGetDelete(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if err := client.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := client.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v" {
		t.Fatalf("Get = %q, want v", got)
	}
	exists, err := client.Exists(ctx, "k")
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true", exists, err)
	}
	removed, err := client.Delete(ctx, "k")
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v; want true", removed, err)
	}
	if _, err := client.Get(ctx, "k"); err != ErrNil {
		t.Fatalf("Get after delete err = %v, want ErrNil", err)
	}
}

func TestClientIncrAndBits(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	n, err := client.Incr(ctx, "counter")
	if err != nil || n != 1 {
		t.Fatalf("Incr = %d, %v; want 1", n, err)
	}
	n, _ = client.IncrBy(ctx, "counter", 4)
	if n != 5 {
		t.Fatalf("IncrBy = %d, want 5", n)
	}
	if _, err := client.SetBit(ctx, "bits", 9, 1); err != nil {
		t.Fatalf("SetBit: %v", err)
	}
	bit, err := client.GetBit(ctx, "bits", 9)
	if err != nil || bit != 1 {
		t.Fatalf("GetBit = %d, %v; want 1", bit, err)
	}
	zero, _ := client.GetBit(ctx, "bits", 10)
	if zero != 0 {
		t.Fatalf("GetBit unset = %d, want 0", zero)
	}
}

func TestClientSetNXTTLAndExpiry(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	stored, err := client.SetNX(ctx, "unique", []byte("v1"), time.Second)
	if err != nil || !stored {
		t.Fatalf("SetNX first = %v, %v; want true, nil", stored, err)
	}
	stored, err = client.SetNX(ctx, "unique", []byte("v2"), time.Second)
	if err != nil || stored {
		t.Fatalf("SetNX duplicate = %v, %v; want false, nil", stored, err)
	}
	got, err := client.Get(ctx, "unique")
	if err != nil || string(got) != "v1" {
		t.Fatalf("Get after duplicate SetNX = %q, %v; want v1, nil", got, err)
	}

	if _, err := client.TTL(ctx, "missing"); !errors.Is(err, ErrNil) {
		t.Fatalf("TTL missing error = %v, want ErrNil", err)
	}
	if err := client.Set(ctx, "persisted", []byte("v"), 0); err != nil {
		t.Fatalf("Set persisted: %v", err)
	}
	ttl, err := client.TTL(ctx, "persisted")
	if err != nil || ttl != 0 {
		t.Fatalf("TTL persisted = %s, %v; want 0, nil", ttl, err)
	}

	expired, err := client.Expire(ctx, "persisted", 1500*time.Millisecond)
	if err != nil || !expired {
		t.Fatalf("Expire existing = %v, %v; want true, nil", expired, err)
	}
	ttl, err = client.TTL(ctx, "persisted")
	if err != nil || ttl != 2*time.Second {
		t.Fatalf("TTL after Expire = %s, %v; want 2s, nil", ttl, err)
	}
	expired, err = client.PExpire(ctx, "persisted", 0)
	if err != nil || !expired {
		t.Fatalf("PExpire existing = %v, %v; want true, nil", expired, err)
	}
	ttl, err = client.TTL(ctx, "persisted")
	if err != nil || ttl != time.Millisecond {
		t.Fatalf("TTL after PExpire zero = %s, %v; want 1ms, nil", ttl, err)
	}
	expired, err = client.Expire(ctx, "missing", time.Second)
	if err != nil || expired {
		t.Fatalf("Expire missing = %v, %v; want false, nil", expired, err)
	}
}

func TestClientEvalAndStreams(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	n, err := client.Eval(ctx, "return 42", []string{"k1", "k2"}, "arg")
	if err != nil || n != 42 {
		t.Fatalf("Eval = %d, %v; want 42, nil", n, err)
	}
	id, err := client.XAdd(ctx, "events", 100, map[string]string{"field": "value"})
	if err != nil || id != "1700000000000-0" {
		t.Fatalf("XAdd = %q, %v; want generated id, nil", id, err)
	}
	if err := client.XGroupCreate(ctx, "events", "workers", "", true); err != nil {
		t.Fatalf("XGroupCreate first: %v", err)
	}
	if err := client.XGroupCreate(ctx, "events", "workers", "0", false); err != nil {
		t.Fatalf("XGroupCreate duplicate BUSYGROUP should be idempotent: %v", err)
	}
	entries, err := client.XReadGroup(ctx, "workers", "c1", "events", 1, time.Millisecond)
	if err != nil {
		t.Fatalf("XReadGroup error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1700000000000-0" || entries[0].Fields["field"] != "value" || entries[0].Fields["trace"] != "abc" {
		t.Fatalf("XReadGroup entries = %#v", entries)
	}
	acked, err := client.XAck(ctx, "events", "workers", entries[0].ID)
	if err != nil || acked != 1 {
		t.Fatalf("XAck = %d, %v; want 1, nil", acked, err)
	}
	acked, err = client.XAck(ctx, "events", "workers")
	if err != nil || acked != 0 {
		t.Fatalf("XAck empty = %d, %v; want 0, nil", acked, err)
	}
}

func TestParseStreamReadSkipsMalformedEntries(t *testing.T) {
	rep := reply{kind: '*', array: []reply{
		{kind: '*', array: []reply{
			{kind: '$', str: []byte("events")},
			{kind: '*', array: []reply{
				{kind: '*', array: []reply{{kind: '$', str: []byte("1-0")}, {kind: '*', array: []reply{{kind: '$', str: []byte("k")}, {kind: '$', str: []byte("v")}}}}},
				{kind: '*', array: []reply{{kind: '$', str: []byte("missing-fields")}}},
			}},
		}},
		{kind: '*', array: []reply{{kind: '$', str: []byte("ignored")}}},
	}}

	entries := parseStreamRead(rep)
	if len(entries) != 1 || entries[0].ID != "1-0" || entries[0].Fields["k"] != "v" {
		t.Fatalf("parseStreamRead = %#v, want one valid entry", entries)
	}
	if entries := parseStreamRead(reply{kind: '*', isNil: true}); entries != nil {
		t.Fatalf("parseStreamRead nil = %#v, want nil", entries)
	}
}

func TestClientPoolReuse(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if _, err := client.Incr(ctx, "n"); err != nil {
			t.Fatalf("Incr #%d: %v", i, err)
		}
	}
	stats := client.Snapshot()
	if stats.Commands < 50 {
		t.Fatalf("commands = %d, want >= 50", stats.Commands)
	}
	if stats.IdleConns == 0 {
		t.Fatalf("idle conns = 0, want pooled connection reuse")
	}
}

func TestReadReplyRejectsMalformedBulkTerminator(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("$3\r\nabcxx"))
	if rep, err := readReply(r); err == nil {
		t.Fatalf("readReply malformed bulk = %+v, nil err; want protocol error", rep)
	}
}

func TestReplyMethodsAndErrorType(t *testing.T) {
	if got := (&Error{Message: "oops"}).Error(); got != "redis: oops" {
		t.Fatalf("Error.Error = %q, want redis: oops", got)
	}

	// reply.bytes boundaries
	if _, err := (reply{kind: '-', str: []byte("err")}).bytes(); err == nil {
		t.Fatal("bytes unexpected kind: want error")
	}
	if _, err := (reply{kind: ':', integer: 7}).bytes(); err != nil {
		t.Fatalf("bytes int kind error = %v, want nil", err)
	}

	// reply.int64 boundaries
	if _, err := (reply{kind: '-', str: []byte("err")}).int64(); err == nil {
		t.Fatal("int64 unexpected kind: want error")
	}
	if n, err := (reply{kind: '$', str: []byte("42")}).int64(); err != nil || n != 42 {
		t.Fatalf("int64 bulk string = %d/%v, want 42/nil", n, err)
	}

	// reply.status boundaries
	if _, err := (reply{kind: '-', str: []byte("err")}).status(); err == nil {
		t.Fatal("status unexpected kind: want error")
	}
}

func TestPoolPutBoundaries(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{netConn: client, reader: bufio.NewReader(client), writer: bufio.NewWriter(client), createdAt: time.Now(), usedAt: time.Now()}, nil
	}, 1, 1, time.Minute, time.Minute)
	t.Cleanup(func() { _ = p.close() })

	// put nil does nothing
	p.put(nil, false)

	c, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// put broken connection
	p.put(c, true)
	if active, idle := p.snapshot(); active != 0 || idle != 0 {
		t.Fatalf("after broken put: active=%d idle=%d, want 0/0", active, idle)
	}

	// get another connection, then close pool and put
	c, err = p.get(context.Background())
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	_ = p.close()
	p.put(c, false)
	if active, idle := p.snapshot(); active != 0 || idle != 0 {
		t.Fatalf("after closed put: active=%d idle=%d, want 0/0", active, idle)
	}
}

func TestPoolStaleBoundaries(t *testing.T) {
	now := time.Now()
	c := &conn{createdAt: now.Add(-2 * time.Hour), usedAt: now.Add(-2 * time.Hour)}
	p := newPool(nil, 1, 1, time.Minute, time.Hour)
	if !p.stale(c, now) {
		t.Fatal("stale by maxLife = false, want true")
	}
	p = newPool(nil, 1, 1, time.Hour, time.Minute)
	if !p.stale(c, now) {
		t.Fatal("stale by idleTTL = false, want true")
	}
	p = newPool(nil, 1, 1, 0, 0)
	if p.stale(c, now) {
		t.Fatal("stale with no limits = true, want false")
	}
}

func TestPoolGetReturnsOnContextCancellationWhileExhausted(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 1, 1, time.Minute, 0)
	t.Cleanup(func() { _ = p.close() })

	first, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("first get: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		c, err := p.get(ctx)
		if c != nil {
			p.put(c, true)
		}
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("get after cancel err = %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		p.put(first, false)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("get did not return on context cancellation before pool signal; eventual err = %v, want context.Canceled", err)
		}
	}
}

func TestDoNilClientOrPool(t *testing.T) {
	var nilClient *Client
	if _, err := nilClient.do(context.Background(), "PING"); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil client do err = %v, want ErrClosed", err)
	}

	c := &Client{}
	if _, err := c.do(context.Background(), "PING"); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil pool do err = %v, want ErrClosed", err)
	}
}

func TestDoContextCancellation(t *testing.T) {
	client, _ := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.do(ctx, "PING"); !errors.Is(err, context.Canceled) {
		t.Fatalf("do cancelled ctx err = %v, want context.Canceled", err)
	}
}

func TestDoTransportError(t *testing.T) {
	client, srv := newTestClient(t)
	ctx := context.Background()
	// Force a transport error by closing the server so pooled connections break.
	srv.close()
	// Drain any idle pooled connection.
	if c, err := client.pool.get(ctx); err == nil {
		client.pool.put(c, true)
	}
	if err := client.Ping(ctx); err == nil {
		t.Fatal("Ping after server close: want error, got nil")
	}
	stats := client.Snapshot()
	if stats.Errors == 0 {
		t.Fatal("expected error counter > 0 after broken ping")
	}
}

func TestDoRedisErrorNotBroken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				w := bufio.NewWriter(conn)
				for {
					args, err := readRequest(r)
					if err != nil {
						return
					}
					if len(args) == 0 {
						continue
					}
					if strings.ToUpper(args[0]) == "EVAL" {
						w.WriteString("-ERR script error\r\n")
						_ = w.Flush()
						continue
					}
					w.WriteString("+PONG\r\n")
					_ = w.Flush()
				}
			}(c)
		}
	}()

	client := New(Config{Addr: ln.Addr().String(), Timeout: time.Second})
	defer client.Close()
	ctx := context.Background()

	_, err = client.Eval(ctx, "bad", nil)
	if err == nil {
		t.Fatal("Eval bad script: want error, got nil")
	}
	var redisErr *Error
	if !errors.As(err, &redisErr) {
		t.Fatalf("Eval err = %T, want *Error", err)
	}

	// Connection should still be usable because protocol errors are not broken.
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping after redis error: %v", err)
	}
}

func TestWriteCommandEmpty(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := writeCommand(w); err == nil {
		t.Fatal("writeCommand empty: want error, got nil")
	}
}

func TestReadReplyUnknownPrefix(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("?oops\r\n"))
	if _, err := readReply(r); err == nil {
		t.Fatal("readReply unknown prefix: want error, got nil")
	}
}

func TestReadReplyInvalidInteger(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(":abc\r\n"))
	if _, err := readReply(r); err == nil {
		t.Fatal("readReply invalid integer: want error, got nil")
	}
}

func TestReadReplyInvalidBulkLength(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("$abc\r\n"))
	if _, err := readReply(r); err == nil {
		t.Fatal("readReply invalid bulk length: want error, got nil")
	}
}

func TestReadReplyInvalidArrayLength(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("*abc\r\n"))
	if _, err := readReply(r); err == nil {
		t.Fatal("readReply invalid array length: want error, got nil")
	}
}

func TestReadReplyReadByteError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := readReply(r); err == nil {
		t.Fatal("readReply read byte error: want error, got nil")
	}
}

func TestReadLineWithoutCR(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("line\n"))
	line, err := readLine(r)
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if string(line) != "line" {
		t.Fatalf("readLine = %q, want line", line)
	}
}

func TestReadLineError(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := readLine(r); err == nil {
		t.Fatal("readLine empty: want error, got nil")
	}
}

func TestReplyBytesUnexpectedKind(t *testing.T) {
	if _, err := (reply{kind: '-', str: []byte("err")}).bytes(); err == nil {
		t.Fatal("bytes unexpected kind: want error")
	}
}

func TestReplyInt64UnexpectedKind(t *testing.T) {
	if _, err := (reply{kind: '-', str: []byte("err")}).int64(); err == nil {
		t.Fatal("int64 unexpected kind: want error")
	}
}

func TestReplyStatusUnexpectedKind(t *testing.T) {
	if _, err := (reply{kind: '-', str: []byte("err")}).status(); err == nil {
		t.Fatal("status unexpected kind: want error")
	}
}

func TestPoolGetFactoryError(t *testing.T) {
	factoryErr := errors.New("dial failed")
	p := newPool(func() (*conn, error) {
		return nil, factoryErr
	}, 2, 2, time.Minute, time.Minute)
	t.Cleanup(func() { _ = p.close() })

	_, err := p.get(context.Background())
	if !errors.Is(err, factoryErr) {
		t.Fatalf("get factory err = %v, want %v", err, factoryErr)
	}
	if active, _ := p.snapshot(); active != 0 {
		t.Fatalf("active = %d, want 0 after factory error", active)
	}
}

func TestPoolPutMaxIdleEviction(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 2, 1, time.Minute, time.Minute)
	t.Cleanup(func() { _ = p.close() })

	c1, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	c2, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("get second: %v", err)
	}

	// Put first connection back (idle=1)
	p.put(c1, false)
	// Put second connection back (idle already at maxIdle=1, should evict)
	p.put(c2, false)

	if _, idle := p.snapshot(); idle != 1 {
		t.Fatalf("idle = %d, want 1 after maxIdle eviction", idle)
	}
}

func TestPoolGetNilContext(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 1, 1, time.Minute, time.Minute)
	t.Cleanup(func() { _ = p.close() })

	//nolint:staticcheck // verifies pool.get preserves nil-context compatibility.
	c, err := p.get(nil)
	if err != nil {
		t.Fatalf("get nil ctx: %v", err)
	}
	p.put(c, false)
}

func TestPoolGetClosedPool(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 1, 1, time.Minute, time.Minute)
	_ = p.close()

	_, err := p.get(context.Background())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("get closed pool err = %v, want ErrClosed", err)
	}
}

func TestPoolCloseTwice(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 1, 1, time.Minute, time.Minute)
	if err := p.close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := p.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestPoolPutBrokenSignalsWaiter(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 1, 1, time.Minute, time.Minute)
	t.Cleanup(func() { _ = p.close() })

	c, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Start a waiter that will be woken by the broken put signal.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan *conn, 1)
	go func() {
		nc, err := p.get(ctx)
		if err == nil {
			done <- nc
		} else {
			done <- nil
		}
	}()

	// Give the goroutine time to start waiting.
	time.Sleep(10 * time.Millisecond)
	p.put(c, true) // broken put should signal

	select {
	case nc := <-done:
		if nc == nil {
			t.Fatal("waiter got nil conn after broken signal")
		}
		p.put(nc, false)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waiter not signaled by broken put")
	}
}

func TestConfigWithDefaults(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.Addr != "127.0.0.1:6379" {
		t.Fatalf("addr = %q, want 127.0.0.1:6379", cfg.Addr)
	}
	if cfg.DialTimeout != 5*time.Second {
		t.Fatalf("dialTimeout = %v, want 5s", cfg.DialTimeout)
	}
	if cfg.Timeout != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", cfg.Timeout)
	}
	if cfg.MaxConns != 16 {
		t.Fatalf("maxConns = %d, want 16", cfg.MaxConns)
	}
	if cfg.MaxIdleConns != 16 {
		t.Fatalf("maxIdleConns = %d, want 16", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("connMaxIdleTime = %v, want 5m", cfg.ConnMaxIdleTime)
	}

	partial := Config{Addr: ":0", MaxConns: 4, ConnMaxIdleTime: time.Minute}.withDefaults()
	if partial.MaxIdleConns != 4 {
		t.Fatalf("maxIdleConns derived = %d, want 4", partial.MaxIdleConns)
	}
}

func TestDialAuthAndSelectErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				w := bufio.NewWriter(conn)
				for {
					args, err := readRequest(r)
					if err != nil {
						return
					}
					if len(args) == 0 {
						continue
					}
					switch strings.ToUpper(args[0]) {
					case "AUTH":
						w.WriteString("-ERR invalid password\r\n")
					case "SELECT":
						w.WriteString("-ERR invalid DB\r\n")
					default:
						w.WriteString("+OK\r\n")
					}
					_ = w.Flush()
				}
			}(c)
		}
	}()

	client := New(Config{Addr: ln.Addr().String(), Password: "wrong", DB: 7, DialTimeout: time.Second, Timeout: time.Second})
	defer client.Close()

	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping with wrong auth: want error, got nil")
	}
}

func TestClientCloseNil(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Fatalf("Close nil client: %v", err)
	}
}

func TestSnapshotNilClient(t *testing.T) {
	var c *Client
	if stats := c.Snapshot(); stats != (Stats{}) {
		t.Fatalf("Snapshot nil = %+v, want zero", stats)
	}
}

func TestPingUnexpectedReply(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				w := bufio.NewWriter(conn)
				for {
					args, err := readRequest(r)
					if err != nil {
						return
					}
					if len(args) == 0 {
						continue
					}
					if strings.ToUpper(args[0]) == "PING" {
						w.WriteString("+NOTPONG\r\n")
						_ = w.Flush()
					}
				}
			}(c)
		}
	}()

	client := New(Config{Addr: ln.Addr().String(), Timeout: time.Second})
	defer client.Close()
	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping unexpected reply: want error, got nil")
	}
}

func TestConnCloseNil(t *testing.T) {
	var c *conn
	if err := c.close(); err != nil {
		t.Fatalf("close nil conn: %v", err)
	}
	c = &conn{}
	if err := c.close(); err != nil {
		t.Fatalf("close conn with nil netConn: %v", err)
	}
}

func TestConnDoDeadlineReset(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	c := &conn{
		netConn: client,
		reader:  bufio.NewReader(client),
		writer:  bufio.NewWriter(client),
	}
	// timeout=0 should reset deadline to zero.
	go func() {
		// consume the command and reply
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 128)
		_, _ = server.Read(buf)
		_, _ = server.Write([]byte("+OK\r\n"))
	}()
	_, err := c.do(0, "PING")
	if err != nil {
		t.Fatalf("do with zero deadline: %v", err)
	}
}

func TestDelEmptyKeys(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	n, err := client.Del(ctx)
	if err != nil {
		t.Fatalf("Del empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("Del empty = %d, want 0", n)
	}
}

func TestXAckEmptyIDs(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	n, err := client.XAck(ctx, "s", "g")
	if err != nil {
		t.Fatalf("XAck empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("XAck empty = %d, want 0", n)
	}
}

func TestXGroupCreateEmptyStart(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if err := client.XGroupCreate(ctx, "s", "g", "", false); err != nil {
		t.Fatalf("XGroupCreate empty start: %v", err)
	}
}

func TestXReadGroupNoCountNoBlock(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if err := client.XGroupCreate(ctx, "s", "g", "0", true); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	_, _ = client.XAdd(ctx, "s", 0, map[string]string{"k": "v"})
	entries, err := client.XReadGroup(ctx, "g", "c", "s", 0, -1)
	if err != nil {
		t.Fatalf("XReadGroup no count/block: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("XReadGroup no count/block: expected entries")
	}
}

func TestSetWithTTL(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if err := client.Set(ctx, "ttlkey", []byte("v"), time.Second); err != nil {
		t.Fatalf("Set with TTL: %v", err)
	}
	ttl, err := client.TTL(ctx, "ttlkey")
	if err != nil || ttl <= 0 {
		t.Fatalf("TTL = %v, %v; want >0", ttl, err)
	}
}

func TestPExpireZeroTTL(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if err := client.Set(ctx, "pekey", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	expired, err := client.PExpire(ctx, "pekey", 0)
	if err != nil || !expired {
		t.Fatalf("PExpire zero = %v, %v; want true, nil", expired, err)
	}
}

func TestPoolStaleReuse(t *testing.T) {
	p := newPool(func() (*conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &conn{
			netConn:   client,
			reader:    bufio.NewReader(client),
			writer:    bufio.NewWriter(client),
			createdAt: time.Now(),
			usedAt:    time.Now(),
		}, nil
	}, 2, 2, time.Millisecond, time.Hour)
	t.Cleanup(func() { _ = p.close() })

	c, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	p.put(c, false)

	// Wait for idle TTL to expire.
	time.Sleep(5 * time.Millisecond)

	c2, err := p.get(context.Background())
	if err != nil {
		t.Fatalf("get after stale: %v", err)
	}
	// c2 should be a new connection because c was stale.
	if c2 == c {
		t.Fatal("expected new conn after stale eviction")
	}
	p.put(c2, false)
}

func TestReplyInt64FromBulkString(t *testing.T) {
	n, err := (reply{kind: '$', str: []byte("99")}).int64()
	if err != nil || n != 99 {
		t.Fatalf("int64 bulk = %d/%v, want 99/nil", n, err)
	}
}

func TestReplyInt64FromStatusString(t *testing.T) {
	n, err := (reply{kind: '+', str: []byte("77")}).int64()
	if err != nil || n != 77 {
		t.Fatalf("int64 status = %d/%v, want 77/nil", n, err)
	}
}

func TestReplyBytesFromStatus(t *testing.T) {
	b, err := (reply{kind: '+', str: []byte("ok")}).bytes()
	if err != nil || string(b) != "ok" {
		t.Fatalf("bytes status = %q/%v, want ok/nil", b, err)
	}
}

func TestReplyBytesFromInteger(t *testing.T) {
	b, err := (reply{kind: ':', integer: 42}).bytes()
	if err != nil || string(b) != "42" {
		t.Fatalf("bytes int = %q/%v, want 42/nil", b, err)
	}
}

func TestReplyStatusNil(t *testing.T) {
	_, err := (reply{kind: '+', isNil: true}).status()
	if !errors.Is(err, ErrNil) {
		t.Fatalf("status nil = %v, want ErrNil", err)
	}
}

func TestReplyBytesNil(t *testing.T) {
	_, err := (reply{kind: '$', isNil: true}).bytes()
	if !errors.Is(err, ErrNil) {
		t.Fatalf("bytes nil = %v, want ErrNil", err)
	}
}

func TestReplyInt64Nil(t *testing.T) {
	_, err := (reply{kind: ':', isNil: true}).int64()
	if !errors.Is(err, ErrNil) {
		t.Fatalf("int64 nil = %v, want ErrNil", err)
	}
}
