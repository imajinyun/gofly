// Package redis provides a minimal RESP2 Redis client with connection pooling.
package redis

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ErrClosed is returned when an operation is attempted on a closed client.
var ErrClosed = errors.New("redis: client is closed")

// Config configures a Redis client.
type Config struct {
	Addr            string        `json:"addr"`
	Password        string        `json:"-"`
	DB              int           `json:"db,omitempty"`
	DialTimeout     time.Duration `json:"dialTimeout,omitempty"`
	Timeout         time.Duration `json:"timeout,omitempty"`
	MaxConns        int           `json:"maxConns,omitempty"`
	MaxIdleConns    int           `json:"maxIdleConns,omitempty"`
	ConnMaxIdleTime time.Duration `json:"connMaxIdleTime,omitempty"`
	ConnMaxLifetime time.Duration `json:"connMaxLifetime,omitempty"`
}

// withDefaults fills zero fields with sensible defaults.
func (c Config) withDefaults() Config {
	if c.Addr == "" {
		c.Addr = "127.0.0.1:6379"
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 3 * time.Second
	}
	if c.MaxConns <= 0 {
		c.MaxConns = 16
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = c.MaxConns
	}
	if c.ConnMaxIdleTime <= 0 {
		c.ConnMaxIdleTime = 5 * time.Minute
	}
	return c
}

// Stats reports client-level counters.
type Stats struct {
	Commands    int64 `json:"commands"`
	Errors      int64 `json:"errors"`
	ActiveConns int   `json:"activeConns"`
	IdleConns   int   `json:"idleConns"`
}

// Client is a minimal, dependency-free Redis client backed by a connection pool.
// It implements the kv.RedisClient interface and exposes the additional commands
// required by the bloom filter and distributed rate limiter.
type Client struct {
	cfg      Config
	pool     *pool
	commands int64
	errors   int64
}

// New creates a Redis client. It does not establish a connection eagerly; use
// Ping to verify connectivity.
func New(cfg Config) *Client {
	cfg = cfg.withDefaults()
	c := &Client{cfg: cfg}
	dialer := &net.Dialer{Timeout: cfg.DialTimeout}
	c.pool = newPool(func() (*conn, error) {
		return c.dial(dialer)
	}, cfg.MaxConns, cfg.MaxIdleConns, cfg.ConnMaxIdleTime, cfg.ConnMaxLifetime)
	return c
}

func (c *Client) dial(dialer *net.Dialer) (*conn, error) {
	netConn, err := dialer.Dial("tcp", c.cfg.Addr)
	if err != nil {
		slog.Warn("redis dial failed", "addr", c.cfg.Addr, "error", err)
		return nil, err
	}
	now := time.Now()
	pc := &conn{
		netConn:   netConn,
		reader:    bufio.NewReader(netConn),
		writer:    bufio.NewWriter(netConn),
		createdAt: now,
		usedAt:    now,
	}
	if c.cfg.Password != "" {
		if _, err := pc.do(c.cfg.Timeout, "AUTH", c.cfg.Password); err != nil {
			_ = pc.close()
			slog.Warn("redis AUTH failed", "addr", c.cfg.Addr, "error", err)
			return nil, err
		}
	}
	if c.cfg.DB != 0 {
		if _, err := pc.do(c.cfg.Timeout, "SELECT", strconv.Itoa(c.cfg.DB)); err != nil {
			_ = pc.close()
			return nil, err
		}
	}
	return pc, nil
}

// do executes a command against a pooled connection.
func (c *Client) do(ctx context.Context, args ...string) (reply, error) {
	if c == nil || c.pool == nil {
		return reply{}, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}
	pc, err := c.pool.get(ctx)
	if err != nil {
		atomic.AddInt64(&c.errors, 1)
		slog.Warn("redis pool error", "addr", c.cfg.Addr, "error", err)
		return reply{}, err
	}
	atomic.AddInt64(&c.commands, 1)
	rep, err := pc.do(c.cfg.Timeout, args...)
	if err != nil {
		// Protocol-level Error replies keep the connection usable; transport
		// errors mean the connection must be discarded.
		var redisErr *Error
		broken := !errors.As(err, &redisErr)
		c.pool.put(pc, broken)
		if broken {
			atomic.AddInt64(&c.errors, 1)
			slog.Warn("redis transport error", "addr", c.cfg.Addr, "error", err)
		}
		return reply{}, err
	}
	c.pool.put(pc, false)
	return rep, nil
}

// Ping verifies connectivity with the server.
func (c *Client) Ping(ctx context.Context) error {
	rep, err := c.do(ctx, "PING")
	if err != nil {
		return err
	}
	status, err := rep.status()
	if err != nil {
		return err
	}
	if !strings.EqualFold(status, "PONG") {
		return errors.New("redis: unexpected PING reply: " + status)
	}
	return nil
}

// --- kv.RedisClient interface ---

// Get returns the raw value stored at key, or kv.ErrNotFound semantics via ErrNil.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	rep, err := c.do(ctx, "GET", key)
	if err != nil {
		return nil, err
	}
	if rep.isNil {
		return nil, ErrNil
	}
	return rep.bytes()
}

// Set stores value at key with an optional TTL.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	args := []string{"SET", key, string(value)}
	if ttl > 0 {
		args = append(args, "PX", strconv.FormatInt(ttl.Milliseconds(), 10))
	}
	_, err := c.do(ctx, args...)
	return err
}

// SetNX stores value at key only when it does not already exist.
func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	args := []string{"SET", key, string(value), "NX"}
	if ttl > 0 {
		args = append(args, "PX", strconv.FormatInt(ttl.Milliseconds(), 10))
	}
	rep, err := c.do(ctx, args...)
	if err != nil {
		return false, err
	}
	if rep.isNil {
		return false, nil
	}
	status, err := rep.status()
	if err != nil {
		return false, err
	}
	return strings.EqualFold(status, "OK"), nil
}

// Delete removes key and reports whether it existed.
func (c *Client) Delete(ctx context.Context, key string) (bool, error) {
	n, err := c.Del(ctx, key)
	return n > 0, err
}

// Exists reports whether key exists.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	rep, err := c.do(ctx, "EXISTS", key)
	if err != nil {
		return false, err
	}
	n, err := rep.int64()
	return n > 0, err
}

// TTL returns the remaining time to live of key.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	rep, err := c.do(ctx, "PTTL", key)
	if err != nil {
		return 0, err
	}
	ms, err := rep.int64()
	if err != nil {
		return 0, err
	}
	switch {
	case ms == -2:
		return 0, ErrNil
	case ms < 0:
		return 0, nil
	default:
		return time.Duration(ms) * time.Millisecond, nil
	}
}

// Close releases all pooled connections.
func (c *Client) Close() error {
	if c == nil || c.pool == nil {
		return nil
	}
	return c.pool.close()
}

// --- extended commands ---

// Del removes one or more keys and returns the count removed.
func (c *Client) Del(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	rep, err := c.do(ctx, append([]string{"DEL"}, keys...)...)
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// Incr increments the integer value stored at key by one.
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	rep, err := c.do(ctx, "INCR", key)
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// IncrBy increments the integer value stored at key by delta.
func (c *Client) IncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	rep, err := c.do(ctx, "INCRBY", key, strconv.FormatInt(delta, 10))
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// Expire sets a TTL on key and reports whether the timeout was set.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	seconds := int64((ttl + time.Second - 1) / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	rep, err := c.do(ctx, "EXPIRE", key, strconv.FormatInt(seconds, 10))
	if err != nil {
		return false, err
	}
	n, err := rep.int64()
	return n > 0, err
}

// PExpire sets a millisecond-precision TTL on key.
func (c *Client) PExpire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ms := ttl.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	rep, err := c.do(ctx, "PEXPIRE", key, strconv.FormatInt(ms, 10))
	if err != nil {
		return false, err
	}
	n, err := rep.int64()
	return n > 0, err
}

// SetBit sets or clears the bit at offset and returns the original bit value.
func (c *Client) SetBit(ctx context.Context, key string, offset uint64, value int) (int64, error) {
	bit := "0"
	if value != 0 {
		bit = "1"
	}
	rep, err := c.do(ctx, "SETBIT", key, strconv.FormatUint(offset, 10), bit)
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// GetBit returns the bit value at offset.
func (c *Client) GetBit(ctx context.Context, key string, offset uint64) (int64, error) {
	rep, err := c.do(ctx, "GETBIT", key, strconv.FormatUint(offset, 10))
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// Eval runs a Lua script. The numeric reply is returned when the script yields
// an integer; otherwise the bulk/array reply is converted to bytes.
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...string) (int64, error) {
	cmd := make([]string, 0, 3+len(keys)+len(args))
	cmd = append(cmd, "EVAL", script, strconv.Itoa(len(keys)))
	cmd = append(cmd, keys...)
	cmd = append(cmd, args...)
	rep, err := c.do(ctx, cmd...)
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// StreamEntry is a single entry read from a Redis stream.
type StreamEntry struct {
	ID     string
	Fields map[string]string
}

// XAdd appends an entry to a stream and returns its generated ID. A maxLen of
// >0 trims the stream approximately to that length.
func (c *Client) XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error) {
	args := []string{"XADD", stream}
	if maxLen > 0 {
		args = append(args, "MAXLEN", "~", strconv.FormatInt(maxLen, 10))
	}
	args = append(args, "*")
	for k, v := range fields {
		args = append(args, k, v)
	}
	rep, err := c.do(ctx, args...)
	if err != nil {
		return "", err
	}
	return rep.status()
}

// XGroupCreate creates a consumer group, optionally creating the stream
// (MKSTREAM). It treats a BUSYGROUP reply as success so calls are idempotent.
func (c *Client) XGroupCreate(ctx context.Context, stream, group, start string, mkStream bool) error {
	if start == "" {
		start = "$"
	}
	args := []string{"XGROUP", "CREATE", stream, group, start}
	if mkStream {
		args = append(args, "MKSTREAM")
	}
	_, err := c.do(ctx, args...)
	if err != nil {
		var redisErr *Error
		if errors.As(err, &redisErr) && strings.Contains(redisErr.Message, "BUSYGROUP") {
			return nil
		}
		return err
	}
	return nil
}

// XReadGroup reads up to count entries for a consumer in a group, blocking up
// to block for new entries. It returns entries from the ">" backlog.
func (c *Client) XReadGroup(ctx context.Context, group, consumer, stream string, count int, block time.Duration) ([]StreamEntry, error) {
	args := []string{"XREADGROUP", "GROUP", group, consumer}
	if count > 0 {
		args = append(args, "COUNT", strconv.Itoa(count))
	}
	if block >= 0 {
		args = append(args, "BLOCK", strconv.FormatInt(block.Milliseconds(), 10))
	}
	args = append(args, "STREAMS", stream, ">")
	rep, err := c.do(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseStreamRead(rep), nil
}

// XAck acknowledges processed entries for a consumer group.
func (c *Client) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := append([]string{"XACK", stream, group}, ids...)
	rep, err := c.do(ctx, args...)
	if err != nil {
		return 0, err
	}
	return rep.int64()
}

// parseStreamRead extracts entries from the nested XREADGROUP reply shape:
// [ [streamName, [ [id, [f1,v1,...]], ... ]] ].
func parseStreamRead(rep reply) []StreamEntry {
	if rep.isNil || len(rep.array) == 0 {
		return nil
	}
	var entries []StreamEntry
	for _, stream := range rep.array {
		if len(stream.array) < 2 {
			continue
		}
		for _, entry := range stream.array[1].array {
			if len(entry.array) < 2 {
				continue
			}
			id, _ := entry.array[0].status()
			fieldArr := entry.array[1].array
			fields := make(map[string]string, len(fieldArr)/2)
			for i := 0; i+1 < len(fieldArr); i += 2 {
				k, _ := fieldArr[i].status()
				v, _ := fieldArr[i+1].status()
				fields[k] = v
			}
			entries = append(entries, StreamEntry{ID: id, Fields: fields})
		}
	}
	return entries
}

// Snapshot returns client and pool counters.
func (c *Client) Snapshot() Stats {
	if c == nil {
		return Stats{}
	}
	active, idle := c.pool.snapshot()
	return Stats{
		Commands:    atomic.LoadInt64(&c.commands),
		Errors:      atomic.LoadInt64(&c.errors),
		ActiveConns: active,
		IdleConns:   idle,
	}
}
