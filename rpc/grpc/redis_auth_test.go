package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"

	redisv9 "github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
)

type fakeRedisHashReader struct {
	token string
	err   error
	calls int
}

func (f *fakeRedisHashReader) HGet(context.Context, string, string) *redisv9.StringCmd {
	f.calls++
	return redisv9.NewStringResult(f.token, f.err)
}

func TestRedisTokenValidator(t *testing.T) {
	reader := &fakeRedisHashReader{token: "secret"}
	validator, err := NewRedisTokenValidator(reader, "rpc:tokens", true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("app", "orders"))
	for range 2 {
		next, err := validator.Validator()(ctx, "secret")
		if err != nil {
			t.Fatal(err)
		}
		if got := auth.SubjectFromContext(next); got != "orders" {
			t.Fatalf("subject = %q, want orders", got)
		}
	}
	if reader.calls != 1 {
		t.Fatalf("redis calls = %d, want cached single call", reader.calls)
	}
	if _, err := validator.Validator()(ctx, "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong token error = %v", err)
	}
}

func TestRedisTokenValidatorFailsClosed(t *testing.T) {
	backendErr := errors.New("redis unavailable")
	for _, tc := range []struct {
		name   string
		strict bool
		err    error
	}{
		{name: "strict backend error", strict: true, err: backendErr},
		{name: "non-strict backend error", strict: false, err: backendErr},
		{name: "non-strict unknown app", strict: false, err: redisv9.Nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validator, err := NewRedisTokenValidator(&fakeRedisHashReader{err: tc.err}, "rpc:tokens", tc.strict, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("app", "orders"))
			next, err := validator.Validator()(ctx, "secret")
			if err == nil {
				t.Fatalf("Validator succeeded with principal %q, want fail-closed error", auth.SubjectFromContext(next))
			}
		})
	}
}

func TestAppTokenCredentials(t *testing.T) {
	credentials := AppTokenCredentials{App: "orders", Token: "secret"}
	metadata, err := credentials.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metadata["app"] != "orders" || metadata["token"] != "secret" || metadata[auth.MetadataKey] != "Bearer secret" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if !credentials.RequireTransportSecurity() {
		t.Fatal("credentials should require transport security by default")
	}
	if (AppTokenCredentials{AllowInsecure: true}).RequireTransportSecurity() {
		t.Fatal("AllowInsecure should disable transport security requirement")
	}
}

func TestNewRedisTokenValidatorValidation(t *testing.T) {
	if _, err := NewRedisTokenValidator(nil, "tokens", true, time.Minute); err == nil {
		t.Fatal("nil client should fail")
	}
	if _, err := NewRedisTokenValidator(&fakeRedisHashReader{}, " ", true, time.Minute); err == nil {
		t.Fatal("empty hash key should fail")
	}
}
