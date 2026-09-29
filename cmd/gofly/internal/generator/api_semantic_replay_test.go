package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPISemanticReplayGeneratedProfilesCompile(t *testing.T) {
	profiles := []struct {
		name    string
		profile string
	}{
		{name: "native", profile: string(ProfileGoflyAI)},
		{name: "gozero-compatible", profile: string(ProfileGoZeroCompatible)},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			dir := t.TempDir()
			writeGeneratedModule(t, dir, "example.com/catalog")
			if err := GenerateRESTFromAPI(APIOptions{
				APIFile: goctlSemanticFixturePath(t),
				Dir:     dir,
				Package: "api",
				Profile: profile.profile,
			}); err != nil {
				t.Fatalf("GenerateRESTFromAPI(%s): %v", profile.name, err)
			}
			if profile.profile == string(ProfileGoZeroCompatible) {
				writeGoZeroSemanticRuntimeTest(t, dir)
			} else {
				writeNativeSemanticRuntimeTest(t, dir)
			}
			runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
			runGoCommand(t, dir, 3*time.Minute, "test", "./...")
		})
	}
}

func writeNativeSemanticRuntimeTest(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "internal", "api", "http", "v1", "catalog_api")
	source := `package api

import (
    "context"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/imajinyun/gofly/rest"
)

type semanticCatalog struct {
    create  CreateRequest
    deleted CreateRequest
}

func (s *semanticCatalog) CreateItem(_ context.Context, req *CreateRequest) (*CreateResponse, error) {
    s.create = *req
    return &CreateResponse{ID: req.ID}, nil
}

func (s *semanticCatalog) DeleteItem(_ context.Context, req *CreateRequest) error {
    s.deleted = *req
    return nil
}

func (*semanticCatalog) Health(context.Context) (*CreateResponse, error) {
    return &CreateResponse{ID: "healthy"}, nil
}

func TestSemanticRuntime(t *testing.T) {
    server := rest.MustNewServer(rest.Config{Name: "semantic", DisableDefaultMiddlewares: true})
    impl := new(semanticCatalog)
    RegisterCatalogApiRoutes(server, impl)

    create := httptest.NewRequest(http.MethodPost, "/api/v1/items/item-1?dryRun=true", strings.NewReader(` + "`" + `{"item":{"sku":"SKU-1","labels":{"tier":["gold"]}}}` + "`" + `))
    create.Header.Set("Content-Type", "application/json")
    create.Header.Set("X-Tenant-ID", "tenant-1")
    create.Header.Set("X-Request-ID", "request-1")
    createRec := httptest.NewRecorder()
    server.Handler().ServeHTTP(createRec, create)
    if createRec.Code != http.StatusCreated {
        t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
    }
    if impl.create.ID != "item-1" || !impl.create.DryRun || impl.create.TenantID != "tenant-1" || impl.create.RequestID != "request-1" || impl.create.Item == nil || impl.create.Item.SKU != "SKU-1" {
        t.Fatalf("create request = %+v", impl.create)
    }

    deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/items/item-2?dryRun=true", strings.NewReader(` + "`" + `{"item":{"sku":"SKU-2"}}` + "`" + `))
    deleteReq.Header.Set("Content-Type", "application/json")
    deleteReq.Header.Set("X-Tenant-ID", "tenant-2")
    deleteReq.Header.Set("X-Request-ID", "request-2")
    deleteRec := httptest.NewRecorder()
    server.Handler().ServeHTTP(deleteRec, deleteReq)
    if deleteRec.Code != http.StatusNoContent || deleteRec.Body.Len() != 0 {
        t.Fatalf("delete status=%d body=%q", deleteRec.Code, deleteRec.Body.String())
    }
    if impl.deleted.ID != "item-2" || impl.deleted.Item == nil || impl.deleted.Item.SKU != "SKU-2" {
        t.Fatalf("delete request = %+v", impl.deleted)
    }
}
`
	writeSemanticTestFile(t, filepath.Join(dir, "semantic_runtime_test.go"), source)
}

func writeGoZeroSemanticRuntimeTest(t *testing.T, root string) {
	t.Helper()
	source := `package routes

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/imajinyun/gofly/core/auth"
    "github.com/imajinyun/gofly/core/observability/trace"
    "example.com/catalog/internal/svc"
    "github.com/imajinyun/gofly/rest"
)

func TestSemanticRuntime(t *testing.T) {
    server := rest.MustNewServer(rest.Config{Name: "semantic", Preset: rest.PresetStandard})
    token, err := auth.SignJWT(auth.JWTClaims{Subject: "catalog"}, []byte("semantic-secret"))
    if err != nil { t.Fatalf("SignJWT: %v", err) }
    stx := &svc.ServiceContext{Middlewares: map[string]rest.Middleware{
        "Auth": func(next http.Handler) http.Handler { return next },
    }, JWTValidators: map[string]auth.Validator{
        "Auth": auth.JWTValidator([]byte("semantic-secret"), auth.JWTOptions{}),
    }}
    RegisterRoutes(server, stx)

    missing := httptest.NewRequest(http.MethodPost, "/api/v1/items/item-0?dryRun=true", strings.NewReader(` + "`" + `{"item":{"sku":"SKU-0"}}` + "`" + `))
    missing.Header.Set("Content-Type", "application/json")
    missing.Header.Set("X-Tenant-ID", "tenant-0")
    missing.Header.Set("X-Request-ID", "request-0")
    missingRec := httptest.NewRecorder()
    server.Handler().ServeHTTP(missingRec, missing)
    if missingRec.Code != http.StatusUnauthorized || !strings.Contains(missingRec.Body.String(), "unauthenticated") {
        t.Fatalf("missing auth status=%d body=%s", missingRec.Code, missingRec.Body.String())
    }
    if missingRec.Header().Get(rest.RequestIDHeader) != "request-0" || missingRec.Header().Get(trace.TraceParentHeader) == "" {
        t.Fatalf("missing auth correlation headers = %#v", missingRec.Header())
    }

    create := httptest.NewRequest(http.MethodPost, "/api/v1/items/item-1?dryRun=true", strings.NewReader(` + "`" + `{"item":{"sku":"SKU-1"}}` + "`" + `))
    create.Header.Set("Content-Type", "application/json")
    create.Header.Set("X-Tenant-ID", "tenant-1")
    create.Header.Set("X-Request-ID", "request-1")
    create.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
    createRec := httptest.NewRecorder()
    server.Handler().ServeHTTP(createRec, create)
    if createRec.Code != http.StatusCreated {
        t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
    }

    deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/items/item-2?dryRun=true", strings.NewReader(` + "`" + `{"item":{"sku":"SKU-2"}}` + "`" + `))
    deleteReq.Header.Set("Content-Type", "application/json")
    deleteReq.Header.Set("X-Tenant-ID", "tenant-2")
    deleteReq.Header.Set("X-Request-ID", "request-2")
    deleteReq.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
    deleteRec := httptest.NewRecorder()
    server.Handler().ServeHTTP(deleteRec, deleteReq)
    if deleteRec.Code != http.StatusNoContent || deleteRec.Body.Len() != 0 {
        t.Fatalf("delete status=%d body=%q", deleteRec.Code, deleteRec.Body.String())
    }

    invalid := httptest.NewRequest(http.MethodPost, "/api/v1/items/item-3", strings.NewReader("{"))
    invalid.Header.Set("Content-Type", "application/json")
    invalid.Header.Set("X-Tenant-ID", "tenant-3")
    invalid.Header.Set("X-Request-ID", "request-3")
    invalid.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
    invalidRec := httptest.NewRecorder()
    server.Handler().ServeHTTP(invalidRec, invalid)
    if invalidRec.Code != http.StatusBadRequest || !strings.Contains(invalidRec.Body.String(), "invalid_argument") {
        t.Fatalf("invalid status=%d body=%s", invalidRec.Code, invalidRec.Body.String())
    }
    if invalidRec.Header().Get(rest.RequestIDHeader) != "request-3" || invalidRec.Header().Get(trace.TraceParentHeader) == "" {
        t.Fatalf("invalid request correlation headers = %#v", invalidRec.Header())
    }
}
`
	writeSemanticTestFile(t, filepath.Join(root, "internal", "routes", "semantic_runtime_test.go"), source)
}

func writeSemanticTestFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write semantic runtime test %s: %v", path, err)
	}
}

func TestAPISemanticReplayGeneratedArtifacts(t *testing.T) {
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/catalog")
	if err := GenerateRESTFromAPI(APIOptions{
		APIFile: goctlSemanticFixturePath(t),
		Dir:     dir,
		Profile: string(ProfileGoZeroCompatible),
	}); err != nil {
		t.Fatalf("GenerateRESTFromAPI: %v", err)
	}

	types := readSemanticGeneratedFile(t, dir, filepath.Join("internal", "model", "types.go"))
	for _, want := range []string{"*Item  `json:\"item\"`", "map[string][]string `json:\"labels,optional\"`", "\n\tAudit\n"} {
		if !strings.Contains(types, want) {
			t.Fatalf("generated types missing %q:\n%s", want, types)
		}
	}
	routes := readSemanticGeneratedFile(t, dir, filepath.Join("internal", "routes", "routes.go"))
	for _, want := range []string{
		`rest.WithPrefix("/api/v1")`,
		`Path: "/items/{id}"`,
		"catalog.CreateItemHandler(stx)",
		"catalog.DeleteItemHandler(stx)",
	} {
		if !strings.Contains(routes, want) {
			t.Fatalf("generated routes missing %q:\n%s", want, routes)
		}
	}
}

func readSemanticGeneratedFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read generated %s: %v", rel, err)
	}
	return string(data)
}
