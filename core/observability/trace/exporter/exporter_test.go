package exporter

import (
	"context"
	"testing"
	"time"
)

func TestNewOTLPValidation(t *testing.T) {
	if _, err := NewOTLP(context.Background(), OTLPConfig{}); err == nil {
		t.Fatal("NewOTLP should require an endpoint")
	}
	if _, err := NewOTLP(context.Background(), OTLPConfig{Endpoint: "localhost:4317", Protocol: "bogus"}); err == nil {
		t.Fatal("NewOTLP should reject unknown protocol")
	}
}

func TestOTLPConfigTimeoutDefault(t *testing.T) {
	if got := (OTLPConfig{}).timeout(); got != 10*time.Second {
		t.Fatalf("default timeout = %v, want 10s", got)
	}
	if got := (OTLPConfig{Timeout: 3 * time.Second}).timeout(); got != 3*time.Second {
		t.Fatalf("custom timeout = %v, want 3s", got)
	}
}

func TestNewOTLPConstructsExporters(t *testing.T) {
	// Constructing an OTLP exporter does not dial; it only validates options.
	grpcExp, err := NewOTLP(context.Background(), OTLPConfig{
		Endpoint: "localhost:4317",
		Insecure: true,
		Headers:  map[string]string{"x": "y"},
	})
	if err != nil {
		t.Fatalf("gRPC NewOTLP: %v", err)
	}
	_ = grpcExp.Shutdown(context.Background())

	httpExp, err := NewOTLP(context.Background(), OTLPConfig{
		Protocol: ProtocolHTTP,
		Endpoint: "localhost:4318",
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("HTTP NewOTLP: %v", err)
	}
	_ = httpExp.Shutdown(context.Background())
}
