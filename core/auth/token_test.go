package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStaticTokenValidator(t *testing.T) {
	validator := StaticTokenValidator("secret", "svc")

	ctx, err := validator(context.Background(), "secret")
	if err != nil {
		t.Fatalf("validator returned error: %v", err)
	}
	if got := SubjectFromContext(ctx); got != "svc" {
		t.Fatalf("subject = %q, want svc", got)
	}

	if _, err := validator(context.Background(), "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong token error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := validator(context.Background(), ""); !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("empty token error = %v, want ErrMissingCredentials", err)
	}
}

func TestExtractBearer(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{name: "valid", header: "Bearer token", want: "token", ok: true},
		{name: "case insensitive", header: "bearer token", want: "token", ok: true},
		{name: "trim token", header: "  Bearer   token  ", want: "token", ok: true},
		{name: "missing token", header: "Bearer ", ok: false},
		{name: "bad scheme", header: "Basic token", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExtractBearer(tt.header)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ExtractBearer(%q) = %q,%v want %q,%v", tt.header, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestBearerValue(t *testing.T) {
	if got := BearerValue("token"); got != "Bearer token" {
		t.Fatalf("BearerValue = %q, want Bearer token", got)
	}
	if got := BearerValue(""); got != "" {
		t.Fatalf("empty BearerValue = %q, want empty", got)
	}
}

func TestJWTSignVerifyAndValidator(t *testing.T) {
	now := time.Unix(1000, 0)
	secret := []byte("secret")
	token, err := SignJWT(JWTClaims{Subject: "alice", Issuer: "gofly", Audience: "api", ExpiresAt: now.Add(time.Hour).Unix()}, secret)
	if err != nil {
		t.Fatalf("SignJWT returned error: %v", err)
	}
	claims, err := VerifyJWT(token, secret, JWTOptions{Issuer: "gofly", Audience: "api", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("VerifyJWT returned error: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", claims.Subject)
	}
	ctx, err := JWTValidator(secret, JWTOptions{Now: func() time.Time { return now }})(context.Background(), token)
	if err != nil {
		t.Fatalf("JWTValidator returned error: %v", err)
	}
	if got := SubjectFromContext(ctx); got != "alice" {
		t.Fatalf("context subject = %q, want alice", got)
	}
	if _, err := VerifyJWT(token+"x", secret, JWTOptions{Now: func() time.Time { return now }}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("tampered token error = %v, want ErrInvalidCredentials", err)
	}
}

func TestVerifyJWTRejectsExpiredToken(t *testing.T) {
	secret := []byte("secret")
	token, err := SignJWT(JWTClaims{Subject: "alice", ExpiresAt: 1000}, secret)
	if err != nil {
		t.Fatalf("SignJWT returned error: %v", err)
	}
	_, err = VerifyJWT(token, secret, JWTOptions{Now: func() time.Time { return time.Unix(1000, 0) }})
	if !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("VerifyJWT error = %v, want ErrExpiredToken", err)
	}
}

func TestRequestSignature(t *testing.T) {
	secret := []byte("secret")
	body := []byte(`{"name":"gofly"}`)
	timestamp := time.Unix(1000, 0)
	req := httptest.NewRequest("POST", "/users?debug=true", nil)
	req.Header.Set(TimestampHeader, "1000")
	req.Header.Set(SignatureHeader, SignRequest("POST", "/users?debug=true", body, timestamp.Unix(), secret))
	if err := VerifyRequestSignature(req, body, SignatureOptions{Secret: secret, MaxAge: time.Minute, Now: func() time.Time { return timestamp }}); err != nil {
		t.Fatalf("VerifyRequestSignature returned error: %v", err)
	}
	req.Header.Set(SignatureHeader, "sha256=bad")
	if err := VerifyRequestSignature(req, body, SignatureOptions{Secret: secret, Now: func() time.Time { return timestamp }}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad signature error = %v, want ErrInvalidCredentials", err)
	}
}
