// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

// P2CEWMABalancer implements "Power of Two Choices" load balancing with an
// EWMA (exponentially weighted moving average) of per-endpoint latency. For each
// pick it samples two random endpoints and routes to the one with the lower
// load, where load combines decayed average latency with the number of
// in-flight requests:
//
//	load = ewmaLatency * (inflight + 1)
//
// This steers traffic away from slow or saturated nodes far more responsively
// than round-robin or active-count-only P2C.
type P2CEWMABalancer struct {
	mu        sync.Mutex
	decay     time.Duration
	rand      *rand.Rand
	endpoints map[string]*ewmaStat
	now       func() time.Time
}

type ewmaStat struct {
	inflight   int64
	lag        float64 // EWMA of latency, in nanoseconds
	lastUpdate time.Time
	pending    []time.Time // start times of picks awaiting a Report (FIFO)
}

// P2CEWMAOption configures a P2CEWMABalancer.
type P2CEWMAOption func(*P2CEWMABalancer)

// WithEWMADecay sets the decay constant (tau) controlling how quickly old
// latency samples lose weight. Larger values smooth more; smaller values react
// faster. Defaults to 600ms.
func WithEWMADecay(d time.Duration) P2CEWMAOption {
	return func(b *P2CEWMABalancer) {
		if d > 0 {
			b.decay = d
		}
	}
}

// NewP2CEWMABalancer creates a P2C balancer with EWMA latency scoring.
func NewP2CEWMABalancer(opts ...P2CEWMAOption) *P2CEWMABalancer {
	b := &P2CEWMABalancer{
		decay: 600 * time.Millisecond,
		// #nosec G404 -- P2C sampling balances load; it is not used for secrets or security decisions.
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
		endpoints: make(map[string]*ewmaStat),
		now:       time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	if b.decay <= 0 {
		b.decay = 600 * time.Millisecond
	}
	return b
}

// Pick selects an endpoint using the power-of-two-choices rule scored by EWMA
// latency and in-flight load.
func (b *P2CEWMABalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	candidates := normalizeEndpoints(endpoints)
	if len(candidates) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if len(candidates) == 1 {
		b.markPickLocked(candidates[0], now)
		return candidates[0], nil
	}
	i := b.rand.Intn(len(candidates))
	j := b.rand.Intn(len(candidates) - 1)
	if j >= i {
		j++
	}
	a, c := candidates[i], candidates[j]
	chosen := a
	if b.loadLocked(c, now) < b.loadLocked(a, now) {
		chosen = c
	}
	b.markPickLocked(chosen, now)
	return chosen, nil
}

// Report records the outcome of a request to endpoint, updating its EWMA
// latency from the time elapsed since the matching Pick and decrementing the
// in-flight counter. It satisfies EndpointReporter.
func (b *P2CEWMABalancer) Report(ctx context.Context, endpoint string, err error) {
	endpoint = trimEndpoint(endpoint)
	if endpoint == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stat := b.endpoints[endpoint]
	if stat == nil {
		return
	}
	now := b.now()
	if stat.inflight > 0 {
		stat.inflight--
	}
	if len(stat.pending) == 0 {
		return
	}
	start := stat.pending[0]
	stat.pending = stat.pending[1:]
	latency := now.Sub(start)
	if latency < 0 {
		latency = 0
	}
	b.updateLagLocked(stat, latency, now)
}

func (b *P2CEWMABalancer) markPickLocked(endpoint string, now time.Time) {
	stat := b.endpoints[endpoint]
	if stat == nil {
		stat = &ewmaStat{}
		b.endpoints[endpoint] = stat
	}
	stat.inflight++
	stat.pending = append(stat.pending, now)
}

func (b *P2CEWMABalancer) loadLocked(endpoint string, now time.Time) float64 {
	stat := b.endpoints[endpoint]
	if stat == nil {
		// Unmeasured endpoints have zero load so they get probed.
		return 0
	}
	return stat.lag * float64(stat.inflight+1)
}

func (b *P2CEWMABalancer) updateLagLocked(stat *ewmaStat, latency time.Duration, now time.Time) {
	sample := float64(latency)
	if stat.lastUpdate.IsZero() {
		stat.lag = sample
		stat.lastUpdate = now
		return
	}
	delta := now.Sub(stat.lastUpdate)
	if delta < 0 {
		delta = 0
	}
	w := math.Exp(-float64(delta) / float64(b.decay))
	stat.lag = stat.lag*w + sample*(1-w)
	stat.lastUpdate = now
}

func trimEndpoint(endpoint string) string {
	endpoints := normalizeEndpoints([]string{endpoint})
	if len(endpoints) == 0 {
		return ""
	}
	return endpoints[0]
}
