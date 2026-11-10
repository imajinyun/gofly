package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/metadata"
)

var (
	// ErrAuthenticationRejected is intentionally credential-agnostic. Callers
	// must not reveal whether a login, tenant, session, or refresh token exists.
	ErrAuthenticationRejected = errors.New("authentication rejected")
	// ErrAuthenticationUnavailable identifies a storage, random-source, signing,
	// or audit failure that prevented a complete authentication decision.
	ErrAuthenticationUnavailable = errors.New("authentication unavailable")
	// ErrInvalidSessionRequest identifies a malformed self-service session
	// management request. It is separate from credential rejection so the HTTP
	// boundary can return a useful 400 response without disclosing session state.
	ErrInvalidSessionRequest = errors.New("invalid session management request")
	// ErrPasswordTooWeak indicates a self-service password change failed the
	// configured length/complexity policy.
	ErrPasswordTooWeak = errors.New("password does not meet complexity requirements")
)

const (
	defaultAccessTokenIssuer   = "gosky"
	defaultAccessTokenAudience = "gosky-web"
	minimumAccessSecretBytes   = 32
	maxCredentialLength        = 1024
)

// ServiceOptions own authentication policy rather than exposing it through an
// HTTP handler. AccessSecret is deployment-owned; the service never reads it
// from a config file or response payload.
type ServiceOptions struct {
	Repository         IdentityRepository
	AccessSecret       []byte
	Issuer             string
	Audience           string
	AccessTTL          time.Duration
	RefreshTTL         time.Duration
	PasswordParameters PasswordParameters
	Now                func() time.Time
	Random             io.Reader
}

// Service owns password verification, short-lived access-token issuance,
// opaque refresh-token rotation, revocation checks, and authentication audit.
type Service struct {
	repository         IdentityRepository
	accessSecret       []byte
	issuer             string
	audience           string
	accessTTL          time.Duration
	refreshTTL         time.Duration
	passwordParameters PasswordParameters
	now                func() time.Time
	random             io.Reader
}

// LoginInput is deliberately a DTO rather than a persistence entity. Password
// is accepted only for verification and is never included in TokenPair or audit
// records.
type LoginInput struct {
	Realm      Realm
	LoginName  string
	TenantCode string
	Password   string
	RequestID  string
	TraceID    string
}

// SmartWebLoginInput represents the historical smart-web login form after its
// browser-only captcha and password transport have been validated. It has no
// tenant selector because that page has never had one.
type SmartWebLoginInput struct {
	LoginName string
	Password  string
	RequestID string
	TraceID   string
}

// SmartWebIdentity is the minimal current-account projection needed to build
// smart-web's LoginResultVO-compatible JSON. It deliberately excludes the
// password hash, tenant code, session generation, and refresh credentials.
type SmartWebIdentity struct {
	UserID    uint64
	TenantID  uint64
	Subject   string
	LoginName string
}

// SmartWebLoginResult combines an existing Gosky access token with the
// account metadata expected by smart-web. The opaque refresh token remains on
// the native auth API and is never exposed through this compatibility path.
type SmartWebLoginResult struct {
	AccessToken string
	Identity    SmartWebIdentity
}

// RefreshInput carries one opaque refresh credential. The token is hashed
// before repository access and is never stored in an audit record.
type RefreshInput struct {
	Realm        Realm
	RefreshToken string
	RequestID    string
	TraceID      string
}

// LogoutInput binds logout to the principal established by the appropriate
// realm interceptor; a request body cannot choose the target session.
type LogoutInput struct {
	Principal RequestPrincipal
	RequestID string
	TraceID   string
}

// SessionListInput is derived entirely from the verified admin request
// principal. It deliberately has no user, tenant, or realm fields that an
// HTTP client could use to enumerate another identity's sessions.
type SessionListInput struct {
	Principal RequestPrincipal
	Limit     uint
	RequestID string
	TraceID   string
}

// SessionSummary is the client-safe view of an active session. Refresh token
// values, hashes, generations, and user IDs intentionally stay private.
type SessionSummary struct {
	SessionID  string    `json:"sessionId"`
	Current    bool      `json:"current"`
	IssuedAt   time.Time `json:"issuedAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// SessionListResponse is the bounded active-session response for the current
// authenticated admin identity.
type SessionListResponse struct {
	Items []SessionSummary `json:"items"`
}

// SessionRevokeInput targets one opaque session ID, but ownership, tenant,
// and realm remain derived from Principal.
type SessionRevokeInput struct {
	Principal RequestPrincipal
	SessionID string
	RequestID string
	TraceID   string
}

// SessionRevokeResponse is idempotent. Changed=false reveals neither whether
// an identifier exists nor why it was outside the caller's scope.
type SessionRevokeResponse struct {
	Changed bool `json:"changed"`
}

// AdminSessionService owns the authenticated admin self-service session
// surface. It is intentionally distinct from the public login/refresh/logout
// transport contract so routes can enable it only under /api/admin/**.
type AdminSessionService interface {
	ListSessions(context.Context, SessionListInput) (SessionListResponse, error)
	RevokeSession(context.Context, SessionRevokeInput) (SessionRevokeResponse, error)
}

// TokenPair is the HTTP-safe token response. It intentionally contains no
// password hash, user ID, session generation, or persistence timestamps.
type TokenPair struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenType    string `json:"tokenType"`
	ExpiresIn    int64  `json:"expiresIn"`
}

// NewService validates all startup-only authentication settings. A service
// cannot accidentally start with an empty secret or non-positive lifetimes.
func NewService(options ServiceOptions) (*Service, error) {
	if options.Repository == nil {
		return nil, errors.New("identity repository is required")
	}
	if len(options.AccessSecret) < minimumAccessSecretBytes {
		return nil, fmt.Errorf("access secret must contain at least %d bytes", minimumAccessSecretBytes)
	}
	if options.AccessTTL <= 0 || options.RefreshTTL <= options.AccessTTL {
		return nil, errors.New("refresh TTL must be greater than positive access TTL")
	}
	parameters, err := options.PasswordParameters.normalized()
	if err != nil {
		return nil, err
	}
	issuer := strings.TrimSpace(options.Issuer)
	if issuer == "" {
		issuer = defaultAccessTokenIssuer
	}
	audience := strings.TrimSpace(options.Audience)
	if audience == "" {
		audience = defaultAccessTokenAudience
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	return &Service{
		repository:         options.Repository,
		accessSecret:       append([]byte(nil), options.AccessSecret...),
		issuer:             issuer,
		audience:           audience,
		accessTTL:          options.AccessTTL,
		refreshTTL:         options.RefreshTTL,
		passwordParameters: parameters,
		now:                now,
		random:             random,
	}, nil
}

// JWTValidator returns a verifier constrained to this service's issuer and
// audience. Session validity is checked separately by ValidatePrincipal.
func (s *Service) JWTValidator() auth.Validator {
	if s == nil {
		return func(ctx context.Context, _ string) (context.Context, error) {
			return ctx, auth.ErrInvalidCredentials
		}
	}
	return auth.JWTValidator(s.accessSecret, auth.JWTOptions{Issuer: s.issuer, Audience: s.audience, Now: s.now})
}

// Login verifies credentials, persists a refresh session, signs an access
// token, and writes one credential-free audit record before returning tokens.
func (s *Service) Login(ctx context.Context, input LoginInput) (TokenPair, error) {
	if s == nil || !validLoginInput(input) {
		return TokenPair{}, ErrAuthenticationRejected
	}
	identity, err := s.repository.FindLoginIdentity(ctx, strings.TrimSpace(input.LoginName), strings.TrimSpace(input.TenantCode), string(input.Realm))
	if errors.Is(err, ErrIdentityNotFound) {
		return TokenPair{}, s.rejectedAudit(ctx, input.Realm, input.LoginName, "", nil, nil, AuthOperationLogin, "invalid_credentials", input.RequestID, input.TraceID)
	}
	if err != nil {
		return TokenPair{}, unavailable("find login identity", err)
	}
	if err := verifyPassword(input.Password, identity.PasswordHash); err != nil {
		userID, tenantID := identity.UserID, identity.TenantID
		return TokenPair{}, s.rejectedAudit(ctx, input.Realm, input.LoginName, identity.Subject, &userID, &tenantID, AuthOperationLogin, "invalid_credentials", input.RequestID, input.TraceID)
	}

	now := s.now().UTC()
	sessionID, err := s.randomHex(16)
	if err != nil {
		return TokenPair{}, unavailable("generate session ID", err)
	}
	refreshToken, err := s.randomOpaqueToken(32)
	if err != nil {
		return TokenPair{}, unavailable("generate refresh token", err)
	}
	accessToken, err := s.issueAccessToken(identity.Subject, identity.TenantID, input.Realm, sessionID, now)
	if err != nil {
		return TokenPair{}, unavailable("sign access token", err)
	}
	if err := s.repository.CreateSession(ctx, SessionRecord{
		SessionID:        sessionID,
		UserID:           identity.UserID,
		TenantID:         identity.TenantID,
		Realm:            string(input.Realm),
		RefreshTokenHash: hashRefreshToken(refreshToken),
		IssuedAt:         now,
		ExpiresAt:        now.Add(s.refreshTTL),
		LastSeenAt:       now,
	}); err != nil {
		return TokenPair{}, unavailable("create refresh session", err)
	}
	if err := s.recordAudit(ctx, AuthAuditRecord{UserID: &identity.UserID, TenantID: &identity.TenantID, Realm: string(input.Realm), LoginName: strings.TrimSpace(input.LoginName), Subject: identity.Subject, Operation: AuthOperationLogin, Outcome: AuthOutcomeAllowed, RequestID: input.RequestID, TraceID: input.TraceID}); err != nil {
		return TokenPair{}, unavailable("record login audit", err)
	}
	return TokenPair{AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer", ExpiresIn: int64(s.accessTTL.Seconds())}, nil
}

// LoginSmartWeb first resolves exactly one active admin tenant membership for
// a login name, then delegates password verification and session issuance to
// the native Login path. An ambiguous name is deliberately indistinguishable
// from an invalid credential at the HTTP boundary.
func (s *Service) LoginSmartWeb(ctx context.Context, input SmartWebLoginInput) (SmartWebLoginResult, error) {
	if s == nil || !validLoginName(input.LoginName) || !validOpaqueToken(input.Password) {
		return SmartWebLoginResult{}, ErrAuthenticationRejected
	}
	identity, err := s.repository.FindUniqueLoginIdentity(ctx, strings.TrimSpace(input.LoginName), string(RealmAdmin))
	if errors.Is(err, ErrIdentityNotFound) || errors.Is(err, ErrIdentityAmbiguous) {
		return SmartWebLoginResult{}, s.rejectedAudit(ctx, RealmAdmin, input.LoginName, "", nil, nil, AuthOperationLogin, "invalid_credentials", input.RequestID, input.TraceID)
	}
	if err != nil {
		return SmartWebLoginResult{}, unavailable("find smart-web login identity", err)
	}
	tokens, err := s.Login(ctx, LoginInput{
		Realm:      RealmAdmin,
		LoginName:  input.LoginName,
		TenantCode: identity.TenantCode,
		Password:   input.Password,
		RequestID:  input.RequestID,
		TraceID:    input.TraceID,
	})
	if err != nil {
		return SmartWebLoginResult{}, err
	}
	return SmartWebLoginResult{
		AccessToken: tokens.AccessToken,
		Identity: SmartWebIdentity{
			UserID:    identity.UserID,
			TenantID:  identity.TenantID,
			Subject:   identity.Subject,
			LoginName: identity.LoginName,
		},
	}, nil
}

// SmartWebLoginInfo returns current account data only after the caller has
// passed the AdminInterceptor. It repeats the active-session identity binding
// defensively so its result cannot outlive a revoked or disabled account.
func (s *Service) SmartWebLoginInfo(ctx context.Context, principal RequestPrincipal) (SmartWebIdentity, error) {
	if s == nil || !validAdminSessionPrincipal(principal) {
		return SmartWebIdentity{}, ErrAuthenticationRejected
	}
	identity, err := s.repository.FindSmartWebIdentity(ctx, principal.SessionID, principal.Subject, principal.TenantID, string(RealmAdmin), s.now().UTC())
	if errors.Is(err, ErrIdentityNotFound) {
		return SmartWebIdentity{}, ErrAuthenticationRejected
	}
	if err != nil {
		return SmartWebIdentity{}, unavailable("find smart-web login identity", err)
	}
	return SmartWebIdentity{UserID: identity.UserID, TenantID: identity.TenantID, Subject: identity.Subject, LoginName: identity.LoginName}, nil
}

// Refresh atomically consumes a valid refresh-token digest before returning a
// replacement pair. Replaying the previous opaque token returns the same
// credential-agnostic rejection as an unknown token.
func (s *Service) Refresh(ctx context.Context, input RefreshInput) (TokenPair, error) {
	if s == nil || !validRealm(input.Realm) || !validOpaqueToken(input.RefreshToken) {
		return TokenPair{}, ErrAuthenticationRejected
	}
	now := s.now().UTC()
	previousHash := hashRefreshToken(input.RefreshToken)
	identity, err := s.repository.FindRefreshIdentity(ctx, previousHash, now)
	if errors.Is(err, ErrIdentityNotFound) {
		return TokenPair{}, s.rejectedAudit(ctx, input.Realm, "", "", nil, nil, AuthOperationRefresh, "invalid_refresh_token", input.RequestID, input.TraceID)
	}
	if err != nil {
		return TokenPair{}, unavailable("find refresh identity", err)
	}
	if identity.Session.Realm != string(input.Realm) || !validSubject(identity.Subject) {
		userID, tenantID := identity.Session.UserID, identity.Session.TenantID
		return TokenPair{}, s.rejectedAudit(ctx, input.Realm, "", identity.Subject, &userID, &tenantID, AuthOperationRefresh, "realm_mismatch", input.RequestID, input.TraceID)
	}
	nextToken, err := s.randomOpaqueToken(32)
	if err != nil {
		return TokenPair{}, unavailable("generate replacement refresh token", err)
	}
	accessToken, err := s.issueAccessToken(identity.Subject, identity.Session.TenantID, input.Realm, identity.Session.SessionID, now)
	if err != nil {
		return TokenPair{}, unavailable("sign replacement access token", err)
	}
	rotated, err := s.repository.RotateRefreshToken(ctx, previousHash, hashRefreshToken(nextToken), now)
	if err != nil {
		return TokenPair{}, unavailable("rotate refresh token", err)
	}
	if !rotated {
		userID, tenantID := identity.Session.UserID, identity.Session.TenantID
		return TokenPair{}, s.rejectedAudit(ctx, input.Realm, "", identity.Subject, &userID, &tenantID, AuthOperationRefresh, "refresh_replayed", input.RequestID, input.TraceID)
	}
	userID, tenantID := identity.Session.UserID, identity.Session.TenantID
	if err := s.recordAudit(ctx, AuthAuditRecord{UserID: &userID, TenantID: &tenantID, Realm: string(input.Realm), Subject: identity.Subject, Operation: AuthOperationRefresh, Outcome: AuthOutcomeAllowed, RequestID: input.RequestID, TraceID: input.TraceID}); err != nil {
		return TokenPair{}, unavailable("record refresh audit", err)
	}
	return TokenPair{AccessToken: accessToken, RefreshToken: nextToken, TokenType: "Bearer", ExpiresIn: int64(s.accessTTL.Seconds())}, nil
}

// Logout revokes only the session selected by a verified request principal.
// It is idempotent so client retries do not create a second state transition.
func (s *Service) Logout(ctx context.Context, input LogoutInput) error {
	if s == nil || !validRequestPrincipal(input.Principal) {
		return ErrAuthenticationRejected
	}
	now := s.now().UTC()
	if err := s.repository.RevokeSession(ctx, input.Principal.SessionID, now); err != nil {
		return unavailable("revoke refresh session", err)
	}
	if err := s.recordAudit(ctx, AuthAuditRecord{TenantID: &input.Principal.TenantID, Realm: string(input.Principal.Realm), Subject: input.Principal.Subject, Operation: AuthOperationLogout, Outcome: AuthOutcomeAllowed, RequestID: input.RequestID, TraceID: input.TraceID}); err != nil {
		return unavailable("record logout audit", err)
	}
	return nil
}

// ListSessions returns only non-revoked, non-expired admin sessions for the
// verified request principal's canonical user record in its current tenant.
// Resolving the current session first keeps the repository scope independent
// from caller-controlled identity fields.
func (s *Service) ListSessions(ctx context.Context, input SessionListInput) (SessionListResponse, error) {
	if s == nil || !validAdminSessionPrincipal(input.Principal) || input.Limit < 1 || input.Limit > 100 {
		return SessionListResponse{}, ErrInvalidSessionRequest
	}
	current, err := s.currentSession(ctx, input.Principal)
	if err != nil {
		return SessionListResponse{}, err
	}
	sessions, err := s.repository.ListActiveSessions(ctx, current.UserID, current.TenantID, string(RealmAdmin), s.now().UTC(), input.Limit)
	if err != nil {
		return SessionListResponse{}, unavailable("list active sessions", err)
	}
	response := SessionListResponse{Items: make([]SessionSummary, 0, len(sessions))}
	for _, session := range sessions {
		response.Items = append(response.Items, SessionSummary{
			SessionID:  session.SessionID,
			Current:    session.SessionID == input.Principal.SessionID,
			IssuedAt:   session.IssuedAt.UTC(),
			LastSeenAt: session.LastSeenAt.UTC(),
			ExpiresAt:  session.ExpiresAt.UTC(),
		})
	}
	userID, tenantID := current.UserID, current.TenantID
	if err := s.recordAudit(ctx, AuthAuditRecord{UserID: &userID, TenantID: &tenantID, Realm: string(RealmAdmin), Subject: input.Principal.Subject, Operation: AuthOperationSessionList, Outcome: AuthOutcomeAllowed, RequestID: input.RequestID, TraceID: input.TraceID}); err != nil {
		return SessionListResponse{}, unavailable("record session list audit", err)
	}
	return response, nil
}

// RevokeSession revokes one other or current active admin session only when it
// belongs to the verified principal in the same tenant. The repository writes
// the allowed revocation audit in the same transaction as the state change.
func (s *Service) RevokeSession(ctx context.Context, input SessionRevokeInput) (SessionRevokeResponse, error) {
	if s == nil || !validAdminSessionPrincipal(input.Principal) || !validSessionID(input.SessionID) {
		return SessionRevokeResponse{}, ErrInvalidSessionRequest
	}
	current, err := s.currentSession(ctx, input.Principal)
	if err != nil {
		return SessionRevokeResponse{}, err
	}
	userID, tenantID := current.UserID, current.TenantID
	changed, err := s.repository.RevokeOwnedSessionWithAudit(ctx, input.SessionID, userID, tenantID, string(RealmAdmin), s.now().UTC(), AuthAuditRecord{
		UserID:    &userID,
		TenantID:  &tenantID,
		Realm:     string(RealmAdmin),
		Subject:   input.Principal.Subject,
		Operation: AuthOperationSessionRevoke,
		Outcome:   AuthOutcomeAllowed,
		RequestID: input.RequestID,
		TraceID:   input.TraceID,
	})
	if err != nil {
		return SessionRevokeResponse{}, unavailable("revoke owned session", err)
	}
	return SessionRevokeResponse{Changed: changed}, nil
}

func (s *Service) currentSession(ctx context.Context, principal RequestPrincipal) (SessionRecord, error) {
	session, err := s.repository.FindActiveSession(ctx, principal.SessionID, principal.Subject, principal.TenantID, string(principal.Realm), s.now().UTC())
	if errors.Is(err, ErrIdentityNotFound) {
		return SessionRecord{}, ErrAuthenticationRejected
	}
	if err != nil {
		return SessionRecord{}, unavailable("resolve current session", err)
	}
	return session, nil
}

// ValidatePrincipal provides the stateful half of Access JWT authentication.
// A valid signature is insufficient if the persisted session, account, tenant,
// or tenant membership is no longer active.
func (s *Service) ValidatePrincipal(ctx context.Context, principal RequestPrincipal) error {
	if s == nil || !validRequestPrincipal(principal) {
		return ErrAuthenticationRejected
	}
	now := s.now().UTC()
	session, err := s.repository.FindActiveSession(ctx, principal.SessionID, principal.Subject, principal.TenantID, string(principal.Realm), now)
	if errors.Is(err, ErrIdentityNotFound) {
		if auditErr := s.recordAudit(ctx, AuthAuditRecord{TenantID: &principal.TenantID, Realm: string(principal.Realm), Subject: principal.Subject, Operation: AuthOperationAuthenticate, Outcome: AuthOutcomeDenied, ReasonCode: "session_rejected", RequestID: metadata.RequestIDFromContext(ctx)}); auditErr != nil {
			return unavailable("record authentication denial audit", auditErr)
		}
		return ErrAuthenticationRejected
	}
	if err != nil {
		return unavailable("validate session", err)
	}
	userID, tenantID := session.UserID, session.TenantID
	if err := s.recordAudit(ctx, AuthAuditRecord{UserID: &userID, TenantID: &tenantID, Realm: string(principal.Realm), Subject: principal.Subject, Operation: AuthOperationAuthenticate, Outcome: AuthOutcomeAllowed, RequestID: metadata.RequestIDFromContext(ctx)}); err != nil {
		return unavailable("record authentication audit", err)
	}
	return nil
}

func (s *Service) rejectedAudit(ctx context.Context, realm Realm, loginName, subject string, userID, tenantID *uint64, operation, reasonCode, requestID, traceID string) error {
	if err := s.recordAudit(ctx, AuthAuditRecord{UserID: userID, TenantID: tenantID, Realm: string(realm), LoginName: strings.TrimSpace(loginName), Subject: subject, Operation: operation, Outcome: AuthOutcomeDenied, ReasonCode: reasonCode, RequestID: requestID, TraceID: traceID}); err != nil {
		return unavailable("record authentication denial audit", err)
	}
	return ErrAuthenticationRejected
}

func (s *Service) recordAudit(ctx context.Context, record AuthAuditRecord) error {
	if strings.TrimSpace(record.RequestID) == "" {
		record.RequestID = metadata.RequestIDFromContext(ctx)
	}
	return s.repository.RecordAudit(ctx, record)
}

func (s *Service) issueAccessToken(subject string, tenantID uint64, realm Realm, sessionID string, now time.Time) (string, error) {
	if !validSubject(subject) || tenantID == 0 || !validRealm(realm) || !validSessionID(sessionID) {
		return "", errors.New("access token identity is invalid")
	}
	jti, err := s.randomHex(16)
	if err != nil {
		return "", err
	}
	return auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		Issuer:    s.issuer,
		Audience:  s.audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(s.accessTTL).Unix(),
		Extra: map[string]any{
			"tenant_id": strconv.FormatUint(tenantID, 10),
			"realm":     string(realm),
			"sid":       sessionID,
			"jti":       jti,
		},
	}, s.accessSecret)
}

func (s *Service) randomHex(size int) (string, error) {
	bytes, err := randomBytes(s.random, size)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (s *Service) randomOpaqueToken(size int) (string, error) {
	bytes, err := randomBytes(s.random, size)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func randomBytes(source io.Reader, size int) ([]byte, error) {
	if source == nil || size <= 0 {
		return nil, errors.New("secure random source and positive size are required")
	}
	bytes := make([]byte, size)
	if _, err := io.ReadFull(source, bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}

func hashRefreshToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func validLoginInput(input LoginInput) bool {
	return validRealm(input.Realm) &&
		validLoginName(input.LoginName) &&
		validTenantCode(input.TenantCode) &&
		validOpaqueToken(input.Password)
}

func validOpaqueToken(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= maxCredentialLength
}

func validLoginName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 255
}

func validTenantCode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validSubject(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 255
}

func validSessionID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func validRequestPrincipal(principal RequestPrincipal) bool {
	return validSubject(principal.Subject) && principal.TenantID > 0 && validRealm(principal.Realm) && validSessionID(principal.SessionID)
}

func validAdminSessionPrincipal(principal RequestPrincipal) bool {
	return validRequestPrincipal(principal) && principal.Realm == RealmAdmin
}

func unavailable(operation string, err error) error {
	if err == nil {
		return ErrAuthenticationUnavailable
	}
	return fmt.Errorf("%w: %s: %v", ErrAuthenticationUnavailable, operation, err)
}
