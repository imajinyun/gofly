package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// PasswordParameters describe an Argon2id PHC password hash. The defaults
// prioritize interactive login resistance while tests may select a smaller
// bounded configuration to keep their fixture generation fast.
type PasswordParameters struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

func defaultPasswordParameters() PasswordParameters {
	return PasswordParameters{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

func (p PasswordParameters) normalized() (PasswordParameters, error) {
	defaults := defaultPasswordParameters()
	if p.MemoryKiB == 0 {
		p.MemoryKiB = defaults.MemoryKiB
	}
	if p.Iterations == 0 {
		p.Iterations = defaults.Iterations
	}
	if p.Parallelism == 0 {
		p.Parallelism = defaults.Parallelism
	}
	if p.SaltLength == 0 {
		p.SaltLength = defaults.SaltLength
	}
	if p.KeyLength == 0 {
		p.KeyLength = defaults.KeyLength
	}
	if p.MemoryKiB < 8 || p.MemoryKiB > 512*1024 || p.Iterations > 10 || p.Parallelism > 32 || p.SaltLength < 16 || p.SaltLength > 64 || p.KeyLength < 16 || p.KeyLength > 64 {
		return PasswordParameters{}, errors.New("argon2id password parameters are outside supported bounds")
	}
	return p, nil
}

// HashPassword returns an Argon2id PHC hash using the production default
// parameters. User-administration code uses it for initial and reset
// passwords so every stored hash follows the same verification algorithm.
func HashPassword(password string) (string, error) {
	return hashPassword(password, PasswordParameters{})
}

// VerifyPassword checks a plaintext password against a stored PHC string.
// It returns ErrAuthenticationRejected on any mismatch or malformed hash.
func VerifyPassword(password, encoded string) error {
	return verifyPassword(password, encoded)
}

// ValidatePasswordComplexity applies the SmartAdmin complexity rule when
// enabled: 8-20 characters with at least three of four classes
// (lowercase, uppercase, digit, special). With complexity disabled only a
// minimum length is enforced.
func ValidatePasswordComplexity(password string, complexityRequired bool) error {
	length := len([]rune(password))
	if !validOpaqueToken(password) {
		return ErrAuthenticationRejected
	}
	if !complexityRequired {
		if length < 8 {
			return ErrPasswordTooWeak
		}
		return nil
	}
	var classes int
	hasClass := func(match bool) {
		if match {
			classes++
		}
	}
	hasClass(regexpMatchAny(password, unicode.IsLower))
	hasClass(regexpMatchAny(password, unicode.IsUpper))
	hasClass(regexpMatchAny(password, unicode.IsDigit))
	hasClass(strings.ContainsAny(password, `!@#$%^&*~()_+-=`+"`"))
	if length < 8 || length > 20 || classes < 3 {
		return ErrPasswordTooWeak
	}
	return nil
}

func regexpMatchAny(value string, class func(rune) bool) bool {
	for _, r := range value {
		if class(r) {
			return true
		}
	}
	return false
}

func hashPassword(password string, parameters PasswordParameters) (string, error) {
	parameters, err := parameters.normalized()
	if err != nil {
		return "", err
	}
	if !validOpaqueToken(password) {
		return "", errors.New("password is invalid")
	}
	salt, err := randomBytes(rand.Reader, int(parameters.SaltLength))
	if err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, parameters.Iterations, parameters.MemoryKiB, parameters.Parallelism, parameters.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, parameters.MemoryKiB, parameters.Iterations, parameters.Parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(password, encoded string) error {
	parameters, salt, expectedKey, err := parsePasswordHash(encoded)
	if err != nil || !validOpaqueToken(password) {
		return ErrAuthenticationRejected
	}
	actualKey := argon2.IDKey([]byte(password), salt, parameters.Iterations, parameters.MemoryKiB, parameters.Parallelism, parameters.KeyLength)
	if subtle.ConstantTimeCompare(actualKey, expectedKey) != 1 {
		return ErrAuthenticationRejected
	}
	return nil
}

func parsePasswordHash(encoded string) (PasswordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordParameters{}, nil, nil, errors.New("password hash format is invalid")
	}
	parameters, err := parsePasswordParameters(parts[3])
	if err != nil {
		return PasswordParameters{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("password hash salt is invalid")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("password hash key is invalid")
	}
	saltLength, keyLength := len(salt), len(key)
	// Stored hashes need explicit lengths. Zero-value configuration defaults
	// must not turn an empty stored salt into an accepted password hash.
	if saltLength < 16 || saltLength > 64 || keyLength < 16 || keyLength > 64 {
		return PasswordParameters{}, nil, nil, errors.New("password hash parameters are invalid")
	}
	parameters.SaltLength = uint32(saltLength)
	parameters.KeyLength = uint32(keyLength)
	parameters, err = parameters.normalized()
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("password hash parameters are invalid")
	}
	return parameters, salt, key, nil
}

func parsePasswordParameters(value string) (PasswordParameters, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return PasswordParameters{}, errors.New("password hash parameters are invalid")
	}
	parse := func(prefix, value string, max uint64) (uint64, error) {
		if !strings.HasPrefix(value, prefix) {
			return 0, errors.New("password hash parameter is invalid")
		}
		parsed, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 32)
		if err != nil || parsed > max {
			return 0, errors.New("password hash parameter is invalid")
		}
		return parsed, nil
	}
	memory, err := parse("m=", parts[0], 512*1024)
	if err != nil {
		return PasswordParameters{}, err
	}
	iterations, err := parse("t=", parts[1], 10)
	if err != nil {
		return PasswordParameters{}, err
	}
	parallelism, err := parse("p=", parts[2], 32)
	if err != nil {
		return PasswordParameters{}, err
	}
	// Keep the parse limits explicit at the narrowing boundary as well.
	if memory > 512*1024 || iterations > 10 || parallelism > 32 {
		return PasswordParameters{}, errors.New("password hash parameters are invalid")
	}
	return PasswordParameters{MemoryKiB: uint32(memory), Iterations: uint32(iterations), Parallelism: uint8(parallelism)}, nil
}
