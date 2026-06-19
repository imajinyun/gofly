// Package auth provides request authentication primitives for gofly services,
// including bearer-token extraction, static validation and RBAC helpers on a
// Principal type.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// OAuth2 grant types supported by the token endpoint.
const (
	GrantClientCredentials = "client_credentials"
	GrantRefreshToken      = "refresh_token"
)

// OAuth2Client is a registered confidential client allowed to obtain tokens via
// the client_credentials grant.
type OAuth2Client struct {
	ID     string
	Secret string
	// Scopes the client is permitted to request. An empty slice grants no
	// scopes; use {"*"} to allow any requested scope.
	Scopes []string
	// Audience embedded in issued tokens (optional).
	Audience string
}

// allows reports whether the client is permitted the given scope.
func (c OAuth2Client) allows(scope string) bool {
	for _, s := range c.Scopes {
		if s == "*" || s == scope {
			return true
		}
	}
	return false
}

// OAuth2Token is the response body of a successful token request, matching
// RFC 6749 section 5.1.
type OAuth2Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

// OAuth2Error is the RFC 6749 section 5.2 error response.
type OAuth2Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
	status      int
}

func (e *OAuth2Error) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

func oauthError(status int, code, description string) *OAuth2Error {
	return &OAuth2Error{Code: code, Description: description, status: status}
}

// OAuth2Config configures the token endpoint.
type OAuth2Config struct {
	// Keyring signs the issued JWT access tokens, enabling key rotation.
	Keyring *JWTKeyring
	// Issuer placed in the "iss" claim.
	Issuer string
	// TTL is the access-token lifetime; defaults to one hour.
	TTL time.Duration
	// Clients is the registry of confidential clients keyed by client id.
	Clients map[string]OAuth2Client
	// Now overrides the clock (tests).
	Now func() time.Time
}

// OAuth2Server issues JWT access tokens via the OAuth2 client_credentials grant.
type OAuth2Server struct {
	conf OAuth2Config
}

// NewOAuth2Server builds a token server. A keyring is required for signing.
func NewOAuth2Server(conf OAuth2Config) (*OAuth2Server, error) {
	if conf.Keyring == nil {
		return nil, ErrNoActiveKey
	}
	if conf.TTL <= 0 {
		conf.TTL = time.Hour
	}
	if conf.Now == nil {
		conf.Now = time.Now
	}
	if conf.Clients == nil {
		conf.Clients = map[string]OAuth2Client{}
	}
	return &OAuth2Server{conf: conf}, nil
}

// Issue authenticates a client and returns a signed access token covering the
// requested scopes. Requested scopes must all be permitted by the client.
func (s *OAuth2Server) Issue(clientID, clientSecret string, scopes []string) (OAuth2Token, error) {
	client, ok := s.conf.Clients[clientID]
	if !ok {
		return OAuth2Token{}, oauthError(http.StatusUnauthorized, "invalid_client", "unknown client")
	}
	if subtle.ConstantTimeCompare([]byte(clientSecret), []byte(client.Secret)) != 1 {
		return OAuth2Token{}, oauthError(http.StatusUnauthorized, "invalid_client", "invalid client secret")
	}
	granted := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if !client.allows(scope) {
			return OAuth2Token{}, oauthError(http.StatusBadRequest, "invalid_scope", "scope not permitted: "+scope)
		}
		granted = append(granted, scope)
	}
	sort.Strings(granted)

	now := s.conf.Now()
	claims := JWTClaims{
		Subject:   clientID,
		Issuer:    s.conf.Issuer,
		Audience:  client.Audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.conf.TTL).Unix(),
		Extra:     map[string]any{"scope": strings.Join(granted, " "), "client_id": clientID},
	}
	token, err := s.conf.Keyring.Sign(claims)
	if err != nil {
		return OAuth2Token{}, oauthError(http.StatusInternalServerError, "server_error", err.Error())
	}
	return OAuth2Token{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(s.conf.TTL / time.Second),
		Scope:       strings.Join(granted, " "),
	}, nil
}

// TokenHandler exposes the token endpoint as an http.Handler. It accepts
// application/x-www-form-urlencoded POST requests carrying grant_type, scope and
// either form-encoded or Basic-auth client credentials.
func (s *OAuth2Server) TokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeOAuthError(w, oauthError(http.StatusMethodNotAllowed, "invalid_request", "POST required"))
			return
		}
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, oauthError(http.StatusBadRequest, "invalid_request", "malformed form"))
			return
		}
		grant := r.PostForm.Get("grant_type")
		if grant != GrantClientCredentials {
			writeOAuthError(w, oauthError(http.StatusBadRequest, "unsupported_grant_type", grant))
			return
		}
		clientID, clientSecret := clientCredentials(r)
		scopes := strings.Fields(r.PostForm.Get("scope"))
		token, err := s.Issue(clientID, clientSecret, scopes)
		if err != nil {
			writeOAuthError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		// #nosec G117 -- OAuth2 RFC 6749 token responses intentionally marshal the access_token field.
		_ = json.NewEncoder(w).Encode(token)
	})
}

func clientCredentials(r *http.Request) (string, string) {
	if id, secret, ok := r.BasicAuth(); ok {
		// Basic-auth values are URL-encoded per RFC 6749 section 2.3.1.
		if decoded, err := url.QueryUnescape(id); err == nil {
			id = decoded
		}
		if decoded, err := url.QueryUnescape(secret); err == nil {
			secret = decoded
		}
		return id, secret
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

func writeOAuthError(w http.ResponseWriter, err error) {
	oerr, ok := err.(*OAuth2Error)
	if !ok {
		oerr = oauthError(http.StatusInternalServerError, "server_error", err.Error())
	}
	status := oerr.status
	if status == 0 {
		status = http.StatusBadRequest
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Basic")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(oerr)
}

// OAuth2Validator returns a Validator that verifies bearer access tokens issued
// by this server's keyring and enforces that the token carries every required
// scope.
func OAuth2Validator(keyring *JWTKeyring, opts JWTOptions, requiredScopes ...string) Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		claims, err := keyring.Verify(token, opts)
		if err != nil {
			return ctx, err
		}
		principal := claims.Principal()
		if len(requiredScopes) > 0 && !principal.HasAllPermissions(requiredScopes...) {
			return ctx, ErrPermissionDenied
		}
		return NewContext(ctx, principal), nil
	}
}
