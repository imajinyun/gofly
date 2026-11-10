package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory: AUTHN-021 and AUTHN-022. The existing smart-web
// login page must use its original paths and ResponseDTO envelope; it does not
// know about the Gosky-native /api/admin/auth/* surface.
func TestRegisterRoutesSmartWebLoginCompatibilityContract(t *testing.T) {
	compatibilityService, err := smartweb.NewService(&routeSmartWebAuthenticator{}, nil, smartweb.NewCaptchaService(smartweb.CaptchaOptions{
		TTL:  time.Minute,
		Now:  time.Now,
		Code: func() (string, error) { return "ABCD", nil },
		ID:   func() (string, error) { return "captcha-id-000001", nil },
	}))
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetSmartWebService(compatibilityService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "captcha endpoint", method: http.MethodGet, path: "/login/getCaptcha"},
		{name: "login endpoint", method: http.MethodPost, path: "/login", body: `{"loginName":"admin","password":"ODRmYjQ3YmM0ZWM4NjU4NjYyNGJkZDE2N2QwYjE5NmRmNjg4ZGZiNzRlMmU5MDg4ZjNkYWE2MGM3OGQyMTQ0Mg==","captchaCode":"ABCD","captchaUuid":"captcha-id-000001","loginDevice":1}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			var envelope struct {
				Code     int             `json:"code"`
				OK       bool            `json:"ok"`
				Data     json.RawMessage `json:"data"`
				DataType int             `json:"dataType"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode ResponseDTO: %v", err)
			}
			if envelope.Code != 0 || !envelope.OK || envelope.DataType != 1 || len(envelope.Data) == 0 {
				t.Fatalf("ResponseDTO = %+v, want code=0 ok=true dataType=1 with data", envelope)
			}
		})
	}
}

type routeSmartWebAuthenticator struct{}

func (*routeSmartWebAuthenticator) LoginSmartWeb(_ context.Context, input authn.SmartWebLoginInput) (authn.SmartWebLoginResult, error) {
	if input.LoginName != "admin" || input.Password != "correct-password" {
		return authn.SmartWebLoginResult{}, authn.ErrAuthenticationRejected
	}
	return authn.SmartWebLoginResult{AccessToken: "smart-web-access-token", Identity: authn.SmartWebIdentity{UserID: 1001, TenantID: 1002, Subject: "user:admin", LoginName: "admin"}}, nil
}

func (*routeSmartWebAuthenticator) SmartWebLoginInfo(context.Context, authn.RequestPrincipal) (authn.SmartWebIdentity, error) {
	return authn.SmartWebIdentity{}, authn.ErrAuthenticationRejected
}

func (*routeSmartWebAuthenticator) Logout(context.Context, authn.LogoutInput) error { return nil }
