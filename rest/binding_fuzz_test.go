package rest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FuzzBindJSON ensures JSON request binding never panics on arbitrary bodies.
// Decode/validation errors are expected; a panic is a bug.
func FuzzBindJSON(f *testing.F) {
	f.Add(`{"name":"ada"}`)
	f.Add(`{}`)
	f.Add(``)
	f.Add(`{"name":123}`)
	f.Add(`{"unknown":"x"}`)
	f.Add(`[1,2,3]`)
	f.Add(`{"name":"ada","age":30}`)

	f.Fuzz(func(t *testing.T, body string) {
		type payload struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		var p payload
		_ = BindJSON(req, &p)
	})
}

// FuzzBindQuery ensures query-string binding never panics on arbitrary input.
func FuzzBindQuery(f *testing.F) {
	f.Add("name=ada&age=30")
	f.Add("")
	f.Add("age=notanumber")
	f.Add("name=%ZZ")
	f.Add("a=1&a=2&a=3")

	f.Fuzz(func(t *testing.T, raw string) {
		type query struct {
			Name string `query:"name"`
			Age  int    `query:"age"`
		}
		// Build the request without round-tripping through the request target
		// parser: set RawQuery directly so arbitrary bytes exercise BindQuery
		// rather than httptest.NewRequest's target validation.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.RawQuery = raw
		var q query
		_ = BindQuery(req, &q)
	})
}
