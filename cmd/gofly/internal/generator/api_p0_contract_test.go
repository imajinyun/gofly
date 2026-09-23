package generator

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/rest"
)

func TestLoadAPIResolvesImportsForSemanticConsumers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	shared := filepath.Join(dir, "types", "common.api")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("type AuditMeta {\n  RequestID string "+tick+"header:\"X-Request-ID\""+tick+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apiPath := filepath.Join(dir, "orders.api")
	api := "import \"types/common.api\"\n" +
		"type CreateRequest {\n  Meta AuditMeta " + tick + "json:\"meta\"" + tick + "\n}\n" +
		"type CreateResponse {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\n" +
		"service orders-api {\n  @handler create\n  post /orders (CreateRequest) returns (CreateResponse)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := LoadAPI(apiPath)
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if messageByName(doc, "AuditMeta").Name != "AuditMeta" {
		t.Fatalf("LoadAPI messages = %#v, want imported AuditMeta", doc.Messages)
	}
	docPath := filepath.Join(dir, "orders.json")
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: docPath, Format: "openapi"}); err != nil {
		t.Fatalf("GenerateAPIDoc: %v", err)
	}
	docData, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(docData), "\"AuditMeta\"") || !strings.Contains(string(docData), "#/components/schemas/AuditMeta") {
		t.Fatalf("OpenAPI output does not include imported schema:\n%s", docData)
	}
	clientPath := filepath.Join(dir, "orders_client.ts")
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Output: clientPath, Language: "typescript"}); err != nil {
		t.Fatalf("GenerateAPIClient: %v", err)
	}
	clientData, err := os.ReadFile(clientPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientData), "export interface AuditMeta") {
		t.Fatalf("client output does not include imported type:\n%s", clientData)
	}
	typesPath := filepath.Join(dir, "types.go")
	if err := GenerateAPITypes(APITypesOptions{APIFile: apiPath, Output: typesPath, Package: "api"}); err != nil {
		t.Fatalf("GenerateAPITypes: %v", err)
	}
	typesData, err := os.ReadFile(typesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typesData), "type AuditMeta struct") {
		t.Fatalf("types output does not include imported type:\n%s", typesData)
	}
	routesPath := filepath.Join(dir, "routes.json")
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: apiPath, Output: routesPath, Format: "json"}); err != nil {
		t.Fatalf("GenerateAPIRoutes: %v", err)
	}
	if _, err := os.Stat(routesPath); err != nil {
		t.Fatal(err)
	}
}

func TestFormatAPIPreservesSourceContractMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	shared := filepath.Join(dir, "common.api")
	if err := os.WriteFile(shared, []byte("type Shared {\n  ID string "+tick+"json:\"id\""+tick+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apiPath := filepath.Join(dir, "service.api")
	api := "syntax = \"v1\"\nimport \"common.api\"\n" +
		"type Request {\n  Trace string " + tick + "header:\"X-Trace,optional\"" + tick + "\n  Body Shared " + tick + "json:\"body\"" + tick + "\n}\n" +
		"type Response {\n  OK bool " + tick + "json:\"ok\"" + tick + "\n}\n" +
		"@server(group: users prefix: /api/v1 jwt: Auth middleware: audit)\nservice users-api {\n  @doc(respCode: 201)\n  @handler create\n  post /users (Request) returns (Response)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	formatted, err := FormatAPIFromFile(APIFormatOptions{APIFile: apiPath, Write: false})
	if err != nil {
		t.Fatalf("FormatAPIFromFile: %v", err)
	}
	for _, want := range []string{"syntax = \"v1\"", "import \"common.api\"", tick + "header:\"X-Trace,optional\"" + tick, tick + "json:\"body\"" + tick, "@server(", "group: users", "@doc(respcode: \"201\")"} {
		if !strings.Contains(string(formatted), want) {
			t.Fatalf("formatted contract missing %q:\n%s", want, formatted)
		}
	}
}

func TestFormatAPIContentValidationModes(t *testing.T) {
	t.Parallel()
	valid := "type Request {\n  ID string `json:\"id\"`\n}\ntype Response {\n  OK bool `json:\"ok\"`\n}\nservice demo-api {\n  @handler get\n  get /demo (Request) returns (Response)\n}\n"
	formatted, err := FormatAPIContent(valid, false)
	if err != nil || !strings.Contains(string(formatted), "service demo-api") {
		t.Fatalf("FormatAPIContent valid = %q, %v", formatted, err)
	}
	undeclared := "type Request {\n  Shared Missing `json:\"shared\"`\n}\n"
	if _, err := FormatAPIContent(undeclared, false); err == nil {
		t.Fatal("FormatAPIContent accepted an undeclared type")
	}
	if _, err := FormatAPIContent(undeclared, true); err != nil {
		t.Fatalf("FormatAPIContent declare mode: %v", err)
	}
	if _, err := FormatAPIContent("type Broken {", false); err == nil {
		t.Fatal("FormatAPIContent accepted malformed source")
	}
}

func TestNormalizeGoZeroRoutePathWorksWithServeMux(t *testing.T) {
	t.Parallel()
	path := normalizeAPIRoutePath("/users/:id/orders/:order_id")
	if path != "/users/{id}/orders/{order_id}" {
		t.Fatalf("normalizeAPIRoutePath = %q", path)
	}
	srv, err := rest.NewServer(rest.Config{Name: "route-test", DisableDefaultMiddlewares: true})
	if err != nil {
		t.Fatal(err)
	}
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: path, Handler: func(ctx *rest.Context) { ctx.String(http.StatusOK, ctx.PathValue("id")+"/"+ctx.PathValue("order_id")) }})
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/users/42/orders/7", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "42/7" {
		t.Fatalf("route response = %d %q, want 200 42/7", recorder.Code, recorder.Body.String())
	}
}

func TestGeneratedHandlerUsesDocumentedSuccessStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/orders\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick := string(rune(96))
	apiPath := filepath.Join(dir, "orders.api")
	api := "type CreateRequest {\n  Name string " + tick + "json:\"name\"" + tick + "\n}\n" +
		"type CreateResponse {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\n" +
		"service orders-api {\n  @doc(respCode: 201)\n  @handler create\n  post /orders (CreateRequest) returns (CreateResponse)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateRESTFromAPI(APIOptions{APIFile: apiPath, Dir: dir, Profile: string(ProfileGoZeroCompatible)}); err != nil {
		t.Fatal(err)
	}
	handlerPath := filepath.Join(dir, "internal", "api", "http", "v1", "orders", "create.go")
	data, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, "ctx.Error(err)") || !strings.Contains(source, "ctx.JSON(http.StatusCreated, resp)") {
		t.Fatalf("generated handler does not match documented response contract:\n%s", source)
	}
}

func TestGeneratedNativeRESTPreservesRouteStatusAndErrorContracts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	apiPath := filepath.Join(dir, "orders.api")
	api := "type GetRequest {\n  ID string " + tick + "path:\"id\"" + tick + "\n}\n" +
		"type Reply {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\n" +
		"service orders-api {\n" +
		"  @doc(respCode: 202)\n  @handler get\n  get /orders/:id (GetRequest) returns (Reply)\n" +
		"  @handler health\n  get /health returns (Reply)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateRESTFromAPI(APIOptions{
		APIFile:    apiPath,
		Dir:        dir,
		Package:    "api",
		RPCPackage: "example.com/orders/pb",
		TypeGroup:  true,
		Test:       true,
	}); err != nil {
		t.Fatalf("GenerateRESTFromAPI native: %v", err)
	}

	serviceDir := filepath.Join(dir, "internal", "api", "http", "v1", "orders_api")
	methodData, err := os.ReadFile(filepath.Join(serviceDir, "get.go"))
	if err != nil {
		t.Fatal(err)
	}
	methodSource := string(methodData)
	for _, want := range []string{
		`Path: "/orders/{id}"`,
		`responses["202"]`,
		"ctx.JSON(http.StatusAccepted, resp)",
		"ctx.Error(err)",
	} {
		if !strings.Contains(methodSource, want) {
			t.Fatalf("native method missing %q:\n%s", want, methodSource)
		}
	}
	healthData, err := os.ReadFile(filepath.Join(serviceDir, "health.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(healthData), "var req") || !strings.Contains(string(healthData), "ctx.JSON(http.StatusOK, resp)") {
		t.Fatalf("native no-request method drifted:\n%s", healthData)
	}
	for _, path := range []string{
		filepath.Join(serviceDir, "service.go"),
		filepath.Join(serviceDir, "gateway.go"),
		filepath.Join(serviceDir, "get_gateway.go"),
		filepath.Join(serviceDir, "routes.go"),
		filepath.Join(serviceDir, "routes_test.go"),
		filepath.Join(dir, "internal", "api", "http", "v1", "types_get_request.go"),
		filepath.Join(dir, "internal", "api", "http", "v1", "converters.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("native generated file %s: %v", path, err)
		}
	}
}

func TestGenerateRESTFromAPINormalizesGoZeroPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/users\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick := string(rune(96))
	apiPath := filepath.Join(dir, "users.api")
	api := "type GetRequest {\n  ID string " + tick + "path:\"id\"" + tick + "\n}\n" +
		"type GetResponse {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\n" +
		"service users-api {\n  @handler get\n  get /users/:id (GetRequest) returns (GetResponse)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateRESTFromAPI(APIOptions{APIFile: apiPath, Dir: dir, Profile: string(ProfileGoZeroCompatible)}); err != nil {
		t.Fatal(err)
	}
	routesPath := filepath.Join(dir, "internal", "routes", "routes.go")
	data, err := os.ReadFile(routesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Path: \"/users/{id}\"") || strings.Contains(string(data), "Path: \"/users/:id\"") {
		t.Fatalf("generated routes do not normalize go-zero path parameters:\n%s", data)
	}
}

func TestGeneratedClientsPreserveFieldLocationsAndWireNames(t *testing.T) {
	t.Parallel()
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "CreateRequest", Fields: []IDLField{
				{Name: "ID", Type: "string", Tag: "path:\"id\""},
				{Name: "TenantID", Type: "string", Tag: "header:\"X-Tenant-ID\""},
				{Name: "Filter", Type: "string", Tag: "form:\"filter,optional\""},
				{Name: "DisplayName", Type: "string", Tag: "json:\"displayName\""},
			}},
			{Name: "Reply", Fields: []IDLField{{Name: "ID", Type: "string", Tag: "json:\"id\""}}},
		},
		Services: []IDLService{{Name: "users-api", Server: IDLServerAnnotation{Prefix: "/api/v1"}, Methods: []IDLMethod{{
			Name: "Create", Request: "CreateRequest", Response: "Reply", HTTPMethod: http.MethodPost, HTTPPath: "/users/:id",
		}}}},
	}

	tests := []struct {
		name    string
		source  string
		wants   []string
		rejects []string
	}{
		{name: "typescript", source: string(generateTypeScriptClient(doc, "")), wants: []string{"let path = \"/api/v1/users/{id}\"", "path.replace(\"{id}\"", "query.append(\"filter\"", "headers[\"X-Tenant-ID\"]", "JSON.stringify({ \"displayName\": req.displayName })"}, rejects: []string{"query.append(\"tenantID\"", "JSON.stringify(req)"}},
		{name: "javascript", source: string(generateJavaScriptClient(doc, "")), wants: []string{"let path = \"/api/v1/users/{id}\"", "path.replace(\"{id}\"", "query.append(\"filter\"", "headers[\"X-Tenant-ID\"]", "JSON.stringify({ \"displayName\": req.displayName })"}, rejects: []string{"query.append(\"tenantID\"", "JSON.stringify(req)"}},
		{name: "dart", source: string(generateDartClient(doc, "")), wants: []string{"var path = \"/api/v1/users/{id}\"", "path.replaceAll(\"{id}\"", "addQuery(\"filter\"", "headers[\"X-Tenant-ID\"]", "jsonEncode({\"displayName\": req.displayName})"}, rejects: []string{"jsonEncode(req.toJson())"}},
		{name: "java", source: string(generateJavaClient(doc, "")), wants: []string{"String path = \"/api/v1/users/{id}\"", "path.replace(\"{id}\"", "appendQuery(query, \"filter\"", ".header(\"X-Tenant-ID\"", "body.put(\"displayName\", req.displayName)"}, rejects: []string{"mapper.writeValueAsString(req)"}},
		{name: "kotlin", source: string(generateKotlinClient(doc, "")), wants: []string{"var path = \"/api/v1/users/{id}\"", "path.replace(\"{id}\"", "appendQuery(query, \"filter\"", ".header(\"X-Tenant-ID\"", "json.encodeToString(mapOf(\"displayName\" to req.displayName))"}, rejects: []string{"json.encodeToString(req)"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, want := range test.wants {
				if !strings.Contains(test.source, want) {
					t.Fatalf("generated %s client missing %q:\n%s", test.name, want, test.source)
				}
			}
			for _, reject := range test.rejects {
				if strings.Contains(test.source, reject) {
					t.Fatalf("generated %s client contains forbidden %q:\n%s", test.name, reject, test.source)
				}
			}
		})
	}

	spec := buildAPIOpenAPISpec(doc)
	paths := spec["paths"].(map[string]any)
	pathItem, ok := paths["/api/v1/users/{id}"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI paths = %#v, want /api/v1/users/{id}", paths)
	}
	operation := pathItem["post"].(map[string]any)
	parameters := operation["parameters"].([]map[string]any)
	wantLocations := map[string]string{"id": "path", "filter": "query", "X-Tenant-ID": "header"}
	for _, parameter := range parameters {
		name, _ := parameter["name"].(string)
		if want, ok := wantLocations[name]; ok {
			if parameter["in"] != want {
				t.Fatalf("parameter %s = %#v, want in=%s", name, parameter, want)
			}
			delete(wantLocations, name)
		}
	}
	if len(wantLocations) != 0 {
		t.Fatalf("OpenAPI missing parameters: %#v", wantLocations)
	}
	body := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	schema := body["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if len(properties) != 1 || properties["displayName"] == nil {
		t.Fatalf("body properties = %#v, want displayName only", properties)
	}
}

func TestGeneratedClientsCoverNoQueryAndExplicitBaseURL(t *testing.T) {
	t.Parallel()
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Request", Fields: []IDLField{{Name: "ID", Type: "string", Tag: `path:"id"`}}},
			{Name: "Reply", Fields: []IDLField{{Name: "OK", Type: "bool", Tag: `json:"ok"`}}},
		},
		Services: []IDLService{{Name: "users-api", Methods: []IDLMethod{{
			Name: "Get", Request: "Request", Response: "Reply", HTTPMethod: http.MethodGet, HTTPPath: "/users/:id",
		}}}},
	}
	for _, test := range []struct {
		name   string
		source string
		wants  []string
	}{
		{name: "javascript", source: string(generateJavaScriptClient(doc, "https://api.example/")), wants: []string{`constructor(baseURL = "https://api.example")`, "const url = this.baseURL + path;"}},
		{name: "dart", source: string(generateDartClient(doc, "https://api.example/")), wants: []string{`this.baseURL = "https://api.example"`, "final uri = Uri.parse('$baseURL$path');"}},
		{name: "java", source: string(generateJavaClient(doc, "https://api.example/")), wants: []string{`public APIClient() { this("https://api.example"); }`, "String url = baseURL + path;"}},
		{name: "kotlin", source: string(generateKotlinClient(doc, "https://api.example/")), wants: []string{`private val baseURL: String = "https://api.example"`, "val url = baseURL.trimEnd('/') + path"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, want := range test.wants {
				if !strings.Contains(test.source, want) {
					t.Fatalf("generated %s client missing %q:\n%s", test.name, want, test.source)
				}
			}
		})
	}
}

func TestAPIClientFieldClassificationBoundaries(t *testing.T) {
	t.Parallel()
	request := IDLMessage{Fields: []IDLField{
		{Name: "UserID", Type: "string"},
		{Name: "Page", Type: "int"},
		{Name: "Ignored", Type: "string", Tag: `json:"-"`},
		{Name: "DisplayName", Type: "string", Tag: `json:"display-name"`},
		{Name: "Alias", Type: "string", Tag: `query:",optional"`},
	}}
	fields := apiClientFields(IDLMethod{HTTPMethod: http.MethodGet, HTTPPath: "/users/{userID}"}, request)
	if len(fields) != 4 {
		t.Fatalf("fields = %#v, want ignored JSON field omitted", fields)
	}
	wants := []struct {
		property string
		wireName string
		location apiClientFieldLocation
	}{
		{property: "userID", wireName: "userID", location: apiClientFieldPath},
		{property: "page", wireName: "page", location: apiClientFieldQuery},
		{property: "displayName", wireName: "display-name", location: apiClientFieldBody},
		{property: "alias", wireName: "alias", location: apiClientFieldQuery},
	}
	for index, want := range wants {
		got := fields[index]
		if got.Property != want.property || got.WireName != want.wireName || got.Location != want.location {
			t.Fatalf("field[%d] = %#v, want %#v", index, got, want)
		}
	}
	if isAPIClientIdentifier("9invalid") || isAPIClientIdentifier("invalid-name") || isAPIClientIdentifier("") {
		t.Fatal("invalid client identifiers were accepted")
	}
	if fallback := apiClientFieldForPath(nil, "user_id"); fallback.Property != "userId" || fallback.WireName != "user_id" {
		t.Fatalf("path fallback = %#v", fallback)
	}
}

func TestAPIStatusExpressionBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code int
		want string
	}{
		{code: http.StatusOK, want: "http.StatusOK"},
		{code: http.StatusCreated, want: "http.StatusCreated"},
		{code: http.StatusAccepted, want: "http.StatusAccepted"},
		{code: http.StatusNoContent, want: "http.StatusNoContent"},
		{code: http.StatusPartialContent, want: "http.StatusContinue + 106"},
	}
	for _, test := range tests {
		if got := apiHTTPStatusExpression(test.code); got != test.want {
			t.Fatalf("apiHTTPStatusExpression(%d) = %q, want %q", test.code, got, test.want)
		}
	}
	if got := apiSuccessStatus(IDLMethod{Doc: map[string]string{"respcode": "invalid"}}); got != http.StatusOK {
		t.Fatalf("invalid response status = %d, want 200", got)
	}
}

func TestAPIContractMetadataBoundaryBehavior(t *testing.T) {
	t.Parallel()
	tick := string(rune(96))
	content := "syntax = \"v1\"\n" +
		"@server(\n  group: users\n  prefix: /api/v1\n)\n" +
		"service users-api {\n" +
		"  @server(\n    jwt: Auth\n    middleware: audit, trace\n  )\n" +
		"  @doc(\n    respCode: 202\n    responses: \"202-Accepted request<br>400-Invalid request\"\n  )\n" +
		"  @handler create\n  post /users/:id (Request) returns (Response)\n}\n" +
		"type Request {\n  ID string " + tick + "path:\"id\"" + tick + "\n}\n" +
		"type Response {\n  OK bool " + tick + "json:\"ok\"" + tick + "\n}\n"
	doc, err := ParseAPI(content)
	if err != nil {
		t.Fatalf("ParseAPI: %v", err)
	}
	service := doc.Services[0]
	if service.Server.Group != "users" || service.Server.Prefix != "/api/v1" || service.Server.JWT != "" || len(service.Server.Middleware) != 0 {
		t.Fatalf("service annotation = %#v, route metadata must not leak", service.Server)
	}
	method := service.Methods[0]
	if method.Route.Values["jwt"] != "Auth" || method.Route.Values["middleware"] != "audit, trace" {
		t.Fatalf("route annotation = %#v", method.Route)
	}
	if method.Doc["respcode"] != "202" || !strings.Contains(method.Doc["responses"], "Invalid request") {
		t.Fatalf("doc annotation = %#v", method.Doc)
	}
	formatted := string(FormatAPI(doc))
	for _, want := range []string{"syntax = \"v1\"", "group: users", "jwt: Auth", "middleware: audit, trace", "respcode: \"202\""} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted API missing %q:\n%s", want, formatted)
		}
	}

	invalid := []string{
		"@server(\n  group: users\n",
		"type Request {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\nservice api {\n  @doc(\n    respCode: 201\n",
		"type Request {\n  ID string " + tick + "json:\"id\"" + tick + "\n",
		"type Request {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\nservice api {\n  @handler get\n  get / (Request) returns (Request)\n",
	}
	for _, source := range invalid {
		if _, err := ParseAPI(source); err == nil {
			t.Fatalf("ParseAPI accepted unclosed source %q", source)
		}
	}
}

func TestLoadAPIImportSafetyAndLocalOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	owner := filepath.Join(dir, "root.api")
	if _, err := resolveAPIImport(owner, ""); err == nil {
		t.Fatal("empty API import was accepted")
	}
	if _, err := resolveAPIImport(owner, filepath.Join(dir, "abs.api")); err == nil {
		t.Fatal("absolute API import was accepted")
	}
	for _, imported := range []string{"../escape.api", "types.txt"} {
		if _, err := resolveAPIImport(owner, imported); err == nil {
			t.Fatalf("unsafe API import %q was accepted", imported)
		}
	}

	tick := string(rune(96))
	shared := filepath.Join(dir, "shared.api")
	if err := os.WriteFile(shared, []byte("import \"root.api\"\ntype Shared {\n  Value string "+tick+"json:\"value\""+tick+"\n}\ntype Local {\n  Old string "+tick+"json:\"old\""+tick+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(owner, []byte("import \"shared.api\"\ntype Local {\n  New string "+tick+"json:\"new\""+tick+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadAPI(owner)
	if err != nil {
		t.Fatalf("LoadAPI cycle: %v", err)
	}
	local := messageByName(doc, "Local")
	if len(doc.Messages) != 2 || len(local.Fields) != 1 || local.Fields[0].Name != "New" {
		t.Fatalf("merged messages = %#v, want local override and cycle dedupe", doc.Messages)
	}
}

func TestAPIFileConsumerDefaultOutputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tick := string(rune(96))
	apiPath := filepath.Join(dir, "catalog.api")
	api := "type Request {\n  ID string " + tick + "json:\"id\"" + tick + "\n}\n" +
		"type Response {\n  OK bool " + tick + "json:\"ok\"" + tick + "\n}\n" +
		"service catalog-api {\n  @handler list\n  get /catalog (Request) returns (Response)\n}\n"
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	formattedPath := filepath.Join(dir, "formatted.api")
	if _, err := FormatAPIFromFile(APIFormatOptions{APIFile: apiPath, Output: formattedPath}); err != nil {
		t.Fatalf("FormatAPIFromFile output: %v", err)
	}
	if _, err := os.Stat(formattedPath); err != nil {
		t.Fatal(err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{APIFile: apiPath, Write: true}); err != nil {
		t.Fatalf("FormatAPIFromFile write: %v", err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{Dir: dir, Declare: true}); err != nil {
		t.Fatalf("FormatAPIFromFile dir: %v", err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{Dir: dir, Output: filepath.Join(dir, "bad")}); err == nil {
		t.Fatal("format directory accepted an output path")
	}
	notDir := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{Dir: notDir}); err == nil {
		t.Fatal("format directory accepted a file")
	}

	docDir := filepath.Join(dir, "docs")
	clientDir := filepath.Join(dir, "clients")
	typesDir := filepath.Join(dir, "types")
	routesDir := filepath.Join(dir, "routes")
	for _, path := range []string{docDir, clientDir, typesDir, routesDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Dir: docDir}); err != nil {
		t.Fatalf("GenerateAPIDoc default: %v", err)
	}
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Dir: clientDir}); err != nil {
		t.Fatalf("GenerateAPIClient default: %v", err)
	}
	if err := GenerateAPITypes(APITypesOptions{APIFile: apiPath, Dir: typesDir}); err != nil {
		t.Fatalf("GenerateAPITypes default: %v", err)
	}
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: apiPath, Dir: routesDir}); err != nil {
		t.Fatalf("GenerateAPIRoutes default: %v", err)
	}
	for _, path := range []string{
		filepath.Join(docDir, "catalog.md"),
		filepath.Join(clientDir, "catalog_client.ts"),
		filepath.Join(typesDir, "types.go"),
		filepath.Join(routesDir, "catalog.routes.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("default output %s: %v", path, err)
		}
	}
}

func TestLoadAPIRejectsSymlinkAndMissingImport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.api")
	if err := os.WriteFile(outside, []byte("type Outside {\n  ID string\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.api")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	owner := filepath.Join(dir, "root.api")
	if err := os.WriteFile(owner, []byte("import \"linked.api\"\ntype Root {\n  ID string\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAPI(owner); err == nil {
		t.Fatal("LoadAPI followed a symlink import")
	}
	if err := os.WriteFile(owner, []byte("import \"missing.api\"\ntype Root {\n  ID string\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAPI(owner); err == nil || !strings.Contains(err.Error(), "read api file") {
		t.Fatalf("missing import error = %v", err)
	}
}

func TestAPIContractValidationRejectsUnsafeAndAmbiguousMetadata(t *testing.T) {
	t.Parallel()
	validTypes := []IDLMessage{
		{Name: "Request", Fields: []IDLField{{Name: "ID", Type: "string"}}},
		{Name: "Response", Fields: []IDLField{{Name: "OK", Type: "bool"}}},
	}
	method := func(name, handler, verb, path, request, response string) IDLMethod {
		return IDLMethod{Name: name, Handler: handler, HTTPMethod: verb, HTTPPath: path, Request: request, Response: response}
	}
	service := func(name string, methods ...IDLMethod) []IDLService {
		return []IDLService{{Name: name, Methods: methods}}
	}
	tests := []struct {
		name string
		doc  IDLDocument
		want string
	}{
		{
			name: "empty service",
			doc:  IDLDocument{Messages: validTypes, Services: service("", method("Get", "", "GET", "/", "", "Response"))},
			want: "service name is required",
		},
		{
			name: "duplicate normalized route",
			doc: IDLDocument{Messages: validTypes, Services: service("api",
				method("Colon", "colon", "GET", "/users/:id", "Request", "Response"),
				method("Brace", "brace", "GET", "/users/{id}", "Request", "Response"),
			)},
			want: "duplicate route GET /users/{id}",
		},
		{
			name: "incomplete and unknown contracts",
			doc: IDLDocument{Messages: validTypes, Services: service("api",
				method("Missing", "same", "", "", "Unknown", ""),
				method("Unknown", "same", "POST", "/unknown", "", "Unknown"),
			)},
			want: "route Missing is incomplete",
		},
		{
			name: "invalid response docs",
			doc: IDLDocument{Messages: validTypes, Services: service("api", IDLMethod{
				Name: "Create", HTTPMethod: "POST", HTTPPath: "/items/{bad-name}", Request: "Request", Response: "Response",
				Doc: map[string]string{"respcode": "99", "responses": "broken<br>700-Too high"},
			})},
			want: "invalid path parameter",
		},
		{
			name: "invalid service options",
			doc: IDLDocument{Messages: validTypes, Services: []IDLService{{
				Name: "api",
				Server: IDLServerAnnotation{JWT: "shared", Values: map[string]string{
					"timeout": "0s", "maxbytes": "10", "maxbodybytes": "20", "sse": "maybe", "signature": "shared", "priority": "high",
				}},
				Methods: []IDLMethod{method("Get", "", "GET", "/", "", "Response")},
			}}},
			want: "invalid route timeout",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAPI(test.doc)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAPI error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestAPIFieldTagValidationBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tag  string
	}{
		{name: "missing separator", tag: `json:"id"form:"id"`},
		{name: "invalid syntax", tag: `json:id`},
		{name: "invalid key", tag: `bad` + string(rune(127)) + `:"id"`},
		{name: "unterminated value", tag: `json:"id`},
		{name: "invalid quoted value", tag: `json:"\xZZ"`},
		{name: "conditional optional", tag: `json:"id,optional=other"`},
		{name: "empty default", tag: `json:"id,default="`},
		{name: "empty options", tag: `json:"id,options=[]"`},
		{name: "unbalanced options", tag: `json:"id,options=[a,b"`},
		{name: "invalid range shape", tag: `json:"id,range=1:2"`},
		{name: "empty range", tag: `json:"id,range=[:]"`},
		{name: "invalid left range", tag: `json:"id,range=[x:2]"`},
		{name: "invalid right range", tag: `json:"id,range=[1:x]"`},
		{name: "reversed range", tag: `json:"id,range=[2:1]"`},
		{name: "exclusive equal range", tag: `json:"id,range=(1:1]"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := validateAPIFieldTag("Request", IDLField{Name: "ID", Type: "string", Tag: test.tag})
			if len(issues) == 0 {
				t.Fatalf("tag %q was accepted", test.tag)
			}
		})
	}
	for _, valid := range []string{`json:"id,options=[a,b]"`, `json:"id,options=a|b"`, `json:"id,range=[:2)"`, `json:"id,range=(1:]"`} {
		if issues := validateAPIFieldTag("Request", IDLField{Name: "ID", Type: "string", Tag: valid}); len(issues) != 0 {
			t.Fatalf("valid tag %q issues = %v", valid, issues)
		}
	}
}

func TestAPIValidationReportsAllP0ContractIssues(t *testing.T) {
	t.Parallel()
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Nested", Fields: []IDLField{{Name: "Value", Type: "string"}}},
			{Name: "Request", Fields: []IDLField{
				{Name: "Duplicate", Type: "string"},
				{Name: "Duplicate", Type: "string"},
				{Name: "Unknown", Type: "Missing"},
				{Name: "Scalar", Type: "string", Inline: true},
				{Name: "List", Type: "[]Nested", Inline: true},
				{Name: "MissingInline", Type: "Missing", Inline: true},
			}},
			{Name: "Response", Fields: []IDLField{{Name: "OK", Type: "bool"}}},
		},
		Services: []IDLService{{Name: "api", Methods: []IDLMethod{
			{Name: "Broken", Handler: "same", Request: "Unknown", Doc: map[string]string{"respcode": "99", "responses": "broken<br>700-Too high"}},
			{Name: "AlsoBroken", Handler: "same", HTTPMethod: "GET", HTTPPath: "/items/{bad-name}", Response: "Unknown"},
		}}},
	}
	err := ValidateAPI(doc)
	if err == nil {
		t.Fatal("ValidateAPI accepted invalid contract")
	}
	for _, want := range []string{
		"duplicate field Request.Duplicate",
		"unknown field type Request.Unknown Missing",
		"inline field Request must reference a struct",
		"unknown inline field type Request Missing",
		"route Broken is incomplete",
		"duplicate handler Same",
		"references unknown request type Unknown",
		"references unknown response type Unknown",
		"invalid response status code \"99\"",
		"invalid response description \"broken\"",
		"invalid response status code \"700\"",
		"invalid path parameter \"bad-name\"",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q: %v", want, err)
		}
	}
}

func TestAPIServerRouteOptionValidationBranches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		server IDLServerAnnotation
		wants  []string
	}{
		{
			name: "invalid values",
			server: IDLServerAnnotation{JWT: "same", Values: map[string]string{
				"timeout": "bad", "maxbodybytes": "0", "sse": "bad", "signature": "same", "priority": "high",
			}},
			wants: []string{"invalid route timeout", "invalid route max body bytes", "invalid route sse", "reuses", "unsupported route priority"},
		},
		{
			name: "invalid signature",
			server: IDLServerAnnotation{Values: map[string]string{
				"timeout": "1s", "maxbytes": "1024", "sse": "true", "signature": "bad profile!",
			}},
			wants: []string{"invalid signature profile"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := validateAPIServerRouteOptions(IDLService{Name: "api", Server: test.server})
			joined := strings.Join(issues, "; ")
			for _, want := range test.wants {
				if !strings.Contains(joined, want) {
					t.Fatalf("issues %q missing %q", joined, want)
				}
			}
		})
	}
	if issues := validateAPIServerRouteOptions(IDLService{}); len(issues) != 0 {
		t.Fatalf("empty server options issues = %v", issues)
	}
}

func TestAPIContractHelperBoundaryBehavior(t *testing.T) {
	t.Parallel()
	if _, ok := apiFieldWireName(IDLField{Name: "ID", Tag: `query:"id,options=[a"`}, apiTagQuery); ok {
		t.Fatal("apiFieldWireName accepted an unbalanced modifier")
	}
	if _, err := ParseAPI("type Broken {\n  not a valid field !\n}\n"); err == nil || (!strings.Contains(err.Error(), "invalid field") && !strings.Contains(err.Error(), "parse api type")) {
		t.Fatalf("invalid field error = %v", err)
	}
	if _, err := ParseAPI("type Reply {\n  OK bool\n}\nservice api {\n  invalid route\n}\n"); err == nil || !strings.Contains(err.Error(), "invalid route") {
		t.Fatalf("invalid route error = %v", err)
	}
	if _, err := ParseAPI("type Huge {\n" + strings.Repeat("x", 70*1024) + "\n}\n"); err == nil || !strings.Contains(err.Error(), "scan api") {
		t.Fatalf("scanner limit error = %v", err)
	}

	service := IDLService{Name: "users", Server: IDLServerAnnotation{Prefix: "api/v1"}}
	if got := openAPIServicePath(service, "users"); got != "/api/v1/users" {
		t.Fatalf("relative service path = %q", got)
	}
	service.Server.Prefix = "/api/v1/"
	if got := openAPIServicePath(service, "/api/v1/users"); got != "/api/v1/users" {
		t.Fatalf("already-prefixed service path = %q", got)
	}
	service.Server.Prefix = "/"
	if got := openAPIServicePath(service, "/users"); got != "/users" {
		t.Fatalf("root-prefixed service path = %q", got)
	}
	if content := jsonContentRef("", IDLMessage{}, nil); len(content) != 0 {
		t.Fatalf("empty response content = %#v", content)
	}
	if names := openAPIPathParamNames("/users/{unterminated"); len(names) != 0 {
		t.Fatalf("unterminated path names = %v", names)
	}
	if example := openAPIMessageExample(IDLMessage{}, nil); example != nil {
		t.Fatalf("empty message example = %#v", example)
	}
	tags := openAPITags(IDLDocument{Services: []IDLService{{Name: "users"}, {Name: "users"}}})
	if len(tags) != 1 {
		t.Fatalf("deduplicated OpenAPI tags = %#v", tags)
	}
}

func TestAPIFormattingPreservesInlineAndEmptyAnnotations(t *testing.T) {
	t.Parallel()
	doc := IDLDocument{
		Syntax:  "v1",
		Imports: []string{"common.api"},
		Messages: []IDLMessage{{Name: "Request", Fields: []IDLField{
			{Name: "Common", Type: "Common", Inline: true, Tag: `json:",inline"`},
			{Name: "Name", Type: "string"},
		}}},
		Services: []IDLService{
			{Name: "plain-api", Methods: []IDLMethod{{Name: "Get", HTTPMethod: "GET", HTTPPath: "/", Response: "Request"}}},
			{Name: "annotated-api", Server: IDLServerAnnotation{Values: map[string]string{"custom": "value"}}, Methods: []IDLMethod{{
				Name: "Post", HTTPMethod: "POST", HTTPPath: "/", Request: "Request", Response: "Request", Doc: map[string]string{"summary": "create value"},
			}}},
		},
	}
	formatted := string(FormatAPI(doc))
	for _, want := range []string{
		"syntax = \"v1\"",
		"import \"common.api\"",
		"Common `json:\",inline\"`",
		"Name string",
		"@server(custom: value)",
		"@doc(summary: \"create value\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted API missing %q:\n%s", want, formatted)
		}
	}
}

func TestOpenAPIResponseFallbacksAndProjectedBody(t *testing.T) {
	t.Parallel()
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Request", Fields: []IDLField{
				{Name: "ID", Type: "string", Tag: `path:"id"`},
				{Name: "Name", Type: "string", Tag: `json:"name,optional"`},
			}},
			{Name: "Reply", Fields: []IDLField{{Name: "OK", Type: "bool", Tag: `json:"ok"`}}},
		},
		Services: []IDLService{{Name: "items", Methods: []IDLMethod{{
			Name: "Create", HTTPMethod: "POST", HTTPPath: "/items/:id", Request: "Request", Response: "Reply",
			Doc: map[string]string{"respcode": "299", "responses": "299-<br>400-Bad request"},
		}}}},
	}
	spec := buildAPIOpenAPISpec(doc)
	operation := spec["paths"].(map[string]any)["/items/{id}"].(map[string]any)["post"].(map[string]any)
	responses := operation["responses"].(map[string]any)
	if responses["299"].(map[string]any)["description"] != "OK" || responses["400"].(map[string]any)["description"] != "Bad request" {
		t.Fatalf("OpenAPI responses = %#v", responses)
	}
	body := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	schema := body["schema"].(map[string]any)
	if _, ok := schema["required"]; ok {
		t.Fatalf("optional projected body has required fields: %#v", schema)
	}
	if _, err := generateAPIOpenAPI(doc); err != nil {
		t.Fatalf("generateAPIOpenAPI: %v", err)
	}
	if _, err := generateAPIOpenAPIYAML(doc); err != nil {
		t.Fatalf("generateAPIOpenAPIYAML: %v", err)
	}
}
