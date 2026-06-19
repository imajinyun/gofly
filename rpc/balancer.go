// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EndpointReporter receives per-endpoint result reports.
type EndpointReporter interface {
	Report(ctx context.Context, endpoint string, err error)
}

// HealthBalancer tracks endpoint health and ejects unhealthy endpoints.
type HealthBalancer struct {
	mu               sync.Mutex
	next             atomic.Uint64
	failureThreshold int
	ejectionDuration time.Duration
	endpoints        map[string]*endpointHealth
}

// HealthBalancerOption customises HealthBalancer.
type HealthBalancerOption func(*HealthBalancer)

type endpointHealth struct {
	failures  int
	ejectedAt time.Time
}

// RoundRobinBalancer selects endpoints in round-robin order.
type RoundRobinBalancer struct {
	next atomic.Uint64
}

// WeightedRoundRobinBalancer selects endpoints using weighted round-robin.
type WeightedRoundRobinBalancer struct {
	mu      sync.Mutex
	weights map[string]int
	current map[string]int
}

type P2CBalancer struct {
	mu     sync.Mutex
	next   atomic.Uint64
	active map[string]int
}

type ConsistentHashBalancer struct {
	mu            sync.RWMutex
	replicas      int
	ring          []uint32
	nodes         map[uint32]string
	fixedKey      string
	ringSignature string
	next          RoundRobinBalancer
}

type ConsistentHashOption func(*ConsistentHashBalancer)

type hashKeyContextKey struct{}

func (b *RoundRobinBalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	candidates := normalizeEndpoints(endpoints)
	if len(candidates) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	idx := b.next.Add(1) - 1
	// #nosec G115 -- modulo bounds the uint64 counter to len(candidates), so the int conversion cannot overflow.
	return candidates[int(idx%uint64(len(candidates)))], nil
}

func NewHealthBalancer(opts ...HealthBalancerOption) *HealthBalancer {
	b := &HealthBalancer{
		failureThreshold: 2,
		ejectionDuration: 5 * time.Second,
		endpoints:        make(map[string]*endpointHealth),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func NewWeightedRoundRobinBalancer(weights map[string]int) *WeightedRoundRobinBalancer {
	return &WeightedRoundRobinBalancer{weights: normalizeWeights(weights), current: make(map[string]int)}
}

func NewP2CBalancer() *P2CBalancer {
	return &P2CBalancer{active: make(map[string]int)}
}

func NewConsistentHashBalancer(opts ...ConsistentHashOption) *ConsistentHashBalancer {
	b := &ConsistentHashBalancer{replicas: 64, nodes: make(map[uint32]string)}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	if b.replicas <= 0 {
		b.replicas = 64
	}
	return b
}

func WithConsistentHashReplicas(replicas int) ConsistentHashOption {
	return func(b *ConsistentHashBalancer) {
		if replicas > 0 {
			b.replicas = replicas
		}
	}
}

func WithConsistentHashKey(key string) ConsistentHashOption {
	return func(b *ConsistentHashBalancer) {
		b.fixedKey = key
	}
}

func ContextWithHashKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hashKeyContextKey{}, key)
}

func HashKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(hashKeyContextKey{}).(string)
	return key
}

func WithHealthFailureThreshold(n int) HealthBalancerOption {
	return func(b *HealthBalancer) {
		if n > 0 {
			b.failureThreshold = n
		}
	}
}

func WithHealthEjectionDuration(d time.Duration) HealthBalancerOption {
	return func(b *HealthBalancer) {
		if d > 0 {
			b.ejectionDuration = d
		}
	}
}

func (b *HealthBalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	candidates := normalizeEndpoints(endpoints)
	if len(candidates) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	healthy := b.healthyEndpoints(candidates, time.Now())
	if len(healthy) == 0 {
		healthy = candidates
	}
	idx := b.next.Add(1) - 1
	// #nosec G115 -- modulo bounds the uint64 counter to len(healthy), so the int conversion cannot overflow.
	return healthy[int(idx%uint64(len(healthy)))], nil
}

func (b *WeightedRoundRobinBalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	candidates := normalizeEndpoints(endpoints)
	if len(candidates) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current == nil {
		b.current = make(map[string]int)
	}
	var chosen string
	var total int
	for _, endpoint := range candidates {
		weight := b.weightOf(endpoint)
		total += weight
		b.current[endpoint] += weight
		if chosen == "" || b.current[endpoint] > b.current[chosen] {
			chosen = endpoint
		}
	}
	if chosen == "" {
		return "", errors.New("no endpoint to pick")
	}
	b.current[chosen] -= total
	return chosen, nil
}

func (b *WeightedRoundRobinBalancer) weightOf(endpoint string) int {
	if b == nil || b.weights == nil {
		return 1
	}
	weight := b.weights[endpoint]
	if weight <= 0 {
		return 1
	}
	return weight
}

func (b *P2CBalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	candidates := normalizeEndpoints(endpoints)
	if len(candidates) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	if len(candidates) == 1 {
		b.markActive(candidates[0], 1)
		return candidates[0], nil
	}
	idx := b.next.Add(1) - 1
	// #nosec G115 -- modulo bounds the uint64 counter to len(candidates), so the int conversion cannot overflow.
	a := candidates[int(idx%uint64(len(candidates)))]
	// #nosec G115 -- modulo bounds the mixed counter to len(candidates), so the int conversion cannot overflow.
	bidx := int((idx*1103515245 + 12345) % uint64(len(candidates)))
	bEndpoint := candidates[bidx]
	if bEndpoint == a {
		bEndpoint = candidates[(bidx+1)%len(candidates)]
	}
	chosen := b.lessLoaded(a, bEndpoint)
	b.markActive(chosen, 1)
	return chosen, nil
}

func (b *P2CBalancer) Report(ctx context.Context, endpoint string, err error) {
	if ctx.Err() != nil {
		return
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return
	}
	b.markActive(endpoint, -1)
}

func (b *P2CBalancer) lessLoaded(a, c string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active == nil {
		b.active = make(map[string]int)
	}
	if b.active[a] <= b.active[c] {
		return a
	}
	return c
}

func (b *P2CBalancer) markActive(endpoint string, delta int) {
	if b == nil || endpoint == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active == nil {
		b.active = make(map[string]int)
	}
	b.active[endpoint] += delta
	if b.active[endpoint] <= 0 {
		delete(b.active, endpoint)
	}
}

func (b *ConsistentHashBalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	candidates := normalizeEndpoints(endpoints)
	if len(candidates) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	key := b.hashKey(ctx)
	if key == "" {
		return b.next.Pick(ctx, candidates)
	}
	ring, nodes := b.ringFor(candidates)
	if len(ring) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	hash := hashString(key)
	idx := sort.Search(len(ring), func(i int) bool { return ring[i] >= hash })
	if idx == len(ring) {
		idx = 0
	}
	return nodes[ring[idx]], nil
}

func (b *ConsistentHashBalancer) hashKey(ctx context.Context) string {
	if b == nil {
		return ""
	}
	if key := HashKeyFromContext(ctx); key != "" {
		return key
	}
	return b.fixedKey
}

func (b *ConsistentHashBalancer) ringFor(endpoints []string) ([]uint32, map[uint32]string) {
	if b == nil {
		b = NewConsistentHashBalancer()
	}
	signature := strings.Join(endpoints, "\x00")
	b.mu.RLock()
	if b.ringSignature == signature && len(b.ring) > 0 {
		ring := append([]uint32(nil), b.ring...)
		nodes := cloneHashNodes(b.nodes)
		b.mu.RUnlock()
		return ring, nodes
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ringSignature == signature && len(b.ring) > 0 {
		return append([]uint32(nil), b.ring...), cloneHashNodes(b.nodes)
	}
	nodes := make(map[uint32]string, len(endpoints)*b.replicas)
	ring := make([]uint32, 0, len(endpoints)*b.replicas)
	for _, endpoint := range endpoints {
		for i := 0; i < b.replicas; i++ {
			hash := hashString(endpoint + "#" + strconv.Itoa(i))
			nodes[hash] = endpoint
			ring = append(ring, hash)
		}
	}
	sort.Slice(ring, func(i, j int) bool { return ring[i] < ring[j] })
	b.ringSignature = signature
	b.ring = ring
	b.nodes = nodes
	return append([]uint32(nil), ring...), cloneHashNodes(nodes)
}

func hashString(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}

func cloneHashNodes(nodes map[uint32]string) map[uint32]string {
	out := make(map[uint32]string, len(nodes))
	for key, value := range nodes {
		out[key] = value
	}
	return out
}

func (b *HealthBalancer) Report(ctx context.Context, endpoint string, err error) {
	if ctx.Err() != nil {
		return
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.endpoints == nil {
		b.endpoints = make(map[string]*endpointHealth)
	}
	state := b.endpoints[endpoint]
	if state == nil {
		state = &endpointHealth{}
		b.endpoints[endpoint] = state
	}
	if !isRetryable(err) {
		state.failures = 0
		state.ejectedAt = time.Time{}
		return
	}
	state.failures++
	if state.failures >= b.failureThreshold {
		state.ejectedAt = time.Now()
	}
}

func (b *HealthBalancer) healthyEndpoints(endpoints []string, now time.Time) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	healthy := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		state := b.endpoints[endpoint]
		if state == nil || state.ejectedAt.IsZero() {
			healthy = append(healthy, endpoint)
			continue
		}
		if now.Sub(state.ejectedAt) >= b.ejectionDuration {
			state.failures = 0
			state.ejectedAt = time.Time{}
			healthy = append(healthy, endpoint)
		}
	}
	return healthy
}

func normalizeEndpoints(endpoints []string) []string {
	out := make([]string, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		out = append(out, endpoint)
	}
	return out
}

func normalizeWeights(weights map[string]int) map[string]int {
	if len(weights) == 0 {
		return nil
	}
	out := make(map[string]int, len(weights))
	for endpoint, weight := range weights {
		endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if endpoint == "" || weight <= 0 {
			continue
		}
		out[endpoint] = weight
	}
	return out
}
