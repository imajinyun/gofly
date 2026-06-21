package main

import (
	"context"
	"testing"
)

func TestRunGatewayDiscoveryRPCDemo_BitsUT(t *testing.T) {
	if err := runGatewayDiscoveryRPCDemo(context.Background(), 1); err != nil {
		t.Fatalf("runGatewayDiscoveryRPCDemo: %v", err)
	}
}
