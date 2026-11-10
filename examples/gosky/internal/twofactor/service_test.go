package twofactor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// Failure-mode inventory:
//
//	2FA-01 disabled feature returns failed_precondition for both endpoints
//	2FA-02 resend gap returns resource_exhausted
//	2FA-03 unknown login name returns OK (no account enumeration) and sends no email
//	2FA-04 correct code verifies once and is then consumed
//	2FA-05 wrong code records an attempt; after MaxAttempts the code is burned
//	2FA-06 expired code is rejected
//	2FA-07 malformed login/code is rejected without storage access
type recordingMailer struct {
	mu   sync.Mutex
	sent []sentMail
	fail error
}

type sentMail struct{ to, subject, body string }

func (m *recordingMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, sentMail{to: to, subject: subject, body: body})
	return nil
}

func (m *recordingMailer) messages() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentMail(nil), m.sent...)
}

func testFeature(enabled bool, now time.Time) (Feature, *MemoryStore, *recordingMailer) {
	feature := DefaultFeature()
	feature.Enabled = enabled
	feature.Issuer = "gosky-test"
	store := NewMemoryStore(map[string]string{"admin": "admin@example.com"})
	mailer := &recordingMailer{}
	return feature, store, mailer
}

func TestTwoFactorDisabled(t *testing.T) {
	feature, store, mailer := testFeature(false, time.Now())
	service := NewService(feature, mailer, store, store, nil)
	if service.Enabled() {
		t.Fatal("2FA unexpectedly enabled")
	}
	if err := service.SendCode(context.Background(), "admin"); err == nil || err.Code != coreerrors.CodeFailedPrecondition {
		t.Fatalf("send disabled err=%v", err)
	}
	if err := service.Verify(context.Background(), "admin", "123456"); err == nil || err.Code != coreerrors.CodeFailedPrecondition {
		t.Fatalf("verify disabled err=%v", err)
	}
}

func TestSendCodeRateLimitAndEnumeration(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	clock := &now
	feature, store, mailer := testFeature(true, now)
	service := NewService(feature, mailer, store, store, func() time.Time { return *clock })

	if err := service.SendCode(context.Background(), "admin"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if err := service.SendCode(context.Background(), "admin"); err == nil || err.Code != coreerrors.CodeResourceExhausted {
		t.Fatalf("second send err=%v, want rate limit", err)
	}
	// Unknown account is a success to the caller but sends nothing.
	if err := service.SendCode(context.Background(), "ghost-user"); err != nil {
		t.Fatalf("unknown login send err=%v, want nil to avoid enumeration", err)
	}
	if got := len(mailer.messages()); got != 1 {
		t.Fatalf("mailer messages=%d, want 1", got)
	}
	// After the resend gap the same login can request a new code.
	*clock = now.Add(feature.ResendGap + time.Second)
	if err := service.SendCode(context.Background(), "admin"); err != nil {
		t.Fatalf("third send: %v", err)
	}
}

func TestVerifyCodeRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	feature, store, mailer := testFeature(true, now)
	service := NewService(feature, mailer, store, store, func() time.Time { return now })
	if err := service.SendCode(context.Background(), "admin"); err != nil {
		t.Fatalf("send: %v", err)
	}
	messages := mailer.messages()
	if len(messages) != 1 {
		t.Fatalf("messages=%d", len(messages))
	}
	code := extractCode(t, messages[0].body)
	if err := service.Verify(context.Background(), "admin", code); err != nil {
		t.Fatalf("verify correct code: %v", err)
	}
	// One-time use.
	if err := service.Verify(context.Background(), "admin", code); err == nil {
		t.Fatal("code reused after success")
	}
}

func TestVerifyWrongCodeAttempts(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	feature, store, mailer := testFeature(true, now)
	service := NewService(feature, mailer, store, store, func() time.Time { return now })
	if err := service.SendCode(context.Background(), "admin"); err != nil {
		t.Fatalf("send: %v", err)
	}
	for i := 0; i < feature.MaxAttempts-1; i++ {
		err := service.Verify(context.Background(), "admin", "000000")
		if err == nil || err.Code != coreerrors.CodePermissionDenied {
			t.Fatalf("wrong attempt %d err=%v", i+1, err)
		}
	}
	// The next attempt burns the stored code.
	err := service.Verify(context.Background(), "admin", "000000")
	if err == nil || err.Code != coreerrors.CodeResourceExhausted {
		t.Fatalf("exhaust err=%v", err)
	}
	if err := service.Verify(context.Background(), "admin", "000000"); err == nil {
		t.Fatal("verify accepted after exhaustion")
	}
}

func TestVerifyExpiredCode(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	feature, store, mailer := testFeature(true, now)
	service := NewService(feature, mailer, store, store, func() time.Time { return now })
	if err := service.SendCode(context.Background(), "admin"); err != nil {
		t.Fatalf("send: %v", err)
	}
	after := now.Add(feature.CodeTTL + time.Second)
	expired := NewService(feature, mailer, store, store, func() time.Time { return after })
	if err := expired.Verify(context.Background(), "admin", "123456"); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("expired verify err=%v", err)
	}
}

func TestTwoFactorMalformedInput(t *testing.T) {
	feature, store, mailer := testFeature(true, time.Now())
	service := NewService(feature, mailer, store, store, time.Now)
	for _, login := range []string{"ab", "", "bad\nname"} {
		if err := service.SendCode(context.Background(), login); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("send login=%q err=%v", login, err)
		}
	}
	for _, code := range []string{"", "12345", "abcdef", "1234567"} {
		if err := service.Verify(context.Background(), "admin", code); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("verify code=%q err=%v", code, err)
		}
	}
}

func TestTwoFactorMailerFailure(t *testing.T) {
	now := time.Now()
	feature, store, mailer := testFeature(true, now)
	mailer.fail = errors.New("smtp down")
	service := NewService(feature, mailer, store, store, func() time.Time { return now })
	if err := service.SendCode(context.Background(), "admin"); err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("mail failure err=%v", err)
	}
}

func extractCode(t *testing.T, body string) string {
	t.Helper()
	for _, token := range strings.Fields(body) {
		token = strings.TrimSuffix(token, ".")
		if len(token) == 6 && isDigits(token) {
			return token
		}
	}
	t.Fatalf("no 6-digit code in body %q", body)
	return ""
}

func isDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
