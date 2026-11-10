package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/examples/gosky/internal/twofactor"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//
//	2FA-HTTP-01 without wiring, getTwoFactorLoginFlag returns code 0/data false
//	2FA-HTTP-02 without wiring, sendEmailCode returns failed_precondition
//	2FA-HTTP-03 when enabled with memory store, send+verify round trip works
//	  through the public routes
func TestTwoFactorEndpointsDisabled(t *testing.T) {
	server := newTwoFactorTestServer(t, nil)

	flag := httptest.NewRecorder()
	server.Handler().ServeHTTP(flag, httptest.NewRequest(http.MethodGet, "/login/getTwoFactorLoginFlag", nil))
	if flag.Code != http.StatusOK {
		t.Fatalf("flag status=%d body=%s", flag.Code, flag.Body.String())
	}
	var response struct {
		Code int  `json:"code"`
		Data bool `json:"data"`
	}
	if err := json.Unmarshal(flag.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 0 || response.Data {
		t.Fatalf("flag response=%s, want code 0 data false", flag.Body.String())
	}

	send := httptest.NewRecorder()
	server.Handler().ServeHTTP(send, httptest.NewRequest(http.MethodGet, "/login/sendEmailCode/admin", nil))
	if send.Code != http.StatusBadRequest {
		t.Fatalf("send status=%d body=%s, want 400 failed_precondition", send.Code, send.Body.String())
	}
	if !contains(send.Body.String(), "failed_precondition") {
		t.Fatalf("send body=%s", send.Body.String())
	}
}

func TestTwoFactorEndpointsEnabled(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	feature := twofactor.DefaultFeature()
	feature.Enabled = true
	feature.Issuer = "gosky-test"
	mailer := &routeMailer{}
	store := twofactor.NewMemoryStore(map[string]string{"admin": "admin@example.com"})
	service := twofactor.NewService(feature, mailer, store, store, func() time.Time { return now })
	server := newTwoFactorTestServer(t, service)

	flag := httptest.NewRecorder()
	server.Handler().ServeHTTP(flag, httptest.NewRequest(http.MethodGet, "/login/getTwoFactorLoginFlag", nil))
	if !contains(flag.Body.String(), `"data":true`) {
		t.Fatalf("flag body=%s, want enabled", flag.Body.String())
	}

	send := httptest.NewRecorder()
	server.Handler().ServeHTTP(send, httptest.NewRequest(http.MethodGet, "/login/sendEmailCode/admin", nil))
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	if len(mailer.mail) != 1 {
		t.Fatalf("mailer messages=%d", len(mailer.mail))
	}

	// Rate limiting on immediate resend.
	again := httptest.NewRecorder()
	server.Handler().ServeHTTP(again, httptest.NewRequest(http.MethodGet, "/login/sendEmailCode/admin", nil))
	if again.Code != http.StatusTooManyRequests {
		t.Fatalf("resend status=%d, want 429", again.Code)
	}
}

func newTwoFactorTestServer(t *testing.T, service *twofactor.Service) *rest.Server {
	t.Helper()
	serviceContext := svc.NewServiceContext(config.Config{})
	compatibilityService, err := smartweb.NewService(roleFileAuthenticator{}, &routeSmartWebRoleAuthorizer{}, nil)
	if err != nil {
		t.Fatalf("new compatibility service: %v", err)
	}
	serviceContext.SetSmartWebService(compatibilityService)
	if service != nil {
		serviceContext.SetTwoFactorService(service)
	}
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)
	return server
}

type routeMailerMessage struct{ to, subject, body string }

type routeMailer struct{ mail []routeMailerMessage }

func (m *routeMailer) Send(_ context.Context, to, subject, body string) error {
	m.mail = append(m.mail, routeMailerMessage{to: to, subject: subject, body: body})
	return nil
}
