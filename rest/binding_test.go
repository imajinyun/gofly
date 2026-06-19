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
