package main

import (
	"context"
	"testing"
)

func TestRPCIDLMatrixReport_BitsUT(t *testing.T) {
	report, err := runMatrix(context.Background())
	if err != nil {
		t.Fatalf("runMatrix: %v", err)
	}
	if report.Schema != "gofly.rpc_idl_matrix.v1" {
		t.Fatalf("schema = %q, want gofly.rpc_idl_matrix.v1", report.Schema)
	}
	if report.IDL.Proto != "contracts/greeter.proto" || report.IDL.Thrift != "contracts/greeter.thrift" {
		t.Fatalf("idl = %#v, want proto and thrift fixtures", report.IDL)
	}
	if len(report.Streams) != 4 {
		t.Fatalf("streams = %#v, want unary/server/client/bidi matrix", report.Streams)
	}
	wantModes := map[string]bool{
		"unary":         false,
		"server_stream": false,
		"client_stream": false,
		"bidi_stream":   false,
	}
	for _, stream := range report.Streams {
		wantModes[stream.Mode] = true
	}
	for mode, seen := range wantModes {
		if !seen {
			t.Fatalf("stream mode %q was not reported: %#v", mode, report.Streams)
		}
	}
	for _, name := range []string{"recovery", "trace", "logging", "timeout", "retry", "breaker", "validation"} {
		if !contains(report.Interceptors.Unary, name) {
			t.Fatalf("unary interceptors = %#v, missing %s", report.Interceptors.Unary, name)
		}
	}
	for _, name := range []string{"recovery", "trace", "logging", "timeout", "breaker"} {
		if !contains(report.Interceptors.Stream, name) {
			t.Fatalf("stream interceptors = %#v, missing %s", report.Interceptors.Stream, name)
		}
	}
	for _, name := range []string{"round_robin", "weighted_round_robin", "p2c", "consistent_hash", "health_aware"} {
		if len(report.Balancers[name]) == 0 {
			t.Fatalf("balancer %s picks = %#v, want at least one pick", name, report.Balancers[name])
		}
	}
	if len(report.Resolver.Endpoints) != 2 || len(report.Resolver.Updated) != 1 {
		t.Fatalf("resolver = %#v, want initial two endpoints and updated one endpoint", report.Resolver)
	}
	if report.Results["unary"] != "hello gofly" ||
		report.Results["serverStream"] != "stream:first|stream:second" ||
		report.Results["clientStream"] != "collected alice,bob" ||
		report.Results["bidiStream"] != "server:ack:ping" ||
		report.Results["retryAttempts"] != "2" {
		t.Fatalf("results = %#v, want exercised RPC matrix paths", report.Results)
	}
	if report.Runtime["requests"] == "" || report.Runtime["requests"] == "0" {
		t.Fatalf("runtime = %#v, want metrics-observed requests", report.Runtime)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
