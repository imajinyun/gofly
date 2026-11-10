package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAPIImportTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAPIMergesImportedServices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "types/common.api", "type CommonResponse {\n  RequestID string "+tick+"json:\"request_id\""+tick+"\n}\n")
	writeAPIImportTestFile(t, dir, "front.api", "syntax = \"v1\"\nimport \"types/common.api\"\n"+
		"type FrontRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"@server(\n  prefix: /api/v1/front\n  group: front\n)\n"+
		"service front-api {\n  @handler frontPing\n  get /ping (FrontRequest) returns (CommonResponse)\n}\n")
	writeAPIImportTestFile(t, dir, "admin.api", "syntax = \"v1\"\nimport \"types/common.api\"\n"+
		"type AdminRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"@server(\n  prefix: /api/v1/admin\n  group: admin\n)\n"+
		"service admin-api {\n  @handler adminPing\n  get /ping (AdminRequest) returns (CommonResponse)\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"front.api\"\nimport \"admin.api\"\n"+
		"type RootRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"type RootResponse {\n  OK bool "+tick+"json:\"ok\""+tick+"\n}\n"+
		"@server(\n  prefix: /api/v1\n  group: root\n)\n"+
		"service root-api {\n  @handler rootPing\n  get /ping (RootRequest) returns (RootResponse)\n}\n")

	doc, err := LoadAPI(root)
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if len(doc.Services) != 3 {
		t.Fatalf("services = %d, want 3: %#v", len(doc.Services), doc.Services)
	}
	wantServices := []string{"front-api", "admin-api", "root-api"}
	for i, want := range wantServices {
		if doc.Services[i].Name != want {
			t.Fatalf("service[%d] = %q, want %q", i, doc.Services[i].Name, want)
		}
	}
	for _, want := range []string{"CommonResponse", "FrontRequest", "AdminRequest", "RootRequest", "RootResponse"} {
		if messageByName(doc, want).Name != want {
			t.Fatalf("missing imported type %s: %#v", want, doc.Messages)
		}
	}
	if err := ValidateAPI(doc); err != nil {
		t.Fatalf("ValidateAPI: %v", err)
	}
	routes := apiRouteInfos(doc)
	paths := make([]string, 0, len(routes))
	for _, route := range routes {
		paths = append(paths, route.Path)
	}
	for _, want := range []string{"/api/v1/front/ping", "/api/v1/admin/ping", "/api/v1/ping"} {
		found := false
		for _, path := range paths {
			if path == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("routes %v missing %s", paths, want)
		}
	}
	openapiPath := filepath.Join(dir, "openapi.json")
	if err := GenerateAPIDoc(APIDocOptions{APIFile: root, Output: openapiPath, Format: "openapi"}); err != nil {
		t.Fatalf("GenerateAPIDoc: %v", err)
	}
	openapi, err := os.ReadFile(openapiPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/api/v1/front/ping", "/api/v1/admin/ping", "/api/v1/ping", "CommonResponse"} {
		if !strings.Contains(string(openapi), want) {
			t.Fatalf("OpenAPI output missing %s", want)
		}
	}
}

func TestLoadAPIImportedServiceUsesTransitiveType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "types/common.api", "type CommonResponse {\n  RequestID string "+tick+"json:\"request_id\""+tick+"\n}\n")
	writeAPIImportTestFile(t, dir, "front.api", "syntax = \"v1\"\nimport \"types/common.api\"\n"+
		"service front-api {\n  @handler frontPing\n  get /ping () returns (CommonResponse)\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"front.api\"\n"+
		"type RootRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"service root-api {\n  @handler rootPing\n  get /root (RootRequest) returns (CommonResponse)\n}\n")

	doc, err := LoadAPI(root)
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if messageByName(doc, "CommonResponse").Name != "CommonResponse" {
		t.Fatalf("missing transitive type: %#v", doc.Messages)
	}
	if err := ValidateAPI(doc); err != nil {
		t.Fatalf("ValidateAPI: %v", err)
	}
}

func TestValidateAPIAllowsSameRelativePathUnderDifferentPrefixes(t *testing.T) {
	t.Parallel()
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Request", Fields: []IDLField{{Name: "ID", Type: "string"}}},
			{Name: "Response", Fields: []IDLField{{Name: "OK", Type: "bool"}}},
		},
		Services: []IDLService{
			{
				Name:   "front-api",
				Server: IDLServerAnnotation{Prefix: "/api/v1/front"},
				Methods: []IDLMethod{{
					Name: "FrontPing", Handler: "frontPing", HTTPMethod: "GET", HTTPPath: "/ping", Request: "Request", Response: "Response",
				}},
			},
			{
				Name:   "admin-api",
				Server: IDLServerAnnotation{Prefix: "/api/v1/admin"},
				Methods: []IDLMethod{{
					Name: "AdminPing", Handler: "adminPing", HTTPMethod: "GET", HTTPPath: "/ping", Request: "Request", Response: "Response",
				}},
			},
		},
	}
	if err := ValidateAPI(doc); err != nil {
		t.Fatalf("ValidateAPI: %v", err)
	}

	doc.Services[1].Server.Prefix = "/api/v1/front"
	if err := ValidateAPI(doc); err == nil || !strings.Contains(err.Error(), "duplicate route GET /api/v1/front/ping") {
		t.Fatalf("same effective route error = %v", err)
	}
}

func TestLoadAPIImportCycleKeepsServicesOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "b.api", "syntax = \"v1\"\nimport \"a.api\"\n"+
		"type BRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"service b-api {\n  @handler bPing\n  get /b (BRequest) returns (BRequest)\n}\n")
	writeAPIImportTestFile(t, dir, "a.api", "syntax = \"v1\"\nimport \"b.api\"\n"+
		"type ARequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"service a-api {\n  @handler aPing\n  get /a (ARequest) returns (ARequest)\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"a.api\"\n"+
		"service root-api {\n  @handler rootPing\n  get /root () returns ()\n}\n")

	doc, err := LoadAPI(root)
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if len(doc.Services) != 3 {
		t.Fatalf("services = %d, want 3: %#v", len(doc.Services), doc.Services)
	}
	// a.api imports b.api, so b is merged before a; the root service is last.
	if got := []string{doc.Services[0].Name, doc.Services[1].Name, doc.Services[2].Name}; strings.Join(got, ",") != "b-api,a-api,root-api" {
		t.Fatalf("service order = %v", got)
	}
}

func TestFormatAPIFromFileKeepsServiceImports(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "front.api", "type FrontRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"service front-api {\n  @handler frontPing\n  get /front () returns ()\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"front.api\"\n"+
		"service root-api {\n  @handler rootPing\n  get /root () returns ()\n}\n")

	formatted, err := FormatAPIFromFile(APIFormatOptions{APIFile: root})
	if err != nil {
		t.Fatalf("FormatAPIFromFile: %v", err)
	}
	text := string(formatted)
	if !strings.Contains(text, `import "front.api"`) {
		t.Fatalf("formatted contract lost import:\n%s", text)
	}
	if !strings.Contains(text, "service root-api") {
		t.Fatalf("formatted contract lost root service:\n%s", text)
	}
	if strings.Contains(text, "service front-api") {
		t.Fatalf("formatted contract inlined imported service:\n%s", text)
	}
}

func TestLoadAPIImportOnlyRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "front.api", "syntax = \"v1\"\n"+
		"type FrontRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"service front-api {\n  @handler frontPing\n  get /ping (FrontRequest) returns (FrontRequest)\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"front.api\"\n")
	doc, err := LoadAPI(root)
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if len(doc.Services) != 1 || doc.Services[0].Name != "front-api" {
		t.Fatalf("services = %#v", doc.Services)
	}
	if messageByName(doc, "FrontRequest").Name != "FrontRequest" {
		t.Fatalf("messages = %#v", doc.Messages)
	}
	formatted, err := FormatAPIFromFile(APIFormatOptions{APIFile: root})
	if err != nil {
		t.Fatalf("FormatAPIFromFile: %v", err)
	}
	text := string(formatted)
	if !strings.Contains(text, `import "front.api"`) || strings.Contains(text, "service front-api") {
		t.Fatalf("import-only root formatting inlined imported services:\n%s", text)
	}
}

func TestGenerateRESTFromImportedServices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "go.mod"), []byte("module example.com/importedapi\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "front.api", "type FrontRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"@server(\n  prefix: /api/v1/front\n  group: front\n)\n"+
		"service front-api {\n  @handler frontPing\n  get /ping (FrontRequest) returns (FrontRequest)\n}\n")
	writeAPIImportTestFile(t, dir, "admin.api", "type AdminRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"@server(\n  prefix: /api/v1/admin\n  group: admin\n)\n"+
		"service admin-api {\n  @handler adminPing\n  get /ping (AdminRequest) returns (AdminRequest)\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"front.api\"\nimport \"admin.api\"\n"+
		"type RootRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"@server(\n  prefix: /api/v1\n  group: root\n)\n"+
		"service root-api {\n  @handler rootPing\n  get /ping (RootRequest) returns (RootRequest)\n}\n")

	if err := GenerateRESTFromAPI(APIOptions{APIFile: root, Dir: out, Package: "api", Profile: "gozero-compatible"}); err != nil {
		t.Fatalf("GenerateRESTFromAPI: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "internal", "routes", "routes.go"))
	if err != nil {
		t.Fatalf("read generated routes: %v", err)
	}
	for _, want := range []string{
		`rest.WithPrefix("/api/v1/front")`, "front.FrontPingHandler",
		`rest.WithPrefix("/api/v1/admin")`, "admin.AdminPingHandler",
		`rest.WithPrefix("/api/v1")`, "root.RootPingHandler",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated routes missing %s:\n%s", want, data)
		}
	}
	if count := strings.Count(string(data), `Path: "/ping"`); count != 3 {
		t.Fatalf("generated route count = %d, want 3:\n%s", count, data)
	}
}

func TestLoadAPIRejectsMismatchedImportedSyntax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	writeAPIImportTestFile(t, dir, "front.api", "syntax = \"v2\"\n"+
		"type FrontRequest {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"+
		"service front-api {\n  @handler frontPing\n  get /ping (FrontRequest) returns (FrontRequest)\n}\n")
	root := writeAPIImportTestFile(t, dir, "selfexam.api", "syntax = \"v1\"\nimport \"front.api\"\n")
	if _, err := LoadAPI(root); err == nil || !strings.Contains(err.Error(), "uses syntax") {
		t.Fatalf("LoadAPI syntax mismatch error = %v", err)
	}
}
