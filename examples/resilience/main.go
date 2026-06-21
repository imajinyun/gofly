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
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/retry"
)

// errDownstream is the transient error returned by the simulated dependency.
var errDownstream = errors.New("downstream temporarily unavailable")

// flakyDownstream fails with the given probability to simulate an unreliable
// dependency.
func flakyDownstream(failRate float64) error {
	// #nosec G404 -- this example uses math/rand only to simulate non-security failure rates.
	if rand.Float64() < failRate {
		return errDownstream
	}
	return nil
}

func main() {
	ctx := context.Background()

	// Outer layer: a generous limiter that only sheds genuine bursts.
	limiter := limit.New(100, 20)

	// Inner layer: trip after 3 consecutive failures, stay open for 300ms
	// before probing the dependency again.
	br := breaker.New(
		breaker.WithFailureThreshold(3),
		breaker.WithOpenTimeout(300*time.Millisecond),
	)

	// Middle layer: up to 3 attempts with exponential backoff, only retrying
	// the transient downstream error.
	policy := retry.Policy{
		Attempts:    3,
		BackoffFunc: retry.ExponentialBackoff(5*time.Millisecond, 50*time.Millisecond),
		ShouldRetry: func(err error) bool { return errors.Is(err, errDownstream) },
	}

	var (
		ok         int
		rejected   int
		brokenOpen int
		failed     int
	)

	for i := 0; i < 40; i++ {
		// 1) Shed load first.
		if !limiter.Allow() {
			rejected++
			continue
		}

		// 2) Retry around 3) the breaker-protected downstream call.
		err := retry.Do(ctx, policy, func() error {
			return br.Do(ctx, func() error {
				// Degrade the dependency badly for the middle of the run so the
				// breaker visibly trips, then let it recover.
				failRate := 0.3
				if i >= 12 && i < 24 {
					failRate = 0.95
				}
				return flakyDownstream(failRate)
			})
		})

		switch {
		case err == nil:
			ok++
		case errors.Is(err, breaker.ErrOpen):
			brokenOpen++
		default:
			failed++
		}

		time.Sleep(20 * time.Millisecond)
	}

	fmt.Printf("results: ok=%d rejected=%d breaker-open=%d failed=%d\n", ok, rejected, brokenOpen, failed)
	fmt.Printf("final breaker state: %s\n", br.State())
}
