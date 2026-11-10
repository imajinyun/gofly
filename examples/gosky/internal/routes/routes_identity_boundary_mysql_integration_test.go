//go:build integration

package routes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
)

// AUTHN-045: storage content is not a reason to accept malformed hash metadata.
func TestMySQLIdentityRejectsInvalidStoredHashIntegration(t *testing.T) {
	f := newRoleMenuFixture(t)
	makeHash := func(salt []byte, keyLength uint32) string {
		key := argon2.IDKey([]byte("correct-password"), salt, 1, 64, 1, keyLength)
		return "$argon2id$v=19$m=64,t=1,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
	}
	normal := makeHash([]byte("0123456789abcdef"), 32)
	for _, tc := range []struct{ name, hash string }{
		{"empty salt", makeHash(nil, 32)},
		{"short salt", makeHash([]byte("short"), 32)},
		{"oversized salt", makeHash([]byte(strings.Repeat("s", 65)), 32)},
		{"short key", makeHash([]byte("0123456789abcdef"), 8)},
		{"oversized key", makeHash([]byte("0123456789abcdef"), 65)},
		{"memory overflow", strings.Replace(normal, "m=64", "m=4294967296", 1)},
		{"iteration overflow", strings.Replace(normal, "t=1", "t=4294967296", 1)},
		{"parallelism overflow", strings.Replace(normal, "p=1", "p=256", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.db.ExecContext(f.ctx, "UPDATE users SET password_hash=? WHERE subject=?", tc.hash, f.subject("manager")); err != nil {
				t.Fatal(err)
			}
			f.request(t, http.MethodPost, "/api/admin/auth/login", "", map[string]string{"loginName": f.subject("manager"), "tenantCode": f.tenantCode(0), "password": "correct-password"}, http.StatusUnauthorized, nil)
		})
	}
	var sessions int
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM auth_sessions WHERE tenant_id=?", f.domains[0]).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("malformed hashes created %d sessions, err=%v", sessions, err)
	}
	t.Log("invalidStoredHashes=8 rejected=8 sessionsCreated=0")
}

// AUTHN-044: preserve authentication state, failure semantics and audit across
// the contract move. The shared fixture creates and removes only fresh rows.
func TestMySQLIdentityBoundaryLifecycleIntegration(t *testing.T) {
	f := newRoleMenuFixture(t)
	credentials := func(user string, tenant int) map[string]string {
		return map[string]string{"loginName": f.subject(user), "tenantCode": f.tenantCode(tenant), "password": "correct-password"}
	}
	bad := credentials("manager", 0)
	bad["password"] = "incorrect-password"
	f.request(t, http.MethodPost, "/api/admin/auth/login", "", bad, http.StatusUnauthorized, nil)
	var count int
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM auth_sessions WHERE tenant_id = ?", f.domains[0]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed login created %d sessions, err=%v", count, err)
	}
	login := func(user string, tenant int, realm string) authn.TokenPair {
		var pair authn.TokenPair
		f.request(t, http.MethodPost, "/api/"+realm+"/auth/login", "", credentials(user, tenant), http.StatusOK, &pair)
		if pair.AccessToken == "" || pair.RefreshToken == "" || pair.TokenType != "Bearer" {
			t.Fatal("incomplete token response")
		}
		return pair
	}
	first, second := login("manager", 0, "admin"), login("manager", 0, "admin")
	foreign, front, otherUser := login("manager", 1, "admin"), login("manager", 0, "front"), login("reader", 0, "admin")
	firstID := sessionIDFromAccessToken(t, first.AccessToken)
	secondID := sessionIDFromAccessToken(t, second.AccessToken)
	var storedHash string
	if err := f.db.QueryRowContext(f.ctx, "SELECT refresh_token_hash FROM auth_sessions WHERE session_id = ?", firstID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(first.RefreshToken))
	if storedHash != hex.EncodeToString(digest[:]) {
		t.Fatal("refresh credential was not stored as its digest")
	}
	f.request(t, http.MethodPost, "/api/front/auth/refresh", "", map[string]string{"refreshToken": first.RefreshToken}, http.StatusUnauthorized, nil)
	var refreshed authn.TokenPair
	f.request(t, http.MethodPost, "/api/admin/auth/refresh", "", map[string]string{"refreshToken": first.RefreshToken}, http.StatusOK, &refreshed)
	if refreshed.AccessToken == first.AccessToken || refreshed.RefreshToken == first.RefreshToken {
		t.Fatal("refresh did not rotate credentials")
	}
	f.request(t, http.MethodPost, "/api/admin/auth/refresh", "", map[string]string{"refreshToken": first.RefreshToken}, http.StatusUnauthorized, nil)
	var generation int
	if err := f.db.QueryRowContext(f.ctx, "SELECT refresh_generation FROM auth_sessions WHERE session_id = ?", firstID).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("refresh generation=%d err=%v", generation, err)
	}

	var body json.RawMessage
	f.request(t, http.MethodGet, "/api/admin/auth/sessions", refreshed.AccessToken, nil, http.StatusOK, &body)
	if containsCredentialField(string(body)) {
		t.Fatal("session list exposes credential fields")
	}
	var sessions authn.SessionListResponse
	if err := json.Unmarshal(body, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions.Items) != 2 || !hasCurrentSession(sessions.Items, firstID) || !hasSession(sessions.Items, secondID) {
		t.Fatalf("session list leaked or omitted scoped sessions: count=%d", len(sessions.Items))
	}
	for _, token := range []string{foreign.AccessToken, front.AccessToken, otherUser.AccessToken} {
		target := sessionIDFromAccessToken(t, token)
		var result authn.SessionRevokeResponse
		f.request(t, http.MethodDelete, "/api/admin/auth/sessions/"+target, refreshed.AccessToken, nil, http.StatusOK, &result)
		if result.Changed {
			t.Fatal("out-of-scope session was revoked")
		}
		if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM auth_sessions WHERE session_id = ? AND revoked_at IS NULL", target).Scan(&count); err != nil || count != 1 {
			t.Fatalf("out-of-scope session state changed, count=%d err=%v", count, err)
		}
	}
	var revoked authn.SessionRevokeResponse
	f.request(t, http.MethodDelete, "/api/admin/auth/sessions/"+secondID, refreshed.AccessToken, nil, http.StatusOK, &revoked)
	if !revoked.Changed {
		t.Fatal("owned session was not revoked")
	}
	f.request(t, http.MethodGet, "/login/getLoginInfo", second.AccessToken, nil, http.StatusUnauthorized, nil)
	f.request(t, http.MethodGet, "/login/getLoginInfo", refreshed.AccessToken, nil, http.StatusOK, nil)

	if _, err := f.db.ExecContext(f.ctx, "UPDATE tenant_users SET member_state='disabled',left_at=NOW(6) WHERE tenant_id=? AND user_id=(SELECT user_id FROM users WHERE subject=?)", f.domains[0], f.subject("manager")); err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodGet, "/login/getLoginInfo", refreshed.AccessToken, nil, http.StatusUnauthorized, nil)
	f.request(t, http.MethodPost, "/api/admin/auth/refresh", "", map[string]string{"refreshToken": refreshed.RefreshToken}, http.StatusUnauthorized, nil)
	if _, err := f.db.ExecContext(f.ctx, "UPDATE tenant_users SET member_state='active',left_at=NULL WHERE tenant_id=? AND user_id=(SELECT user_id FROM users WHERE subject=?)", f.domains[0], f.subject("manager")); err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodPost, "/api/admin/auth/logout", refreshed.AccessToken, nil, http.StatusOK, nil)
	f.request(t, http.MethodGet, "/login/getLoginInfo", refreshed.AccessToken, nil, http.StatusUnauthorized, nil)
	f.request(t, http.MethodPost, "/api/admin/auth/refresh", "", map[string]string{"refreshToken": refreshed.RefreshToken}, http.StatusUnauthorized, nil)

	rows, err := f.db.QueryContext(f.ctx, "SELECT operation,outcome,COUNT(*) FROM auth_login_logs WHERE tenant_id=? GROUP BY operation,outcome", f.domains[0])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	audits := make(map[string]int)
	for rows.Next() {
		var operation, outcome string
		var n int
		if err := rows.Scan(&operation, &outcome, &n); err != nil {
			t.Fatal(err)
		}
		audits[operation+":"+outcome] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"login:allowed", "login:denied", "refresh:allowed", "session_list:allowed", "session_revoke:allowed", "logout:allowed", "authenticate:denied"} {
		if audits[key] == 0 {
			t.Errorf("missing audit outcome %s", key)
		}
	}
	t.Logf("login=verified refresh=rotated/replay-denied sessionScope=verified revoke=verified disabledMembership=denied logout=verified audits=%v", audits)
}
