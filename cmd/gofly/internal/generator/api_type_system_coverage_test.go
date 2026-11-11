package generator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/core/storage"
)

func TestAPITypeASTKeywordAndTagHelpers(t *testing.T) {
	keywordCases := []struct {
		mask string
		want bool
	}{
		{mask: "type Foo", want: true},
		{mask: "type(", want: true},
		{mask: "type", want: true},
		{mask: "typewriter", want: false},
		{mask: "typed Foo", want: false},
		{mask: "", want: false},
	}
	for _, test := range keywordCases {
		if got := apiTypeKeywordAtStart(test.mask); got != test.want {
			t.Fatalf("apiTypeKeywordAtStart(%q) = %t, want %t", test.mask, got, test.want)
		}
	}
	if !apiTypeGroupStart("type (") || apiTypeGroupStart("type Foo") {
		t.Fatal("apiTypeGroupStart did not distinguish grouped declarations")
	}

	rawType, rawTag, err := splitAPIFieldTypeAndTag("string `json:\"name\"`")
	if err != nil || rawType != "string" || rawTag != `json:"name"` {
		t.Fatalf("splitAPIFieldTypeAndTag() = (%q, %q, %v)", rawType, rawTag, err)
	}
	if _, _, err := splitAPIFieldTypeAndTag("string `json:\"name\"` trailing"); err == nil {
		t.Fatal("splitAPIFieldTypeAndTag accepted trailing text")
	}
}

func TestAPITypeASTTypeExpressionBranches(t *testing.T) {
	content := `type (
  MapValue map[string]*User
  Parenthesized ([]string)
  EmptyInterface interface{}
  Selector time.Time
)

type User {
  ID string
}
`
	doc, err := ParseAPIFile(content, "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile() error = %v", err)
	}
	if len(doc.Aliases) != 4 {
		t.Fatalf("aliases = %#v, want 4", doc.Aliases)
	}

	invalid := []struct {
		name    string
		content string
		want    string
	}{
		{name: "fixed array", content: "type Fixed [4]byte\n", want: "fixed-size array"},
		{name: "nested invalid field", content: "type User {\n  Meta struct {\n    Bad func()\n  }\n}\n", want: "unsupported Go type in .api: func"},
		{name: "unsupported map key", content: "type User {\n  Index map[float64]struct{ Count int }\n}\n", want: "unsupported map key type"},
		{name: "alias to anonymous slice struct", content: "type Users = []struct{ ID string }\n", want: "anonymous struct aliases"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAPIFile(test.content, "types.api")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseAPIFile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAPITypeSystemValidationBranches(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "alias invalid target", content: "type Bad map[float64]string\n", want: "has invalid target"},
		{name: "inline alias to scalar", content: "type Value = string\ntype User {\n  Value\n}\n", want: "inline field User must reference a struct"},
		{name: "unknown inline", content: "type User {\n  Missing\n}\n", want: "unknown inline field type User Missing"},
		{name: "unknown field", content: "type User {\n  ID Missing\n}\n", want: "unknown field type User.ID Missing"},
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

	scope := apiTypeScope{
		messages: map[string]IDLMessage{"User": {Name: "User"}},
		aliases: map[string]IDLTypeAlias{
			"Alias": {Name: "Alias", Target: "User"},
			"Value": {Name: "Value", Target: "any"},
		},
	}
	if _, known, ok := scope.resolveStruct("Alias", nil); !ok || !known {
		t.Fatalf("resolveStruct(Alias) = known %t ok %t, want struct", known, ok)
	}
	if _, known, ok := scope.resolveStruct("Value", nil); ok || !known {
		t.Fatalf("resolveStruct(Value) = known %t ok %t, want known non-struct", known, ok)
	}
	if _, known, ok := scope.resolveStruct("Missing", nil); ok || known {
		t.Fatalf("resolveStruct(Missing) = known %t ok %t, want unknown", known, ok)
	}
}

func TestAPITypeSystemDeclarationValidationBranches(t *testing.T) {
	tests := []struct {
		name string
		doc  IDLDocument
		want string
	}{
		{
			name: "unknown field",
			doc:  IDLDocument{Messages: []IDLMessage{{Name: "User", Fields: []IDLField{{Name: "ID", Type: "Missing"}}}}},
			want: `can not find declaration "Missing" in context`,
		},
		{
			name: "unknown alias target",
			doc:  IDLDocument{Aliases: []IDLTypeAlias{{Name: "Alias", Target: "Missing"}}},
			want: `can not find declaration "Missing" in context`,
		},
		{
			name: "unknown route type",
			doc: IDLDocument{Services: []IDLService{{Name: "Demo", Methods: []IDLMethod{{
				Name: "Get", Request: "Missing", HTTPMethod: "GET", HTTPPath: "/demo",
			}}}}},
			want: `can not find declaration "Missing" in context`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAPITypeDeclarations(test.doc)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateAPITypeDeclarations() error = %v, want %q", err, test.want)
			}
		})
	}
	doc := IDLDocument{Messages: []IDLMessage{{Name: "User", Fields: []IDLField{{Name: "Meta", Nested: &IDLMessage{Fields: []IDLField{{Name: "ID", Type: "string"}}}}}}}}
	if err := validateAPITypeDeclarations(doc); err != nil {
		t.Fatalf("validateAPITypeDeclarations(nested) error = %v", err)
	}
}

func TestAPITypeSystemGoAndOpenAPIBranches(t *testing.T) {
	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "User", Fields: []IDLField{
				{Name: "Created", Type: "time.Time"},
				{Name: "Meta", Nested: &IDLMessage{Fields: []IDLField{{Name: "Source", Type: "string", Tag: `json:"source"`}}}, Tag: `json:"meta"`},
				{Name: "Items", Nested: &IDLMessage{Fields: []IDLField{{Name: "Name", Type: "string"}}}, NestedSlice: true, Tag: `json:"items"`},
				{Name: "Index", Nested: &IDLMessage{Fields: []IDLField{{Name: "Count", Type: "int"}}}, NestedMapKey: "string", Tag: `json:"index"`},
				{Name: "Audit", Type: "*Audit", Inline: true, Tag: `json:"audit"`},
			}},
		},
		Aliases: []IDLTypeAlias{{Name: "Created", Target: "time.Time", Alias: true}},
	}
	if !apiMessageUsesTime(doc.Messages[0]) {
		t.Fatal("apiMessageUsesTime(nested time.Time) = false, want true")
	}
	if got := apiGoImports(doc); len(got) != 1 || got[0] != "time" {
		t.Fatalf("apiGoImports() = %v, want [time]", got)
	}

	var goTypes bytes.Buffer
	writeAPIMessage(&goTypes, doc.Messages[0])
	for _, marker := range []string{"Meta struct {", "Items []struct {", "Index map[string]struct {", "*Audit `json:\"audit\"`"} {
		if !strings.Contains(goTypes.String(), marker) {
			t.Fatalf("generated Go missing %q:\n%s", marker, goTypes.String())
		}
	}

	spec := buildAPIOpenAPISpec(doc)
	components := spec["components"].(map[string]any)["schemas"].(map[string]any)
	user := components["User"].(map[string]any)
	props := user["properties"].(map[string]any)
	if _, ok := props["meta"].(map[string]any)["properties"]; !ok {
		t.Fatalf("meta schema = %#v, want nested properties", props["meta"])
	}
	if _, ok := props["items"].(map[string]any)["items"]; !ok {
		t.Fatalf("items schema = %#v, want array items", props["items"])
	}
	if _, ok := props["index"].(map[string]any)["additionalProperties"]; !ok {
		t.Fatalf("index schema = %#v, want additionalProperties", props["index"])
	}
	example := openAPIMessageExample(doc.Messages[0], openAPIMessageNames(doc))
	if _, ok := example["items"].([]any); !ok {
		t.Fatalf("nested slice example = %#v, want array", example["items"])
	}
	if _, ok := example["meta"].(map[string]any); !ok {
		t.Fatalf("nested struct example = %#v, want object", example["meta"])
	}

	markdown := string(generateAPIMarkdown(IDLDocument{Messages: doc.Messages, Aliases: doc.Aliases}))
	for _, marker := range []string{"## Aliases", "struct{...}", "[]struct{...}", "map[string]struct{...}"} {
		if !strings.Contains(markdown, marker) {
			t.Fatalf("markdown missing %q:\n%s", marker, markdown)
		}
	}

	diff := APIDiffResult{AddedAliases: []IDLTypeAlias{{Name: "Raw", Target: "any"}}, ChangedAliases: []APIAliasDiffChange{{Name: "UserID"}}}
	if got := string(formatAPIDiffMarkdown(diff)); !strings.Contains(got, "Added aliases") || !strings.Contains(got, "Changed aliases") {
		t.Fatalf("diff markdown missing alias sections:\n%s", got)
	}
}

func TestAPITypeSystemBreakingAndEmbeddedBranches(t *testing.T) {
	nested := apiFieldTypeSignature(IDLField{
		Name:         "Index",
		Nested:       &IDLMessage{Fields: []IDLField{{Name: "Count", Type: "int"}}},
		NestedMapKey: "string",
	})
	if !strings.Contains(nested, "map[string]struct{") {
		t.Fatalf("nested map signature = %q", nested)
	}
	slice := apiFieldTypeSignature(IDLField{
		Name:        "Items",
		Nested:      &IDLMessage{Fields: []IDLField{{Name: "Name", Type: "string"}}},
		NestedSlice: true,
	})
	if !strings.HasPrefix(slice, "[]struct{") {
		t.Fatalf("nested slice signature = %q", slice)
	}

	doc, err := ParseAPIFile("type User {\n  time.Time\n}\n", "types.api")
	if err != nil {
		t.Fatalf("ParseAPIFile(time.Time embed) error = %v", err)
	}
	if len(doc.Messages) != 1 || len(doc.Messages[0].Fields) != 1 || !doc.Messages[0].Fields[0].Inline || doc.Messages[0].Fields[0].Name != "Time" {
		t.Fatalf("embedded time.Time = %#v", doc.Messages)
	}
	if _, err := ParseAPIFile("type User {\n  *json.RawMessage\n}\n", "types.api"); err == nil || !strings.Contains(err.Error(), "unsupported embedded field json.RawMessage") {
		t.Fatalf("embedded selector error = %v", err)
	}
	if _, err := ParseAPIFile("type Value Box[int]\n", "types.api"); err == nil || !strings.Contains(err.Error(), "generics are not supported") {
		t.Fatalf("generic instantiation error = %v", err)
	}

	diff := APIDiffResult{
		RemovedAliases: []IDLTypeAlias{{Name: "Old", Target: "string"}},
		AddedAliases:   []IDLTypeAlias{{Name: "Raw", Target: "any"}},
		ChangedAliases: []APIAliasDiffChange{{Name: "UserID"}},
	}
	markdown := string(formatAPIDiffMarkdown(diff))
	for _, marker := range []string{"Removed aliases", "Added aliases", "Changed aliases"} {
		if !strings.Contains(markdown, marker) {
			t.Fatalf("diff markdown missing %q:\n%s", marker, markdown)
		}
	}
}

func TestAPITypeSystemSignatureAndFieldHelperBranches(t *testing.T) {
	if got := apiFieldSignature(IDLField{Name: "ID", Type: "string"}); got != "ID:string" {
		t.Fatalf("plain field signature = %q", got)
	}
	if got := apiFieldSignature(IDLField{Name: "Meta", Nested: &IDLMessage{Fields: []IDLField{{Name: "Source", Type: "string"}}}}); !strings.HasPrefix(got, "Meta:struct{") {
		t.Fatalf("nested field signature = %q", got)
	}
	if got := apiMessageSignature(IDLMessage{Alias: true, Fields: []IDLField{{Name: "ID", Type: "string"}}}); !strings.HasPrefix(got, "alias;") {
		t.Fatalf("alias message signature = %q", got)
	}

	field, ok, err := parseAPIField("Name string `json:\"name\"`")
	if err != nil || !ok || field.Tag != `json:"name"` {
		t.Fatalf("parseAPIField(tagged) = (%#v, %t, %v)", field, ok, err)
	}
	if _, _, err := parseAPIField("Name []"); err == nil {
		t.Fatal("parseAPIField accepted an empty slice type")
	}
	if _, ok, err := parseAPIField(""); err != nil || ok {
		t.Fatalf("parseAPIField(empty) = (%t, %v), want no field", ok, err)
	}
}

func TestAPITypeSystemMergeAndBreakingBranches(t *testing.T) {
	messages, aliases := mergeAPIDeclarations(
		[]IDLMessage{{Name: "Shared", Fields: []IDLField{{Name: "ID", Type: "string"}}}},
		[]IDLTypeAlias{{Name: "LocalAlias", Target: "string"}},
		[]IDLMessage{{Name: "LocalAlias", Fields: []IDLField{{Name: "Name", Type: "string"}}}},
		[]IDLTypeAlias{{Name: "Shared", Target: "any", Alias: true}},
	)
	if len(messages) != 1 || messages[0].Name != "LocalAlias" {
		t.Fatalf("messages = %#v, want LocalAlias message", messages)
	}
	if len(aliases) != 1 || aliases[0].Name != "Shared" || !aliases[0].Alias {
		t.Fatalf("aliases = %#v, want Shared alias", aliases)
	}

	if _, issues := apiBuildTypeScope(IDLDocument{Aliases: []IDLTypeAlias{{Name: "X", Target: "string"}, {Name: "X", Target: "int"}}}); len(issues) != 1 || !strings.Contains(issues[0], "duplicate type X") {
		t.Fatalf("duplicate alias issues = %v", issues)
	}

	report := reportIDLChanges(
		IDLDocument{Kind: "api", Aliases: []IDLTypeAlias{{Name: "Removed", Target: "string"}}},
		IDLDocument{Kind: "api", Aliases: []IDLTypeAlias{{Name: "Added", Target: "string"}}},
	)
	if !report.HasBreaking() {
		t.Fatalf("removed alias should be breaking: %#v", report)
	}
	foundAdded := false
	for _, change := range report.Changes {
		if change.Subject == "alias Added" && change.Severity == SeverityInfo {
			foundAdded = true
		}
	}
	if !foundAdded {
		t.Fatalf("added alias info change missing: %#v", report.Changes)
	}

	field, ok, err := parseAPIField("*Audit `json:\"audit\"`")
	if err != nil || !ok || !field.Inline || field.Name != "Audit" || field.Tag != `json:"audit"` {
		t.Fatalf("parseAPIField(inline) = (%#v, %t, %v)", field, ok, err)
	}
}

func TestGeneratorHelperCoverageBranches(t *testing.T) {
	if got := grpcAliasLiteral(nil); got != "nil" {
		t.Fatalf("grpcAliasLiteral(nil) = %q", got)
	}
	if got := grpcAliasLiteral(map[string]string{"b": "2", "a": "1"}); got != `map[string]string{"a":"1","b":"2",}` {
		t.Fatalf("grpcAliasLiteral(map) = %q", got)
	}

	table := SQLTable{
		Name:        "users",
		CachePrefix: "cache",
		Columns: []SQLColumn{
			{Name: "id", Type: "int64", PrimaryKey: true},
			{Name: "name", Type: "varchar(64)"},
		},
	}
	template, err := renderModelEntityTemplate("package {{.Package}}\n\ntype {{.Type}} struct {\n{{.Fields}}}\n", table, "User", modelStyleGORM, "model")
	if err != nil {
		t.Fatalf("renderModelEntityTemplate() error = %v", err)
	}
	if !strings.Contains(string(template), "type User struct {") {
		t.Fatalf("rendered template = %s", template)
	}

	dir := t.TempDir()
	if err := writeRepoFile(dir, table, "repo", "example.com/model", modelStyleSQL, false, storage.DialectMySQL); err != nil {
		t.Fatalf("writeRepoFile() error = %v", err)
	}
	if err := writeGORMRepoFile(dir, table, "example.com/model", false); err != nil {
		t.Fatalf("writeGORMRepoFile() error = %v", err)
	}

	completerFile := filepath.Join(dir, "handler.go")
	if err := os.WriteFile(completerFile, []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	completer := NewHandlerCompleter(completerFile, "s", "demo", nil)
	count, err := completer.appendMethods([]Method{
		{Name: "WithComment", Comment: "comment", Body: "return nil"},
		{Name: "WithSignature", Signature: "func (s *Service) WithSignature() {}", Body: "return nil"},
		{Name: "Todo"},
	})
	if err != nil || count != 3 {
		t.Fatalf("appendMethods() = (%d, %v), want 3", count, err)
	}
	data, err := os.ReadFile(completerFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"WithComment", "WithSignature", "// TODO: implement"} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("completer output missing %q:\n%s", marker, data)
		}
	}
}

func TestGeneratedFileRollbackHelpers(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "nested", "file.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeGeneratedFileUnder(root, filepath.Join("nested", "file.txt")); err != nil {
		t.Fatalf("removeGeneratedFileUnder() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("removed file still exists: %v", err)
	}
	if err := removeGeneratedFileUnder("", "file.txt"); err == nil {
		t.Fatal("removeGeneratedFileUnder accepted an empty root")
	}

	for _, name := range []string{"first.txt", "second.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := rollbackMiddlewarePresetFiles(root, []string{"missing.txt", "first.txt", "second.txt"}); err != nil {
		t.Fatalf("rollbackMiddlewarePresetFiles() error = %v", err)
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("rollback left %s: %v", name, err)
		}
	}
}

func TestRESTServiceAndGatewayRequestlessBranches(t *testing.T) {
	svc := IDLService{
		Name: "Demo",
		Methods: []IDLMethod{{
			Name: "Ping", Response: "Reply", HTTPMethod: "GET", HTTPPath: "/ping",
		}},
	}
	var service bytes.Buffer
	writeRESTService(&service, svc)
	if !strings.Contains(service.String(), "Ping(ctx context.Context) (*Reply, error)") {
		t.Fatalf("request-less service method missing:\n%s", service.String())
	}
	var gateway bytes.Buffer
	writeRESTGatewayMethod(&gateway, svc.Methods[0], "DemoGateway", "pb")
	if !strings.Contains(gateway.String(), "func (g *DemoGateway) Ping(ctx context.Context) (*Reply, error)") {
		t.Fatalf("request-less gateway method missing:\n%s", gateway.String())
	}
	var route bytes.Buffer
	writeRESTRoute(&route, svc.Methods[0])
	if !strings.Contains(route.String(), "resp, err := impl.Ping(ctx.Request.Context())") {
		t.Fatalf("request-less route handler missing:\n%s", route.String())
	}
}

func TestBreakingRemovalBranches(t *testing.T) {
	report := reportIDLChanges(
		IDLDocument{
			Kind:     "api",
			Messages: []IDLMessage{{Name: "Gone"}},
			Enums:    []IDLEnum{{Name: "Status", Values: []IDLEnumValue{{Name: "OK", Number: 1}}}},
			Services: []IDLService{{Name: "Demo", Methods: []IDLMethod{{Name: "Ping"}}}},
		},
		IDLDocument{Kind: "api"},
	)
	for _, want := range []string{"type Gone", "enum Status", "service Demo"} {
		found := false
		for _, change := range report.Changes {
			if change.Subject == want && change.Severity == SeverityBreaking {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing breaking change %q: %#v", want, report.Changes)
		}
	}

	methodReport := reportIDLChanges(
		IDLDocument{Kind: "api", Services: []IDLService{{Name: "Demo", Methods: []IDLMethod{{Name: "Ping"}}}}},
		IDLDocument{Kind: "api", Services: []IDLService{{Name: "Demo"}}},
	)
	foundMethod := false
	for _, change := range methodReport.Changes {
		if change.Subject == "Demo.Ping" && change.Severity == SeverityBreaking {
			foundMethod = true
		}
	}
	if !foundMethod {
		t.Fatalf("missing method removal: %#v", methodReport.Changes)
	}

	enumReport := reportIDLChanges(
		IDLDocument{Kind: "api", Enums: []IDLEnum{{Name: "Status", Values: []IDLEnumValue{{Name: "OK", Number: 1}, {Name: "Gone", Number: 2}}}}},
		IDLDocument{Kind: "api", Enums: []IDLEnum{{Name: "Status", Values: []IDLEnumValue{{Name: "OK", Number: 1}}}}},
	)
	if !enumReport.HasBreaking() {
		t.Fatalf("enum value removal should be breaking: %#v", enumReport)
	}
}

func TestAPITypeSystemGroupedOutput(t *testing.T) {
	dir := t.TempDir()
	doc := IDLDocument{
		Messages: []IDLMessage{{Name: "User", Fields: []IDLField{{Name: "ID", Type: "string"}}}},
		Aliases:  []IDLTypeAlias{{Name: "Created", Target: "time.Time", Alias: true}},
	}
	if err := writeGroupedTypesFiles(dir, doc, "types"); err != nil {
		t.Fatalf("writeGroupedTypesFiles() error = %v", err)
	}
	for _, name := range []string{"types_aliases.go", "types_user.go"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if name == "types_aliases.go" && !strings.Contains(string(data), `"time"`) {
			t.Fatalf("%s missing time import:\n%s", name, data)
		}
	}
}

func TestAPITypeSystemAliasExpansionBranches(t *testing.T) {
	scope := apiTypeScope{
		messages: map[string]IDLMessage{"User": {Name: "User"}},
		aliases: map[string]IDLTypeAlias{
			"IDs":   {Name: "IDs", Target: "[]int64"},
			"Users": {Name: "Users", Target: "map[string]*User"},
			"Self":  {Name: "Self", Target: "Self"},
		},
	}
	if got := apiClientTypeRef(scope, "IDs").String(); got != "[]int64" {
		t.Fatalf("IDs expansion = %q", got)
	}
	if got := apiClientTypeRef(scope, "Users").String(); got != "map[string]*User" {
		t.Fatalf("Users expansion = %q", got)
	}
	if got := apiClientTypeRef(scope, "Self").String(); got != "Self" {
		t.Fatalf("Self expansion = %q, want cycle guard", got)
	}
	if got := apiClientTypeRef(scope, "Missing").String(); got != "Missing" {
		t.Fatalf("Missing expansion = %q", got)
	}
}

func TestOpenAPIAliasSchemaInvalidAndSelfReference(t *testing.T) {
	if got := openAPIAliasSchema(IDLTypeAlias{Name: "Bad", Target: "map[float64]string"}, nil); len(got) != 0 {
		t.Fatalf("invalid alias schema = %#v, want empty", got)
	}
	if got := openAPIAliasSchema(IDLTypeAlias{Name: "Self", Target: "Self"}, map[string]struct{}{"Self": {}}); len(got) != 0 {
		t.Fatalf("self alias schema = %#v, want empty", got)
	}
}

func TestAPITypeSystemMultiNameTagSafety(t *testing.T) {
	for _, tag := range []string{"", `json:"-"`, `json:"-,omitempty"`, `validate:"required"`} {
		if _, _, unsafe := apiMultiNameTagConflict(tag); unsafe {
			t.Fatalf("tag %q should be safe for multi-name fields", tag)
		}
	}
	for _, tag := range []string{`json:"shared"`, `form:"name"`, `path:"id"`, `header:"X-ID"`} {
		if _, _, unsafe := apiMultiNameTagConflict(tag); !unsafe {
			t.Fatalf("tag %q should be unsafe for multi-name fields", tag)
		}
	}
}
