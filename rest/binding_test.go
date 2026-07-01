package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	coreerrors "github.com/imajinyun/gofly/core/errors"
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

func TestValidationFailuresErrorAndDefensiveCopy(t *testing.T) {
	var empty ValidationFailures
	if got := empty.Error(); got != "validation failed" {
		t.Fatalf("empty ValidationFailures.Error() = %q, want validation failed", got)
	}
	failures := ValidationFailures{
		{Field: "name", Rule: "required", Message: "name is required"},
		{Field: "age", Rule: "min", Message: "age must be positive"},
	}
	if got := failures.Error(); got != "name is required" {
		t.Fatalf("ValidationFailures.Error() = %q, want first message", got)
	}
	copy := failures.ValidationFailures()
	copy[0].Message = "mutated"
	if failures[0].Message != "name is required" {
		t.Fatalf("ValidationFailures.ValidationFailures leaked mutable backing array: %+v", failures)
	}
}

func TestBindingScalarAndNumericBoundaries(t *testing.T) {
	type sample struct {
		Name    string
		Enabled bool
		Count   int8
		Total   uint16
		Ratio   float32
		Tags    []int
		Nested  *int
	}
	var got sample
	value := reflect.ValueOf(&got).Elem()
	if err := setFieldValue(value.FieldByName("Name"), []string{"first", "last"}); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if err := setFieldValue(value.FieldByName("Enabled"), []string{"true"}); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if err := setFieldValue(value.FieldByName("Count"), []string{"7"}); err != nil {
		t.Fatalf("set count: %v", err)
	}
	if err := setFieldValue(value.FieldByName("Total"), []string{"42"}); err != nil {
		t.Fatalf("set total: %v", err)
	}
	if err := setFieldValue(value.FieldByName("Ratio"), []string{"1.5"}); err != nil {
		t.Fatalf("set ratio: %v", err)
	}
	if err := setFieldValue(value.FieldByName("Tags"), []string{"1", "2"}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	if err := setFieldValue(value.FieldByName("Nested"), []string{"9"}); err != nil {
		t.Fatalf("set nested pointer: %v", err)
	}
	if got.Name != "last" || !got.Enabled || got.Count != 7 || got.Total != 42 || got.Ratio != 1.5 || len(got.Tags) != 2 || got.Tags[1] != 2 || got.Nested == nil || *got.Nested != 9 {
		t.Fatalf("bound scalar sample = %+v, want all scalar/slice/pointer fields set", got)
	}
	if err := setFieldValue(value.FieldByName("Count"), []string{"128"}); err == nil {
		t.Fatal("int8 overflow succeeded, want parse error")
	}
	if err := setScalar(value, "unsupported"); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("setScalar struct error = %v, want unsupported kind", err)
	}

	var nilInt *int
	if fieldValue(reflect.ValueOf(nilInt)) != nil || numericValue(reflect.ValueOf(nilInt)) != 0 {
		t.Fatal("nil pointer fieldValue/numericValue should return nil and 0")
	}
	if numericValue(reflect.ValueOf([]string{"a", "b"})) != 2 || numericValue(reflect.ValueOf(map[string]int{"x": 1})) != 1 || numericValue(reflect.ValueOf("abc")) != 3 {
		t.Fatal("numericValue should return len for slice/map/string")
	}
	zero := 0
	nonZero := 3
	if !isZero(reflect.ValueOf(&zero)) || isZero(reflect.ValueOf(&nonZero)) {
		t.Fatal("isZero should dereference pointers and inspect pointed value")
	}
	if numericValue(reflect.ValueOf(int64(-4))) != -4 || numericValue(reflect.ValueOf(uint32(5))) != 5 || numericValue(reflect.ValueOf(float64(2.5))) != 2.5 || numericValue(reflect.ValueOf(struct{}{})) != 0 {
		t.Fatal("numericValue should handle signed, unsigned, float, and unsupported values")
	}
}

func TestBindingTargetValidationAndJSONErrors(t *testing.T) {
	t.Run("invalid bind targets", func(t *testing.T) {
		if err := BindQuery(httptest.NewRequest(http.MethodGet, "/", nil), nil); err == nil || !strings.Contains(err.Error(), "bind target is nil") {
			t.Fatalf("BindQuery nil target error = %v, want nil target", err)
		}
		var nilStruct *struct{ Name string }
		if err := Validate(nilStruct); err == nil || !strings.Contains(err.Error(), "non-nil pointer") {
			t.Fatalf("Validate nil pointer error = %v, want non-nil pointer", err)
		}
		if err := BindHeader(httptest.NewRequest(http.MethodGet, "/", nil), struct{}{}); err == nil || !strings.Contains(err.Error(), "non-nil pointer") {
			t.Fatalf("BindHeader non-pointer error = %v, want pointer error", err)
		}
		var scalar int
		if err := Validate(&scalar); err == nil || !strings.Contains(err.Error(), "must point to a struct") {
			t.Fatalf("Validate scalar pointer error = %v, want struct pointer", err)
		}
	})

	t.Run("json decode and validate", func(t *testing.T) {
		type payload struct {
			Name string `json:"name" validate:"required"`
		}
		var got payload
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"gofly","extra":true}`))
		if err := BindJSON(req, &got); err == nil || !strings.Contains(err.Error(), "decode json body") || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("BindJSON unknown field error = %v, want decode json unknown field", err)
		}

		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		got = payload{}
		if err := BindJSON(req, &got); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("BindJSON validation error = %v, want required validation", err)
		}
	})
}

func TestBindValuesEmbeddedPointerSliceAndParseBranches(t *testing.T) {
	type Embedded struct {
		Trace string `query:"trace"`
	}
	type request struct {
		Embedded
		Ignored       string    `query:"-"`
		EmptyName     string    `query:",omitempty"`
		FormFallback  string    `form:"fallback"`
		Flag          *bool     `query:"flag"`
		Scores        []uint8   `query:"score"`
		Ratios        []float64 `query:"ratio"`
		unsupportedCh chan int  `query:"hidden"`
	}

	req := httptest.NewRequest(http.MethodGet, "/?trace=abc&Ignored=set&fallback=form&flag=true&score=7&score=8&ratio=1.25&ratio=2.5&hidden=1", nil)
	var got request
	if err := BindQuery(req, &got); err != nil {
		t.Fatalf("BindQuery embedded request returned error: %v", err)
	}
	if got.Trace != "abc" || got.FormFallback != "form" || got.Flag == nil || *got.Flag != true || len(got.Scores) != 2 || got.Scores[1] != 8 || len(got.Ratios) != 2 || got.Ratios[0] != 1.25 {
		t.Fatalf("BindQuery embedded request = %+v, want embedded/form/pointer/slices bound", got)
	}
	if got.Ignored != "" || got.EmptyName != "" {
		t.Fatalf("ignored fields were bound: ignored=%q empty=%q", got.Ignored, got.EmptyName)
	}

	parseCases := []struct {
		name   string
		target any
		url    string
		want   string
	}{
		{
			name: "bool parse",
			target: &struct {
				Flag bool `query:"flag"`
			}{},
			url:  "/?flag=not-bool",
			want: "bind query field Flag",
		},
		{
			name: "uint parse",
			target: &struct {
				Scores []uint8 `query:"score"`
			}{},
			url:  "/?score=300",
			want: "bind query field Scores",
		},
		{
			name: "float parse",
			target: &struct {
				Ratio float32 `query:"ratio"`
			}{},
			url:  "/?ratio=not-float",
			want: "bind query field Ratio",
		},
		{
			name: "unsupported exported kind",
			target: &struct {
				Ch chan int `query:"ch"`
			}{},
			url:  "/?ch=1",
			want: "unsupported kind chan",
		},
	}
	for _, tt := range parseCases {
		t.Run(tt.name, func(t *testing.T) {
			err := BindQuery(httptest.NewRequest(http.MethodGet, tt.url, nil), tt.target)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("BindQuery error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestBindRequestSourceOrderingAndMethodBodyBranches(t *testing.T) {
	type request struct {
		ID    int    `path:"id" validate:"min=1"`
		Name  string `json:"name" query:"name" validate:"required"`
		Role  string `header:"X-Role" validate:"oneof=admin user"`
		Debug bool   `query:"debug"`
	}

	req := httptest.NewRequest(http.MethodGet, "/users/11?name=query&debug=true", strings.NewReader(`{invalid-json`))
	req.SetPathValue("id", "11")
	req.Header.Set("X-Role", "admin")
	var got request
	if err := BindRequest(req, &got); err != nil {
		t.Fatalf("BindRequest GET with body returned error: %v", err)
	}
	if got.ID != 11 || got.Name != "query" || got.Role != "admin" || !got.Debug {
		t.Fatalf("BindRequest GET = %+v, want path/query/header and skipped body", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/users/12?name=query&debug=false", strings.NewReader(`{"name":"json"}`))
	req.SetPathValue("id", "12")
	req.Header.Set("X-Role", "user")
	got = request{}
	if err := BindRequest(req, &got); err != nil {
		t.Fatalf("BindRequest POST returned error: %v", err)
	}
	if got.ID != 12 || got.Name != "query" || got.Role != "user" || got.Debug {
		t.Fatalf("BindRequest POST = %+v, want later query binding to override json name", got)
	}

	req = httptest.NewRequest(http.MethodDelete, "/users/13?name=delete", strings.NewReader(`{invalid-json`))
	req.SetPathValue("id", "13")
	req.Header.Set("X-Role", "admin")
	got = request{}
	if err := BindRequest(req, &got); err != nil {
		t.Fatalf("BindRequest DELETE with body returned error: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/users/14?name=post", strings.NewReader(`{invalid-json`))
	req.SetPathValue("id", "14")
	req.Header.Set("X-Role", "admin")
	if err := BindRequest(req, &got); err == nil || !strings.Contains(err.Error(), "decode json body") {
		t.Fatalf("BindRequest POST invalid body error = %v, want decode json body", err)
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

func TestValidationFailuresOfAndAdapter(t *testing.T) {
	custom := ValidationFailures{{Field: "name", Rule: "custom", Message: "name is reserved"}}
	got := ValidationFailuresOf(custom)
	if len(got) != 1 || got[0].Field != "name" || got[0].Rule != "custom" || got[0].Message != "name is reserved" {
		t.Fatalf("ValidationFailuresOf(custom) = %#v, want custom field failure", got)
	}
	got[0].Field = "mutated"
	if custom[0].Field != "name" {
		t.Fatalf("ValidationFailuresOf returned aliased slice: %#v", custom)
	}

	validator := ValidatorFunc(func(value any) error {
		req, ok := value.(*struct {
			Name string `json:"name"`
		})
		if !ok || req.Name != "reserved" {
			return nil
		}
		return ValidationFailures{{Field: "name", Rule: "reserved", Message: "name is reserved"}}
	})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"reserved"}`))
	ctx := &Context{Request: req, Validator: validator}
	var body struct {
		Name string `json:"name"`
	}
	if err := ctx.Bind(&body); err == nil || len(ValidationFailuresOf(err)) != 1 {
		t.Fatalf("Context.Bind custom validator error = %v, want one validation failure", err)
	}
}

func TestContextErrorWritesValidationFields(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx := &Context{Response: rec, Request: httptest.NewRequest(http.MethodGet, "/", nil)}
	ctx.Error(&ValidationError{Field: "quantity", Rule: "min=1"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("validation error status = %d, want 400", rec.Code)
	}
	var got ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode ErrorResponse: %v", err)
	}
	if got.Code != "invalid_argument" || len(got.Fields) != 1 || got.Fields[0].Field != "quantity" || got.Fields[0].Rule != "min=1" {
		t.Fatalf("ErrorResponse = %#v, want invalid_argument with quantity field", got)
	}
}

func TestContextErrorWritesBindingFailuresAsInvalidArgument(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{name: "bad json body", method: http.MethodPost, path: "/orders/7?page=2", body: `{"name":`, want: "decode json body"},
		{name: "bad path value", method: http.MethodGet, path: "/orders/not-int?page=2", want: "bind path field ID"},
		{name: "bad query value", method: http.MethodGet, path: "/orders/7?page=zero", want: "bind query field Page"},
		{name: "validation value", method: http.MethodGet, path: "/orders/7?page=0", want: "field Page failed min=1 validation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type request struct {
				ID   int `path:"id" validate:"min=1"`
				Page int `query:"page" validate:"min=1"`
			}
			rec := httptest.NewRecorder()
			s := MustNewServer(Config{})
			s.AddRoute(Route{Method: tt.method, Path: "/orders/{id}", Handler: func(ctx *Context) {
				var req request
				if err := ctx.BindRequest(&req); err != nil {
					if coreerrors.CodeOf(err) != coreerrors.CodeInvalidArgument {
						t.Fatalf("binding error code = %s, want invalid_argument", coreerrors.CodeOf(err))
					}
					ctx.Error(err)
					return
				}
				ctx.JSON(http.StatusOK, req)
			}})

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			var got ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode ErrorResponse: %v", err)
			}
			if got.Code != coreerrors.CodeInvalidArgument || !strings.Contains(got.Text, tt.want) {
				t.Fatalf("ErrorResponse = %#v, want invalid_argument containing %q", got, tt.want)
			}
		})
	}
}

func TestOpenAPIValidationEnvelopeRuntimeGolden(t *testing.T) {
	type createOrderRequest struct {
		Tenant   string   `header:"X-Tenant" validate:"required"`
		ID       int      `path:"id" validate:"min=1"`
		Page     int      `query:"page" validate:"min=1,max=100"`
		SKU      string   `json:"sku" validate:"required,min=3,max=64"`
		Status   string   `json:"status" validate:"oneof=pending paid canceled"`
		Quantity int      `json:"quantity" validate:"min=1,max=100"`
		Labels   []string `json:"labels" validate:"min=1,max=3"`
	}

	tests := []struct {
		name      string
		method    string
		target    string
		pathValue string
		header    string
		body      string
		validator Validator
		wantText  string
		wantField string
		wantRule  string
	}{
		{
			name:      "path parse failure",
			method:    http.MethodPost,
			target:    "/orders/not-int?page=1",
			pathValue: "not-int",
			header:    "tenant-a",
			body:      `{"sku":"ABC","status":"pending","quantity":1,"labels":["new"]}`,
			wantText:  "bind path field ID",
		},
		{
			name:      "query tag validation failure",
			method:    http.MethodPost,
			target:    "/orders/7?page=0",
			pathValue: "7",
			header:    "tenant-a",
			body:      `{"sku":"ABC","status":"pending","quantity":1,"labels":["new"]}`,
			wantText:  "field Page failed min=1 validation",
			wantField: "Page",
			wantRule:  "min=1",
		},
		{
			name:      "header tag validation failure",
			method:    http.MethodPost,
			target:    "/orders/7?page=1",
			pathValue: "7",
			body:      `{"sku":"ABC","status":"pending","quantity":1,"labels":["new"]}`,
			wantText:  "field Tenant failed required validation",
			wantField: "Tenant",
			wantRule:  "required",
		},
		{
			name:      "body schema decode failure",
			method:    http.MethodPost,
			target:    "/orders/7?page=1",
			pathValue: "7",
			header:    "tenant-a",
			body:      `{"sku":"ABC","status":"pending","quantity":"many","labels":["new"]}`,
			wantText:  "decode json body",
		},
		{
			name:      "body tag enum validation failure",
			method:    http.MethodPost,
			target:    "/orders/7?page=1",
			pathValue: "7",
			header:    "tenant-a",
			body:      `{"sku":"ABC","status":"shipped","quantity":1,"labels":["new"]}`,
			wantText:  "field Status failed oneof=pending paid canceled validation",
			wantField: "Status",
			wantRule:  "oneof=pending paid canceled",
		},
		{
			name:      "validator adapter field failure",
			method:    http.MethodPost,
			target:    "/orders/7?page=1",
			pathValue: "7",
			header:    "tenant-a",
			body:      `{"sku":"BLOCKED","status":"pending","quantity":1,"labels":["new"]}`,
			validator: ValidatorFunc(func(value any) error {
				req, ok := value.(*createOrderRequest)
				if !ok || req.SKU != "BLOCKED" {
					return nil
				}
				return ValidationFailures{{Field: "sku", Rule: "reserved", Message: "sku is reserved"}}
			}),
			wantText:  "sku is reserved",
			wantField: "sku",
			wantRule:  "reserved",
		},
	}

	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodPost, Path: "/orders/{id}", Handler: func(ctx *Context) {
		for _, tt := range tests {
			if ctx.Request.Header.Get("X-Test-Case") == tt.name {
				ctx.Validator = tt.validator
				break
			}
		}
		var req createOrderRequest
		if err := ctx.BindRequest(&req); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, req)
	}})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Case", tt.name)
			if tt.header != "" {
				req.Header.Set("X-Tenant", tt.header)
			}
			req.SetPathValue("id", tt.pathValue)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
			var got ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode rest.ErrorResponse: %v", err)
			}
			if got.Code != coreerrors.CodeInvalidArgument || got.Status != http.StatusBadRequest || !strings.Contains(got.Text, tt.wantText) {
				t.Fatalf("rest.ErrorResponse = %#v, want invalid_argument status 400 text containing %q", got, tt.wantText)
			}
			if tt.wantField == "" {
				if len(got.Fields) != 0 {
					t.Fatalf("fields = %#v, want no field-level failures for parse/decode error", got.Fields)
				}
				return
			}
			if len(got.Fields) != 1 || got.Fields[0].Field != tt.wantField || got.Fields[0].Rule != tt.wantRule {
				t.Fatalf("fields = %#v, want %s/%s", got.Fields, tt.wantField, tt.wantRule)
			}
		})
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
