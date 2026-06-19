// Package endpoint provides RPC client middleware primitives: chaining,
// hedging, timeouts and retries.
package endpoint

import (
	"context"
	"sync/atomic"
	"time"

	core "github.com/gofly/gofly/core"
)

// HedgeConfig controls request hedging behaviour.
type HedgeConfig struct {
	Delay     time.Duration
	MaxHedges int
	Clone     func(any) any
}

// HedgeStats reports hedging outcomes.
type HedgeStats struct {
	Primary   int64 `json:"primary"`
	Hedges    int64 `json:"hedges"`
	Wins      int64 `json:"wins"`
	Errors    int64 `json:"errors"`
	Cancelled int64 `json:"cancelled"`
}

// Hedger issues speculative duplicate requests to reduce tail latency.
type Hedger struct {
	config    HedgeConfig
	primary   atomic.Int64
	hedges    atomic.Int64
	wins      atomic.Int64
	errors    atomic.Int64
	cancelled atomic.Int64
}

// NewHedger creates a hedger with the given config.
func NewHedger(config HedgeConfig) *Hedger {
	if config.Delay <= 0 {
		config.Delay = 10 * time.Millisecond
	}
	if config.MaxHedges <= 0 {
		config.MaxHedges = 1
	}
	return &Hedger{config: config}
}

func HedgingMiddleware(config HedgeConfig) Middleware {
	return NewHedger(config).Middleware()
}

func (h *Hedger) Middleware() Middleware {
	return func(next Endpoint) Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			ctx = core.Context(ctx)
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			type result struct {
				value  any
				err    error
				hedged bool
			}
			results := make(chan result, h.config.MaxHedges+1)
			launch := func(hedged bool) {
				request := req
				if hedged && h.config.Clone != nil {
					request = h.config.Clone(req)
				}
				if hedged {
					h.hedges.Add(1)
				} else {
					h.primary.Add(1)
				}
				go func() {
					value, err := next(callCtx, request)
					select {
					case results <- result{value: value, err: err, hedged: hedged}:
					case <-callCtx.Done():
						h.cancelled.Add(1)
					}
				}()
			}
			launch(false)
			launched := 1
			inflight := 1
			var lastErr error
			timer := time.NewTimer(h.config.Delay)
			defer timer.Stop()
			for inflight > 0 {
				select {
				case <-ctx.Done():
					h.cancelled.Add(1)
					return nil, ctx.Err()
				case <-timer.C:
					if launched <= h.config.MaxHedges {
						launch(true)
						launched++
						inflight++
					}
				case res := <-results:
					inflight--
					if res.err == nil {
						if res.hedged {
							h.wins.Add(1)
						}
						cancel()
						return res.value, nil
					}
					h.errors.Add(1)
					lastErr = res.err
					if inflight == 0 && launched <= h.config.MaxHedges {
						launch(true)
						launched++
						inflight++
					}
				}
			}
			return nil, lastErr
		}
	}
}

func (h *Hedger) Snapshot() HedgeStats {
	if h == nil {
		return HedgeStats{}
	}
	return HedgeStats{Primary: h.primary.Load(), Hedges: h.hedges.Load(), Wins: h.wins.Load(), Errors: h.errors.Load(), Cancelled: h.cancelled.Load()}
}
