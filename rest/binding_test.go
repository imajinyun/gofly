package rest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextBindRequest(t *testing.T) {
	type request struct {
		ID      int      `path:"id" validate:"min=1"`
		Name    string   `json:"name" validate:"required"`
		Page    int      `query:"page" validate:"min=1"`
		Role    string   `header:"X-Role" validate:"oneof=admin user"`
		Filters []string `query:"filter"`
	}

	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/users/{id}", Handler: func(ctx *Context) {
		var req request
		if err := ctx.BindRequest(&req); err != nil {
			ctx.String(http.StatusBadRequest, err.Error())
			return
		}
		ctx.String(http.StatusOK, req.Name)
	}})

	req := httptest.NewRequest(http.MethodPost, "/users/7?page=2&filter=a&filter=b", strings.NewReader(`{"name":"gofly"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Role", "admin")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "gofly" {
		t.Fatalf("body = %q, want gofly", rec.Body.String())
	}
}

func TestBindPathAndHeader(t *testing.T) {
	type pathRequest struct {
		ID int `path:"id" validate:"min=1"`
	}
	type headerRequest struct {
		Token string `header:"X-Token" validate:"required"`
	}

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.SetPathValue("id", "42")
	req.Header.Set("X-Token", "secret")

	var pathGot pathRequest
	if err := BindPath(req, &pathGot); err != nil {
		t.Fatalf("BindPath returned error: %v", err)
	}
	var headerGot headerRequest
	if err := BindHeader(req, &headerGot); err != nil {
		t.Fatalf("BindHeader returned error: %v", err)
	}
	if pathGot.ID != 42 || headerGot.Token != "secret" {
		t.Fatalf("bound request = path %+v header %+v, want id 42 and token secret", pathGot, headerGot)
	}
}

func TestContextBindPathAndHeader(t *testing.T) {
	type pathRequest struct {
		ID int `path:"id" validate:"min=1"`
	}
	type headerRequest struct {
		Token string `header:"X-Token" validate:"required"`
	}

	req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
	req.SetPathValue("id", "7")
	req.Header.Set("X-Token", "ctx-token")
	ctx := &Context{Request: req, Response: httptest.NewRecorder()}

	var pathGot pathRequest
	if err := ctx.BindPath(&pathGot); err != nil {
		t.Fatalf("Context.BindPath returned error: %v", err)
	}
	var headerGot headerRequest
	if err := ctx.BindHeader(&headerGot); err != nil {
		t.Fatalf("Context.BindHeader returned error: %v", err)
	}
	if pathGot.ID != 7 || headerGot.Token != "ctx-token" {
		t.Fatalf("bound context request = path %+v header %+v, want id 7 and token ctx-token", pathGot, headerGot)
	}
}

func TestValidationErrorError(t *testing.T) {
	if got := (*ValidationError)(nil).Error(); got != "" {
		t.Fatalf("nil ValidationError Error() = %q, want empty", got)
	}
	if got := (&ValidationError{Text: "custom"}).Error(); got != "custom" {
		t.Fatalf("custom ValidationError Error() = %q, want custom", got)
	}
	if got := (&ValidationError{Field: "age", Rule: "min=18"}).Error(); got != "field age failed min=18 validation" {
		t.Fatalf("ValidationError Error() = %q, want field age failed min=18 validation", got)
	}
}

func TestValidateRejectsRequiredAndRange(t *testing.T) {
	type request struct {
		Name string `validate:"required"`
		Age  int    `validate:"min=18,max=60"`
	}

	if err := Validate(&request{Age: 20}); err == nil {
		t.Fatal("Validate should reject missing required name")
	}
	var validationErr *ValidationError
	if err := Validate(&request{Name: "gofly", Age: 17}); !errors.As(err, &validationErr) || validationErr.Rule != "min=18" {
		t.Fatalf("Validate error = %v, want min=18 ValidationError", err)
	}
	if err := Validate(&request{Name: "gofly", Age: 20}); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}
