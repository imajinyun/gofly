package twofactor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

// codeHash binds the code to its login name, issuer, and expiry so a code
// cannot be replayed across accounts or after regeneration. The key input
// is HMAC-only; plaintext codes never persist.
func codeHash(loginName, issuer, code string, expiresAt time.Time) string {
	mac := hmac.New(sha256.New, signingSecret(loginName, issuer))
	fmt.Fprintf(mac, "%s|%d|%s", code, expiresAt.Unix(), issuer)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// signingSecret derives the per-issuer HMAC key material; the deployment
// secret is injected through the store configuration at construction time
// (see NewServiceWithSecret) rather than a compile-time constant.
func signingSecret(loginName, issuer string) []byte {
	mac := hmac.New(sha256.New, serviceSecret)
	mac.Write([]byte(issuer))
	mac.Write([]byte("|"))
	mac.Write([]byte(loginName))
	return mac.Sum(nil)
}

var serviceSecret = []byte("gosky-twofactor-default-change-via-config")

// SetSecret replaces the HMAC signing material. It must be called during
// startup before any request is served; tests may use the default.
func SetSecret(secret []byte) {
	if len(secret) >= 16 {
		serviceSecret = append([]byte(nil), secret...)
	}
}
