// Package auth provides request authentication primitives for gofly services,
// including JWT signing/verification, OAuth2, RBAC and API key management.
package auth

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNoActiveKey is returned when a JWTKeyring has no key available for signing.
var ErrNoActiveKey = errors.New("auth: no active signing key")

// SigningKey is a versioned HMAC secret used to sign and verify JWTs. The KID
// is embedded in the token header so verifiers can pick the right secret during
// rotation windows.
type SigningKey struct {
	KID    string
	Secret []byte
	// NotAfter, when set, marks the key as retired for signing after the given
	// time. Retired keys remain valid for verification until removed.
	NotAfter time.Time
}

// JWTKeyring holds the active signing key plus any older keys still trusted for
// verification, enabling zero-downtime secret rotation. It is safe for
// concurrent use.
type JWTKeyring struct {
	mu     sync.RWMutex
	active string
	keys   map[string]SigningKey
	now    func() time.Time
}

// NewJWTKeyring creates a keyring with the given active key. Additional trusted
// (verification-only) keys can be added with Add.
func NewJWTKeyring(active SigningKey) (*JWTKeyring, error) {
	if active.KID == "" || len(active.Secret) == 0 {
		return nil, ErrMissingCredentials
	}
	kr := &JWTKeyring{
		active: active.KID,
		keys:   map[string]SigningKey{active.KID: active},
		now:    time.Now,
	}
	return kr, nil
}

// SetClock overrides the time source (used for tests).
func (kr *JWTKeyring) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	kr.mu.Lock()
	kr.now = now
	kr.mu.Unlock()
}

// Add registers a key for verification without making it active.
func (kr *JWTKeyring) Add(key SigningKey) error {
	if key.KID == "" || len(key.Secret) == 0 {
		return ErrMissingCredentials
	}
	kr.mu.Lock()
	defer kr.mu.Unlock()
	kr.keys[key.KID] = key
	return nil
}

// Rotate installs a new active signing key while keeping the previous keys
// available for verification.
func (kr *JWTKeyring) Rotate(key SigningKey) error {
	if key.KID == "" || len(key.Secret) == 0 {
		return ErrMissingCredentials
	}
	kr.mu.Lock()
	defer kr.mu.Unlock()
	kr.keys[key.KID] = key
	kr.active = key.KID
	return nil
}

// Remove drops a key from the keyring. Removing the active key is rejected.
func (kr *JWTKeyring) Remove(kid string) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	if kid == kr.active {
		return errors.New("auth: cannot remove active key")
	}
	delete(kr.keys, kid)
	return nil
}

// ActiveKID returns the KID of the current signing key.
func (kr *JWTKeyring) ActiveKID() string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.active
}

// KIDs returns all trusted key identifiers, sorted.
func (kr *JWTKeyring) KIDs() []string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	out := make([]string, 0, len(kr.keys))
	for kid := range kr.keys {
		out = append(out, kid)
	}
	sort.Strings(out)
	return out
}

func (kr *JWTKeyring) signingKey() (SigningKey, bool) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	key, ok := kr.keys[kr.active]
	if !ok {
		return SigningKey{}, false
	}
	if !key.NotAfter.IsZero() && !kr.now().Before(key.NotAfter) {
		return SigningKey{}, false
	}
	return key, true
}

func (kr *JWTKeyring) verifyKey(kid string) (SigningKey, bool) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	key, ok := kr.keys[kid]
	return key, ok
}

// Sign signs the claims with the active key, embedding its KID in the header.
func (kr *JWTKeyring) Sign(claims JWTClaims) (string, error) {
	key, ok := kr.signingKey()
	if !ok {
		return "", ErrNoActiveKey
	}
	return SignJWTWithKID(claims, key.KID, key.Secret)
}

// Verify validates a token by selecting the key referenced in its "kid" header.
// Tokens without a kid are rejected to avoid ambiguity during rotation.
func (kr *JWTKeyring) Verify(token string, opts JWTOptions) (JWTClaims, error) {
	kid, err := kidFromToken(token)
	if err != nil {
		return JWTClaims{}, err
	}
	if kid == "" {
		return JWTClaims{}, ErrInvalidCredentials
	}
	key, ok := kr.verifyKey(kid)
	if !ok {
		return JWTClaims{}, ErrInvalidCredentials
	}
	return VerifyJWT(token, key.Secret, opts)
}

// Validator returns a Validator backed by the keyring, suitable for the REST and
// gRPC auth middleware.
func (kr *JWTKeyring) Validator(opts JWTOptions) Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		claims, err := kr.Verify(token, opts)
		if err != nil {
			return ctx, err
		}
		return NewContext(ctx, claims.Principal()), nil
	}
}
