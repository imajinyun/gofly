package grpc

import (
	"context"
	"hash/fnv"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	P2CEWMABalancerName        = "gofly_p2c_ewma"
	ConsistentHashBalancerName = "gofly_consistent_hash"
)

type grpcHashKey struct{}

func init() {
	balancer.Register(base.NewBalancerBuilder(P2CEWMABalancerName, &p2cPickerBuilder{}, base.Config{HealthCheck: true}))
	balancer.Register(base.NewBalancerBuilder(ConsistentHashBalancerName, &consistentHashPickerBuilder{}, base.Config{HealthCheck: true}))
}

// WithBalancerName configures a registered gRPC load-balancing policy.
func WithBalancerName(name string) ClientOption {
	return withClientDialOptions(stdgrpcServiceConfigOption(name))
}

// WithHashKey attaches the key used by the consistent-hash balancer.
func WithHashKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, grpcHashKey{}, key)
}

func hashKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(grpcHashKey{}).(string)
	return key
}

type p2cPickerBuilder struct{}

func (*p2cPickerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	entries := make([]*p2cEntry, 0, len(info.ReadySCs))
	for subConn, subConnInfo := range info.ReadySCs {
		entries = append(entries, &p2cEntry{subConn: subConn, address: subConnInfo.Address.Addr})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].address < entries[j].address })
	return &p2cPicker{entries: entries, random: rand.New(rand.NewSource(time.Now().UnixNano()))} // #nosec G404 -- load balancing sampling is not security-sensitive.
}

type p2cEntry struct {
	subConn  balancer.SubConn
	address  string
	inflight atomic.Int64
	latency  atomic.Int64
}

func (e *p2cEntry) load() int64 {
	latency := e.latency.Load()
	if latency <= 0 {
		latency = int64(time.Millisecond)
	}
	return latency * (e.inflight.Load() + 1)
}

func (e *p2cEntry) done(start time.Time, info balancer.DoneInfo) {
	duration := time.Since(start).Nanoseconds()
	if duration <= 0 {
		duration = 1
	}
	if info.Err != nil && status.Code(info.Err) != codes.Canceled {
		duration *= 2
	}
	for {
		previous := e.latency.Load()
		next := duration
		if previous > 0 {
			next = (previous*4 + duration) / 5
		}
		if e.latency.CompareAndSwap(previous, next) {
			break
		}
	}
	e.inflight.Add(-1)
}

type p2cPicker struct {
	mu      sync.Mutex
	entries []*p2cEntry
	random  *rand.Rand
}

func (p *p2cPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	if len(p.entries) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	selected := p.entries[0]
	if len(p.entries) > 1 {
		p.mu.Lock()
		first := p.random.Intn(len(p.entries))
		second := p.random.Intn(len(p.entries) - 1)
		if second >= first {
			second++
		}
		p.mu.Unlock()
		selected = p.entries[first]
		if p.entries[second].load() < selected.load() {
			selected = p.entries[second]
		}
	}
	selected.inflight.Add(1)
	start := time.Now()
	return balancer.PickResult{SubConn: selected.subConn, Done: func(info balancer.DoneInfo) { selected.done(start, info) }}, nil
}

type consistentHashPickerBuilder struct{}

func (*consistentHashPickerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	entries := make([]consistentHashEntry, 0, len(info.ReadySCs))
	for subConn, subConnInfo := range info.ReadySCs {
		entries = append(entries, consistentHashEntry{subConn: subConn, address: subConnInfo.Address.Addr})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].address < entries[j].address })
	return consistentHashPicker{entries: entries}
}

type consistentHashEntry struct {
	subConn balancer.SubConn
	address string
}

type consistentHashPicker struct {
	entries []consistentHashEntry
}

func (p consistentHashPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if len(p.entries) == 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}
	key := hashKeyFromContext(info.Ctx)
	if key == "" {
		return balancer.PickResult{}, status.Error(codes.InvalidArgument, "consistent hash key is required")
	}
	selected := p.entries[0]
	var selectedScore uint64
	for index, entry := range p.entries {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(key))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(entry.address))
		score := hasher.Sum64()
		if index == 0 || score > selectedScore {
			selected = entry
			selectedScore = score
		}
	}
	return balancer.PickResult{SubConn: selected.subConn}, nil
}
