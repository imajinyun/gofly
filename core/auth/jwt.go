// Package auth provides request authentication primitives for gofly services,
// including bearer-token extraction, static validation and RBAC helpers on a
// Principal type.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const jwtAlgorithmHS256 = "HS256"

// ErrExpiredToken is returned when a JWT has passed its expiration time.
var ErrExpiredToken = errors.New("expired token")

// JWTClaims holds the standard JWT claim set used by gofly.
type JWTClaims struct {
	Subject   string         `json:"sub,omitempty"`
	Issuer    string         `json:"iss,omitempty"`
	Audience  string         `json:"aud,omitempty"`
	ExpiresAt int64          `json:"exp,omitempty"`
	IssuedAt  int64          `json:"iat,omitempty"`
	NotBefore int64          `json:"nbf,omitempty"`
	Extra     map[string]any `json:"-"`
}

// JWTOptions customises JWT signing and verification.
type JWTOptions struct {
	Issuer   string
	Audience string
	Now      func() time.Time
}

// SignJWT signs claims with secret using HS256.
func SignJWT(claims JWTClaims, secret []byte) (string, error) {
	return SignJWTWithKID(claims, "", secret)
}

// SignJWTWithKID signs the claims and, when kid is non-empty, embeds it in the
// token header so verifiers can select the matching key during rotation.
func SignJWTWithKID(claims JWTClaims, kid string, secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", ErrMissingCredentials
	}
	header := map[string]string{"alg": jwtAlgorithmHS256, "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	headerData, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal jwt header: %w", err)
	}
	payloadData, err := marshalClaims(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerData) + "." + base64.RawURLEncoding.EncodeToString(payloadData)
	signature := signHS256(signingInput, secret)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func VerifyJWT(token string, secret []byte, opts JWTOptions) (JWTClaims, error) {
	if len(secret) == 0 || token == "" {
		return JWTClaims{}, ErrMissingCredentials
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return JWTClaims{}, ErrInvalidCredentials
	}
	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return JWTClaims{}, ErrInvalidCredentials
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerData, &header); err != nil || header.Alg != jwtAlgorithmHS256 {
		return JWTClaims{}, ErrInvalidCredentials
	}
	want := signHS256(parts[0]+"."+parts[1], secret)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, want) {
		return JWTClaims{}, ErrInvalidCredentials
	}
	payloadData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return JWTClaims{}, ErrInvalidCredentials
	}
	claims, err := unmarshalClaims(payloadData)
	if err != nil {
		return JWTClaims{}, err
	}
	if err := validateClaims(claims, opts); err != nil {
		return JWTClaims{}, err
	}
	return claims, nil
}

func JWTValidator(secret []byte, opts JWTOptions) Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		claims, err := VerifyJWT(token, secret, opts)
		if err != nil {
			return ctx, err
		}
		return NewContext(ctx, claims.Principal()), nil
	}
}

// Principal derives an RBAC Principal from the claims. Roles are read from the
// "roles" claim and permissions from the "permissions" (or "scope"/"scp")
// claims, accepting either a JSON array or a space-delimited string.
func (c JWTClaims) Principal() Principal {
	p := Principal{Subject: c.Subject, Claims: c.Extra}
	p.Roles = claimToStrings(c.Extra["roles"])
	perms := claimToStrings(c.Extra["permissions"])
	if len(perms) == 0 {
		perms = claimToStrings(c.Extra["scope"])
	}
	if len(perms) == 0 {
		perms = claimToStrings(c.Extra["scp"])
	}
	p.Permissions = perms
	return p
}

func claimToStrings(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		fields := strings.Fields(v)
		if len(fields) == 0 {
			return nil
		}
		return fields
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func marshalClaims(claims JWTClaims) ([]byte, error) {
	values := make(map[string]any, len(claims.Extra)+6)
	for key, value := range claims.Extra {
		values[key] = value
	}
	if claims.Subject != "" {
		values["sub"] = claims.Subject
	}
	if claims.Issuer != "" {
		values["iss"] = claims.Issuer
	}
	if claims.Audience != "" {
		values["aud"] = claims.Audience
	}
	if claims.ExpiresAt > 0 {
		values["exp"] = claims.ExpiresAt
	}
	if claims.IssuedAt > 0 {
		values["iat"] = claims.IssuedAt
	}
	if claims.NotBefore > 0 {
		values["nbf"] = claims.NotBefore
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal jwt claims: %w", err)
	}
	return data, nil
}

func unmarshalClaims(data []byte) (JWTClaims, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return JWTClaims{}, ErrInvalidCredentials
	}
	claims := JWTClaims{Extra: map[string]any{}}
	for key, value := range raw {
		switch key {
		case "sub":
			claims.Subject, _ = value.(string)
		case "iss":
			claims.Issuer, _ = value.(string)
		case "aud":
			claims.Audience, _ = value.(string)
		case "exp":
			claims.ExpiresAt = jsonNumberInt64(value)
		case "iat":
			claims.IssuedAt = jsonNumberInt64(value)
		case "nbf":
			claims.NotBefore = jsonNumberInt64(value)
		default:
			claims.Extra[key] = value
		}
	}
	return claims, nil
}

func validateClaims(claims JWTClaims, opts JWTOptions) error {
	nowFunc := opts.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	now := nowFunc().Unix()
	if claims.ExpiresAt > 0 && now >= claims.ExpiresAt {
		return ErrExpiredToken
	}
	if claims.NotBefore > 0 && now < claims.NotBefore {
		return ErrInvalidCredentials
	}
	if opts.Issuer != "" && claims.Issuer != opts.Issuer {
		return ErrInvalidCredentials
	}
	if opts.Audience != "" && claims.Audience != opts.Audience {
		return ErrInvalidCredentials
	}
	return nil
}

func jsonNumberInt64(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// kidFromToken extracts the "kid" (key id) from a JWT header without verifying
// the signature. It returns an empty string when the header carries no kid.
func kidFromToken(token string) (string, error) {
	if token == "" {
		return "", ErrMissingCredentials
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrInvalidCredentials
	}
	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrInvalidCredentials
	}
	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
	}
	if err := json.Unmarshal(headerData, &header); err != nil || header.Alg != jwtAlgorithmHS256 {
		return "", ErrInvalidCredentials
	}
	return header.KID, nil
}

func signHS256(input string, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(input))
	return mac.Sum(nil)
}
