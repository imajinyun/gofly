package authn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
)

const authenticationServiceTestSecret = "gosky-authentication-service-secret-at-least-32-bytes"

func TestService_LoginRefreshLogoutAndSessionValidation(t *testing.T) {
	now := time.Date(2026, time.October, 5, 7, 0, 0, 0, time.UTC)
	passwordHash, err := hashPassword("correct horse battery staple", PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	repository := &authenticationServiceRepository{
		identity: LoginIdentity{
			UserID:       10086,
			TenantID:     1001,
			Subject:      "user:10086",
			PasswordHash: passwordHash,
			AccountType:  "both",
			MemberRealm:  "both",
		},
	}
	service, err := NewService(ServiceOptions{
		Repository:         repository,
		AccessSecret:       []byte(authenticationServiceTestSecret),
		Issuer:             "gosky",
		Audience:           "gosky-web",
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         24 * time.Hour,
		PasswordParameters: PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new authentication service: %v", err)
	}

	tokens, err := service.Login(t.Context(), LoginInput{
		Realm:      RealmAdmin,
		LoginName:  "operator",
		TenantCode: "acme",
		Password:   "correct horse battery staple",
		RequestID:  "login-request",
		TraceID:    "login-trace",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiresIn != int64((15*time.Minute).Seconds()) {
		t.Fatalf("login tokens = %#v, want non-empty access/refresh and 15-minute expiry", tokens)
	}
	claims, err := auth.VerifyJWT(tokens.AccessToken, []byte(authenticationServiceTestSecret), auth.JWTOptions{Issuer: "gosky", Audience: "gosky-web", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("verify login access token: %v", err)
	}
	if claims.Subject != "user:10086" || claims.Extra["tenant_id"] != "1001" || claims.Extra["realm"] != string(RealmAdmin) || claims.Extra["sid"] == "" || claims.Extra["jti"] == "" {
		t.Fatalf("login claims = %#v, want gosky identity/session claims", claims)
	}
	principal := RequestPrincipal{Subject: claims.Subject, TenantID: 1001, Realm: RealmAdmin, SessionID: claims.Extra["sid"].(string)}
	if err := service.ValidatePrincipal(t.Context(), principal); err != nil {
		t.Fatalf("validate active principal: %v", err)
	}

	refreshed, err := service.Refresh(t.Context(), RefreshInput{
		Realm:        RealmAdmin,
		RefreshToken: tokens.RefreshToken,
		RequestID:    "refresh-request",
		TraceID:      "refresh-trace",
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.AccessToken == tokens.AccessToken || refreshed.RefreshToken == tokens.RefreshToken {
		t.Fatalf("refresh must rotate both credentials: login=%#v refresh=%#v", tokens, refreshed)
	}
	if _, err := service.Refresh(t.Context(), RefreshInput{Realm: RealmAdmin, RefreshToken: tokens.RefreshToken}); !errors.Is(err, ErrAuthenticationRejected) {
		t.Fatalf("replayed refresh error = %v, want %v", err, ErrAuthenticationRejected)
	}

	if err := service.Logout(t.Context(), LogoutInput{Principal: principal, RequestID: "logout-request", TraceID: "logout-trace"}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if err := service.ValidatePrincipal(t.Context(), principal); !errors.Is(err, ErrAuthenticationRejected) {
		t.Fatalf("validate revoked principal error = %v, want %v", err, ErrAuthenticationRejected)
	}
	if got := repository.auditOperations(); got != "login:allowed,authenticate:allowed,refresh:allowed,refresh:denied,logout:allowed,authenticate:denied" {
		t.Fatalf("authentication audits = %q", got)
	}
}

func TestService_LoginRejectsInvalidPasswordWithoutCreatingSession(t *testing.T) {
	passwordHash, err := hashPassword("correct password", PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	repository := &authenticationServiceRepository{identity: LoginIdentity{UserID: 1, TenantID: 1001, Subject: "user:1", PasswordHash: passwordHash, AccountType: "admin", MemberRealm: "admin"}}
	service, err := NewService(ServiceOptions{Repository: repository, AccessSecret: []byte(authenticationServiceTestSecret), AccessTTL: time.Minute, RefreshTTL: time.Hour, PasswordParameters: PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}})
	if err != nil {
		t.Fatalf("new authentication service: %v", err)
	}
	if _, err := service.Login(t.Context(), LoginInput{Realm: RealmAdmin, LoginName: "operator", TenantCode: "acme", Password: "wrong password"}); !errors.Is(err, ErrAuthenticationRejected) {
		t.Fatalf("invalid password error = %v, want %v", err, ErrAuthenticationRejected)
	}
	if repository.session.SessionID != "" {
		t.Fatalf("invalid password created session %#v", repository.session)
	}
	if got := repository.auditOperations(); got != "login:denied" {
		t.Fatalf("authentication audits = %q, want login denial only", got)
	}
}

type authenticationServiceRepository struct {
	identity LoginIdentity
	session  SessionRecord
	subject  string
	audits   []AuthAuditRecord
}

func (r *authenticationServiceRepository) FindLoginIdentity(_ context.Context, _, _, _ string) (LoginIdentity, error) {
	if r.identity.Subject == "" {
		return LoginIdentity{}, ErrIdentityNotFound
	}
	return r.identity, nil
}

func (r *authenticationServiceRepository) FindUniqueLoginIdentity(_ context.Context, _, _ string) (LoginIdentity, error) {
	if r.identity.Subject == "" {
		return LoginIdentity{}, ErrIdentityNotFound
	}
	return r.identity, nil
}

func (r *authenticationServiceRepository) FindSmartWebIdentity(_ context.Context, sessionID, subject string, tenantID uint64, realm string, now time.Time) (LoginIdentity, error) {
	if _, err := r.FindActiveSession(context.Background(), sessionID, subject, tenantID, realm, now); err != nil {
		return LoginIdentity{}, err
	}
	return r.identity, nil
}

func (r *authenticationServiceRepository) CreateSession(_ context.Context, session SessionRecord) error {
	r.session = session
	r.subject = r.identity.Subject
	return nil
}

func (r *authenticationServiceRepository) FindActiveSession(_ context.Context, sessionID, subject string, tenantID uint64, realm string, now time.Time) (SessionRecord, error) {
	if r.session.SessionID != sessionID || r.subject != subject || r.session.TenantID != tenantID || r.session.Realm != realm || r.session.RevokedAt != nil || !r.session.ExpiresAt.After(now) {
		return SessionRecord{}, ErrIdentityNotFound
	}
	return r.session, nil
}

func (r *authenticationServiceRepository) FindRefreshIdentity(_ context.Context, refreshTokenHash string, now time.Time) (RefreshIdentity, error) {
	if r.session.RefreshTokenHash != refreshTokenHash || r.session.RevokedAt != nil || !r.session.ExpiresAt.After(now) {
		return RefreshIdentity{}, ErrIdentityNotFound
	}
	return RefreshIdentity{Session: r.session, Subject: r.subject}, nil
}

func (r *authenticationServiceRepository) RotateRefreshToken(_ context.Context, previousHash, nextHash string, now time.Time) (bool, error) {
	if r.session.RefreshTokenHash != previousHash || r.session.RevokedAt != nil || !r.session.ExpiresAt.After(now) {
		return false, nil
	}
	r.session.RefreshTokenHash = nextHash
	r.session.RefreshGeneration++
	r.session.LastSeenAt = now
	return true, nil
}

func (r *authenticationServiceRepository) RevokeSession(_ context.Context, sessionID string, now time.Time) error {
	if r.session.SessionID == sessionID && r.session.RevokedAt == nil {
		r.session.RevokedAt = &now
		r.session.LastSeenAt = now
	}
	return nil
}

func (r *authenticationServiceRepository) ListActiveSessions(_ context.Context, userID, tenantID uint64, realm string, now time.Time, limit uint) ([]SessionRecord, error) {
	if r.session.UserID != userID || r.session.TenantID != tenantID || r.session.Realm != realm || r.session.RevokedAt != nil || !r.session.ExpiresAt.After(now) || limit == 0 {
		return []SessionRecord{}, nil
	}
	return []SessionRecord{r.session}, nil
}

func (r *authenticationServiceRepository) RevokeOwnedSessionWithAudit(_ context.Context, sessionID string, userID, tenantID uint64, realm string, now time.Time, audit AuthAuditRecord) (bool, error) {
	if r.session.SessionID != sessionID || r.session.UserID != userID || r.session.TenantID != tenantID || r.session.Realm != realm || r.session.RevokedAt != nil || !r.session.ExpiresAt.After(now) {
		return false, nil
	}
	r.session.RevokedAt = &now
	r.session.LastSeenAt = now
	r.audits = append(r.audits, audit)
	return true, nil
}

func (r *authenticationServiceRepository) RecordAudit(_ context.Context, record AuthAuditRecord) error {
	r.audits = append(r.audits, record)
	return nil
}

func (r *authenticationServiceRepository) auditOperations() string {
	if len(r.audits) == 0 {
		return ""
	}
	out := r.audits[0].Operation + ":" + r.audits[0].Outcome
	for _, audit := range r.audits[1:] {
		out += "," + audit.Operation + ":" + audit.Outcome
	}
	return out
}

var _ IdentityRepository = (*authenticationServiceRepository)(nil)
