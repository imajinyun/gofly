package rpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDNSResolverResolveAndConfigBoundaries(t *testing.T) {
	if _, err := NewDNSResolver(DNSResolverConfig{}); err == nil || !strings.Contains(err.Error(), "host is required") {
		t.Fatalf("empty DNS config error = %v, want host required", err)
	}
	if _, err := NewDNSResolver(DNSResolverConfig{Host: "svc.local"}); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("missing DNS port error = %v, want port error", err)
	}

	resolver, err := NewDNSResolver(DNSResolverConfig{
		Host:   " svc.local ",
		Port:   8080,
		Scheme: "grpc",
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{
				net.ParseIP("::1"),
				nil,
				net.ParseIP("0.0.0.0"),
				net.ParseIP("10.0.0.2"),
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"grpc://10.0.0.2:8080", "grpc://[::1]:8080"}
	if !sameEndpoints(endpoints, want) {
		t.Fatalf("DNS endpoints = %#v, want %#v", endpoints, want)
	}

	var nilResolver *DNSResolver
	if _, err := nilResolver.Resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "resolver is nil") {
		t.Fatalf("nil DNS resolve error = %v, want resolver is nil", err)
	}
}

func TestDNSResolverWatchEmitsChangedEndpoints(t *testing.T) {
	lookup := &dnsLookupStub{results: [][]net.IP{
		{net.ParseIP("10.0.0.1")},
		{net.ParseIP("10.0.0.1")},
		{net.ParseIP("10.0.0.2")},
	}}
	resolver, err := NewDNSResolver(DNSResolverConfig{
		Host:          "svc.local",
		Port:          9000,
		LookupIP:      lookup.LookupIP,
		WatchInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, err := resolver.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := readDNSResolverUpdate(t, updates)
	if !sameEndpoints(first, []string{"http://10.0.0.1:9000"}) {
		t.Fatalf("first DNS update = %#v", first)
	}
	second := readDNSResolverUpdate(t, updates)
	if !sameEndpoints(second, []string{"http://10.0.0.2:9000"}) {
		t.Fatalf("second DNS update = %#v", second)
	}
	cancel()
	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("DNS watch channel still open after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DNS watch close")
	}
}

func TestDNSResolverFailoverKeepsLastKnownEndpoint(t *testing.T) {
	lookup := &dnsLookupStub{
		results: [][]net.IP{{net.ParseIP("10.0.0.1")}},
		err:     errors.New("dns unavailable"),
	}
	dnsResolver, err := NewDNSResolver(DNSResolverConfig{
		Host:     "svc.local",
		Port:     8080,
		LookupIP: lookup.LookupIP,
	})
	if err != nil {
		t.Fatal(err)
	}
	failover, err := NewFailoverResolver(dnsResolver)
	if err != nil {
		t.Fatal(err)
	}
	first, err := failover.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sameEndpoints(first, []string{"http://10.0.0.1:8080"}) {
		t.Fatalf("first DNS failover endpoints = %#v", first)
	}
	lookup.EnableError()
	stale, err := failover.Resolve(context.Background())
	if err != nil {
		t.Fatalf("stale DNS failover error = %v, want cached endpoint", err)
	}
	if !sameEndpoints(stale, first) {
		t.Fatalf("stale DNS endpoints = %#v, want %#v", stale, first)
	}
	snapshot := failover.Snapshot()
	if !snapshot.Stale || snapshot.Fallbacks == 0 || !strings.Contains(snapshot.Error, "dns unavailable") {
		t.Fatalf("DNS failover snapshot = %#v, want stale unavailable state", snapshot)
	}
}

func readDNSResolverUpdate(t *testing.T, updates <-chan []string) []string {
	t.Helper()
	select {
	case endpoints, ok := <-updates:
		if !ok {
			t.Fatal("DNS watch channel closed before update")
		}
		return endpoints
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DNS resolver update")
		return nil
	}
}

type dnsLookupStub struct {
	mu      sync.Mutex
	results [][]net.IP
	err     error
	failed  bool
}

func (s *dnsLookupStub) LookupIP(ctx context.Context, _ string) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return nil, s.err
	}
	if len(s.results) == 0 {
		return nil, nil
	}
	result := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return append([]net.IP(nil), result...), nil
}

func (s *dnsLookupStub) EnableError() {
	s.mu.Lock()
	s.failed = true
	s.mu.Unlock()
}
