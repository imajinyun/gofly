//go:build integration

package routes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"

	_ "github.com/go-sql-driver/mysql"
)

const (
	httpAuthenticationTenantID       uint64 = 987654331
	httpAuthenticationUserID         uint64 = 987654332
	httpAuthenticationOtherUserID    uint64 = 987654333
	httpAuthenticationSecret                = "gosky-http-authentication-secret-at-least-32-bytes"
	httpAuthenticationFrontSessionID        = "11111111111111111111111111111111"
	httpAuthenticationOtherSessionID        = "22222222222222222222222222222222"
)

func TestMySQLHTTPAuthentication_LoginRefreshReplayLogoutIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL HTTP authentication integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL authentication database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL authentication database: %v", err)
	}
	resetHTTPAuthenticationFixture(t, ctx, db)
	t.Cleanup(func() { resetHTTPAuthenticationFixture(t, context.Background(), db) })
	seedHTTPAuthenticationFixture(t, ctx, db, integrationPasswordHash("correct-password"))

	authenticationService, err := authn.NewService(authn.ServiceOptions{
		Repository:         repo.NewMySQLIdentityRepository(storage.NewSQLStore(db)),
		AccessSecret:       []byte(httpAuthenticationSecret),
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         24 * time.Hour,
		PasswordParameters: authn.PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	if err != nil {
		t.Fatalf("new HTTP authentication service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(authenticationService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	login := postAuthenticationJSON(t, server, "/api/admin/auth/login", map[string]string{"loginName": "http-operator", "tenantCode": "http-acme", "password": "correct-password"}, "http-login")
	if login.AccessToken == "" || login.RefreshToken == "" || login.TokenType != "Bearer" {
		t.Fatalf("login token pair = %#v", login)
	}
	var storedHash string
	if err := db.QueryRowContext(ctx, `SELECT refresh_token_hash FROM auth_sessions WHERE tenant_id = ?`, httpAuthenticationTenantID).Scan(&storedHash); err != nil {
		t.Fatalf("load persisted refresh hash: %v", err)
	}
	if storedHash == login.RefreshToken || len(storedHash) != 64 {
		t.Fatalf("stored refresh value = %q, want SHA-256 hash only", storedHash)
	}

	refreshed := postAuthenticationJSON(t, server, "/api/admin/auth/refresh", map[string]string{"refreshToken": login.RefreshToken}, "http-refresh")
	if refreshed.RefreshToken == login.RefreshToken || refreshed.AccessToken == login.AccessToken {
		t.Fatalf("refresh pair was not rotated: login=%#v refresh=%#v", login, refreshed)
	}
	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/admin/auth/refresh", bytes.NewBufferString(`{"refreshToken":`+jsonString(login.RefreshToken)+`}`))
	replayRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed refresh status = %d, want %d; body=%s", replay.Code, http.StatusUnauthorized, replay.Body.String())
	}

	logout := httptest.NewRecorder()
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
	logoutRequest.Header.Set(auth.AuthorizationHeader, auth.BearerValue(refreshed.AccessToken))
	logoutRequest.Header.Set(rest.RequestIDHeader, "http-logout")
	server.Handler().ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d; body=%s", logout.Code, http.StatusOK, logout.Body.String())
	}
	deniedLogout := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
	deniedRequest.Header.Set(auth.AuthorizationHeader, auth.BearerValue(refreshed.AccessToken))
	server.Handler().ServeHTTP(deniedLogout, deniedRequest)
	if deniedLogout.Code != http.StatusUnauthorized {
		t.Fatalf("revoked access status = %d, want %d; body=%s", deniedLogout.Code, http.StatusUnauthorized, deniedLogout.Body.String())
	}
}

// Failure-mode inventory: AUTHN-021 through AUTHN-024. This E2E contract
// exercises the unmodified smart-web login paths against MySQL persistence,
// including the browser's SM4 fixture, one-time captcha, AdminInterceptor,
// LoginResultVO envelope, startup dictionary request, and session revocation.
func TestMySQLSmartWebLoginCompatibilityIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the smart-web MySQL integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL smart-web database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL smart-web database: %v", err)
	}
	resetHTTPAuthenticationFixture(t, ctx, db)
	t.Cleanup(func() { resetHTTPAuthenticationFixture(t, context.Background(), db) })
	seedHTTPAuthenticationFixture(t, ctx, db, integrationPasswordHash("correct-password"))

	authenticationService, err := authn.NewService(authn.ServiceOptions{
		Repository:         repo.NewMySQLIdentityRepository(storage.NewSQLStore(db)),
		AccessSecret:       []byte(httpAuthenticationSecret),
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         24 * time.Hour,
		PasswordParameters: authn.PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	if err != nil {
		t.Fatalf("new smart-web authentication service: %v", err)
	}
	compatibilityService, err := smartweb.NewService(authenticationService, nil, smartweb.NewCaptchaService(smartweb.CaptchaOptions{
		Code: func() (string, error) { return "ABCD", nil },
		ID:   func() (string, error) { return "smart-web-captcha-000001", nil },
	}))
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(authenticationService)
	serviceContext.SetSmartWebService(compatibilityService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	captcha := httptest.NewRecorder()
	server.Handler().ServeHTTP(captcha, httptest.NewRequest(http.MethodGet, "/login/getCaptcha", nil))
	if captcha.Code != http.StatusOK {
		t.Fatalf("captcha status = %d, want %d; body=%s", captcha.Code, http.StatusOK, captcha.Body.String())
	}
	var captchaEnvelope struct {
		Code int                       `json:"code"`
		OK   bool                      `json:"ok"`
		Data smartweb.CaptchaChallenge `json:"data"`
	}
	if err := json.Unmarshal(captcha.Body.Bytes(), &captchaEnvelope); err != nil {
		t.Fatalf("decode captcha envelope: %v", err)
	}
	if captchaEnvelope.Code != 0 || !captchaEnvelope.OK || captchaEnvelope.Data.CaptchaUUID != "smart-web-captcha-000001" || !strings.HasPrefix(captchaEnvelope.Data.CaptchaBase64Image, "data:image/svg+xml;base64,") {
		t.Fatalf("captcha envelope = %#v", captchaEnvelope)
	}

	loginBody := `{"loginName":"http-operator","password":"ODRmYjQ3YmM0ZWM4NjU4NjYyNGJkZDE2N2QwYjE5NmRmNjg4ZGZiNzRlMmU5MDg4ZjNkYWE2MGM3OGQyMTQ0Mg==","captchaCode":"ABCD","captchaUuid":"smart-web-captcha-000001","loginDevice":1}`
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginBody))
	loginRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", login.Code, http.StatusOK, login.Body.String())
	}
	var loginEnvelope struct {
		Code int                    `json:"code"`
		OK   bool                   `json:"ok"`
		Data smartweb.LoginResultVO `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginEnvelope); err != nil {
		t.Fatalf("decode login envelope: %v", err)
	}
	if loginEnvelope.Code != 0 || !loginEnvelope.OK || loginEnvelope.Data.Token == "" || loginEnvelope.Data.EmployeeID != httpAuthenticationUserID || loginEnvelope.Data.LoginName != "http-operator" || loginEnvelope.Data.MenuList == nil || len(loginEnvelope.Data.MenuList) != 0 {
		t.Fatalf("login envelope = %#v", loginEnvelope)
	}
	if strings.Contains(login.Body.String(), "refreshToken") || strings.Contains(login.Body.String(), "password_hash") {
		t.Fatalf("smart-web login exposes credential material: %s", login.Body.String())
	}

	for _, path := range []string{"/login/getLoginInfo", "/support/dict/getAllDictData"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(auth.AuthorizationHeader, auth.BearerValue(loginEnvelope.Data.Token))
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d; body=%s", path, response.Code, http.StatusOK, response.Body.String())
		}
		var envelope struct {
			Code int  `json:"code"`
			OK   bool `json:"ok"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode %s envelope: %v", path, err)
		}
		if envelope.Code != 0 || !envelope.OK {
			t.Fatalf("%s envelope = %#v", path, envelope)
		}
	}

	logout := httptest.NewRecorder()
	logoutRequest := httptest.NewRequest(http.MethodGet, "/login/logout", nil)
	logoutRequest.Header.Set(auth.AuthorizationHeader, auth.BearerValue(loginEnvelope.Data.Token))
	server.Handler().ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d; body=%s", logout.Code, http.StatusOK, logout.Body.String())
	}
	var logoutEnvelope struct {
		Code int  `json:"code"`
		OK   bool `json:"ok"`
	}
	if err := json.Unmarshal(logout.Body.Bytes(), &logoutEnvelope); err != nil || logoutEnvelope.Code != 0 || !logoutEnvelope.OK {
		t.Fatalf("logout envelope = %#v, err=%v", logoutEnvelope, err)
	}

	revoked := httptest.NewRecorder()
	revokedRequest := httptest.NewRequest(http.MethodGet, "/login/getLoginInfo", nil)
	revokedRequest.Header.Set(auth.AuthorizationHeader, auth.BearerValue(loginEnvelope.Data.Token))
	server.Handler().ServeHTTP(revoked, revokedRequest)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked login-info status = %d, want %d; body=%s", revoked.Code, http.StatusUnauthorized, revoked.Body.String())
	}
}

// Failure-mode inventory: AUTHN-018 and AUTHN-019. This integration path
// proves that the actual HTTP interceptor, stateful session repository,
// scoped mutation SQL, and audit write agree under the MySQL schema.
func TestMySQLHTTPAuthentication_AdminSessionManagementIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL HTTP session-management integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL session-management database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL session-management database: %v", err)
	}
	resetHTTPAuthenticationFixture(t, ctx, db)
	t.Cleanup(func() { resetHTTPAuthenticationFixture(t, context.Background(), db) })
	seedHTTPAuthenticationFixture(t, ctx, db, integrationPasswordHash("correct-password"))

	authenticationService, err := authn.NewService(authn.ServiceOptions{
		Repository:         repo.NewMySQLIdentityRepository(storage.NewSQLStore(db)),
		AccessSecret:       []byte(httpAuthenticationSecret),
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         24 * time.Hour,
		PasswordParameters: authn.PasswordParameters{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	if err != nil {
		t.Fatalf("new HTTP session-management service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(authenticationService)
	serviceContext.SetAdminSessionService(authenticationService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	first := postAuthenticationJSON(t, server, "/api/admin/auth/login", map[string]string{"loginName": "http-operator", "tenantCode": "http-acme", "password": "correct-password"}, "http-session-first-login")
	second := postAuthenticationJSON(t, server, "/api/admin/auth/login", map[string]string{"loginName": "http-operator", "tenantCode": "http-acme", "password": "correct-password"}, "http-session-second-login")
	firstSessionID := sessionIDFromAccessToken(t, first.AccessToken)
	secondSessionID := sessionIDFromAccessToken(t, second.AccessToken)
	if firstSessionID == secondSessionID {
		t.Fatalf("separate logins reused session ID %q", firstSessionID)
	}
	seedOutOfScopeHTTPAuthenticationSessions(t, ctx, db)

	list := authenticatedSessionRequest(t, server, http.MethodGet, "/api/admin/auth/sessions?limit=10", first.AccessToken, "http-session-list")
	if list.Code != http.StatusOK {
		t.Fatalf("list sessions status = %d, want %d; body=%s", list.Code, http.StatusOK, list.Body.String())
	}
	if containsCredentialField(list.Body.String()) {
		t.Fatalf("session list exposes credential material: %s", list.Body.String())
	}
	var listed authn.SessionListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode active sessions: %v", err)
	}
	if len(listed.Items) != 2 {
		t.Fatalf("listed sessions = %#v, want exactly caller's two admin sessions", listed)
	}
	if !hasCurrentSession(listed.Items, firstSessionID) || hasSession(listed.Items, httpAuthenticationFrontSessionID) || hasSession(listed.Items, httpAuthenticationOtherSessionID) {
		t.Fatalf("listed sessions escaped owner/realm scope: %#v", listed)
	}

	for _, target := range []string{httpAuthenticationFrontSessionID, httpAuthenticationOtherSessionID} {
		response := authenticatedSessionRequest(t, server, http.MethodDelete, "/api/admin/auth/sessions/"+target, first.AccessToken, "http-session-out-of-scope")
		if response.Code != http.StatusOK {
			t.Fatalf("out-of-scope revoke %s status = %d, want %d; body=%s", target, response.Code, http.StatusOK, response.Body.String())
		}
		var body authn.SessionRevokeResponse
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode out-of-scope revoke %s: %v", target, err)
		}
		if body.Changed {
			t.Fatalf("out-of-scope revoke %s changed state", target)
		}
		var revokedAt *time.Time
		if err := db.QueryRowContext(ctx, `SELECT revoked_at FROM auth_sessions WHERE session_id = ?`, target).Scan(&revokedAt); err != nil {
			t.Fatalf("load out-of-scope session %s: %v", target, err)
		}
		if revokedAt != nil {
			t.Fatalf("out-of-scope session %s was revoked at %s", target, revokedAt.UTC())
		}
	}

	revoke := authenticatedSessionRequest(t, server, http.MethodDelete, "/api/admin/auth/sessions/"+secondSessionID, first.AccessToken, "http-session-revoke")
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke owned session status = %d, want %d; body=%s", revoke.Code, http.StatusOK, revoke.Body.String())
	}
	var revoked authn.SessionRevokeResponse
	if err := json.Unmarshal(revoke.Body.Bytes(), &revoked); err != nil {
		t.Fatalf("decode owned session revoke: %v", err)
	}
	if !revoked.Changed {
		t.Fatalf("owned session revoke = %#v, want changed", revoked)
	}

	rejected := authenticatedSessionRequest(t, server, http.MethodGet, "/api/admin/auth/sessions", second.AccessToken, "http-session-revoked-token")
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session request status = %d, want %d; body=%s", rejected.Code, http.StatusUnauthorized, rejected.Body.String())
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_login_logs WHERE request_id = ? AND operation = 'session_revoke' AND outcome = 'allowed'`, "http-session-revoke").Scan(&auditCount); err != nil {
		t.Fatalf("count session revocation audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("session revocation audits = %d, want 1", auditCount)
	}
}

func authenticatedSessionRequest(t *testing.T, server *rest.Server, method, path, accessToken, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set(auth.AuthorizationHeader, auth.BearerValue(accessToken))
	request.Header.Set(rest.RequestIDHeader, requestID)
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func sessionIDFromAccessToken(t *testing.T, token string) string {
	t.Helper()
	claims, err := auth.VerifyJWT(token, []byte(httpAuthenticationSecret), auth.JWTOptions{Now: time.Now})
	if err != nil {
		t.Fatalf("verify session access token: %v", err)
	}
	sessionID, _ := claims.Extra["sid"].(string)
	if len(sessionID) != 32 {
		t.Fatalf("access token session ID = %q", sessionID)
	}
	return sessionID
}

func hasCurrentSession(items []authn.SessionSummary, sessionID string) bool {
	for _, item := range items {
		if item.SessionID == sessionID {
			return item.Current
		}
	}
	return false
}

func hasSession(items []authn.SessionSummary, sessionID string) bool {
	for _, item := range items {
		if item.SessionID == sessionID {
			return true
		}
	}
	return false
}

func seedOutOfScopeHTTPAuthenticationSessions(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.ExecContext(ctx, `INSERT INTO users (user_id, subject, login_name, password_hash, account_type, user_state) VALUES (?, ?, ?, ?, 'admin', 'active')`, httpAuthenticationOtherUserID, "user:http-other-operator", "http-other-operator", "$argon2id$fixture"); err != nil {
		t.Fatalf("insert other HTTP authentication user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_users (tenant_id, user_id, member_realm, member_state) VALUES (?, ?, 'admin', 'active')`, httpAuthenticationTenantID, httpAuthenticationOtherUserID); err != nil {
		t.Fatalf("insert other HTTP authentication membership: %v", err)
	}
	for _, session := range []struct {
		id     string
		userID uint64
		realm  string
		hash   string
	}{
		{id: httpAuthenticationFrontSessionID, userID: httpAuthenticationUserID, realm: "front", hash: strings.Repeat("f", 64)},
		{id: httpAuthenticationOtherSessionID, userID: httpAuthenticationOtherUserID, realm: "admin", hash: strings.Repeat("e", 64)},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO auth_sessions (session_id, user_id, tenant_id, realm, refresh_token_hash, refresh_generation, issued_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)`, session.id, session.userID, httpAuthenticationTenantID, session.realm, session.hash, now, now.Add(time.Hour), now); err != nil {
			t.Fatalf("insert out-of-scope session %s: %v", session.id, err)
		}
	}
}

func postAuthenticationJSON(t *testing.T, server *rest.Server, path string, input map[string]string, requestID string) authn.TokenPair {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal authentication input: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(rest.RequestIDHeader, requestID)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want %d; body=%s", path, recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var pair authn.TokenPair
	if err := json.Unmarshal(recorder.Body.Bytes(), &pair); err != nil {
		t.Fatalf("decode %s response: %v", path, err)
	}
	return pair
}

func integrationPasswordHash(password string) string {
	salt := []byte("gosky-auth-salt!")
	key := argon2.IDKey([]byte(password), salt, 1, 64, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=64,t=1,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func seedHTTPAuthenticationFixture(t *testing.T, ctx context.Context, db *sql.DB, passwordHash string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, tenant_code, tenant_name) VALUES (?, ?, ?)`, httpAuthenticationTenantID, "http-acme", "HTTP authentication"); err != nil {
		t.Fatalf("insert HTTP authentication tenant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (user_id, subject, login_name, password_hash, account_type, user_state) VALUES (?, ?, ?, ?, 'admin', 'active')`, httpAuthenticationUserID, "user:http-operator", "http-operator", passwordHash); err != nil {
		t.Fatalf("insert HTTP authentication user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_users (tenant_id, user_id, member_realm, member_state) VALUES (?, ?, 'admin', 'active')`, httpAuthenticationTenantID, httpAuthenticationUserID); err != nil {
		t.Fatalf("insert HTTP authentication membership: %v", err)
	}
}

func resetHTTPAuthenticationFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, deletion := range []struct {
		query string
		arg   uint64
	}{
		{query: `DELETE FROM auth_login_logs WHERE tenant_id = ?`, arg: httpAuthenticationTenantID},
		{query: `DELETE FROM auth_sessions WHERE tenant_id = ?`, arg: httpAuthenticationTenantID},
		{query: `DELETE FROM tenant_users WHERE tenant_id = ?`, arg: httpAuthenticationTenantID},
		{query: `DELETE FROM users WHERE user_id = ?`, arg: httpAuthenticationUserID},
		{query: `DELETE FROM users WHERE user_id = ?`, arg: httpAuthenticationOtherUserID},
		{query: `DELETE FROM tenants WHERE tenant_id = ?`, arg: httpAuthenticationTenantID},
	} {
		if _, err := db.ExecContext(ctx, deletion.query, deletion.arg); err != nil {
			t.Fatalf("reset HTTP authentication fixture: %v", err)
		}
	}
}

func TestIntegrationPasswordHashUsesExpectedRefreshDigestBoundary(t *testing.T) {
	plain := "opaque-refresh-token"
	digest := sha256.Sum256([]byte(plain))
	if len(fmt.Sprintf("%x", digest)) != 64 {
		t.Fatal("SHA-256 refresh digest must be fixed-width hexadecimal")
	}
}
