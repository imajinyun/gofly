package rpc

import (
	"net/http"
	"testing"
)

func BenchmarkDefaultHTTPClientConstruction_BitsBench(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		client := NewHTTPClient(DefaultTransportConfig())
		if client == nil || client.Transport == nil {
			b.Fatal("client transport is nil")
		}
		if _, ok := client.Transport.(*http.Transport); !ok {
			b.Fatalf("transport type = %T, want *http.Transport", client.Transport)
		}
	}
}
