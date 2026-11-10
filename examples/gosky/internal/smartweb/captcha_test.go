package smartweb

import (
	"strings"
	"testing"
	"time"
)

// Failure-mode inventory: AUTHN-021 and AUTHN-022. Captchas are short-lived
// and single-use so a browser replay cannot satisfy a later login request.
func TestCaptchaServiceConsumesAndExpiresChallenges(t *testing.T) {
	now := time.Date(2026, time.October, 6, 4, 0, 0, 0, time.UTC)
	service := NewCaptchaService(CaptchaOptions{
		TTL:  time.Minute,
		Now:  func() time.Time { return now },
		Code: func() (string, error) { return "ABCD", nil },
		ID:   func() (string, error) { return "captcha-id-000001", nil },
	})
	challenge, err := service.NewChallenge()
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	if challenge.CaptchaUUID != "captcha-id-000001" || challenge.ExpireSeconds != 60 || !strings.HasPrefix(challenge.CaptchaBase64Image, "data:image/svg+xml;base64,") {
		t.Fatalf("challenge = %#v", challenge)
	}
	if !service.Verify(challenge.CaptchaUUID, "ABCD") {
		t.Fatal("first valid captcha verification was rejected")
	}
	if service.Verify(challenge.CaptchaUUID, "ABCD") {
		t.Fatal("consumed captcha was accepted again")
	}

	challenge, err = service.NewChallenge()
	if err != nil {
		t.Fatalf("new expiring challenge: %v", err)
	}
	now = now.Add(time.Minute)
	if service.Verify(challenge.CaptchaUUID, "ABCD") {
		t.Fatal("expired captcha was accepted")
	}
}
