package generator

import (
	"strings"
	"testing"
)

func TestAPIDiffRenderingContractBoundaries(t *testing.T) {
	base := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Shared", Fields: []IDLField{{Name: "ID", Type: "string"}}},
			{Name: "Removed", Fields: []IDLField{{Name: "Value", Type: "string"}}},
		},
		Services: []IDLService{{Name: "Catalog", Methods: []IDLMethod{
			{Name: "Get", HTTPMethod: "GET", HTTPPath: "/items/{id}", Handler: "getItem", Request: "Shared", Response: "Shared"},
			{Name: "Remove", HTTPMethod: "DELETE", HTTPPath: "/items/{id}", Response: "Shared"},
		}}},
	}
	target := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Shared", Fields: []IDLField{{Name: "ID", Type: "int64"}}},
			{Name: "Added", Fields: []IDLField{{Name: "Enabled", Type: "bool"}}},
		},
		Services: []IDLService{{Name: "Catalog", Methods: []IDLMethod{
			{Name: "Get", HTTPMethod: "GET", HTTPPath: "/items/{id}", Handler: "getItemV2", Request: "Shared", Response: "Added"},
			{Name: "Create", HTTPMethod: "POST", HTTPPath: "/items", Request: "Added", Response: "Shared"},
		}}},
	}

	diff := DiffAPI(base, target)
	if len(diff.AddedRoutes) != 1 || len(diff.RemovedRoutes) != 1 || len(diff.ChangedRoutes) != 1 ||
		len(diff.AddedTypes) != 1 || len(diff.RemovedTypes) != 1 || len(diff.ChangedTypes) != 1 {
		t.Fatalf("diff = %+v, want every route and type change family", diff)
	}
	text := string(formatAPIDiffText(diff))
	markdown := string(formatAPIDiffMarkdown(diff))
	for _, marker := range []string{"Added routes", "Removed routes", "Changed routes", "Added types", "Removed types", "Changed types"} {
		if !strings.Contains(text, marker) || !strings.Contains(markdown, marker) {
			t.Fatalf("diff output missing %q:\ntext=%s\nmarkdown=%s", marker, text, markdown)
		}
	}
	if got := string(formatAPIDiffText(APIDiffResult{})); got != "No API changes\n" {
		t.Fatalf("empty text diff = %q", got)
	}
	if got := string(formatAPIDiffMarkdown(APIDiffResult{})); !strings.Contains(got, "No API changes.") {
		t.Fatalf("empty markdown diff = %q", got)
	}
	if apiEmptyDash("") != "-" || apiEmptyDash("Request") != "Request" ||
		apiDiffListPrefix("Removed routes") != "-" || apiDiffListPrefix("Added routes") != "+" {
		t.Fatal("API diff rendering helpers violated their stable output contract")
	}
	signature := apiRouteSignature(APIRouteInfo{
		Service: "Catalog", Method: "GET", Path: "/items", Handler: "listItems",
		Request: "Request", Response: "Reply", Group: "admin", Prefix: "/v2",
		JWT: "auth", Middlewares: []string{"trace", "audit"},
	})
	for _, marker := range []string{"Catalog", "handler=listItems", "request=Request", "response=Reply", "group=admin", "prefix=/v2", "jwt=auth", "middlewares=trace,audit"} {
		if !strings.Contains(signature, marker) {
			t.Fatalf("route signature %q missing %q", signature, marker)
		}
	}
}

func TestOpenAPIConversionContractBoundaries(t *testing.T) {
	components := map[string]openAPISpecSchema{
		"User": {
			Type: "object",
			Properties: map[string]openAPISpecSchema{
				"id":     {Type: "integer", Format: "int32"},
				"score":  {Type: "number", Format: "float"},
				"active": {Type: "boolean"},
				"tags":   {Type: "array"},
			},
		},
	}
	message := openAPISchemaToMessage("Alias", openAPISpecSchema{Ref: "#/components/schemas/User"}, components)
	if message.Name != "User" || len(message.Fields) != 4 {
		t.Fatalf("schema message = %+v", message)
	}
	if openAPISchemaType(openAPISpecSchema{Type: "array"}, components) != "[]string" ||
		openAPISchemaType(openAPISpecSchema{Type: "array", Items: &openAPISpecSchema{Ref: "#/components/schemas/User"}}, components) != "[]User" ||
		openAPISchemaType(openAPISpecSchema{Type: "integer"}, components) != "int64" ||
		openAPISchemaType(openAPISpecSchema{Type: "number"}, components) != "float64" ||
		openAPISchemaType(openAPISpecSchema{Type: "object"}, components) != "string" {
		t.Fatal("OpenAPI schema type mapping drifted")
	}

	pathParameter := openAPIParameter{Name: "id", In: "path", Required: true, Schema: openAPISpecSchema{Type: "string"}}
	queryParameter := openAPIParameter{Name: "filter", In: "query", Schema: openAPISpecSchema{Type: "string"}}
	headerParameter := openAPIParameter{Name: "tenant", In: "header", Schema: openAPISpecSchema{Type: "string"}}
	operation := openAPIOperation{
		Parameters: []openAPIParameter{pathParameter, queryParameter, headerParameter},
		RequestBody: &openAPIRequestBody{Content: map[string]openAPIMediaType{
			"application/json": {Schema: openAPISpecSchema{Type: "object", Properties: map[string]openAPISpecSchema{
				"id":   {Type: "integer"},
				"name": {Type: "string"},
			}}},
		}},
		Responses: map[string]openAPIResponse{
			"201": {Content: map[string]openAPIMediaType{"application/*+json": {Schema: openAPISpecSchema{Ref: "#/components/schemas/User"}}}},
		},
	}
	if got := openAPIRequestName("CreateUser", operation); got != "CreateUserRequest" {
		t.Fatalf("request name = %q", got)
	}
	if got := openAPIResponseName("CreateUser", operation); got != "User" {
		t.Fatalf("response name = %q", got)
	}
	request, ok := openAPIRequestMessage("CreateUserRequest", operation, components)
	if !ok || len(request.Fields) != 4 {
		t.Fatalf("merged request = %+v, %t", request, ok)
	}
	if _, ok := openAPIRequestMessage("", operation, components); ok {
		t.Fatal("empty request name should not produce a message")
	}
	if _, ok := openAPIRequestMessage("Empty", openAPIOperation{}, components); ok {
		t.Fatal("empty operation should not produce a request message")
	}

	params := openAPIResolveParameters([]openAPIParameter{
		{Ref: "#/components/parameters/ID"},
		{Ref: "#/components/parameters/Missing"},
		queryParameter,
	}, map[string]openAPIParameter{"ID": pathParameter})
	if len(params) != 2 || params[0].Name != "id" || params[1].Name != "filter" {
		t.Fatalf("resolved parameters = %+v", params)
	}
	joined := openAPIJoinParameters([]openAPIParameter{pathParameter, queryParameter}, []openAPIParameter{
		{Name: "filter", In: "query", Required: true},
		headerParameter,
	})
	if len(joined) != 3 || !joined[1].Required || joined[2].Name != "tenant" {
		t.Fatalf("joined parameters = %+v", joined)
	}
	if got := openAPIJoinParameters(nil, params); len(got) != len(params) {
		t.Fatalf("join nil base = %+v", got)
	}
	if got := openAPIJoinParameters(params, nil); len(got) != len(params) {
		t.Fatalf("join nil override = %+v", got)
	}

	fallbackSchema, ok := openAPIMediaSchema(map[string]openAPIMediaType{
		"text/plain": {Schema: openAPISpecSchema{Type: "string"}},
	})
	if !ok || fallbackSchema.Type != "string" {
		t.Fatalf("fallback media schema = %+v, %t", fallbackSchema, ok)
	}
	if _, ok := openAPIMediaSchema(nil); ok {
		t.Fatal("empty media content should not return a schema")
	}
}

func TestGeneratedAPIClientsPreserveRequestContracts(t *testing.T) {
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "Child", Fields: []IDLField{{Name: "Value", Type: "string"}}},
			{Name: "LookupRequest", Fields: []IDLField{
				{Name: "ID", Type: "string"},
				{Name: "Tags", Type: "[]string"},
				{Name: "Active", Type: "bool"},
				{Name: "Count", Type: "int64"},
				{Name: "Ratio", Type: "float32"},
				{Name: "Child", Type: "Child"},
			}},
			{Name: "Reply", Fields: []IDLField{{Name: "OK", Type: "bool"}}},
		},
		Services: []IDLService{{Name: "Catalog", Methods: []IDLMethod{
			{Name: "Lookup", Request: "LookupRequest", Response: "Reply", HTTPMethod: "GET", HTTPPath: "/items/{id}"},
			{Name: "Create", Request: "LookupRequest", Response: "Reply", HTTPMethod: "POST", HTTPPath: "/items"},
			{Name: "Ping", Response: "Reply", HTTPMethod: "GET", HTTPPath: "/ping"},
		}}},
	}

	tests := []struct {
		name    string
		source  string
		markers []string
	}{
		{
			name:   "typescript",
			source: string(generateTypeScriptClient(doc, "https://api.example/")),
			markers: []string{
				`constructor(private readonly baseURL: string = "https://api.example")`,
				`path.replace("{id}"`,
				`new URLSearchParams()`,
				`Array.isArray(req.tags)`,
				`init.body = JSON.stringify(req)`,
				`async ping(): Promise<Reply>`,
			},
		},
		{
			name:   "javascript",
			source: string(generateJavaScriptClient(doc, "")),
			markers: []string{
				`constructor(baseURL = '')`,
				`path.replace("{id}"`,
				`new URLSearchParams()`,
				`Array.isArray(req.tags)`,
				`init.body = JSON.stringify(req)`,
				`async ping()`,
			},
		},
		{
			name:   "dart",
			source: string(generateDartClient(doc, "https://api.example/")),
			markers: []string{
				`List<String>? tags`,
				`Child.fromJson`,
				`path.replaceAll("{id}"`,
				`final query = <String, List<String>>{}`,
				`body: jsonEncode(req.toJson())`,
				`Future<Reply> ping()`,
			},
		},
		{
			name:   "java",
			source: string(generateJavaClient(doc, "https://api.example/")),
			markers: []string{
				`java.util.List<String> tags`,
				`path.replace("{id}"`,
				`StringBuilder query`,
				`BodyPublishers.ofString(mapper.writeValueAsString(req))`,
				`public Reply ping()`,
			},
		},
		{
			name:   "kotlin",
			source: string(generateKotlinClient(doc, "https://api.example/")),
			markers: []string{
				`val tags: List<String>?`,
				`path.replace("{id}"`,
				`val query = StringBuilder()`,
				`BodyPublishers.ofString(json.encodeToString(req))`,
				`fun ping(): Reply`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, marker := range test.markers {
				if !strings.Contains(test.source, marker) {
					t.Fatalf("%s client missing %q:\n%s", test.name, marker, test.source)
				}
			}
		})
	}

	if clientFieldNameForParam(IDLMessage{}, "user_id") != "userId" {
		t.Fatal("missing client path field should fall back to lower camel case")
	}
	if dartType("uint32") != "int" || dartType("float64") != "double" ||
		javaBoxedType("uint32") != "Integer" || javaBoxedType("uint64") != "Long" ||
		javaBoxedType("float64") != "Double" || kotlinType("uint32") != "Int" ||
		kotlinType("uint64") != "Long" || kotlinType("float64") != "Double" ||
		typeScriptType("[]float64") != "number[]" {
		t.Fatal("generated client scalar type mapping drifted")
	}
}
