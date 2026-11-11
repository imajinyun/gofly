package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const apiTypeSystemFixture = `type (
  JSONValue any
  RawJSON = any
  UserID int64
  Tags []string
  Meta struct {
    Source string ` + "`json:\"source\"`" + `
  }
  MetaAlias = struct {
    Detail any ` + "`json:\"detail\"`" + `
  }
  MetaPtr = *Meta
)

type CreateRequest {
  ID      string    ` + "`json:\"id\"`" + `
  A, B    string    ` + "`json:\"-\"`" + `
  Payload JSONValue ` + "`json:\"payload\"`" + `
  Extra   any       ` + "`json:\"extra,omitempty\"`" + `
  Attrs   struct {
    Source string ` + "`json:\"source\"`" + `
  } ` + "`json:\"attrs\"`" + `
  Items []struct {
    Name string ` + "`json:\"name\"`" + `
  } ` + "`json:\"items\"`" + `
  Index map[string]struct {
    Count int ` + "`json:\"count\"`" + `
  } ` + "`json:\"index\"`" + `
}

type CreateResponse {
  ID UserID ` + "`json:\"id\"`" + `
}

service Demo {
  post /items (CreateRequest) returns (CreateResponse)
  post /meta (Meta) returns (MetaAlias)
}
`

func TestAPITypeSystemValidationAndRoutes(t *testing.T) {
	doc, err := ParseAPIFile(apiTypeSystemFixture, "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile() error = %v", err)
	}
	if err := ValidateAPI(doc); err != nil {
		t.Fatalf("ValidateAPI() error = %v", err)
	}
	if len(doc.Aliases) != 5 {
		t.Fatalf("aliases = %#v, want 5", doc.Aliases)
	}
	if len(doc.Messages) != 4 {
		t.Fatalf("messages = %#v, want 4", doc.Messages)
	}

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "alias cycle",
			content: "type A = B\ntype B = A\n",
			want:    "type alias cycle: A -> B -> A",
		},
		{
			name:    "defined type cycle",
			content: "type A B\ntype B A\n",
			want:    "recursive type cycle: A -> B -> A",
		},
		{
			name:    "unknown alias target",
			content: "type A Missing\n",
			want:    "unknown type alias target Missing",
		},
		{
			name:    "duplicate alias and struct",
			content: "type User struct {\n  ID string\n}\ntype User string\n",
			want:    "duplicate type User",
		},
		{
			name:    "route alias to any",
			content: "type Value = any\nservice Demo {\n  get /value returns (Value)\n}\n",
			want:    "route GetValue response type Value is not a struct",
		},
		{
			name:    "route defined type to any",
			content: "type Value any\nservice Demo {\n  get /value returns (Value)\n}\n",
			want:    "route GetValue response type Value is not a struct",
		},
		{
			name:    "route unknown any",
			content: "service Demo {\n  get /value returns (any)\n}\n",
			want:    "route GetValue references unknown response type any",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc, err := ParseAPIFile(test.content, "types.api")
			if err != nil {
				t.Fatalf("ParseAPIFile() error = %v", err)
			}
			err = ValidateAPI(doc)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAPI() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAPITypeSystemFormatIdempotent(t *testing.T) {
	doc, err := ParseAPIFile(apiTypeSystemFixture, "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile() error = %v", err)
	}
	first := FormatAPI(doc)
	reparsed, err := ParseAPIFile(string(first), "types.api")
	if err != nil {
		t.Fatalf("reparse formatted api: %v\n%s", err, first)
	}
	second := FormatAPI(reparsed)
	if string(first) != string(second) {
		t.Fatalf("format is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	for _, marker := range []string{"type JSONValue any", "type RawJSON = any", "type MetaPtr = *Meta", "A string", "B string", "map[string]struct {", "[]struct {"} {
		if !strings.Contains(string(first), marker) {
			t.Fatalf("formatted api missing %q:\n%s", marker, first)
		}
	}
}

func TestAPITypeSystemOpenAPI(t *testing.T) {
	doc, err := ParseAPIFile(apiTypeSystemFixture, "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile() error = %v", err)
	}
	spec := buildAPIOpenAPISpec(doc)
	components := spec["components"].(map[string]any)["schemas"].(map[string]any)
	createRequest := components["CreateRequest"].(map[string]any)
	properties := createRequest["properties"].(map[string]any)

	if got, ok := properties["extra"].(map[string]any); !ok || len(got) != 0 {
		t.Fatalf("extra schema = %#v, want free-form {}", properties["extra"])
	}
	if got, ok := properties["payload"].(map[string]any); !ok || got["$ref"] != "#/components/schemas/JSONValue" {
		t.Fatalf("payload schema = %#v, want $ref JSONValue", properties["payload"])
	}
	jsonValue := components["JSONValue"].(map[string]any)
	if len(jsonValue) != 0 {
		t.Fatalf("JSONValue component = %#v, want {}", jsonValue)
	}
	index := properties["index"].(map[string]any)
	if index["type"] != "object" {
		t.Fatalf("index schema = %#v, want object", index)
	}
	additional, ok := index["additionalProperties"].(map[string]any)
	if !ok || additional["type"] != "object" {
		t.Fatalf("index additionalProperties = %#v, want nested object", index["additionalProperties"])
	}

	imported, err := parseOpenAPIToIDL([]byte(`{
  "openapi": "3.0.3",
  "info": {"title": "free-form"},
  "paths": {"/values": {"get": {"operationId": "getValues", "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Value"}}}}}}}},
  "components": {"schemas": {"Value": {"type": "object", "properties": {"extra": {"type": "object"}}}}}
}`), "free-form")
	if err != nil {
		t.Fatalf("parseOpenAPIToIDL() error = %v", err)
	}
	foundAny := false
	for _, msg := range imported.Messages {
		for _, field := range msg.Fields {
			if field.Type == "any" {
				foundAny = true
			}
		}
	}
	if !foundAny {
		t.Fatalf("imported free-form object did not map to any: %#v", imported.Messages)
	}
}

func TestAPITypeSystemClientMappings(t *testing.T) {
	doc, err := ParseAPIFile(apiTypeSystemFixture, "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile() error = %v", err)
	}
	doc = apiDocumentWithResolvedInlineFields(doc)

	typeScript := string(generateTypeScriptClient(doc, "https://api.example.com"))
	for _, marker := range []string{
		"export type JSONValue = unknown;",
		"export type UserID = number;",
		"payload?: JSONValue;",
		"extra?: unknown;",
		"attrs?: { source?: string };",
		"items?: { name?: string }[];",
		"index?: Record<string, { count?: number }>;",
	} {
		if !strings.Contains(typeScript, marker) {
			t.Fatalf("typescript client missing %q:\n%s", marker, typeScript)
		}
	}

	dart := string(generateDartClient(doc, "https://api.example.com"))
	for _, marker := range []string{
		"typedef JSONValue = Object;",
		"typedef UserID = int;",
		"class CreateRequestAttrs {",
		"final CreateRequestAttrs? attrs;",
		"final List<CreateRequestItems>? items;",
	} {
		if !strings.Contains(dart, marker) {
			t.Fatalf("dart client missing %q:\n%s", marker, dart)
		}
	}

	java := string(generateJavaClient(doc, "https://api.example.com"))
	for _, marker := range []string{
		"public Object extra;",
		"public static class CreateRequestAttrs {",
		"public CreateRequestAttrs attrs;",
		"public java.util.List<CreateRequestItems> items;",
	} {
		if !strings.Contains(java, marker) {
			t.Fatalf("java client missing %q:\n%s", marker, java)
		}
	}

	kotlin := string(generateKotlinClient(doc, "https://api.example.com"))
	for _, marker := range []string{
		"typealias JSONValue = Any",
		"typealias UserID = Long",
		"data class CreateRequestAttrs(",
		"val attrs: CreateRequestAttrs? = null",
	} {
		if !strings.Contains(kotlin, marker) {
			t.Fatalf("kotlin client missing %q:\n%s", marker, kotlin)
		}
	}

	javascript := string(generateJavaScriptClient(doc, "https://api.example.com"))
	if !strings.Contains(javascript, "export class APIClient") {
		t.Fatalf("javascript client missing APIClient:\n%s", javascript)
	}

	timeDoc := IDLDocument{
		Aliases:  []IDLTypeAlias{{Name: "Created", Target: "time.Time", Alias: true}},
		Messages: []IDLMessage{{Name: "User", Fields: []IDLField{{Name: "At", Type: "Created", Tag: `json:"at"`}}}},
	}
	for language, want := range map[string]string{
		"typescript": "export type Created = string;",
		"dart":       "typedef Created = String;",
		"java":       "public String at;",
		"kotlin":     "typealias Created = String",
	} {
		var generated string
		switch language {
		case "typescript":
			generated = string(generateTypeScriptClient(timeDoc, ""))
		case "dart":
			generated = string(generateDartClient(timeDoc, ""))
		case "java":
			generated = string(generateJavaClient(timeDoc, ""))
		case "kotlin":
			generated = string(generateKotlinClient(timeDoc, ""))
		}
		if !strings.Contains(generated, want) {
			t.Fatalf("%s client missing %q:\n%s", language, want, generated)
		}
	}
}

func TestAPITypeSystemFixtures(t *testing.T) {
	root := repositoryRoot(t)
	base := filepath.Join(root, "testdata", "api", "types")
	data, err := os.ReadFile(filepath.Join(base, "expectations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expectations struct {
		Fixtures []struct {
			File     string   `json:"file"`
			Messages []string `json:"messages"`
			Aliases  []string `json:"aliases"`
		} `json:"fixtures"`
		Invalid []struct {
			File  string `json:"file"`
			Error string `json:"error"`
		} `json:"invalid"`
	}
	if err := json.Unmarshal(data, &expectations); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range expectations.Fixtures {
		t.Run(fixture.File, func(t *testing.T) {
			path := filepath.Join(base, fixture.File)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := ParseAPIFile(string(content), path)
			if err != nil {
				t.Fatalf("ParseAPIFile() error = %v", err)
			}
			if err := ValidateAPI(doc); err != nil {
				t.Fatalf("ValidateAPI() error = %v", err)
			}
			messageNames := make([]string, 0, len(doc.Messages))
			for _, msg := range doc.Messages {
				messageNames = append(messageNames, exportName(msg.Name))
			}
			aliasNames := make([]string, 0, len(doc.Aliases))
			for _, alias := range doc.Aliases {
				aliasNames = append(aliasNames, exportName(alias.Name))
			}
			if strings.Join(messageNames, ",") != strings.Join(fixture.Messages, ",") {
				t.Fatalf("messages = %v, want %v", messageNames, fixture.Messages)
			}
			if strings.Join(aliasNames, ",") != strings.Join(fixture.Aliases, ",") {
				t.Fatalf("aliases = %v, want %v", aliasNames, fixture.Aliases)
			}
		})
	}
	for _, invalid := range expectations.Invalid {
		t.Run(invalid.File, func(t *testing.T) {
			path := filepath.Join(base, invalid.File)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseAPIFile(string(content), path)
			if err == nil || !strings.Contains(err.Error(), invalid.Error) {
				t.Fatalf("ParseAPIFile() error = %v, want %q", err, invalid.Error)
			}
		})
	}
}

func TestAPITypeSystemImportedAliases(t *testing.T) {
	dir := t.TempDir()
	common := "type UserID int64\n\ntype Base struct {\n  ID string `json:\"id\"`\n}\n"
	root := "import \"common.api\"\n\ntype User {\n  ID UserID `json:\"id\"`\n}\n\nservice Demo {\n  get /users (User) returns (Base)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "common.api"), []byte(common), 0o644); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(dir, "root.api")
	if err := os.WriteFile(rootPath, []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadAPI(rootPath)
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if err := ValidateAPI(doc); err != nil {
		t.Fatalf("ValidateAPI() error = %v", err)
	}
	if len(doc.Aliases) != 1 || doc.Aliases[0].Name != "UserID" {
		t.Fatalf("aliases = %#v, want imported UserID", doc.Aliases)
	}

	localOverride := "import \"common.api\"\n\ntype UserID string\n\ntype User {\n  ID UserID `json:\"id\"`\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "override.api"), []byte(localOverride), 0o644); err != nil {
		t.Fatal(err)
	}
	overridden, err := LoadAPI(filepath.Join(dir, "override.api"))
	if err != nil {
		t.Fatalf("LoadAPI(override) error = %v", err)
	}
	if len(overridden.Aliases) != 1 || overridden.Aliases[0].Target != "string" {
		t.Fatalf("overridden aliases = %#v, want local UserID string", overridden.Aliases)
	}
}

func TestAPITypeSystemGeneratedGoCompiles(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "types.api")
	content := "type Created = time.Time\n\ntype Meta struct {\n  Source string `json:\"source\"`\n}\n\ntype MetaPtr = *Meta\n\ntype User {\n  ID string `json:\"id\"`\n  At Created `json:\"at\"`\n  Meta struct {\n    Source string `json:\"source\"`\n  } `json:\"meta\"`\n}\n"
	if err := os.WriteFile(apiPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "types.go")
	if err := GenerateAPITypes(APITypesOptions{APIFile: apiPath, Output: output, Package: "types"}); err != nil {
		t.Fatalf("GenerateAPITypes() error = %v", err)
	}
	generated, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"import (", "\"time\"", "type Created = time.Time", "type MetaPtr = *Meta", "type User struct {", "Created `json:\"at\"`", "Meta struct {"} {
		if !strings.Contains(string(generated), marker) {
			t.Fatalf("generated types missing %q:\n%s", marker, generated)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/types\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGoCommand(t, dir, 2*time.Minute, "test", "./...")
}

func TestAPITypeSystemDiffAndMerge(t *testing.T) {
	base := IDLDocument{Kind: "api", Aliases: []IDLTypeAlias{{Name: "UserID", Target: "int64", Alias: false}}}
	target := IDLDocument{Kind: "api", Aliases: []IDLTypeAlias{{Name: "UserID", Target: "string", Alias: false}, {Name: "Raw", Target: "any", Alias: true}}}
	diff := DiffAPI(base, target)
	if len(diff.AddedAliases) != 1 || diff.AddedAliases[0].Name != "Raw" {
		t.Fatalf("added aliases = %#v, want Raw", diff.AddedAliases)
	}
	if len(diff.ChangedAliases) != 1 || diff.ChangedAliases[0].Name != "UserID" {
		t.Fatalf("changed aliases = %#v, want UserID", diff.ChangedAliases)
	}
	if apiDiffEmpty(diff) {
		t.Fatal("DiffAPI() reported empty for alias changes")
	}
	text := string(formatAPIDiffText(diff))
	if !strings.Contains(text, "Added aliases") || !strings.Contains(text, "Changed aliases") {
		t.Fatalf("diff text missing alias sections:\n%s", text)
	}

	messages, aliases := mergeAPIDeclarations(
		[]IDLMessage{{Name: "User", Fields: []IDLField{{Name: "ID", Type: "string"}}}},
		[]IDLTypeAlias{{Name: "UserID", Target: "int64"}},
		[]IDLMessage{{Name: "User", Fields: []IDLField{{Name: "Name", Type: "string"}}}},
		[]IDLTypeAlias{{Name: "UserID", Target: "string", Alias: true}},
	)
	if len(messages) != 1 || messages[0].Fields[0].Name != "Name" {
		t.Fatalf("messages = %#v, want local User override", messages)
	}
	if len(aliases) != 1 || aliases[0].Target != "string" || !aliases[0].Alias {
		t.Fatalf("aliases = %#v, want local UserID alias override", aliases)
	}

	breaking := reportIDLChanges(
		IDLDocument{Kind: "api", Services: []IDLService{{Name: "Demo", Methods: []IDLMethod{{Name: "Get", Response: "UserID"}}}}, Aliases: []IDLTypeAlias{{Name: "UserID", Target: "int64"}}},
		IDLDocument{Kind: "api", Services: []IDLService{{Name: "Demo", Methods: []IDLMethod{{Name: "Get", Response: "UserID"}}}}, Aliases: []IDLTypeAlias{{Name: "UserID", Target: "string"}}},
	)
	if !breaking.HasBreaking() {
		t.Fatalf("alias target change used by a route should be breaking: %#v", breaking)
	}
}

func TestAPITypeSystemOpenAPIImportFreeFormRoundTrip(t *testing.T) {
	source := `{
  "openapi": "3.0.3",
  "info": {"title": "roundtrip"},
  "paths": {"/items": {"get": {"operationId": "getItems", "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object", "properties": {"extra": {"type": "object"}, "labels": {"type": "object", "additionalProperties": {"type": "string"}}}}}}}}}}}
}`
	doc, err := parseOpenAPIToIDL([]byte(source), "roundtrip")
	if err != nil {
		t.Fatalf("parseOpenAPIToIDL() error = %v", err)
	}
	encoded, err := json.Marshal(doc.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"Type":"any"`) || !strings.Contains(string(encoded), `"Type":"map[string]string"`) {
		t.Fatalf("imported messages = %s, want any and map[string]string", encoded)
	}
}
