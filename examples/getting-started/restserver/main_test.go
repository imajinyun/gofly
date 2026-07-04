package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemas(t *testing.T) {
	user := userSchema()
	if user.Type != "object" || user.Properties["id"].Type != "string" || user.Properties["name"].Type != "string" || len(user.Required) != 2 {
		t.Fatalf("user schema = %#v, want id/name object schema", user)
	}
	create := createUserSchema()
	if create.Type != "object" || create.Properties["name"].Type != "string" || len(create.Required) != 1 {
		t.Fatalf("create schema = %#v, want required name object schema", create)
	}
}

func TestNewRESTServerRoutes(t *testing.T) {
	srv := newRESTServer()

	getUser := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getUser, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	if getUser.Code != http.StatusOK || !strings.Contains(getUser.Body.String(), `"id":"42"`) {
		t.Fatalf("GET /users/42 = %d %s, want 200 user 42", getUser.Code, getUser.Body.String())
	}

	createUser := httptest.NewRecorder()
	srv.Handler().ServeHTTP(createUser, httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"ada"}`)))
	if createUser.Code != http.StatusCreated || !strings.Contains(createUser.Body.String(), `"name":"ada"`) {
		t.Fatalf("POST /users = %d %s, want 201 ada", createUser.Code, createUser.Body.String())
	}

	badUser := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badUser, httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{`)))
	if badUser.Code != http.StatusBadRequest || !strings.Contains(badUser.Body.String(), `"code":"invalid_argument"`) {
		t.Fatalf("POST /users bad json = %d %s, want 400 invalid_argument", badUser.Code, badUser.Body.String())
	}

	openapi := httptest.NewRecorder()
	srv.Handler().ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if openapi.Code != http.StatusOK || !strings.Contains(openapi.Body.String(), "restserver demo") {
		t.Fatalf("GET /openapi.json = %d %s, want contract", openapi.Code, openapi.Body.String())
	}
}
