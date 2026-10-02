package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Failure-mode inventory for the OpenAPI round-trip contract:
//   - remote and parent-directory references must fail closed before generation;
//   - JSON and YAML sources must import into a parseable gofly API document;
//   - routes and response-less operations must survive export;
//   - the generated temporary module must compile and register imported routes.
func TestOpenAPIRoundTripContract(t *testing.T) {
	root := repositoryRoot(t)
	fixtures := filepath.Join(root, "testdata", "api", "openapi", "roundtrip")

	for _, name := range []string{"remote-ref.json", "escape-ref.yaml"} {
		t.Run("rejects/"+name, func(t *testing.T) {
			err := GenerateAPIFromOpenAPI(APIImportOptions{
				Source: filepath.Join(fixtures, name),
				Output: filepath.Join(t.TempDir(), "imported.api"),
			})
			if err == nil || !strings.Contains(err.Error(), "only local OpenAPI references are supported") {
				t.Fatalf("GenerateAPIFromOpenAPI(%s) error = %v, want local-reference rejection", name, err)
			}
		})
	}

	t.Run("json/import-generate-export-and-smoke", func(t *testing.T) {
		work := t.TempDir()
		apiPath := filepath.Join(work, "widget.api")
		if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: filepath.Join(fixtures, "roundtrip.json"), Output: apiPath}); err != nil {
			t.Fatalf("import OpenAPI: %v", err)
		}
		doc, err := LoadAPI(apiPath)
		if err != nil {
			t.Fatalf("load imported API: %v", err)
		}
		request := messageByName(doc, "GetWidgetRequest")
		for _, want := range []struct{ field, tag string }{
			{field: "Id", tag: `path:"id"`},
			{field: "Verbose", tag: `form:"verbose,optional"`},
			{field: "XTenantID", tag: `header:"X-Tenant-ID"`},
		} {
			var got string
			for _, field := range request.Fields {
				if field.Name == want.field {
					got = field.Tag
					break
				}
			}
			if got != want.tag {
				t.Fatalf("imported field %s tag = %q, want %q", want.field, got, want.tag)
			}
		}
		openAPIPath := filepath.Join(work, "roundtrip.openapi.json")
		if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: openAPIPath, Format: "openapi"}); err != nil {
			t.Fatalf("export OpenAPI: %v", err)
		}
		assertOpenAPIRoundTripExport(t, openAPIPath, []string{"/widgets", "/widgets/{id}"}, "DeleteWidget")
		assertOpenAPIRoundTripSemantics(t, openAPIPath)

		project := filepath.Join(work, "project")
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatalf("create generated project directory: %v", err)
		}
		writeGeneratedModule(t, project, "example.com/openapi-roundtrip")
		if err := GenerateRESTFromAPI(APIOptions{APIFile: apiPath, Dir: project, Profile: string(ProfileGoZeroCompatible)}); err != nil {
			t.Fatalf("generate service: %v", err)
		}
		writeOpenAPIRoundTripSmoke(t, project)
		runGoCommand(t, project, 3*time.Minute, "mod", "tidy")
		runGoCommand(t, project, 3*time.Minute, "test", "./...")
	})

	t.Run("yaml/import-and-export", func(t *testing.T) {
		work := t.TempDir()
		apiPath := filepath.Join(work, "health.api")
		if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: filepath.Join(fixtures, "roundtrip.yaml"), Output: apiPath}); err != nil {
			t.Fatalf("import YAML OpenAPI: %v", err)
		}
		openAPIPath := filepath.Join(work, "health.openapi.json")
		if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: openAPIPath, Format: "openapi"}); err != nil {
			t.Fatalf("export YAML import: %v", err)
		}
		assertOpenAPIRoundTripExport(t, openAPIPath, []string{"/health"}, "Health")
	})
}

func assertOpenAPIRoundTripSemantics(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode round-trip OpenAPI: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	widgetByID, _ := paths["/widgets/{id}"].(map[string]any)
	get, _ := widgetByID["get"].(map[string]any)
	parameters, _ := get["parameters"].([]any)
	locations := map[string]string{}
	for _, raw := range parameters {
		parameter, _ := raw.(map[string]any)
		locations[parameter["name"].(string)] = parameter["in"].(string)
	}
	for name, location := range map[string]string{"id": "path", "verbose": "query", "X-Tenant-ID": "header"} {
		if locations[name] != location {
			t.Fatalf("exported parameter locations = %#v, want %s in %s", locations, name, location)
		}
	}
	getResponses, _ := get["responses"].(map[string]any)
	if _, ok := getResponses["200"]; !ok {
		t.Fatalf("GET responses = %#v, want 200", getResponses)
	}
	if response, ok := getResponses["404"].(map[string]any); !ok || response["description"] != "Not found" {
		t.Fatalf("GET 404 response = %#v, want documented Not found response", getResponses["404"])
	}
	widgets, _ := paths["/widgets"].(map[string]any)
	create, _ := widgets["post"].(map[string]any)
	createResponses, _ := create["responses"].(map[string]any)
	if _, ok := createResponses["201"]; !ok {
		t.Fatalf("POST responses = %#v, want 201", createResponses)
	}
	deleteOperation, _ := widgetByID["delete"].(map[string]any)
	deleteResponses, _ := deleteOperation["responses"].(map[string]any)
	if response, ok := deleteResponses["204"].(map[string]any); !ok || response["content"] != nil {
		t.Fatalf("DELETE 204 response = %#v, want response-less 204", deleteResponses["204"])
	}
}

func assertOpenAPIRoundTripExport(t *testing.T, path string, requiredPaths []string, operationID string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode exported OpenAPI: %v", err)
	}
	if doc.OpenAPI != "3.0.3" {
		t.Fatalf("export OpenAPI version = %q, want 3.0.3", doc.OpenAPI)
	}
	for _, route := range requiredPaths {
		if _, ok := doc.Paths[route]; !ok {
			t.Fatalf("exported OpenAPI paths = %#v, missing %q", doc.Paths, route)
		}
	}
	found := false
	for _, item := range doc.Paths {
		for _, operation := range item {
			if operationMap, ok := operation.(map[string]any); ok && operationMap["operationId"] == operationID {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("exported OpenAPI does not contain operationId %q", operationID)
	}
}

func writeOpenAPIRoundTripSmoke(t *testing.T, root string) {
	t.Helper()
	source := `package routes

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "example.com/openapi-roundtrip/internal/svc"
    "github.com/imajinyun/gofly/rest"
)

func TestOpenAPIRoundTripRouteSmoke(t *testing.T) {
    server := rest.MustNewServer(rest.Config{Name: "roundtrip", DisableDefaultMiddlewares: true})
    RegisterRoutes(server, &svc.ServiceContext{Middlewares: map[string]rest.Middleware{}})
    rec := httptest.NewRecorder()
    request := httptest.NewRequest(http.MethodGet, "/widgets/widget-1?verbose=true", nil)
    request.Header.Set("X-Tenant-ID", "tenant-1")
    server.Handler().ServeHTTP(rec, request)
    if rec.Code != http.StatusOK {
        t.Fatalf("generated route status = %d body=%s", rec.Code, rec.Body.String())
    }
}
`
	path := filepath.Join(root, "internal", "routes", "openapi_roundtrip_smoke_test.go")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write generated route smoke test: %v", err)
	}
}
