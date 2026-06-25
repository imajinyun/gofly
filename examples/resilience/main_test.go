package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFlakyDownstreamBoundaries_BitsUT(t *testing.T) {
	if err := flakyDownstream(0); err != nil {
		t.Fatalf("flakyDownstream(0) = %v, want nil", err)
	}
	if err := flakyDownstream(1); !errors.Is(err, errDownstream) {
		t.Fatalf("flakyDownstream(1) = %v, want errDownstream", err)
	}
}

func TestMainDemo_BitsUT(t *testing.T) {
	main()
}

func TestRunDrillJSONContract_BitsUT(t *testing.T) {
	report := runDrill(context.Background(), drillConfig{
		Requests:         14,
		Rate:             1,
		Burst:            10,
		FailureThreshold: 3,
		OpenTimeout:      time.Millisecond,
		RetryAttempts:    3,
	})

	if report.Schema != "gofly.resilience_drill.v1" {
		t.Fatalf("schema = %q", report.Schema)
	}
	if report.Results.DownstreamCalls < 5 {
		t.Fatalf("downstream calls = %d, want retry evidence", report.Results.DownstreamCalls)
	}
	if report.Results.BreakerOpen == 0 {
		t.Fatalf("breakerOpen = 0, want breaker-open evidence: %#v", report.Results)
	}
	if report.Results.Rejected == 0 {
		t.Fatalf("rejected = 0, want rate-limit evidence: %#v", report.Results)
	}
	if !report.Results.Recovered || report.Results.FinalBreaker != "closed" {
		t.Fatalf("recovery evidence = recovered %t final %q", report.Results.Recovered, report.Results.FinalBreaker)
	}
}
