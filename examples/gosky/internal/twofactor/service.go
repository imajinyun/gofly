// Package twofactor implements the audited email-code second factor for the
// SmartAdmin login flow. T4 scope decision (recorded in the long-range plan):
// build the full verification flow with a configurable Mailer port and
// rate limiting, but ship the local driver as disabled-by-default; turning
// 2FA on requires explicit identityStore.twoFactor configuration and a
// configured SMTP transport.
package twofactor

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// Feature controls whether the email second factor is offered.
type Feature struct {
	Enabled     bool          `json:"enabled"`
	CodeTTL     time.Duration `json:"-"`
	ResendGap   time.Duration `json:"-"`
	MaxAttempts int           `json:"-"`
	Issuer      string        `json:"-"`
}

// DefaultFeature returns the disabled deployment configuration.
func DefaultFeature() Feature {
	return Feature{
		Enabled:     false,
		CodeTTL:     5 * time.Minute,
		ResendGap:   60 * time.Second,
		MaxAttempts: 5,
		Issuer:      "gosky",
	}
}

// Mailer delivers a code email. Deployments without SMTP wire no mailer;
// SendCode then fails unavailable rather than silently logging secrets.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// IdentityResolver finds a login name's delivery email without revealing
// whether the account exists (see SendCode behavior).
type IdentityResolver interface {
	LoginEmail(ctx context.Context, loginName string) (email string, known bool, err error)
}

// CodeStore persists hashed codes and attempt state.
type CodeStore interface {
	Save(ctx context.Context, loginName, codeHash string, expiresAt time.Time) error
	Load(ctx context.Context, loginName string) (codeHash string, expiresAt time.Time, exists bool, err error)
	Delete(ctx context.Context, loginName string) error
	CountAttempts(ctx context.Context, loginName string, since time.Time) (int, error)
	RecordAttempt(ctx context.Context, loginName string, at time.Time) error
}

// Service issues and verifies email login codes.
type Service struct {
	feature  Feature
	mailer   Mailer
	store    CodeStore
	resolver IdentityResolver
	now      func() time.Time
	mu       sync.Mutex
	lastSent map[string]time.Time
}

func NewService(feature Feature, mailer Mailer, store CodeStore, resolver IdentityResolver, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		feature:  normalize(feature),
		mailer:   mailer,
		store:    store,
		resolver: resolver,
		now:      now,
		lastSent: make(map[string]time.Time),
	}
}

func normalize(feature Feature) Feature {
	defaults := DefaultFeature()
	if feature.CodeTTL <= 0 {
		feature.CodeTTL = defaults.CodeTTL
	}
	if feature.ResendGap <= 0 {
		feature.ResendGap = defaults.ResendGap
	}
	if feature.MaxAttempts <= 0 {
		feature.MaxAttempts = defaults.MaxAttempts
	}
	return feature
}

// Enabled reports whether the login page should show the email-code field.
func (s *Service) Enabled() bool { return s != nil && s.feature.Enabled }

// ErrTwoFactorDisabled is returned when the feature is not configured.
var ErrTwoFactorDisabled = errors.New("two factor is disabled")

// SendCode issues and emails a new code. It returns nil for an unknown
// login name to avoid account enumeration; rate limiting is enforced first.
func (s *Service) SendCode(ctx context.Context, loginName string) *coreerrors.Error {
	if s == nil || !s.feature.Enabled {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "two factor is disabled")
	}
	loginName = normalizeLoginName(loginName)
	if !validLoginName(loginName) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "login name is invalid")
	}
	if s.mailer == nil {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "two factor mail transport is not configured")
	}

	s.mu.Lock()
	if last, ok := s.lastSent[loginName]; ok && s.now().Sub(last) < s.feature.ResendGap {
		s.mu.Unlock()
		return coreerrors.New(coreerrors.CodeResourceExhausted, "verification code requested too often")
	}
	s.mu.Unlock()

	email, known, err := s.resolver.LoginEmail(ctx, loginName)
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "resolve login", err)
	}
	// Issue the code even for unknown identities would leak existence;
	// rate limiting above still limits calls.
	if !known || email == "" {
		return nil
	}
	code, hash, err := generateCodeAndHash(loginName, s.feature.Issuer, s.now(), s.now().Add(s.feature.CodeTTL))
	if err != nil {
		return coreerrors.New(coreerrors.CodeInternal, "generate verification code")
	}
	if err := s.store.Save(ctx, loginName, hash, s.now().Add(s.feature.CodeTTL)); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "store verification code", err)
	}
	body := fmt.Sprintf("Your %s login verification code is %s. It expires in %d minutes.",
		s.feature.Issuer, code, int(s.feature.CodeTTL.Minutes()))
	if err := s.mailer.Send(ctx, email, s.feature.Issuer+" login code", body); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "send verification email", err)
	}
	s.mu.Lock()
	s.lastSent[loginName] = s.now()
	s.mu.Unlock()
	return nil
}

// Verify validates a submitted code. On success the stored code is
// consumed; on failure the attempt is counted until the limit is reached.
func (s *Service) Verify(ctx context.Context, loginName, code string) *coreerrors.Error {
	if s == nil || !s.feature.Enabled {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "two factor is disabled")
	}
	loginName = normalizeLoginName(loginName)
	code = strings.TrimSpace(code)
	if !validLoginName(loginName) || !validCode(code) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "verification code is invalid")
	}
	hash, expiresAt, exists, err := s.store.Load(ctx, loginName)
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "read verification code", err)
	}
	if !exists || s.now().After(expiresAt) {
		return coreerrors.New(coreerrors.CodePermissionDenied, "verification code has expired")
	}
	attempts, err := s.store.CountAttempts(ctx, loginName, s.now().Add(-s.feature.CodeTTL))
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "count verification attempts", err)
	}
	if attempts >= s.feature.MaxAttempts {
		_ = s.store.Delete(ctx, loginName)
		return coreerrors.New(coreerrors.CodeResourceExhausted, "too many verification attempts")
	}
	// The current submission counts against the limit.
	if err := s.store.RecordAttempt(ctx, loginName, s.now()); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "record verification attempt", err)
	}
	if attempts+1 >= s.feature.MaxAttempts {
		_ = s.store.Delete(ctx, loginName)
		return coreerrors.New(coreerrors.CodeResourceExhausted, "too many verification attempts")
	}
	expected := codeHash(loginName, s.feature.Issuer, code, expiresAt)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(hash)) != 1 {
		return coreerrors.New(coreerrors.CodePermissionDenied, "verification code is incorrect")
	}
	if err := s.store.Delete(ctx, loginName); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "consume verification code", err)
	}
	return nil
}

func normalizeLoginName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validLoginName(value string) bool {
	length := len([]rune(value))
	return length >= 3 && length <= 64 && !strings.ContainsAny(value, "\x00\n\r")
}

func validCode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// generateCodeAndHash produces a 6-digit numeric code and its HMAC hash.
func generateCodeAndHash(loginName, issuer string, _, expiresAt time.Time) (string, string, error) {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	return code, codeHash(loginName, issuer, code, expiresAt), nil
}

// RateLimitKeys returns login names currently carrying a resend throttle,
// for the support cache console.
func (s *Service) RateLimitKeys() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.lastSent))
	for key := range s.lastSent {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ClearRateLimit drops all resend-throttle timestamps.
func (s *Service) ClearRateLimit() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSent = make(map[string]time.Time)
}
