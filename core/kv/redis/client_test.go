package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	redisv9 "github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"

	"github.com/imajinyun/gofly/core/security"
)

// mockServer deliberately implements only the Redis commands exposed through
// the gofly adapter. It forces go-redis's RESP2 fallback by rejecting HELLO,
// covering compatibility with legacy Redis servers and proxies.
type mockServer struct {
	ln     net.Listener
	mu     sync.Mutex
	data   map[string][]byte
	pttl   map[string]int64
	groups map[string]bool
}

func newMockServer(t *testing.T) *mockServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockServer{
		ln:     ln,
		data:   make(map[string][]byte),
		pttl:   make(map[string]int64),
		groups: make(map[string]bool),
	}
	go s.serve()
	return s
}

func (s *mockServer) addr() string { return s.ln.Addr().String() }

func (s *mockServer) close() { _ = s.ln.Close() }

func (s *mockServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *mockServer) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	for {
		args, err := readRequest(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		s.dispatch(writer, args)
		if err := writer.Flush(); err != nil {
			return
		}
	}
}

func (s *mockServer) dispatch(w *bufio.Writer, args []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch strings.ToUpper(args[0]) {
	case "HELLO":
		writeError(w, "ERR unknown command 'HELLO'")
	case "PING":
		writeStatus(w, "PONG")
	case "AUTH", "SELECT":
		writeStatus(w, "OK")
	case "SET":
		if hasArg(args, "NX") {
			if _, ok := s.data[args[1]]; ok {
				writeNil(w)
				return
			}
		}
		s.data[args[1]] = []byte(args[2])
		delete(s.pttl, args[1])
		if px := argAfter(args, "PX"); px != "" {
			ms, _ := strconv.ParseInt(px, 10, 64)
			s.pttl[args[1]] = ms
		}
		if ex := argAfter(args, "EX"); ex != "" {
			seconds, _ := strconv.ParseInt(ex, 10, 64)
			s.pttl[args[1]] = seconds * 1000
		}
		writeStatus(w, "OK")
	case "GET":
		if value, ok := s.data[args[1]]; ok {
			writeBulk(w, value)
		} else {
			writeNil(w)
		}
	case "DEL":
		n := int64(0)
		for _, key := range args[1:] {
			if _, ok := s.data[key]; ok {
				delete(s.data, key)
				delete(s.pttl, key)
				n++
			}
		}
		writeInt(w, n)
	case "EXISTS":
		if _, ok := s.data[args[1]]; ok {
			writeInt(w, 1)
		} else {
			writeInt(w, 0)
		}
	case "INCR":
		writeInt(w, s.incrBy(args[1], 1))
	case "INCRBY":
		delta, _ := strconv.ParseInt(args[2], 10, 64)
		writeInt(w, s.incrBy(args[1], delta))
	case "EXPIRE", "PEXPIRE":
		if _, ok := s.data[args[1]]; !ok {
			writeInt(w, 0)
			return
		}
		ms, _ := strconv.ParseInt(args[2], 10, 64)
		if strings.EqualFold(args[0], "EXPIRE") {
			ms *= 1000
		}
		s.pttl[args[1]] = ms
		writeInt(w, 1)
	case "PTTL":
		if _, ok := s.data[args[1]]; !ok {
			writeInt(w, -2)
		} else if ttl, ok := s.pttl[args[1]]; ok {
			writeInt(w, ttl)
		} else {
			writeInt(w, -1)
		}
	case "SETBIT":
		offset, _ := strconv.ParseUint(args[2], 10, 64)
		previous := s.getBit(args[1], offset)
		s.setBit(args[1], offset, args[3] == "1")
		writeInt(w, int64(previous))
	case "GETBIT":
		offset, _ := strconv.ParseUint(args[2], 10, 64)
		writeInt(w, int64(s.getBit(args[1], offset)))
	case "EVAL":
		if strings.Contains(args[1], "bad") {
			writeError(w, "ERR script error")
		} else {
			writeInt(w, 42)
		}
	case "XADD":
		writeBulk(w, []byte("1700000000000-0"))
	case "XGROUP":
		groupKey := args[2] + ":" + args[3]
		if s.groups[groupKey] {
			writeError(w, "BUSYGROUP Consumer Group name already exists")
			return
		}
		s.groups[groupKey] = true
		writeStatus(w, "OK")
	case "XREADGROUP":
		stream := args[len(args)-2]
		if stream == "empty" {
			writeNullArray(w)
		} else {
			writeXReadGroup(w, stream)
		}
	case "XACK":
		writeInt(w, int64(len(args)-3))
	default:
		writeError(w, "ERR unknown command")
	}
}

func (s *mockServer) incrBy(key string, delta int64) int64 {
	current, _ := strconv.ParseInt(string(s.data[key]), 10, 64)
	current += delta
	s.data[key] = []byte(strconv.FormatInt(current, 10))
	return current
}

func (s *mockServer) setBit(key string, offset uint64, set bool) {
	byteIndex := offset / 8
	bitIndex := 7 - (offset % 8)
	value := s.data[key]
	for uint64(len(value)) <= byteIndex {
		value = append(value, 0)
	}
	if set {
		value[byteIndex] |= 1 << bitIndex
	} else {
		value[byteIndex] &^= 1 << bitIndex
	}
	s.data[key] = value
}

func (s *mockServer) getBit(key string, offset uint64) int {
	byteIndex := offset / 8
	bitIndex := 7 - (offset % 8)
	value := s.data[key]
	if uint64(len(value)) <= byteIndex {
		return 0
	}
	return int((value[byteIndex] >> bitIndex) & 1)
}

func readRequest(r *bufio.Reader) ([]string, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if prefix != '*' {
		return nil, errors.New("unexpected RESP request prefix")
	}
	count, err := readRESPInt(r)
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for range count {
		if prefix, err := r.ReadByte(); err != nil || prefix != '$' {
			return nil, errors.New("expected RESP bulk string")
		}
		length, err := readRESPInt(r)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:length]))
	}
	return args, nil
}

func readRESPInt(r *bufio.Reader) (int, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(line))
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

func writeStatus(w *bufio.Writer, value string) { _, _ = w.WriteString("+" + value + "\r\n") }
func writeError(w *bufio.Writer, value string)  { _, _ = w.WriteString("-" + value + "\r\n") }
func writeNil(w *bufio.Writer)                  { _, _ = w.WriteString("$-1\r\n") }
func writeNullArray(w *bufio.Writer)            { _, _ = w.WriteString("*-1\r\n") }
func writeInt(w *bufio.Writer, value int64) {
	_, _ = w.WriteString(":" + strconv.FormatInt(value, 10) + "\r\n")
}
func writeBulk(w *bufio.Writer, value []byte) {
	_, _ = w.WriteString("$" + strconv.Itoa(len(value)) + "\r\n")
	_, _ = w.Write(value)
	_, _ = w.WriteString("\r\n")
}

func writeXReadGroup(w *bufio.Writer, stream string) {
	_, _ = w.WriteString("*1\r\n*2\r\n")
	writeBulk(w, []byte(stream))
	_, _ = w.WriteString("*1\r\n*2\r\n")
	writeBulk(w, []byte("1700000000000-0"))
	_, _ = w.WriteString("*4\r\n")
	writeBulk(w, []byte("field"))
	writeBulk(w, []byte("value"))
	writeBulk(w, []byte("trace"))
	writeBulk(w, []byte("abc"))
}

func newTestClient(t *testing.T) (*Client, *mockServer) {
	t.Helper()
	server := newMockServer(t)
	client := New(Config{Addr: server.addr(), Timeout: time.Second})
	t.Cleanup(func() {
		_ = client.Close()
		server.close()
	})
	return client, server
}

func TestClientKeyValueCompatibility(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := client.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := client.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("Get = %q, %v; want v, nil", got, err)
	}
	stored, err := client.SetNX(ctx, "k", []byte("next"), time.Second)
	if err != nil || stored {
		t.Fatalf("duplicate SetNX = %v, %v; want false, nil", stored, err)
	}
	exists, err := client.Exists(ctx, "k")
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true, nil", exists, err)
	}
	removed, err := client.Delete(ctx, "k")
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v; want true, nil", removed, err)
	}
	if _, err := client.Get(ctx, "k"); !errors.Is(err, ErrNil) {
		t.Fatalf("Get missing error = %v, want ErrNil", err)
	}
}

func TestClientExpiryAndBitCompatibility(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if _, err := client.TTL(ctx, "missing"); !errors.Is(err, ErrNil) {
		t.Fatalf("TTL missing error = %v, want ErrNil", err)
	}
	if err := client.Set(ctx, "persisted", []byte("v"), 0); err != nil {
		t.Fatalf("Set persisted: %v", err)
	}
	if ttl, err := client.TTL(ctx, "persisted"); err != nil || ttl != 0 {
		t.Fatalf("persistent TTL = %s, %v; want 0, nil", ttl, err)
	}
	if changed, err := client.Expire(ctx, "persisted", 1500*time.Millisecond); err != nil || !changed {
		t.Fatalf("Expire = %v, %v; want true, nil", changed, err)
	}
	if ttl, err := client.TTL(ctx, "persisted"); err != nil || ttl != 2*time.Second {
		t.Fatalf("Expire TTL = %s, %v; want 2s, nil", ttl, err)
	}
	if changed, err := client.PExpire(ctx, "persisted", 0); err != nil || !changed {
		t.Fatalf("PExpire = %v, %v; want true, nil", changed, err)
	}
	if ttl, err := client.TTL(ctx, "persisted"); err != nil || ttl != time.Millisecond {
		t.Fatalf("PExpire TTL = %s, %v; want 1ms, nil", ttl, err)
	}
	if _, err := client.SetBit(ctx, "bits", 9, 1); err != nil {
		t.Fatalf("SetBit: %v", err)
	}
	if bit, err := client.GetBit(ctx, "bits", 9); err != nil || bit != 1 {
		t.Fatalf("GetBit = %d, %v; want 1, nil", bit, err)
	}
	if _, err := client.GetBit(ctx, "bits", ^uint64(0)); err == nil {
		t.Fatal("GetBit overflowing offset: want error")
	}
}

func TestClientExtendedCommandCompatibility(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	if n, err := client.Incr(ctx, "counter"); err != nil || n != 1 {
		t.Fatalf("Incr = %d, %v; want 1, nil", n, err)
	}
	if n, err := client.IncrBy(ctx, "counter", 4); err != nil || n != 5 {
		t.Fatalf("IncrBy = %d, %v; want 5, nil", n, err)
	}
	if n, err := client.Eval(ctx, "return 42", []string{"k1"}, "arg"); err != nil || n != 42 {
		t.Fatalf("Eval = %d, %v; want 42, nil", n, err)
	}
	if _, err := client.Eval(ctx, "bad", nil); err == nil {
		t.Fatal("Eval bad script: want error")
	} else {
		var redisErr *Error
		if !errors.As(err, &redisErr) || redisErr.Message != "ERR script error" {
			t.Fatalf("Eval bad script error = %T %[1]v, want compatible *Error", err)
		}
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping after Redis error: %v", err)
	}
	if id, err := client.XAdd(ctx, "events", 100, map[string]string{"field": "value"}); err != nil || id != "1700000000000-0" {
		t.Fatalf("XAdd = %q, %v; want generated id, nil", id, err)
	}
	if err := client.XGroupCreate(ctx, "events", "workers", "", true); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if err := client.XGroupCreate(ctx, "events", "workers", "0", false); err != nil {
		t.Fatalf("duplicate XGroupCreate: %v", err)
	}
	entries, err := client.XReadGroup(ctx, "workers", "c1", "events", 1, time.Millisecond)
	if err != nil || len(entries) != 1 || entries[0].Fields["trace"] != "abc" {
		t.Fatalf("XReadGroup = %#v, %v; want one entry", entries, err)
	}
	empty, err := client.XReadGroup(ctx, "workers", "c1", "empty", 1, time.Millisecond)
	if err != nil || empty != nil {
		t.Fatalf("empty XReadGroup = %#v, %v; want nil, nil", empty, err)
	}
	if n, err := client.XAck(ctx, "events", "workers", entries[0].ID); err != nil || n != 1 {
		t.Fatalf("XAck = %d, %v; want 1, nil", n, err)
	}
	if n, err := client.XAck(ctx, "events", "workers"); err != nil || n != 0 {
		t.Fatalf("empty XAck = %d, %v; want 0, nil", n, err)
	}
}

func TestClientConfigurationAndPoolSnapshot(t *testing.T) {
	config := Config{}.withDefaults()
	if config.Addr != "127.0.0.1:6379" || len(config.Addrs) != 1 || config.Addrs[0] != config.Addr {
		t.Fatalf("defaults = %#v; want default standalone address", config)
	}
	options := redisOptions(Config{Addr: "node:6379", Cluster: true}.withDefaults(), nil)
	if options.Protocol != 2 || !options.ContextTimeoutEnabled || !options.DisableIdentity || !options.IsClusterMode {
		t.Fatalf("v9 options must preserve RESP2, context deadline, identity, cluster settings: %#v", options)
	}
	if config.DisableBreaker || config.BreakerAdaptive || config.BreakerFailureThreshold != 3 || config.BreakerOpenTimeout != time.Second {
		t.Fatalf("breaker defaults = %#v; want enabled threshold 3 and 1s cooldown", config)
	}
	if config.SlowThreshold != 100*time.Millisecond {
		t.Fatalf("slow threshold default = %v, want 100ms", config.SlowThreshold)
	}
	cluster := New(Config{Addrs: []string{"a:6379", "b:6379"}})
	defer cluster.Close()
	if _, ok := cluster.client.(*redisv9.ClusterClient); !ok {
		t.Fatalf("multiple Addrs client = %T, want *redis.ClusterClient", cluster.client)
	}
	if cluster.breaker == nil {
		t.Fatal("default Redis client must install the breaker hook")
	}
	withoutBreaker := New(Config{DisableBreaker: true})
	defer withoutBreaker.Close()
	if withoutBreaker.breaker != nil {
		t.Fatal("DisableBreaker client must not install a breaker")
	}
	adaptive := New(Config{BreakerAdaptive: true})
	defer adaptive.Close()
	if adaptive.googleBreaker == nil || adaptive.breaker != nil {
		t.Fatalf("adaptive Redis breaker = google:%v consecutive:%v, want Google SRE only", adaptive.googleBreaker != nil, adaptive.breaker != nil)
	}
	if mode := adaptive.DiagnosticsSnapshot().BreakerMode; mode != "adaptive" {
		t.Fatalf("adaptive diagnostics breaker mode = %q, want adaptive", mode)
	}
	client, _ := newTestClient(t)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	stats := client.Snapshot()
	if stats.Commands == 0 || stats.IdleConns == 0 {
		t.Fatalf("Snapshot = %+v; want commands and idle v9 pool connection", stats)
	}
}

func TestConfigValidationAndBlockingClient(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{name: "empty cluster address", cfg: Config{Addrs: []string{" "}}, want: ErrEmptyAddress},
		{name: "cluster database", cfg: Config{Addr: "node:6379", Cluster: true, DB: 1}, want: ErrClusterDatabase},
		{name: "unsupported protocol", cfg: Config{Addr: "node:6379", Protocol: 1}, want: errors.New("unsupported protocol")},
		{name: "RESP2 maintenance notifications", cfg: Config{Addr: "node:6379", MaintNotifications: MaintNotificationsAuto}, want: ErrMaintenanceNotificationsProtocol},
		{name: "invalid maintenance notifications", cfg: Config{Addr: "node:6379", Protocol: 3, MaintNotifications: "invalid"}, want: errors.New("unsupported maintenance notifications")},
		{name: "cluster sentinel conflict", cfg: Config{Addr: "node:6379", Cluster: true, MasterName: "master"}, want: ErrHAConfiguration},
		{name: "conflicting replica routes", cfg: Config{Addr: "node:6379", Cluster: true, RouteByLatency: true, RouteRandomly: true}, want: ErrHAConfiguration},
		{name: "standalone replica route", cfg: Config{Addr: "node:6379", ReadOnly: true}, want: ErrHAConfiguration},
		{name: "sentinel maintenance notifications", cfg: Config{Addr: "node:6379", MasterName: "master", Protocol: 3, MaintNotifications: MaintNotificationsAuto}, want: ErrHAConfiguration},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewChecked(context.Background(), tt.cfg); err == nil || (tt.want != nil && !errors.Is(err, tt.want) && !strings.Contains(err.Error(), tt.want.Error())) {
				t.Fatalf("NewChecked error = %v, want %v", err, tt.want)
			}
		})
	}

	client, _ := newTestClient(t)
	blocking, err := client.NewBlockingClient(context.Background(), 2*time.Second, 1)
	if err != nil {
		t.Fatalf("NewBlockingClient: %v", err)
	}
	t.Cleanup(func() { _ = blocking.Close() })
	if blocking.cfg.MaxConns != 1 || blocking.cfg.MaxIdleConns != 1 || blocking.cfg.MinIdleConns != 1 || blocking.cfg.Timeout != 2*time.Second {
		t.Fatalf("blocking config = %#v, want dedicated one-connection pool", blocking.cfg)
	}
}

func TestConfigNormalizesClusterAddressesAndMaintenanceMode(t *testing.T) {
	config := Config{
		Addrs:              []string{" node-a:6379 ", "node-b:6379", "node-a:6379", " "},
		Protocol:           3,
		MaintNotifications: "AUTO",
	}.withDefaults()
	if got, want := config.Addrs, []string{"node-a:6379", "node-b:6379"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized Addrs = %#v, want %#v", got, want)
	}
	if config.MaintNotifications != MaintNotificationsAuto {
		t.Fatalf("maintenance mode = %q, want auto", config.MaintNotifications)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("normalized config Validate: %v", err)
	}
	options := redisOptions(config, nil)
	if options.MaintNotificationsConfig == nil || options.MaintNotificationsConfig.Mode != maintnotifications.ModeAuto {
		t.Fatalf("v9 maintenance config = %#v, want auto", options.MaintNotificationsConfig)
	}
}

func TestConfigMapsHAOptionsToV9(t *testing.T) {
	config := Config{
		Addrs:            []string{"sentinel-a:26379", "sentinel-b:26379"},
		MasterName:       "mymaster",
		SentinelUsername: "sentinel-user",
		SentinelPassword: "sentinel-password",
		ReadOnly:         true,
		RouteByLatency:   true,
	}.withDefaults()
	if err := config.Validate(); err != nil {
		t.Fatalf("HA config Validate: %v", err)
	}
	options := redisOptions(config, nil)
	if options.MasterName != "mymaster" || options.SentinelUsername != "sentinel-user" || options.SentinelPassword != "sentinel-password" || !options.ReadOnly || !options.RouteByLatency || options.RouteRandomly {
		t.Fatalf("v9 HA options = %#v", options)
	}
	client := New(config)
	defer client.Close()
	if _, ok := client.client.(*redisv9.ClusterClient); !ok {
		t.Fatalf("routed Sentinel client = %T, want *redis.ClusterClient", client.client)
	}
	plainFailover := New(Config{Addrs: []string{"sentinel-a:26379"}, MasterName: "mymaster"})
	defer plainFailover.Close()
	if _, ok := plainFailover.client.(*redisv9.Client); !ok {
		t.Fatalf("plain Sentinel client = %T, want *redis.Client", plainFailover.client)
	}
}

func TestDiagnosticsSnapshotSanitizesRedisConfiguration(t *testing.T) {
	client := New(Config{
		Addrs:            []string{"sentinel-a:26379", "sentinel-b:26379"},
		MasterName:       "mymaster",
		Password:         "redis-password",
		SentinelPassword: "sentinel-password",
		Protocol:         3,
		ReadOnly:         true,
		RouteRandomly:    true,
		TLS:              security.TLSConfig{CAFile: "redis-ca.pem", ServerName: "redis.internal"},
	})
	defer client.Close()

	snapshot := client.DiagnosticsSnapshot()
	if snapshot.Topology != "sentinel-cluster" || snapshot.SeedCount != 2 || snapshot.Protocol != 3 || !snapshot.ReadOnly || !snapshot.RouteRandomly || !snapshot.TLSConfigured {
		t.Fatalf("DiagnosticsSnapshot = %#v", snapshot)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	for _, secret := range []string{"redis-password", "sentinel-password", "sentinel-a:26379", "redis-ca.pem", "redis.internal"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, data)
		}
	}
}

func TestConfigDefaultsTLSAndEagerConnect(t *testing.T) {
	config := Config{}.withDefaults()
	if config.Protocol != 2 || !config.DisableIdentity || config.MaxRetries != -1 || config.PingTimeout != time.Second {
		t.Fatalf("compatibility defaults = %#v", config)
	}
	if config.EnableIdentity {
		t.Fatal("identity must be opt-in")
	}

	server := newMockServer(t)
	client, err := NewChecked(context.Background(), Config{Addr: server.addr(), EagerConnect: true, PingTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewChecked eager client: %v", err)
	}
	defer client.Close()

	if _, err := NewChecked(context.Background(), Config{Addr: server.addr(), TLS: security.TLSConfig{CAFile: "missing.pem"}}); err == nil {
		t.Fatal("NewChecked invalid TLS: want error")
	}
}

func TestNewOwnsIndependentClientPools(t *testing.T) {
	first := New(Config{Addr: "redis.internal:6379", DisableBreaker: true, SlowThreshold: -1})
	second := New(Config{Addr: "redis.internal:6379", DisableBreaker: true, SlowThreshold: -1})
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	if first == second || first.client == second.client {
		t.Fatal("New clients with the same address must retain independent ownership and pools")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}
	if second.closed.Load() {
		t.Fatal("closing one independently owned client closed its peer")
	}
}

func TestClientClosedAndCanceledContext(t *testing.T) {
	var nilClient *Client
	if err := nilClient.Close(); err != nil {
		t.Fatalf("Close nil client: %v", err)
	}
	if _, err := nilClient.Get(context.Background(), "key"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get nil client error = %v, want ErrClosed", err)
	}
	client, _ := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Get(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get canceled context error = %v, want context.Canceled", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := client.Get(context.Background(), "key"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get closed client error = %v, want ErrClosed", err)
	}
}
