// Package redis adapts go-redis/v9 to gofly's stable key-value contracts.
package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	redisv9 "github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/security"
)

// ErrClosed is returned when an operation is attempted on a closed client.
// It remains a gofly-owned sentinel so callers stay insulated from go-redis.
var ErrClosed = errors.New("redis: client is closed")

// ErrNil is returned when Redis replies with a nil bulk string or array.
// It remains distinct from redisv9.Nil so existing gofly cache callers do not
// need to import the underlying client implementation.
var ErrNil = errors.New("redis: nil reply")

// Error represents a Redis server error reply. It preserves the original
// adapter's exported error type so callers can classify server-side command
// failures without importing the go-redis implementation.
type Error struct {
	Message string
}

func (e *Error) Error() string { return "redis: " + e.Message }

var (
	// ErrEmptyAddress is returned when a Redis client has no usable endpoint.
	ErrEmptyAddress = errors.New("redis: address is required")
	// ErrClusterDatabase is returned because Redis Cluster does not support SELECT.
	ErrClusterDatabase = errors.New("redis: database must be zero in cluster mode")
	// ErrMaintenanceNotificationsProtocol is returned when maintenance
	// notifications are requested without RESP3.
	ErrMaintenanceNotificationsProtocol = errors.New("redis: maintenance notifications require protocol 3")
	// ErrHAConfiguration is returned when mutually exclusive Redis topology
	// settings are combined.
	ErrHAConfiguration = errors.New("redis: invalid high-availability configuration")
)

const (
	// MaintNotificationsDisabled avoids an additional connect-time command.
	MaintNotificationsDisabled = "disabled"
	// MaintNotificationsAuto attempts maintenance notification support and
	// downgrades it on a server-side error.
	MaintNotificationsAuto = "auto"
	// MaintNotificationsEnabled requires maintenance notification support.
	MaintNotificationsEnabled = "enabled"
)

// Config configures a Redis client. Addr preserves the original single-node
// configuration. Addrs and Cluster opt into the v9 cluster client.
type Config struct {
	Addr    string   `json:"addr"`
	Addrs   []string `json:"addrs,omitempty"`
	Cluster bool     `json:"cluster,omitempty"`
	// MasterName enables Redis Sentinel failover using Addrs as sentinel seeds.
	MasterName       string `json:"masterName,omitempty"`
	SentinelUsername string `json:"sentinelUsername,omitempty"`
	SentinelPassword string `json:"-"`
	// ReadOnly permits reads from replicas only when an explicit route policy is enabled.
	ReadOnly        bool               `json:"readOnly,omitempty"`
	RouteByLatency  bool               `json:"routeByLatency,omitempty"`
	RouteRandomly   bool               `json:"routeRandomly,omitempty"`
	Username        string             `json:"username,omitempty"`
	Password        string             `json:"-"`
	TLS             security.TLSConfig `json:"tls,omitempty"`
	Protocol        int                `json:"protocol,omitempty"`
	DisableIdentity bool               `json:"disableIdentity,omitempty"`
	// EnableIdentity re-enables v9 CLIENT SETINFO. It is disabled by default
	// for compatibility with legacy Redis servers and proxies.
	EnableIdentity bool `json:"enableIdentity,omitempty"`
	// MaintNotifications controls v9 CLIENT MAINT_NOTIFICATIONS. It defaults to
	// disabled because this adapter defaults to RESP2 for proxy compatibility.
	MaintNotifications string        `json:"maintNotifications,omitempty"`
	EagerConnect       bool          `json:"eagerConnect,omitempty"`
	PingTimeout        time.Duration `json:"pingTimeout,omitempty"`
	DB                 int           `json:"db,omitempty"`
	DialTimeout        time.Duration `json:"dialTimeout,omitempty"`
	Timeout            time.Duration `json:"timeout,omitempty"`
	MaxConns           int           `json:"maxConns,omitempty"`
	MaxIdleConns       int           `json:"maxIdleConns,omitempty"`
	ConnMaxIdleTime    time.Duration `json:"connMaxIdleTime,omitempty"`
	ConnMaxLifetime    time.Duration `json:"connMaxLifetime,omitempty"`
	MinIdleConns       int           `json:"minIdleConns,omitempty"`
	PoolTimeout        time.Duration `json:"poolTimeout,omitempty"`
	MaxRetries         int           `json:"maxRetries,omitempty"`
	// DisableBreaker opts out of the default Redis command breaker.
	DisableBreaker bool `json:"disableBreaker,omitempty"`
	// BreakerFailureThreshold is the number of consecutive failed Redis
	// commands before the adapter rejects further calls.
	BreakerFailureThreshold int `json:"breakerFailureThreshold,omitempty"`
	// BreakerOpenTimeout is the cool-down before a half-open Redis probe.
	BreakerOpenTimeout time.Duration `json:"breakerOpenTimeout,omitempty"`
}

// withDefaults fills zero fields with sensible defaults.
func (c Config) withDefaults() Config {
	c.Addr = strings.TrimSpace(c.Addr)
	c.MasterName = strings.TrimSpace(c.MasterName)
	hadAddrs := len(c.Addrs) > 0
	c.Addrs = normalizeAddrs(c.Addrs)
	if len(c.Addrs) == 0 && !hadAddrs && c.Addr == "" {
		c.Addr = "127.0.0.1:6379"
	}
	if len(c.Addrs) == 0 && !hadAddrs {
		c.Addrs = []string{c.Addr}
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 3 * time.Second
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = time.Second
	}
	if c.Protocol == 0 {
		c.Protocol = 2
	}
	if c.MaintNotifications == "" {
		c.MaintNotifications = MaintNotificationsDisabled
	} else {
		c.MaintNotifications = strings.ToLower(strings.TrimSpace(c.MaintNotifications))
	}
	if !c.EnableIdentity {
		c.DisableIdentity = true
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
	if c.PoolTimeout <= 0 {
		c.PoolTimeout = c.Timeout
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = -1
	}
	if !c.DisableBreaker {
		if c.BreakerFailureThreshold <= 0 {
			c.BreakerFailureThreshold = 3
		}
		if c.BreakerOpenTimeout <= 0 {
			c.BreakerOpenTimeout = time.Second
		}
	}
	return c
}

// Validate checks Redis configuration without connecting to the server.
func (c Config) Validate() error {
	if len(c.Addrs) == 0 && strings.TrimSpace(c.Addr) == "" {
		return ErrEmptyAddress
	}
	for _, addr := range c.Addrs {
		if strings.TrimSpace(addr) == "" {
			return ErrEmptyAddress
		}
	}
	if (c.Cluster || len(c.Addrs) > 1) && c.DB != 0 {
		return ErrClusterDatabase
	}
	if c.Cluster && c.MasterName != "" {
		return fmt.Errorf("%w: cluster and masterName cannot be combined", ErrHAConfiguration)
	}
	if c.RouteByLatency && c.RouteRandomly {
		return fmt.Errorf("%w: routeByLatency and routeRandomly cannot both be enabled", ErrHAConfiguration)
	}
	if c.ReadOnly && !c.Cluster && c.MasterName == "" {
		return fmt.Errorf("%w: readOnly requires cluster or masterName", ErrHAConfiguration)
	}
	if c.Protocol != 0 && c.Protocol != 2 && c.Protocol != 3 {
		return fmt.Errorf("redis: unsupported protocol %d", c.Protocol)
	}
	mode := maintnotifications.Mode(strings.ToLower(strings.TrimSpace(c.MaintNotifications)))
	if !mode.IsValid() {
		return fmt.Errorf("redis: unsupported maintenance notifications mode %q", c.MaintNotifications)
	}
	if mode != maintnotifications.ModeDisabled && c.Protocol != 3 {
		return ErrMaintenanceNotificationsProtocol
	}
	if mode != maintnotifications.ModeDisabled && c.MasterName != "" {
		return fmt.Errorf("%w: maintenance notifications are unsupported with masterName", ErrHAConfiguration)
	}
	return nil
}

func normalizeAddrs(addrs []string) []string {
	if len(addrs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(addrs))
	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		result = append(result, addr)
	}
	return result
}

// Stats reports client-level command and v9 pool counters.
type Stats struct {
	Commands     int64  `json:"commands"`
	Errors       int64  `json:"errors"`
	ActiveConns  int    `json:"activeConns"`
	IdleConns    int    `json:"idleConns"`
	PoolHits     uint32 `json:"poolHits"`
	PoolMisses   uint32 `json:"poolMisses"`
	PoolTimeouts uint32 `json:"poolTimeouts"`
	StaleConns   uint32 `json:"staleConns"`
	WaitCount    uint32 `json:"waitCount"`
}

// DiagnosticsSnapshot is a security-safe Redis runtime view for an admin or
// control-plane surface. It intentionally omits passwords, seed addresses, TLS
// certificate paths, and command arguments.
type DiagnosticsSnapshot struct {
	Topology           string                  `json:"topology"`
	SeedCount          int                     `json:"seedCount"`
	Protocol           int                     `json:"protocol"`
	MaintNotifications string                  `json:"maintNotifications"`
	ReadOnly           bool                    `json:"readOnly"`
	RouteByLatency     bool                    `json:"routeByLatency"`
	RouteRandomly      bool                    `json:"routeRandomly"`
	TLSConfigured      bool                    `json:"tlsConfigured"`
	IdentityEnabled    bool                    `json:"identityEnabled"`
	EagerConnect       bool                    `json:"eagerConnect"`
	Closed             bool                    `json:"closed"`
	Breaker            breaker.BreakerSnapshot `json:"breaker"`
	Pool               Stats                   `json:"pool"`
}

// Client is a go-redis/v9-backed client that preserves the gofly Redis API.
// The public adapter deliberately keeps callers insulated from v9 command
// types, connection-pool internals, and Redis protocol negotiation details.
type Client struct {
	cfg      Config
	client   redisv9.UniversalClient
	breaker  *breaker.Breaker
	commands atomic.Int64
	errors   atomic.Int64
	closed   atomic.Bool
}

// New creates a Redis client without connecting eagerly; use Ping to verify
// connectivity. Existing Addr configurations create a single-node client.
// Multiple Addrs or Cluster create a Redis Cluster client.
func New(cfg Config) *Client {
	cfg = cfg.withDefaults()
	client, err := newClient(cfg)
	if err != nil {
		return &Client{cfg: cfg}
	}
	return client
}

// NewChecked validates cfg and optionally verifies connectivity when
// EagerConnect is enabled. New remains available for lazy, compatibility-safe
// construction.
func NewChecked(ctx context.Context, cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	if !cfg.EagerConnect {
		return client, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: eager ping: %w", err)
	}
	return client, nil
}

func newClient(cfg Config) (*Client, error) {
	tlsConfig, err := cfg.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("redis: TLS config: %w", err)
	}
	client := &Client{
		cfg:    cfg,
		client: redisv9.NewUniversalClient(redisOptions(cfg, tlsConfig)),
	}
	client.client.AddHook(redisObservabilityHook{})
	if !cfg.DisableBreaker {
		client.breaker = breaker.New(
			breaker.WithFailureThreshold(cfg.BreakerFailureThreshold),
			breaker.WithOpenTimeout(cfg.BreakerOpenTimeout),
		)
		client.client.AddHook(redisBreakerHook{breaker: client.breaker})
	}
	return client, nil
}

func redisOptions(cfg Config, tlsConfig *tls.Config) *redisv9.UniversalOptions {
	return &redisv9.UniversalOptions{
		Addrs:                 append([]string(nil), cfg.Addrs...),
		MasterName:            cfg.MasterName,
		SentinelUsername:      cfg.SentinelUsername,
		SentinelPassword:      cfg.SentinelPassword,
		ReadOnly:              cfg.ReadOnly,
		RouteByLatency:        cfg.RouteByLatency,
		RouteRandomly:         cfg.RouteRandomly,
		DB:                    cfg.DB,
		Username:              cfg.Username,
		Password:              cfg.Password,
		Protocol:              cfg.Protocol,
		DialTimeout:           cfg.DialTimeout,
		ReadTimeout:           cfg.Timeout,
		WriteTimeout:          cfg.Timeout,
		ContextTimeoutEnabled: true,
		MaxRetries:            cfg.MaxRetries,
		DialerRetries:         1,
		PoolSize:              cfg.MaxConns,
		MinIdleConns:          cfg.MinIdleConns,
		MaxIdleConns:          cfg.MaxIdleConns,
		ConnMaxIdleTime:       cfg.ConnMaxIdleTime,
		ConnMaxLifetime:       cfg.ConnMaxLifetime,
		PoolTimeout:           cfg.PoolTimeout,
		DisableIdentity:       cfg.DisableIdentity,
		TLSConfig:             tlsConfig,
		IsClusterMode:         cfg.Cluster,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: maintnotifications.Mode(cfg.MaintNotifications),
		},
	}
}

// NewBlockingClient creates a small, dedicated v9 client for blocking Redis
// commands such as XREADGROUP. It must be closed by the caller. It is kept
// separate from the ordinary command pool so long polls cannot starve writes.
func (c *Client) NewBlockingClient(ctx context.Context, readTimeout time.Duration, poolSize int) (*Client, error) {
	if c == nil {
		return nil, ErrClosed
	}
	if poolSize <= 0 {
		poolSize = 1
	}
	if readTimeout <= 0 {
		readTimeout = c.cfg.Timeout
	}
	cfg := c.cfg
	cfg.MaxConns = poolSize
	cfg.MaxIdleConns = poolSize
	cfg.MinIdleConns = poolSize
	cfg.Timeout = readTimeout
	cfg.PoolTimeout = readTimeout
	cfg.EagerConnect = false
	return NewChecked(ctx, cfg)
}

func (c *Client) command(ctx context.Context) (context.Context, redisv9.UniversalClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if c == nil || c.client == nil || c.closed.Load() {
		return nil, nil, ErrClosed
	}
	c.commands.Add(1)
	return ctx, c.client, nil
}

func (c *Client) finish(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, redisv9.Nil) {
		return ErrNil
	}
	if errors.Is(err, redisv9.ErrClosed) {
		return ErrClosed
	}
	if c != nil {
		c.errors.Add(1)
	}
	var redisErr redisv9.Error
	if errors.As(err, &redisErr) {
		return &Error{Message: redisErr.Error()}
	}
	return err
}

// Ping verifies connectivity with the server.
func (c *Client) Ping(ctx context.Context) error {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return err
	}
	status, err := client.Ping(ctx).Result()
	if err = c.finish(err); err != nil {
		return err
	}
	if !strings.EqualFold(status, "PONG") {
		return fmt.Errorf("redis: unexpected PING reply: %s", status)
	}
	return nil
}

// Get returns the raw value stored at key, or ErrNil when it is missing.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return nil, err
	}
	value, err := client.Get(ctx, key).Bytes()
	return value, c.finish(err)
}

// Set stores value at key with an optional TTL.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return err
	}
	return c.finish(client.Set(ctx, key, value, ttl).Err())
}

// SetNX stores value at key only when it does not already exist.
func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return false, err
	}
	stored, err := client.SetNX(ctx, key, value, ttl).Result()
	return stored, c.finish(err)
}

// Delete removes key and reports whether it existed.
func (c *Client) Delete(ctx context.Context, key string) (bool, error) {
	n, err := c.Del(ctx, key)
	return n > 0, err
}

// Exists reports whether key exists.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return false, err
	}
	n, err := client.Exists(ctx, key).Result()
	return n > 0, c.finish(err)
}

// TTL returns the remaining time to live of key. Persistent keys return zero.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	ttl, err := client.PTTL(ctx, key).Result()
	if err = c.finish(err); err != nil {
		return 0, err
	}
	switch ttl {
	case -2:
		return 0, ErrNil
	case -1:
		return 0, nil
	default:
		return ttl, nil
	}
}

// Close releases v9 connection-pool resources. It is safe to call repeatedly.
func (c *Client) Close() error {
	if c == nil || c.client == nil || !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	err := c.client.Close()
	if errors.Is(err, redisv9.ErrClosed) {
		return nil
	}
	return err
}

// Del removes one or more keys and returns the count removed.
func (c *Client) Del(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	n, err := client.Del(ctx, keys...).Result()
	return n, c.finish(err)
}

// Incr increments the integer value stored at key by one.
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	n, err := client.Incr(ctx, key).Result()
	return n, c.finish(err)
}

// IncrBy increments the integer value stored at key by delta.
func (c *Client) IncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	n, err := client.IncrBy(ctx, key, delta).Result()
	return n, c.finish(err)
}

// Expire sets a TTL on key and reports whether the timeout was set.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = time.Second
	} else {
		ttl = (ttl + time.Second - 1) / time.Second * time.Second
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return false, err
	}
	changed, err := client.Expire(ctx, key, ttl).Result()
	return changed, c.finish(err)
}

// PExpire sets a millisecond-precision TTL on key.
func (c *Client) PExpire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 || ttl < time.Millisecond {
		ttl = time.Millisecond
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return false, err
	}
	changed, err := client.PExpire(ctx, key, ttl).Result()
	return changed, c.finish(err)
}

// SetBit sets or clears the bit at offset and returns the original bit value.
func (c *Client) SetBit(ctx context.Context, key string, offset uint64, value int) (int64, error) {
	if offset > ^uint64(0)>>1 {
		return 0, fmt.Errorf("redis: bit offset %d exceeds int64", offset)
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	n, err := client.SetBit(ctx, key, int64(offset), value).Result()
	return n, c.finish(err)
}

// GetBit returns the bit value at offset.
func (c *Client) GetBit(ctx context.Context, key string, offset uint64) (int64, error) {
	if offset > ^uint64(0)>>1 {
		return 0, fmt.Errorf("redis: bit offset %d exceeds int64", offset)
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	n, err := client.GetBit(ctx, key, int64(offset)).Result()
	return n, c.finish(err)
}

// Eval runs a Lua script and returns an integer result.
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...string) (int64, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	values := make([]interface{}, len(args))
	for i := range args {
		values[i] = args[i]
	}
	result, err := client.Eval(ctx, script, keys, values...).Result()
	if err = c.finish(err); err != nil {
		return 0, err
	}
	return redisInteger(result)
}

func redisInteger(value interface{}) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case string:
		return strconv.ParseInt(value, 10, 64)
	case []byte:
		return strconv.ParseInt(string(value), 10, 64)
	default:
		return 0, fmt.Errorf("redis: expected integer EVAL reply, got %T", value)
	}
}

// StreamEntry is a single entry read from a Redis stream.
type StreamEntry struct {
	ID     string
	Fields map[string]string
}

// XAdd appends an entry to a stream and returns its generated ID. A maxLen of
// >0 trims the stream approximately to that length.
func (c *Client) XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return "", err
	}
	id, err := client.XAdd(ctx, &redisv9.XAddArgs{
		Stream: stream, MaxLen: maxLen, Approx: maxLen > 0, Values: fields,
	}).Result()
	return id, c.finish(err)
}

// XGroupCreate creates a consumer group, optionally creating the stream
// (MKSTREAM). BUSYGROUP is treated as success so calls remain idempotent.
func (c *Client) XGroupCreate(ctx context.Context, stream, group, start string, mkStream bool) error {
	if start == "" {
		start = "$"
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return err
	}
	if mkStream {
		err = client.XGroupCreateMkStream(ctx, stream, group, start).Err()
	} else {
		err = client.XGroupCreate(ctx, stream, group, start).Err()
	}
	if err != nil && strings.Contains(strings.ToUpper(err.Error()), "BUSYGROUP") {
		return nil
	}
	return c.finish(err)
}

// XReadGroup reads entries for a consumer from a stream consumer group.
func (c *Client) XReadGroup(ctx context.Context, group, consumer, stream string, count int, block time.Duration) ([]StreamEntry, error) {
	ctx, client, err := c.command(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := client.XReadGroup(ctx, &redisv9.XReadGroupArgs{
		Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: int64(count), Block: block,
	}).Result()
	// go-redis uses redis.Nil for a timeout or an empty stream read. The
	// previous gofly client returned an empty entry list without an error.
	if errors.Is(err, redisv9.Nil) {
		return nil, nil
	}
	if err = c.finish(err); err != nil {
		return nil, err
	}
	return flattenStreams(entries), nil
}

func flattenStreams(streams []redisv9.XStream) []StreamEntry {
	var entries []StreamEntry
	for _, stream := range streams {
		for _, message := range stream.Messages {
			fields := make(map[string]string, len(message.Values))
			for key, value := range message.Values {
				fields[key] = fmt.Sprint(value)
			}
			entries = append(entries, StreamEntry{ID: message.ID, Fields: fields})
		}
	}
	return entries
}

// XAck acknowledges processed entries for a consumer group.
func (c *Client) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ctx, client, err := c.command(ctx)
	if err != nil {
		return 0, err
	}
	n, err := client.XAck(ctx, stream, group, ids...).Result()
	return n, c.finish(err)
}

// Snapshot returns gofly command counters and the v9 pool state.
func (c *Client) Snapshot() Stats {
	if c == nil {
		return Stats{}
	}
	stats := Stats{Commands: c.commands.Load(), Errors: c.errors.Load()}
	if c.client == nil {
		return stats
	}
	pool := c.client.PoolStats()
	if pool == nil {
		return stats
	}
	stats.IdleConns = int(pool.IdleConns)
	stats.PoolHits = pool.Hits
	stats.PoolMisses = pool.Misses
	stats.PoolTimeouts = pool.Timeouts
	stats.StaleConns = pool.StaleConns
	stats.WaitCount = pool.WaitCount
	if pool.TotalConns >= pool.IdleConns {
		stats.ActiveConns = int(pool.TotalConns - pool.IdleConns)
	}
	return stats
}

// DiagnosticsSnapshot returns safe topology, routing, breaker, and pool state
// without exposing Redis credentials, endpoints, or command payloads.
func (c *Client) DiagnosticsSnapshot() DiagnosticsSnapshot {
	if c == nil {
		return DiagnosticsSnapshot{}
	}
	snapshot := DiagnosticsSnapshot{
		Topology:           redisTopology(c.cfg),
		SeedCount:          len(c.cfg.Addrs),
		Protocol:           c.cfg.Protocol,
		MaintNotifications: c.cfg.MaintNotifications,
		ReadOnly:           c.cfg.ReadOnly,
		RouteByLatency:     c.cfg.RouteByLatency,
		RouteRandomly:      c.cfg.RouteRandomly,
		TLSConfigured:      redisTLSConfigured(c.cfg.TLS),
		IdentityEnabled:    !c.cfg.DisableIdentity,
		EagerConnect:       c.cfg.EagerConnect,
		Closed:             c.closed.Load(),
		Pool:               c.Snapshot(),
	}
	if c.breaker != nil {
		snapshot.Breaker = c.breaker.Snapshot()
	}
	return snapshot
}

func redisTopology(cfg Config) string {
	switch {
	case cfg.MasterName != "" && (cfg.RouteByLatency || cfg.RouteRandomly):
		return "sentinel-cluster"
	case cfg.MasterName != "":
		return "sentinel"
	case cfg.Cluster || len(cfg.Addrs) > 1:
		return "cluster"
	default:
		return "node"
	}
}

func redisTLSConfigured(cfg security.TLSConfig) bool {
	return cfg.Enabled() || strings.TrimSpace(cfg.CAFile) != "" ||
		strings.TrimSpace(cfg.ServerName) != "" || cfg.InsecureSkipVerify
}
