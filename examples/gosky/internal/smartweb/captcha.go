package smartweb

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultCaptchaTTL = 2 * time.Minute
	captchaCodeLength = 4
	captchaAlphabet   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// CaptchaChallenge mirrors smart-web's existing CaptchaVO field names. The
// plaintext answer is intentionally absent from this browser-facing value.
type CaptchaChallenge struct {
	CaptchaUUID        string `json:"captchaUuid"`
	CaptchaBase64Image string `json:"captchaBase64Image"`
	ExpireSeconds      int64  `json:"expireSeconds"`
}

type captchaRecord struct {
	code      string
	expiresAt time.Time
}

// CaptchaOptions make the ephemeral captcha store testable while production
// defaults use crypto/rand and wall-clock time.
type CaptchaOptions struct {
	TTL  time.Duration
	Now  func() time.Time
	ID   func() (string, error)
	Code func() (string, error)
}

// CaptchaService stores bounded-lifetime, one-time challenges in process.
// It is deliberately a login-abuse friction mechanism, not an identity store.
// Deployments requiring cross-instance captcha validation should terminate the
// login route at one sticky gateway or supply a shared implementation later.
type CaptchaService struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	newID   func() (string, error)
	newCode func() (string, error)
	items   map[string]captchaRecord
}

func NewCaptchaService(options CaptchaOptions) *CaptchaService {
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultCaptchaTTL
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	newID := options.ID
	if newID == nil {
		newID = randomCaptchaID
	}
	newCode := options.Code
	if newCode == nil {
		newCode = randomCaptchaCode
	}
	return &CaptchaService{ttl: ttl, now: now, newID: newID, newCode: newCode, items: make(map[string]captchaRecord)}
}

func (s *CaptchaService) NewChallenge() (CaptchaChallenge, error) {
	if s == nil || s.now == nil || s.newID == nil || s.newCode == nil {
		return CaptchaChallenge{}, errors.New("captcha service is unavailable")
	}
	id, err := s.newID()
	if err != nil || len(id) < 16 || len(id) > 128 {
		return CaptchaChallenge{}, errors.New("generate captcha identifier")
	}
	code, err := s.newCode()
	if err != nil || !validCaptchaCode(code) {
		return CaptchaChallenge{}, errors.New("generate captcha challenge")
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, record := range s.items {
		if !record.expiresAt.After(now) {
			delete(s.items, key)
		}
	}
	s.items[id] = captchaRecord{code: code, expiresAt: now.Add(s.ttl)}
	return CaptchaChallenge{
		CaptchaUUID:        id,
		CaptchaBase64Image: renderCaptchaImage(code),
		ExpireSeconds:      int64(s.ttl.Seconds()),
	}, nil
}

// Verify is intentionally consume-first: successful, malformed, and expired
// attempts all remove the record, preventing brute-force retries on one ID.
func (s *CaptchaService) Verify(id, answer string) bool {
	if s == nil || s.now == nil {
		return false
	}
	id = strings.TrimSpace(id)
	answer = strings.ToUpper(strings.TrimSpace(answer))
	if id == "" || !validCaptchaCode(answer) {
		return false
	}
	now := s.now().UTC()
	s.mu.Lock()
	record, found := s.items[id]
	if found {
		delete(s.items, id)
	}
	s.mu.Unlock()
	if !found || !record.expiresAt.After(now) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(record.code), []byte(answer)) == 1
}

func randomCaptchaID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func randomCaptchaCode() (string, error) {
	var builder strings.Builder
	builder.Grow(captchaCodeLength)
	for range captchaCodeLength {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(captchaAlphabet))))
		if err != nil {
			return "", err
		}
		builder.WriteByte(captchaAlphabet[index.Int64()])
	}
	return builder.String(), nil
}

func validCaptchaCode(value string) bool {
	if len(value) != captchaCodeLength {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune(captchaAlphabet, character) {
			return false
		}
	}
	return true
}

func renderCaptchaImage(code string) string {
	// The code alphabet is fixed and contains no XML metacharacters. SVG keeps
	// this small compatibility asset dependency-free while remaining a normal
	// browser image URL for smart-web's existing <img> element.
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="110" height="40" viewBox="0 0 110 40"><rect width="110" height="40" fill="#f5f5f5"/><path d="M4 11H106M4 29H106" stroke="#c5c5c5" stroke-width="1"/><text x="12" y="28" font-family="monospace" font-size="24" font-weight="700" letter-spacing="6" fill="#183153">%s</text></svg>`, code)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

// CacheKeys returns current live captcha ids for the support cache console.
func (s *CaptchaService) CacheKeys() []string {
	if s == nil {
		return nil
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.items))
	for key, record := range s.items {
		if record.expiresAt.After(now) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// CacheClear drops all captcha challenges for the support cache console.
func (s *CaptchaService) CacheClear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]captchaRecord)
}
