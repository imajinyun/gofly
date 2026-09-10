package grpc

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/status"
)

type testSubConn struct {
	balancer.SubConn
	name string
}

func TestGoflyGRPCBalancersRegistered(t *testing.T) {
	for _, name := range []string{P2CEWMABalancerName, ConsistentHashBalancerName} {
		if balancer.Get(name) == nil {
			t.Fatalf("balancer %q is not registered", name)
		}
	}
}

func TestConsistentHashPickerIsStable(t *testing.T) {
	a := &testSubConn{name: "a"}
	b := &testSubConn{name: "b"}
	picker := (&consistentHashPickerBuilder{}).Build(base.PickerBuildInfo{ReadySCs: map[balancer.SubConn]base.SubConnInfo{
		a: {Address: resolver.Address{Addr: "10.0.0.1:8080"}},
		b: {Address: resolver.Address{Addr: "10.0.0.2:8080"}},
	}})
	if _, err := picker.Pick(balancer.PickInfo{Ctx: context.Background()}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing key error = %v, want InvalidArgument", err)
	}
	ctx := WithHashKey(context.Background(), "tenant-42")
	first, err := picker.Pick(balancer.PickInfo{Ctx: ctx})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		next, err := picker.Pick(balancer.PickInfo{Ctx: ctx})
		if err != nil || next.SubConn != first.SubConn {
			t.Fatalf("consistent hash selection changed: %v, %v", next.SubConn, err)
		}
	}
}

func TestP2CEWMAPickerTracksCompletion(t *testing.T) {
	subConn := &testSubConn{name: "only"}
	picker := (&p2cPickerBuilder{}).Build(base.PickerBuildInfo{ReadySCs: map[balancer.SubConn]base.SubConnInfo{
		subConn: {Address: resolver.Address{Addr: "10.0.0.1:8080"}},
	}}).(*p2cPicker)
	result, err := picker.Pick(balancer.PickInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if got := picker.entries[0].inflight.Load(); got != 1 {
		t.Fatalf("inflight = %d, want 1", got)
	}
	result.Done(balancer.DoneInfo{Err: errors.New("failed")})
	if got := picker.entries[0].inflight.Load(); got != 0 {
		t.Fatalf("inflight after Done = %d, want 0", got)
	}
	if picker.entries[0].latency.Load() <= 0 {
		t.Fatal("latency was not recorded")
	}
}

func TestP2CEWMAPickerPrefersLowerLoad(t *testing.T) {
	slow := &testSubConn{name: "slow"}
	fast := &testSubConn{name: "fast"}
	picker := (&p2cPickerBuilder{}).Build(base.PickerBuildInfo{ReadySCs: map[balancer.SubConn]base.SubConnInfo{
		slow: {Address: resolver.Address{Addr: "10.0.0.1:8080"}},
		fast: {Address: resolver.Address{Addr: "10.0.0.2:8080"}},
	}}).(*p2cPicker)
	for _, entry := range picker.entries {
		if entry.address == "10.0.0.1:8080" {
			entry.latency.Store(100)
		} else {
			entry.latency.Store(1)
		}
	}
	result, err := picker.Pick(balancer.PickInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SubConn != fast {
		t.Fatalf("selected = %v, want lower-load subconn", result.SubConn)
	}
	result.Done(balancer.DoneInfo{})
}

func TestP2CEWMAPickerEmpty(t *testing.T) {
	picker := (&p2cPickerBuilder{}).Build(base.PickerBuildInfo{})
	if _, err := picker.Pick(balancer.PickInfo{}); !errors.Is(err, balancer.ErrNoSubConnAvailable) {
		t.Fatalf("Pick error = %v, want ErrNoSubConnAvailable", err)
	}
}

func TestBalancerServiceConfigs(t *testing.T) {
	if got := serviceConfigForBalancer(P2CEWMABalancerName); got != `{"loadBalancingConfig":[{"gofly_p2c_ewma":{}}]}` {
		t.Fatalf("p2c config = %q", got)
	}
	b := NewResolverBuilder(nil, WithConsistentHashResolver())
	if b.serviceConfig != serviceConfigForBalancer(ConsistentHashBalancerName) {
		t.Fatalf("consistent hash resolver config = %q", b.serviceConfig)
	}
}
