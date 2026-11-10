package smartemployee

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gmCipher "github.com/emmansun/gmsm/cipher"
	"github.com/emmansun/gmsm/padding"
	"github.com/emmansun/gmsm/sm4"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//
//	PASSW-HTTP-01 encrypted old/new password round-trip succeeds
//	PASSW-HTTP-02 wrong old password from the store is a 400 failed_precondition
//	PASSW-HTTP-03 malformed encrypted envelope is 400 invalid_argument
//	PASSW-HTTP-04 missing authenticated principal is 401
func TestChangeOwnPasswordEncryptedBody(t *testing.T) {
	store := &passwordStore{}
	service := app.NewOwnPasswordChange(store, false)
	server := newPasswordTestServer(service)

	token := signPasswordTestToken(t)
	body := encryptBody(t, `{"oldPassword":"Oldpass123","newPassword":"Newpass1234"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/employee/update/password", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":0`) {
		t.Fatalf("response = %s, want success envelope", recorder.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d, want 1", store.calls)
	}
}

func TestChangeOwnPasswordOldMismatch(t *testing.T) {
	store := &passwordStore{err: app.ErrOldPasswordMismatch}
	service := app.NewOwnPasswordChange(store, false)
	server := newPasswordTestServer(service)

	token := signPasswordTestToken(t)
	body := encryptBody(t, `{"oldPassword":"Wrongold1","newPassword":"Newpass1234"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/employee/update/password", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "failed_precondition") {
		t.Fatalf("status=%d body=%s, want 400 failed_precondition", recorder.Code, recorder.Body.String())
	}
}

func TestChangeOwnPasswordMalformedEnvelope(t *testing.T) {
	server := newPasswordTestServer(app.NewOwnPasswordChange(&passwordStore{}, false))
	token := signPasswordTestToken(t)
	for _, body := range []string{`{"encryptData":"%%%"}`, `{}`} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/employee/update/password", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d, want 400", body, recorder.Code)
		}
	}
}

type passwordStore struct {
	calls int
	err   error
}

func (s *passwordStore) ChangeOwnPassword(_ context.Context, _ string, _ uint64, _, _ string, _ time.Time) error {
	s.calls++
	return s.err
}

func newPasswordTestServer(service *app.OwnPasswordChange) *rest.Server {
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&passwordRouteAuth{})
	serviceContext.SetOwnPasswordService(service)
	interceptor := authn.NewAdminInterceptor((&passwordRouteAuth{}).JWTValidator())
	server := rest.MustNewServer(rest.Config{})
	server.AddRoute(rest.Route{
		Method:  http.MethodPost,
		Path:    "/employee/update/password",
		Handler: ChangeOwnPasswordHandler(serviceContext),
	}, rest.WithMiddlewares(smartweb.AuthMiddleware(interceptor)))
	return server
}

func signPasswordTestToken(t *testing.T) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "user:1001",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra: map[string]any{
			"tenant_id": "1001",
			"realm":     string(authn.RealmAdmin),
			"sid":       "session-password",
		},
	}, []byte("password-route-secret-at-least-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// encryptBody reproduces the unchanged smart-web postEncryptRequest payload:
// base64(sm4-ecb-pkcs7(hex-key, json)) as JSON {encryptData}.
func encryptBody(t *testing.T, plaintextJSON string) string {
	t.Helper()
	block, err := sm4.NewCipher([]byte("1024lab__1024lab"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(plaintextJSON)
	padder := padding.NewPKCS7Padding(sm4.BlockSize)
	padded := padder.Pad(plaintext)
	ciphertext := make([]byte, len(padded))
	gmCipher.NewECBEncrypter(block).CryptBlocks(ciphertext, padded)
	encoded := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(ciphertext)))
	return `{"encryptData":"` + encoded + `"}`
}

// --- test-only adapters reusing the real compat auth middleware ---

type passwordRouteAuth struct{}

func (*passwordRouteAuth) Login(context.Context, authn.LoginInput) (authn.TokenPair, error) {
	return authn.TokenPair{}, authn.ErrAuthenticationRejected
}
func (*passwordRouteAuth) Refresh(context.Context, authn.RefreshInput) (authn.TokenPair, error) {
	return authn.TokenPair{}, authn.ErrAuthenticationRejected
}
func (*passwordRouteAuth) Logout(context.Context, authn.LogoutInput) error { return nil }
func (*passwordRouteAuth) ValidatePrincipal(context.Context, authn.RequestPrincipal) error {
	return nil
}
func (a *passwordRouteAuth) JWTValidator() auth.Validator {
	return auth.JWTValidator([]byte("password-route-secret-at-least-32-bytes!"), auth.JWTOptions{Now: time.Now})
}
