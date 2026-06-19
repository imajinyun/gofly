package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestKeyringSignAndVerify(t *testing.T) {
	kr, err := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("secret-one")})
	if err != nil {
		t.Fatalf("NewJWTKeyring returned error: %v", err)
	}
	now := time.Unix(1000, 0)
	token, err := kr.Sign(JWTClaims{Subject: "alice", ExpiresAt: now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	kid, err := kidFromToken(token)
	if err != nil || kid != "k1" {
		t.Fatalf("kidFromToken = %q, %v, want k1", kid, err)
	}
	claims, err := kr.Verify(token, JWTOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", claims.Subject)
	}
}

func TestKeyringRotationKeepsOldTokensValid(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	kr, _ := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("secret-one")})
	kr.SetClock(clock)

	oldToken, _ := kr.Sign(JWTClaims{Subject: "alice", ExpiresAt: now.Add(time.Hour).Unix()})

	// rotate to a new active key
	if err := kr.Rotate(SigningKey{KID: "k2", Secret: []byte("secret-two")}); err != nil {
		t.Fatalf("Rotate returned error: %v", err)
	}
	if kr.ActiveKID() != "k2" {
		t.Fatalf("active kid = %q, want k2", kr.ActiveKID())
	}

	// new tokens use k2
	newToken, _ := kr.Sign(JWTClaims{Subject: "bob", ExpiresAt: now.Add(time.Hour).Unix()})
	if kid, _ := kidFromToken(newToken); kid != "k2" {
		t.Fatalf("new token kid = %q, want k2", kid)
	}

	// old token still verifies against retained k1
	if _, err := kr.Verify(oldToken, JWTOptions{Now: clock}); err != nil {
		t.Fatalf("old token verify error: %v", err)
	}
	if _, err := kr.Verify(newToken, JWTOptions{Now: clock}); err != nil {
		t.Fatalf("new token verify error: %v", err)
	}

	// after removing k1, old tokens are rejected
	if err := kr.Remove("k1"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if _, err := kr.Verify(oldToken, JWTOptions{Now: clock}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("removed-key token error = %v, want ErrInvalidCredentials", err)
	}
}

func TestKeyringRejectsRemovingActive(t *testing.T) {
	kr, _ := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("s")})
	if err := kr.Remove("k1"); err == nil {
		t.Fatal("removing active key should error")
	}
}

func TestKeyringAddAndKIDsBoundaries_BitsUT(t *testing.T) {
	kr, err := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("secret-one")})
	if err != nil {
		t.Fatalf("NewJWTKeyring returned error: %v", err)
	}
	if err := kr.Add(SigningKey{KID: "bad"}); !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("Add invalid key error = %v, want ErrMissingCredentials", err)
	}
	if err := kr.Add(SigningKey{KID: "k2", Secret: []byte("secret-two")}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if got, want := kr.KIDs(), []string{"k1", "k2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("KIDs = %#v, want %#v", got, want)
	}
	now := time.Unix(1000, 0)
	token, err := SignJWTWithKID(JWTClaims{Subject: "bob", ExpiresAt: now.Add(time.Hour).Unix()}, "k2", []byte("secret-two"))
	if err != nil {
		t.Fatalf("SignJWTWithKID returned error: %v", err)
	}
	claims, err := kr.Verify(token, JWTOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Verify k2 token returned error: %v", err)
	}
	if claims.Subject != "bob" {
		t.Fatalf("subject = %q, want bob", claims.Subject)
	}
}

func TestKeyringValidator(t *testing.T) {
	now := time.Unix(1000, 0)
	kr, _ := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("secret")})
	kr.SetClock(func() time.Time { return now })
	token, _ := kr.Sign(JWTClaims{Subject: "alice", ExpiresAt: now.Add(time.Hour).Unix(), Extra: map[string]any{"roles": []string{"admin"}}})
	ctx, err := kr.Validator(JWTOptions{Now: func() time.Time { return now }})(context.Background(), token)
	if err != nil {
		t.Fatalf("Validator returned error: %v", err)
	}
	principal, ok := FromContext(ctx)
	if !ok || principal.Subject != "alice" || !principal.HasRole("admin") {
		t.Fatalf("principal = %#v, want alice admin", principal)
	}
}

func TestKeyringRejectsTokenWithoutKID(t *testing.T) {
	kr, _ := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("secret")})
	// token signed without kid header
	plain, _ := SignJWT(JWTClaims{Subject: "alice"}, []byte("secret"))
	if _, err := kr.Verify(plain, JWTOptions{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("no-kid token error = %v, want ErrInvalidCredentials", err)
	}
}

func TestKeyringExpiredSigningKey(t *testing.T) {
	now := time.Unix(2000, 0)
	kr, _ := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("secret"), NotAfter: time.Unix(1000, 0)})
	kr.SetClock(func() time.Time { return now })
	if _, err := kr.Sign(JWTClaims{Subject: "alice"}); !errors.Is(err, ErrNoActiveKey) {
		t.Fatalf("expired signing key error = %v, want ErrNoActiveKey", err)
	}
}

func TestNewJWTKeyringValidation(t *testing.T) {
	if _, err := NewJWTKeyring(SigningKey{KID: "", Secret: []byte("s")}); !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("missing kid error = %v, want ErrMissingCredentials", err)
	}
	if _, err := NewJWTKeyring(SigningKey{KID: "k1"}); !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("missing secret error = %v, want ErrMissingCredentials", err)
	}
}
