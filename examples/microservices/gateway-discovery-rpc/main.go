// Command gateway-discovery-rpc demonstrates routing RPC calls through the
// gateway with service discovery.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/imajinyun/gofly/core/discovery"
)

func main() {
	if err := runGatewayDiscoveryRPCDemo(context.Background(), 1); err != nil {
		panic(err)
	}
}

func runGatewayDiscoveryRPCDemo(ctx context.Context, eventLimit int) error {
	registry := discovery.NewMemoryRegistry()
	watch, err := registry.Watch(ctx, "greeter", discovery.WithVersion("v1"), discovery.WithZone("local-a"))
	if err != nil {
		return err
	}
	lease, err := registry.Register(ctx, discovery.Instance{
		Service:  "greeter",
		Endpoint: "http://127.0.0.1:8081",
		Version:  "v1",
		Zone:     "local-a",
		Status:   discovery.StatusHealthy,
		Tags:     map[string]string{"transport": "rpc", "gateway": "true"},
	}, discovery.WithTTL(time.Minute))
	if err != nil {
		return err
	}
	defer lease.Close(context.WithoutCancel(ctx))
	resolved, err := registry.Resolve(ctx, "greeter", discovery.WithVersion("v1"), discovery.WithZone("local-a"))
	if err != nil {
		return err
	}
	for i := 0; i < eventLimit; i++ {
		event := <-watch
		fmt.Printf("discovery event=%s instances=%d\n", event.Type, len(event.Instances))
	}
	fmt.Printf("gateway would route /gw/greeter to %s\n", resolved[0].Endpoint)
	return nil
}
