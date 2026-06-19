package metadata

import (
	"context"
	"testing"
)

func TestMetadataContext(t *testing.T) {
	ctx := Append(context.Background(), RequestIDKey, "rid-1", "tenant", "demo")
	md, ok := FromContext(ctx)
	if !ok {
		t.Fatal("metadata not found")
	}
	if md.Get(RequestIDKey) != "rid-1" {
		t.Fatalf("request id = %q, want rid-1", md.Get(RequestIDKey))
	}
	if RequestIDFromContext(ctx) != "rid-1" {
		t.Fatalf("RequestIDFromContext = %q, want rid-1", RequestIDFromContext(ctx))
	}
}

func TestWithRequestIDPreservesExistingMetadata(t *testing.T) {
	ctx := Append(context.Background(), "tenant", "demo", "service", "checkout")
	ctx = WithRequestID(ctx, "rid-2")

	md, ok := FromContext(ctx)
	if !ok {
		t.Fatal("metadata not found")
	}
	if got := md.Get(RequestIDKey); got != "rid-2" {
		t.Fatalf("request id = %q, want rid-2", got)
	}
	if got := md.Get("tenant"); got != "demo" {
		t.Fatalf("tenant = %q, want preserved demo", got)
	}
	if got := md.Get("service"); got != "checkout" {
		t.Fatalf("service = %q, want preserved checkout", got)
	}
}

func TestEnsureRequestIDPreservesExistingMetadata(t *testing.T) {
	ctx := Append(context.Background(), "tenant", "demo")
	next, id := EnsureRequestID(ctx)
	if id == "" {
		t.Fatal("EnsureRequestID returned empty id")
	}
	md, ok := FromContext(next)
	if !ok {
		t.Fatal("metadata not found")
	}
	if got := md.Get(RequestIDKey); got != id {
		t.Fatalf("request id = %q, want %q", got, id)
	}
	if got := md.Get("tenant"); got != "demo" {
		t.Fatalf("tenant = %q, want preserved demo", got)
	}
}

func TestNewMetadata(t *testing.T) {
	md := New("k1", "v1", "k2", "v2", "odd")
	if got := md.Get("k1"); got != "v1" {
		t.Fatalf("k1 = %q, want v1", got)
	}
	if got := md.Get("k2"); got != "v2" {
		t.Fatalf("k2 = %q, want v2", got)
	}
	if got := md.Get("odd"); got != "" {
		t.Fatalf("odd = %q, want empty", got)
	}
	if got := md.Get("missing"); got != "" {
		t.Fatalf("missing = %q, want empty", got)
	}
}

func TestMDGetNilGuard(t *testing.T) {
	var nilMD MD
	if got := nilMD.Get("any"); got != "" {
		t.Fatalf("nil MD Get = %q, want empty", got)
	}
}

func TestWithRequestIDGeneratesWhenEmpty(t *testing.T) {
	ctx := WithRequestID(context.Background(), "")
	if RequestIDFromContext(ctx) == "" {
		t.Fatal("WithRequestID with empty id should generate one")
	}
}
