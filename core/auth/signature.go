// Package auth provides request authentication primitives for gofly services,
// including bearer-token extraction, static validation and RBAC helpers on a
// Principal type.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// SignatureHeader is the HMAC request signature header.
	SignatureHeader = "X-Gofly-Signature"
	// TimestampHeader is the request timestamp header used with signatures.
	TimestampHeader = "X-Gofly-Timestamp"
)

// SignatureOptions configures HMAC request signature verification.
type SignatureOptions struct {
	Secret []byte
	MaxAge time.Duration
	Now    func() time.Time
}

// SignRequest computes an HMAC-SHA256 signature for the given request parts.
func SignRequest(method string, path string, body []byte, timestamp int64, secret []byte) string {
	canonical := canonicalRequest(method, path, body, timestamp)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifyRequestSignature(r *http.Request, body []byte, opts SignatureOptions) error {
	if len(opts.Secret) == 0 {
		return ErrMissingCredentials
	}
	signature := r.Header.Get(SignatureHeader)
	timestampText := r.Header.Get(TimestampHeader)
	if signature == "" || timestampText == "" {
		return ErrMissingCredentials
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return ErrInvalidCredentials
	}
	if opts.MaxAge > 0 {
		now := time.Now
		if opts.Now != nil {
			now = opts.Now
		}
		delta := now().Sub(time.Unix(timestamp, 0))
		if delta < -opts.MaxAge || delta > opts.MaxAge {
			return ErrExpiredToken
		}
	}
	want := SignRequest(r.Method, r.URL.RequestURI(), body, timestamp, opts.Secret)
	if !equalSignature(signature, want) {
		return ErrInvalidCredentials
	}
	return nil
}

func canonicalRequest(method string, path string, body []byte, timestamp int64) string {
	return strings.ToUpper(method) + "\n" + path + "\n" + strconv.FormatInt(timestamp, 10) + "\n" + fmt.Sprintf("%x", sha256.Sum256(body))
}

func equalSignature(a string, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return hmac.Equal([]byte(a), []byte(b))
}
