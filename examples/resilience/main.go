// Command resilience demonstrates composing gofly's resilience primitives —
// a rate limiter, a circuit breaker, and a retry policy — around a single
// unreliable downstream call.
//
// The layering, from outer to inner, is:
//
//		rate limit  ->  retry  ->  circuit breaker  ->  downstream
//
//	  - the limiter sheds load before any work is attempted,
//	  - retry transparently re-attempts transient failures with backoff,
//	  - the breaker trips after repeated failures to stop hammering a sick
//	    dependency and fails fast until it recovers.
//
// Run it:
//
//	go run ./examples/resilience
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/retry"
)

// errDownstream is the transient error returned by the simulated dependency.
var errDownstream = errors.New("downstream temporarily unavailable")

type drillConfig struct {
	Requests         int           `json:"requests"`
	Rate             int           `json:"rate"`
	Burst            int           `json:"burst"`
	FailureThreshold int           `json:"failureThreshold"`
	OpenTimeout      time.Duration `json:"openTimeout"`
	RetryAttempts    int           `json:"retryAttempts"`
}

type drillReport struct {
	Schema   string       `json:"schema"`
	Scenario string       `json:"scenario"`
	Layers   []string     `json:"layers"`
	Config   drillConfig  `json:"config"`
	Results  drillResults `json:"results"`
	Gates    []string     `json:"gates"`
}

type drillResults struct {
	OK              int    `json:"ok"`
	Rejected        int    `json:"rejected"`
	BreakerOpen     int    `json:"breakerOpen"`
	Failed          int    `json:"failed"`
	DownstreamCalls int    `json:"downstreamCalls"`
	FinalBreaker    string `json:"finalBreaker"`
	Recovered       bool   `json:"recovered"`
}

// flakyDownstream fails with the given probability to simulate an unreliable
// dependency.
func flakyDownstream(failRate float64) error {
	// #nosec G404 -- this example uses math/rand only to simulate non-security failure rates.
	if rand.Float64() < failRate {
		return errDownstream
	}
	return nil
}

func defaultDrillConfig() drillConfig {
	return drillConfig{
		Requests:         14,
		Rate:             1,
		Burst:            10,
		FailureThreshold: 3,
		OpenTimeout:      10 * time.Millisecond,
		RetryAttempts:    3,
	}
}

func runDrill(ctx context.Context, cfg drillConfig) drillReport {
	if cfg.Requests <= 0 {
		cfg.Requests = 14
	}
	if cfg.Rate <= 0 {
		cfg.Rate = 1
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 10
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 10 * time.Millisecond
	}
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = 3
	}

	// Outer layer: a limiter that sheds the tail of this burst.
	limiter := limit.New(cfg.Rate, cfg.Burst)

	// Inner layer: trip after 3 consecutive failures, stay open for 300ms
	// before probing the dependency again.
	br := breaker.New(
		breaker.WithFailureThreshold(cfg.FailureThreshold),
		breaker.WithOpenTimeout(cfg.OpenTimeout),
	)

	// Middle layer: up to 3 attempts with exponential backoff, only retrying
	// the transient downstream error.
	policy := retry.Policy{
		Attempts:    cfg.RetryAttempts,
		BackoffFunc: retry.FixedBackoff(time.Millisecond),
		ShouldRetry: func(err error) bool { return errors.Is(err, errDownstream) },
	}

	results := drillResults{}
	downstreamPattern := []bool{false, false, false, true, true}
	downstreamIndex := 0

	for i := 0; i < cfg.Requests; i++ {
		// 1) Shed load first.
		if !limiter.Allow() {
			results.Rejected++
			continue
		}
		if i == cfg.Burst-2 {
			time.Sleep(cfg.OpenTimeout + time.Millisecond)
		}

		// 2) Retry around 3) the breaker-protected downstream call.
		err := retry.Do(ctx, policy, func() error {
			return br.Do(ctx, func() error {
				results.DownstreamCalls++
				if downstreamIndex >= len(downstreamPattern) {
					return nil
				}
				ok := downstreamPattern[downstreamIndex]
				downstreamIndex++
				if ok {
					return nil
				}
				return errDownstream
			})
		})

		switch {
		case err == nil:
			results.OK++
			if br.State() == breaker.Closed {
				results.Recovered = true
			}
		case errors.Is(err, breaker.ErrOpen):
			results.BreakerOpen++
		default:
			results.Failed++
		}
	}

	results.FinalBreaker = br.State().String()
	return drillReport{
		Schema:   "gofly.resilience_drill.v1",
		Scenario: "limiter-retry-breaker-recovery",
		Layers:   []string{"rate-limit", "retry", "circuit-breaker", "downstream"},
		Config:   cfg,
		Results:  results,
		Gates:    []string{"make resilience-drill-check", "make examples-smoke"},
	}
}

func main() {
	jsonOutput := flag.Bool("json", false, "emit machine-readable resilience drill evidence")
	flag.Parse()

	report := runDrill(context.Background(), defaultDrillConfig())
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("results: ok=%d rejected=%d breaker-open=%d failed=%d\n", report.Results.OK, report.Results.Rejected, report.Results.BreakerOpen, report.Results.Failed)
	fmt.Printf("downstream calls: %d recovered=%t\n", report.Results.DownstreamCalls, report.Results.Recovered)
	fmt.Printf("final breaker state: %s\n", report.Results.FinalBreaker)
}
