package generator

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseAPIGoctlGroupedDeclarations(t *testing.T) {
	doc, err := LoadAPI(goctlSemanticFixturePath(t))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	for _, name := range []string{"Audit", "Item", "CreateRequest", "CreateResponse"} {
		if messageByName(doc, name).Name == "" {
			t.Fatalf("loaded messages = %#v, missing %s", doc.Messages, name)
		}
	}
	if got := doc.Imports; !reflect.DeepEqual(got, []string{"types/common.api"}) {
		t.Fatalf("imports = %#v, want grouped import", got)
	}
}

func TestParseAPIGoctlCompositeFieldTypes(t *testing.T) {
	doc, err := LoadAPI(goctlSemanticFixturePath(t))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if got := apiTestFieldType(messageByName(doc, "CreateRequest"), "Item"); got != "*Item" {
		t.Fatalf("CreateRequest.Item type = %q, want *Item", got)
	}
	if got := apiTestFieldType(messageByName(doc, "Item"), "Labels"); got != "map[string][]string" {
		t.Fatalf("Item.Labels type = %q, want map[string][]string", got)
	}
}

func TestAPITypeRefRoundTripAndValidation(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "named", raw: "Item", want: "Item"},
		{name: "pointer", raw: "*Item", want: "*Item"},
		{name: "slice pointer", raw: "[]*Item", want: "[]*Item"},
		{name: "nested map", raw: "map[string][]*Item", want: "map[string][]*Item"},
		{name: "invalid map key", raw: "map[Item]string", wantErr: "unsupported map key"},
		{name: "map missing opening bracket", raw: "map string", wantErr: "opening bracket"},
		{name: "map missing key", raw: "map[]string", wantErr: "type name is required"},
		{name: "missing bracket", raw: "map[string", wantErr: "closing bracket"},
		{name: "map missing value", raw: "map[string]", wantErr: "type name is required"},
		{name: "pointer missing element", raw: "*", wantErr: "type name is required"},
		{name: "slice missing element", raw: "[]", wantErr: "type name is required"},
		{name: "unexpected suffix", raw: "Item value", wantErr: "unexpected suffix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref, err := parseAPITypeRef(test.raw)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseAPITypeRef(%q) error = %v, want %q", test.raw, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAPITypeRef(%q): %v", test.raw, err)
			}
			if got := ref.String(); got != test.want {
				t.Fatalf("parseAPITypeRef(%q).String() = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestAPITypeRefDefensiveFallbacks(t *testing.T) {
	if got := (apiTypeRef{Kind: 255}).String(); got != "" {
		t.Fatalf("unknown type kind String() = %q, want empty", got)
	}
	if got := apiTypeRefString(nil); got != "" {
		t.Fatalf("apiTypeRefString(nil) = %q, want empty", got)
	}
	if got := apiTypeRefElement(apiTypeRef{Kind: apiTypeKindSlice}); got != (apiTypeRef{}) {
		t.Fatalf("apiTypeRefElement without element = %#v, want zero value", got)
	}
	if got := apiTypeScriptTypeRef(apiTypeRef{Kind: apiTypeKindNamed, Name: "any"}); got != "unknown" {
		t.Fatalf("TypeScript any = %q, want unknown", got)
	}
	if got := apiDartTypeRef(apiTypeRef{Kind: apiTypeKindNamed, Name: "any"}); got != "Object" {
		t.Fatalf("Dart any = %q, want Object", got)
	}
	if got := apiKotlinTypeRef(apiTypeRef{Kind: apiTypeKindNamed, Name: "any"}); got != "Any" {
		t.Fatalf("Kotlin any = %q, want Any", got)
	}
}

func TestAPITypeRefConsumerMappings(t *testing.T) {
	names := map[string]struct{}{"Item": {}}
	tests := []struct {
		name       string
		raw        string
		goType     string
		typeScript string
		dart       string
		java       string
		kotlin     string
	}{
		{name: "pointer", raw: "*Item", goType: "*Item", typeScript: "Item", dart: "Item", java: "Item", kotlin: "Item"},
		{name: "slice pointer", raw: "[]*Item", goType: "[]*Item", typeScript: "Item[]", dart: "List<Item>", java: "java.util.List<Item>", kotlin: "List<Item>"},
		{name: "nested map", raw: "map[string][]string", goType: "map[string][]string", typeScript: "Record<string, string[]>", dart: "Map<String, List<String>>", java: "java.util.Map<String, java.util.List<String>>", kotlin: "Map<String, List<String>>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := apiGoType(test.raw); got != test.goType {
				t.Fatalf("apiGoType(%q) = %q, want %q", test.raw, got, test.goType)
			}
			if got := typeScriptType(test.raw); got != test.typeScript {
				t.Fatalf("typeScriptType(%q) = %q, want %q", test.raw, got, test.typeScript)
			}
			if got := dartType(test.raw); got != test.dart {
				t.Fatalf("dartType(%q) = %q, want %q", test.raw, got, test.dart)
			}
			if got := javaType(test.raw); got != test.java {
				t.Fatalf("javaType(%q) = %q, want %q", test.raw, got, test.java)
			}
			if got := kotlinType(test.raw); got != test.kotlin {
				t.Fatalf("kotlinType(%q) = %q, want %q", test.raw, got, test.kotlin)
			}
			if schema := openAPISchema(test.raw, names); len(schema) == 0 {
				t.Fatalf("openAPISchema(%q) is empty", test.raw)
			}
		})
	}
}

func TestParseAPIRouteLocalServerAnnotation(t *testing.T) {
	doc, err := ParseAPI(`type Request {
  ID string
}
type Response {
  OK bool
}
@server(group: users prefix: /api/v1)
service users-api {
  @server(handler: create group: admin)
  post /users (Request) returns (Response)
  @handler health
  get /health returns (Response)
}`)
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if len(doc.Services) != 1 {
		t.Fatalf("services = %#v, want one", doc.Services)
	}
	service := doc.Services[0]
	if service.Server.Group != "users" || service.Server.Prefix != "/api/v1" {
		t.Fatalf("service annotation = %#v, want users /api/v1", service.Server)
	}
	if len(service.Methods) != 2 {
		t.Fatalf("methods = %#v, want two", service.Methods)
	}
	if got := service.Methods[0].Handler; got != "create" || service.Methods[0].Route.Group != "admin" {
		t.Fatalf("create route = %#v, want route-local handler/group", service.Methods[0])
	}
	if got := service.Methods[1].Handler; got != "health" {
		t.Fatalf("health handler = %q, route annotation leaked", got)
	}
}

func TestEffectiveAPIRouteAnnotation(t *testing.T) {
	service := IDLServerAnnotation{
		Group:      "catalog",
		Prefix:     "/api/v1",
		JWT:        "Auth",
		Middleware: []string{"Trace"},
		Values:     map[string]string{"timeout": "2s"},
	}

	inherited := effectiveAPIRouteAnnotation(service, IDLRouteAnnotation{})
	inherited.Values["timeout"] = "changed"
	if service.Values["timeout"] != "2s" {
		t.Fatalf("effective annotation mutated service values: %#v", service.Values)
	}

	overridden := effectiveAPIRouteAnnotation(service, IDLRouteAnnotation{
		Group: "admin",
		Values: map[string]string{
			"prefix":     "/internal",
			"jwt":        "AdminAuth",
			"middleware": "Audit,RateLimit",
			"timeout":    "5s",
		},
	})
	if overridden.Group != "admin" || overridden.Prefix != "/internal" || overridden.JWT != "AdminAuth" {
		t.Fatalf("effective annotation = %#v, want route overrides", overridden)
	}
	if !reflect.DeepEqual(overridden.Middleware, []string{"Audit", "RateLimit"}) || overridden.Values["timeout"] != "5s" {
		t.Fatalf("effective middleware/values = %#v/%#v", overridden.Middleware, overridden.Values)
	}

	plural := effectiveAPIRouteAnnotation(IDLServerAnnotation{}, IDLRouteAnnotation{
		Values: map[string]string{"middlewares": "Auth, Trace"},
	})
	if !reflect.DeepEqual(plural.Middleware, []string{"Auth", "Trace"}) {
		t.Fatalf("plural middleware = %#v", plural.Middleware)
	}
}

func TestFormatAPIRouteMetadataAndEmptyRoute(t *testing.T) {
	doc := IDLDocument{
		Kind: "api",
		Services: []IDLService{{
			Name: "health-api",
			Methods: []IDLMethod{{
				Name:       "Ready",
				HTTPMethod: "GET",
				HTTPPath:   "/ready",
				Route: IDLRouteAnnotation{
					Handler: "ready",
					Group:   "ops",
					Values:  map[string]string{"timeout": "1s"},
				},
			}},
		}},
	}

	formatted := string(FormatAPI(doc))
	for _, want := range []string{
		"@server(handler: ready group: ops timeout: 1s)",
		"get /ready",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("FormatAPI() missing %q:\n%s", want, formatted)
		}
	}
	if _, err := ParseAPI(formatted); err != nil {
		t.Fatalf("ParseAPI(FormatAPI()) = %v\n%s", err, formatted)
	}
}

func TestValidateAPICompositeTypeFailures(t *testing.T) {
	types := map[string]IDLMessage{"Request": {Name: "Request"}}
	tests := []struct {
		name      string
		field     IDLField
		wantIssue string
	}{
		{name: "malformed type", field: IDLField{Name: "Items", Type: "map[string"}, wantIssue: "has invalid type"},
		{name: "inline map", field: IDLField{Name: "Labels", Type: "map[string]string", Inline: true}, wantIssue: "must reference a struct"},
		{name: "unknown nested value", field: IDLField{Name: "Items", Type: "map[string][]Missing"}, wantIssue: "unknown field type Request.Items Missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := validateAPIMessage(IDLMessage{Name: "Request", Fields: []IDLField{test.field}}, types)
			if len(issues) != 1 || !strings.Contains(issues[0], test.wantIssue) {
				t.Fatalf("validateAPIMessage() = %#v, want %q", issues, test.wantIssue)
			}
		})
	}
}

func TestParseAPICompositeSyntaxErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "grouped alias", content: "type (\nUserID = string\n)", wantErr: "unsupported api type alias UserID"},
		{name: "invalid grouped declaration", content: "type (\n!\n)", wantErr: "invalid grouped type"},
		{name: "unclosed import block", content: "import (\n\"types.api\"", wantErr: "import block is not closed"},
		{name: "unclosed type block", content: "type (", wantErr: "type block is not closed"},
		{name: "field with invalid name", content: "type Request {\n1Name string\n}", wantErr: "invalid field"},
		{name: "field with invalid type", content: "type Request {\nName []\n}", wantErr: "type name is required"},
		{name: "field with invalid tag", content: "type Request {\nName string `json:\"name\"` trailing\n}", wantErr: "invalid field tag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAPI(test.content)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ParseAPI() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestParseAPIFieldTokenBoundaries(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "empty", line: ""},
		{name: "no separator", line: "!"},
		{name: "invalid first identifier character", line: "1Name string"},
		{name: "invalid later identifier character", line: "Na-me string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field, ok, err := parseAPIField(test.line)
			if err != nil || ok || field != (IDLField{}) {
				t.Fatalf("parseAPIField(%q) = (%#v, %t, %v), want zero, false, nil", test.line, field, ok, err)
			}
		})
	}
	if isAPIIdentifier("") {
		t.Fatal("empty identifier must be rejected")
	}
}

func TestParseAPIRoutesWithoutRequestOrResponse(t *testing.T) {
	doc, err := LoadAPI(goctlSemanticFixturePath(t))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	deleteMethod := apiTestMethod(doc, "DeleteItem")
	if deleteMethod.Request != "CreateRequest" || deleteMethod.Response != "" {
		t.Fatalf("delete method = %#v, want request and no response", deleteMethod)
	}
	healthMethod := apiTestMethod(doc, "Health")
	if healthMethod.Request != "" || healthMethod.Response != "CreateResponse" {
		t.Fatalf("health method = %#v, want no request and response", healthMethod)
	}
}

func TestFormatAPIGoctlSemanticRoundTrip(t *testing.T) {
	doc, err := LoadAPI(goctlSemanticFixturePath(t))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	formatted := string(FormatAPI(doc))
	for _, want := range []string{
		`import "types/common.api"`,
		`Labels map[string][]string`,
		`Item *Item`,
		`@server(group: catalog middleware: Auth prefix: /api/v1)`,
		`delete /items/:id (CreateRequest)`,
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted API missing %q:\n%s", want, formatted)
		}
	}
	reparsed, err := ParseAPI(formatted)
	if err != nil {
		t.Fatalf("ParseAPI(formatted): %v\n%s", err, formatted)
	}
	if got := apiTestFieldType(messageByName(reparsed, "Item"), "Labels"); got != "map[string][]string" {
		t.Fatalf("round-trip Item.Labels = %q", got)
	}
}

func TestGenerateAPISemanticFixtureResponseLessContracts(t *testing.T) {
	doc, err := LoadAPI(goctlSemanticFixturePath(t))
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	spec := buildAPIOpenAPISpec(doc)
	paths := spec["paths"].(map[string]any)
	deleteOperation := paths["/api/v1/items/{id}"].(map[string]any)["delete"].(map[string]any)
	responses := deleteOperation["responses"].(map[string]any)
	noContent := responses["204"].(map[string]any)
	if _, ok := noContent["content"]; ok {
		t.Fatalf("response-less OpenAPI response = %#v, want no content", noContent)
	}

	clients := map[string]string{
		"typescript": string(generateTypeScriptClient(doc, "")),
		"javascript": string(generateJavaScriptClient(doc, "")),
		"dart":       string(generateDartClient(doc, "")),
		"java":       string(generateJavaClient(doc, "")),
		"kotlin":     string(generateKotlinClient(doc, "")),
	}
	for name, want := range map[string]string{
		"typescript": "async deleteItem(req: CreateRequest): Promise<void>",
		"javascript": "async deleteItem(req)",
		"dart":       "Future<void> deleteItem(CreateRequest req)",
		"java":       "public void deleteItem(CreateRequest req)",
		"kotlin":     "fun deleteItem(req: CreateRequest): Unit",
	} {
		if !strings.Contains(clients[name], want) {
			t.Fatalf("%s client missing %q:\n%s", name, want, clients[name])
		}
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/catalog\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateRESTFromAPI(APIOptions{APIFile: goctlSemanticFixturePath(t), Dir: root, Profile: string(ProfileGoZeroCompatible)}); err != nil {
		t.Fatalf("GenerateRESTFromAPI: %v", err)
	}
	handler, err := os.ReadFile(filepath.Join(root, "internal", "api", "http", "v1", "catalog", "deleteitem.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"err := catalogapp.NewDeleteItemLogic", "ctx.Response.WriteHeader(http.StatusNoContent)"} {
		if !strings.Contains(string(handler), want) {
			t.Fatalf("response-less handler missing %q:\n%s", want, handler)
		}
	}
	route := restRouteForTest(t, root, "http.MethodDelete", "/items/{id}")
	if route == "" {
		t.Fatal("generated response-less route is missing")
	}
}

func restRouteForTest(t *testing.T, root, methodMarker, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "internal", "routes", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if strings.Contains(source, methodMarker) && strings.Contains(source, `Path: "`+path+`"`) {
		return source
	}
	return ""
}

func TestParseAPIRejectsUnsupportedTypeAlias(t *testing.T) {
	_, err := ParseAPI("type UserID = string\n")
	if err == nil || !strings.Contains(err.Error(), "unsupported api type alias UserID") {
		t.Fatalf("alias error = %v, want stable unsupported alias error", err)
	}
}

func goctlSemanticFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "testdata", "goctl-api-semantic", "contract.api")
}

func apiTestFieldType(message IDLMessage, name string) string {
	for _, field := range message.Fields {
		if field.Name == name {
			return field.Type
		}
	}
	return ""
}

func apiTestMethod(doc IDLDocument, name string) IDLMethod {
	for _, service := range doc.Services {
		for _, method := range service.Methods {
			if method.Name == name {
				return method
			}
		}
	}
	return IDLMethod{}
}
