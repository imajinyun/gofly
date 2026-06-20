package generator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/rpc"
)

const testProto = `syntax = "proto3";

package greeter.v1;
import "common.proto";
option go_package = "example.com/hello/api/greeter/v1;greeterv1";

enum HelloStatus {
  HELLO_STATUS_UNSPECIFIED = 0;
  HELLO_STATUS_OK = 1;
}

message SayHelloRequest {
  string name = 1;
  int64 age = 2;
  map<string, string> labels = 3;
  repeated string tags = 4;
}

message SayHelloResponse {
  string message = 1;
}

service GreeterService {
  rpc SayHello(SayHelloRequest) returns (SayHelloResponse);
}
`

const testAPI = `type LoginReq {
    Username string ` + "`json:\"username\"`" + `
    Password string ` + "`json:\"password\"`" + `
}

type LoginResp {
    Token string ` + "`json:\"token\"`" + `
}

service user-api {
    @handler login
    post /api/login (LoginReq) returns (LoginResp)
}
`

const testDDL = `CREATE TABLE users (
  id BIGINT PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  email VARCHAR(255),
  created_at TIMESTAMP
);`

func TestGeneratedArtifactsCompileInTemporaryModules(t *testing.T) {
	tests := []struct {
		name    string
		module  string
		setup   func(t *testing.T, dir string, module string)
		timeout time.Duration
	}{
		{
			name:   "api gen",
			module: "example.com/generated/api",
			setup: func(t *testing.T, dir string, module string) {
				writeGeneratedModule(t, dir, module)
				apiFile := filepath.Join(dir, "login.api")
				if err := os.WriteFile(apiFile, []byte(testAPI), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := GenerateRESTFromAPI(APIOptions{APIFile: apiFile, Dir: dir, Package: "api"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "rpc gen",
			module: "example.com/generated/rpc",
			setup: func(t *testing.T, dir string, module string) {
				writeGeneratedModule(t, dir, module)
				protoFile := filepath.Join(dir, "greeter.proto")
				if err := os.WriteFile(protoFile, []byte(testProto), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := GenerateRPCFromProto(RPCOptions{ProtoFile: protoFile, Dir: filepath.Join(dir, "internal", "rpc"), Package: "rpc"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "model sql gen",
			module: "example.com/generated/modelsql",
			setup: func(t *testing.T, dir string, module string) {
				writeGeneratedModule(t, dir, module)
				ddlFile := filepath.Join(dir, "schema.sql")
				if err := os.WriteFile(ddlFile, []byte(testDDL), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlFile, Dir: dir, Package: "model", Module: module, Style: "sql"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "model gorm gen",
			module: "example.com/generated/modelgorm",
			setup: func(t *testing.T, dir string, module string) {
				writeGeneratedModule(t, dir, module)
				ddlFile := filepath.Join(dir, "schema.sql")
				if err := os.WriteFile(ddlFile, []byte(testDDL), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlFile, Dir: dir, Package: "model", Module: module, Style: "gorm"}); err != nil {
					t.Fatal(err)
				}
			},
			timeout: 3 * time.Minute,
		},
		{
			name:   "model mongo gen",
			module: "example.com/generated/modelmongo",
			setup: func(t *testing.T, dir string, module string) {
				writeGeneratedModule(t, dir, module)
				if err := GenerateMongoModel(MongoModelOptions{Type: "UserProfile", Dir: filepath.Join(dir, "internal", "model"), Package: "model", Cache: true, Style: "driver"}); err != nil {
					t.Fatal(err)
				}
			},
			timeout: 3 * time.Minute,
		},
		{
			name:   "gateway gen",
			module: "example.com/generated/gateway",
			setup: func(t *testing.T, dir string, module string) {
				if err := GenerateGateway(GatewayOptions{Name: "edge", Module: module, Dir: dir}); err != nil {
					t.Fatal(err)
				}
				appendGoflyReplace(t, filepath.Join(dir, "go.mod"))
			},
			timeout: 3 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir, tt.module)
			timeout := tt.timeout
			if timeout <= 0 {
				timeout = 2 * time.Minute
			}
			runGoCommand(t, dir, timeout, "mod", "tidy")
			runGoCommand(t, dir, timeout, "test", "./...")
		})
	}
}

func TestRPCAndOpenAPIHelperBoundaries_BitsUT(t *testing.T) {
	requestTests := []struct {
		name       string
		methodName string
		params     string
		want       string
	}{
		{name: "empty params", methodName: "say_hello", params: "", want: "SayHelloRequest"},
		{name: "typed param", methodName: "ignored", params: "1: UserReq req", want: "UserReq"},
		{name: "namespaced param", methodName: "ignored", params: "1: user.UserReq req", want: "UserReq"},
	}
	for _, tt := range requestTests {
		t.Run("request/"+tt.name, func(t *testing.T) {
			if got := thriftMethodRequest(tt.methodName, tt.params); got != tt.want {
				t.Fatalf("thriftMethodRequest(%q, %q) = %q, want %q", tt.methodName, tt.params, got, tt.want)
			}
		})
	}
	typeTests := map[string]string{
		" string ":             "string",
		"binary":               "string",
		"bool":                 "bool",
		"i8":                   "int32",
		"i16":                  "int32",
		"i32":                  "int32",
		"i64":                  "int64",
		"double":               "double",
		"void":                 "Empty",
		"list<i64>":            "repeated int64",
		"list<user.Profile>":   "repeated Profile",
		"custom_type":          "CustomType",
	}
	for input, want := range typeTests {
		t.Run("type/"+input, func(t *testing.T) {
			if got := thriftTypeToProto(input); got != want {
				t.Fatalf("thriftTypeToProto(%q) = %q, want %q", input, got, want)
			}
		})
	}

	var item openAPIPathItem
	data := []byte(`{"parameters":[{"name":"id","in":"path"}],"get":{"operationId":"listUsers"},"x-vendor":{"ignored":true}}`)
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatalf("UnmarshalJSON path item: %v", err)
	}
	if _, ok := item["get"]; !ok || len(item[openAPIPathParametersKey].Parameters) != 1 {
		t.Fatalf("path item = %#v, want get operation and path parameters", item)
	}
	if err := json.Unmarshal([]byte(`{"parameters":{}}`), &item); err == nil || !strings.Contains(err.Error(), "parse openapi path parameters") {
		t.Fatalf("invalid path parameters error = %v, want wrapped parse error", err)
	}
	if err := json.Unmarshal([]byte(`{"get":[]}`), &item); err == nil || !strings.Contains(err.Error(), "parse openapi get operation") {
		t.Fatalf("invalid operation error = %v, want wrapped parse error", err)
	}
}

func writeGeneratedModule(t *testing.T, dir string, module string) {
	t.Helper()
	goMod := fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/gofly/gofly v0.0.0\n", module)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	appendGoflyReplace(t, filepath.Join(dir, "go.mod"))
}

func appendGoflyReplace(t *testing.T, goModPath string) {
	t.Helper()
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "replace github.com/gofly/gofly =>") {
		return
	}
	root := repositoryRoot(t)
	line := fmt.Sprintf("\nreplace github.com/gofly/gofly => %s\n", filepath.ToSlash(root))
	if err := os.WriteFile(goModPath, append(data, []byte(line)...), 0o644); err != nil {
		t.Fatal(err)
	}
	copyFrameworkGoSum(t, filepath.Dir(goModPath))
}

func copyFrameworkGoSum(t *testing.T, moduleDir string) {
	t.Helper()
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "go.sum"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseProto(t *testing.T) {
	doc, err := ParseProto(testProto)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Package != "greeter.v1" {
		t.Fatalf("package = %q, want greeter.v1", doc.Package)
	}
	if len(doc.Imports) != 1 || doc.Imports[0] != "common.proto" {
		t.Fatalf("imports = %v, want common.proto", doc.Imports)
	}
	if len(doc.Messages) != 2 || len(doc.Enums) != 1 || len(doc.Services) != 1 {
		t.Fatalf("messages=%d enums=%d services=%d, want 2, 1 and 1", len(doc.Messages), len(doc.Enums), len(doc.Services))
	}
	if doc.Services[0].Methods[0].Request != "SayHelloRequest" {
		t.Fatalf("request = %q, want SayHelloRequest", doc.Services[0].Methods[0].Request)
	}
}

func TestAPIAnnotationParsingBoundaries(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "blank", raw: " ", want: nil},
		{name: "comma separated", raw: "auth, trace", want: []string{"auth", "trace"}},
		{name: "semicolon separated", raw: "auth;trace", want: []string{"auth", "trace"}},
		{name: "skip empty parts", raw: "auth,, ; trace ; ", want: []string{"auth", "trace"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAPIAnnotationList(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitAPIAnnotationList(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}

	values := parseAPIAnnotationValues(`group: admin prefix=/v1 jwt: "required" middleware:'auth' ignored`)
	if values["group"] != "admin" ||
		values["prefix"] != "/v1" ||
		values["jwt"] != "required" ||
		values["middleware"] != "auth" {
		t.Fatalf("parseAPIAnnotationValues = %#v, want parsed annotation values", values)
	}
}

func TestProtoCodegenTypeAndPackageBoundaries(t *testing.T) {
	typeTests := []struct {
		protoType string
		want      string
	}{
		{protoType: "string", want: "string"},
		{protoType: "bytes", want: "[]byte"},
		{protoType: "repeated int64", want: "[]int64"},
		{protoType: "map<string, int32>", want: "map[string]int32"},
		{protoType: ".demo.v1.User", want: "*User"},
	}
	for _, tt := range typeTests {
		t.Run(tt.protoType, func(t *testing.T) {
			if got := protoGoType(tt.protoType); got != tt.want {
				t.Fatalf("protoGoType(%q) = %q, want %q", tt.protoType, got, tt.want)
			}
		})
	}

	packageTests := []struct {
		name string
		doc  IDLDocument
		want string
	}{
		{name: "go package semicolon", doc: IDLDocument{GoPackage: "example.com/app/api;apipb"}, want: "apipb"},
		{name: "go package path", doc: IDLDocument{GoPackage: "example.com/app/user-pb"}, want: "user_pb"},
		{name: "proto package", doc: IDLDocument{Package: "order.v1"}, want: "orderv1"},
		{name: "default", doc: IDLDocument{}, want: "pb"},
	}
	for _, tt := range packageTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferGoPackageName(tt.doc); got != tt.want {
				t.Fatalf("inferGoPackageName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatAPIDiffMarkdownBoundaries(t *testing.T) {
	empty := string(formatAPIDiffMarkdown(APIDiffResult{}))
	if !strings.Contains(empty, "# API Diff") || !strings.Contains(empty, "No API changes.") {
		t.Fatalf("empty markdown diff = %q, want no-change document", empty)
	}

	diff := APIDiffResult{
		AddedRoutes:   []APIRouteInfo{{Method: "get", Path: "/users", Response: "UserResp"}},
		RemovedRoutes: []APIRouteInfo{{Method: "delete", Path: "/users/{id}", Request: "DeleteReq", Response: "DeleteResp"}},
		ChangedRoutes: []APIRouteChange{{
			Key:    "GET /users",
			Base:   APIRouteInfo{Service: "user-api", Method: "get", Path: "/users", Handler: "list", Response: "OldResp"},
			Target: APIRouteInfo{Service: "user-api", Method: "get", Path: "/users", Handler: "listUsers", Response: "UserResp"},
		}},
		AddedTypes:   []IDLMessage{{Name: "User", Fields: []IDLField{{Name: "Name", Type: "string"}}}},
		RemovedTypes: []IDLMessage{{Name: "Legacy", Fields: []IDLField{{Name: "ID", Type: "int64"}}}},
		ChangedTypes: []APITypeDiffChange{{
			Name:   "User",
			Base:   IDLMessage{Name: "User", Fields: []IDLField{{Name: "Name", Type: "string"}}},
			Target: IDLMessage{Name: "User", Fields: []IDLField{{Name: "Name", Type: "string"}, {Name: "Email", Type: "string"}}},
		}},
	}
	out := string(formatAPIDiffMarkdown(diff))
	for _, want := range []string{
		"## Added routes",
		"| get | `/users` | `-` | `UserResp` |",
		"## Removed routes",
		"| delete | `/users/{id}` | `DeleteReq` | `DeleteResp` |",
		"## Changed routes",
		"GET /users",
		"## Added types",
		"`User`",
		"## Removed types",
		"`Legacy`",
		"## Changed types",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown diff missing %q:\n%s", want, out)
		}
	}
}

func TestWriteRESTFilesBoundaries(t *testing.T) {
	if err := writeRESTFiles(IDLDocument{}, APIOptions{Dir: t.TempDir(), Package: "api"}); err == nil {
		t.Fatal("writeRESTFiles without service succeeded, want error")
	}

	doc := IDLDocument{
		Messages: []IDLMessage{
			{Name: "ListUsersReq", Fields: []IDLField{{Name: "Page", Type: "int32"}}},
			{Name: "ListUsersResp", Fields: []IDLField{{Name: "Names", Type: "[]string"}}},
		},
		Services: []IDLService{{
			Name: "user-api",
			Methods: []IDLMethod{{
				Name:       "ListUsers",
				Request:    "ListUsersReq",
				Response:   "ListUsersResp",
				HTTPMethod: "post",
				HTTPPath:   "/users/list",
			}},
		}},
	}
	dir := t.TempDir()
	if err := writeRESTFiles(doc, APIOptions{Dir: dir, Package: "api", RPCPackage: "example.com/orders/rpcpb", Test: true, TypeGroup: true}); err != nil {
		t.Fatalf("writeRESTFiles with rpc gateway: %v", err)
	}
	base := filepath.Join(dir, "v1")
	for _, rel := range []string{
		"types_list_users_req.go",
		"types_list_users_resp.go",
		"converters.go",
		filepath.Join("user_api", "types.go"),
		filepath.Join("user_api", "service.go"),
		filepath.Join("user_api", "gateway.go"),
		filepath.Join("user_api", "list_users.go"),
		filepath.Join("user_api", "list_users_gateway.go"),
		filepath.Join("user_api", "routes.go"),
		filepath.Join("user_api", "routes_test.go"),
	} {
		if _, err := os.Stat(filepath.Join(base, rel)); err != nil {
			t.Fatalf("expected REST generated file %s: %v", rel, err)
		}
	}

	routeData, err := os.ReadFile(filepath.Join(base, "user_api", "list_users.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`http.MethodPost`, `Path: "/users/list"`, "ctx.BindRequest(&req)", "impl.ListUsers"} {
		if !strings.Contains(string(routeData), want) {
			t.Fatalf("REST method file missing %q:\n%s", want, routeData)
		}
	}
	routesData, err := os.ReadFile(filepath.Join(base, "user_api", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(routesData), "RegisterUserApiGatewayRoutes") || !strings.Contains(string(routesData), "NewUserApiGateway") {
		t.Fatalf("routes.go missing gateway route registration:\n%s", routesData)
	}
	convertersData, err := os.ReadFile(filepath.Join(base, "converters.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(convertersData), "func toRPCListUsersReq") || !strings.Contains(string(convertersData), "rpcpb.ListUsersResp") {
		t.Fatalf("converters.go missing rpc converters:\n%s", convertersData)
	}
}

func TestProtoBreakingDescriptorBoundaries(t *testing.T) {
	if _, err := DetectProtoChanges(ProtoBreakingOptions{}); err == nil || !strings.Contains(err.Error(), "base and target proto files are required") {
		t.Fatalf("DetectProtoChanges missing paths error = %v, want required paths", err)
	}

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.proto")
	targetPath := filepath.Join(dir, "target.proto")
	if err := os.WriteFile(targetPath, []byte(`syntax = "proto3";
package demo.v1;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
service Greeter { rpc Ping(PingReq) returns (PingResp); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectProtoDescriptorChanges(ProtoBreakingOptions{Base: missing, Target: targetPath}); err == nil || !strings.Contains(err.Error(), "read base proto") {
		t.Fatalf("DetectProtoDescriptorChanges missing base error = %v, want read base proto", err)
	}
	basePath := filepath.Join(dir, "base.proto")
	baseProto := `syntax = "proto3";
package demo.v1;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
message PongResp { string message = 1; }
service Greeter { rpc Ping(PingReq) returns (PingResp); }
service Removed { rpc Gone(PingReq) returns (PingResp); }
service Streams {
  rpc Upload(stream PingReq) returns (PingResp);
  rpc Watch(PingReq) returns (stream PingResp);
  rpc Chat(stream PingReq) returns (stream PingResp);
}
`
	if err := os.WriteFile(basePath, []byte(baseProto), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectProtoDescriptorChanges(ProtoBreakingOptions{Base: basePath, Target: missing}); err == nil || !strings.Contains(err.Error(), "read target proto") {
		t.Fatalf("DetectProtoDescriptorChanges missing target error = %v, want read target proto", err)
	}
	targetProto := `syntax = "proto3";
package demo.v1;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
message PongResp { string message = 1; }
service Greeter { rpc Ping(PingReq) returns (PongResp); }
service Added { rpc Record(PingReq) returns (PongResp); }
`
	if err := os.WriteFile(targetPath, []byte(targetProto), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptorReport, err := DetectProtoDescriptorChanges(ProtoBreakingOptions{Base: basePath, Target: targetPath})
	if err != nil {
		t.Fatalf("DetectProtoDescriptorChanges: %v", err)
	}
	if descriptorReport.Breaking == 0 || len(descriptorReport.Changes) == 0 {
		t.Fatalf("descriptor report = %#v, want breaking changes", descriptorReport)
	}
	var sawAddedService, sawRemovedService, sawSignature bool
	for _, change := range descriptorReport.Changes {
		if change.Category == rpc.DescriptorChangeService && change.Severity == rpc.DescriptorChangeInfo && strings.Contains(change.Subject, "Added") {
			sawAddedService = true
		}
		if change.Category == rpc.DescriptorChangeService && change.Severity == rpc.DescriptorChangeBreaking && strings.Contains(change.Subject, "Removed") {
			sawRemovedService = true
		}
		if change.Category == rpc.DescriptorChangeSignature && change.Severity == rpc.DescriptorChangeBreaking && strings.Contains(change.Subject, "response") {
			sawSignature = true
		}
	}
	if !sawAddedService || !sawRemovedService || !sawSignature {
		t.Fatalf("descriptor changes missing expected added/removed/signature entries: %#v", descriptorReport.Changes)
	}

	breakingReport, err := DetectProtoChanges(ProtoBreakingOptions{Base: basePath, Target: targetPath})
	if err != nil {
		t.Fatalf("DetectProtoChanges: %v", err)
	}
	if !breakingReport.HasBreaking() || breakingReport.IsEmpty() {
		t.Fatalf("breaking report = %#v, want non-empty breaking report", breakingReport)
	}

	streamTests := []struct {
		name   string
		method IDLMethod
		want   rpc.StreamMode
	}{
		{name: "unary", method: IDLMethod{}, want: rpc.StreamModeUnary},
		{name: "client", method: IDLMethod{ClientStream: true}, want: rpc.StreamModeClientStream},
		{name: "server", method: IDLMethod{ServerStream: true}, want: rpc.StreamModeServerStream},
		{name: "bidi", method: IDLMethod{ClientStream: true, ServerStream: true}, want: rpc.StreamModeBidiStream},
	}
	for _, tt := range streamTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protoStreamMode(tt.method); got != tt.want {
				t.Fatalf("protoStreamMode(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	mapped := descriptorCompatibilityToBreakingReport(rpc.DescriptorCompatibilityReport{Changes: []rpc.DescriptorChange{
		{Category: rpc.DescriptorChangeMethod, Severity: rpc.DescriptorChangeBreaking, Subject: "svc/m", Description: "method removed"},
		{Category: rpc.DescriptorChangeStream, Severity: rpc.DescriptorChangeWarning, Subject: "svc/s", Description: "stream changed"},
		{Category: rpc.DescriptorChangeVersion, Severity: rpc.DescriptorChangeInfo, Subject: "svc", Description: "version changed"},
		{Category: rpc.DescriptorChangeTimeout, Severity: rpc.DescriptorChangeInfo, Subject: "svc/m", Description: "timeout changed"},
		{Category: rpc.DescriptorChangeCodec, Severity: "custom", Subject: "svc/m", Description: "codec changed"},
	}})
	if mapped.Breaking != 1 || mapped.Warnings != 1 || len(mapped.Changes) != 5 {
		t.Fatalf("mapped descriptor report = %#v, want one breaking and one warning", mapped)
	}
	wantCategories := []ChangeCategory{CategoryMethod, CategoryStream, CategoryService, CategorySignature, CategoryService}
	wantSeverities := []ChangeSeverity{SeverityBreaking, SeverityWarning, SeverityInfo, SeverityInfo, SeverityInfo}
	for i := range mapped.Changes {
		if mapped.Changes[i].Category != wantCategories[i] || mapped.Changes[i].Severity != wantSeverities[i] {
			t.Fatalf("mapped change[%d] = %#v, want category %q severity %q", i, mapped.Changes[i], wantCategories[i], wantSeverities[i])
		}
	}
}

func TestGenerateGRPCFromProtoBoundaries(t *testing.T) {
	if err := GenerateGRPCFromProto(GRPCOptions{}); err == nil || !strings.Contains(err.Error(), "proto file is required") {
		t.Fatalf("GenerateGRPCFromProto missing file error = %v, want proto file required", err)
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.proto")
	if err := GenerateGRPCFromProto(GRPCOptions{ProtoFile: missing, Dir: dir}); err == nil || !strings.Contains(err.Error(), "read proto file") {
		t.Fatalf("GenerateGRPCFromProto missing proto error = %v, want read proto file", err)
	}
	noService := filepath.Join(dir, "empty.proto")
	if err := os.WriteFile(noService, []byte(`syntax = "proto3";
package demo.v1;
message PingReq { string name = 1; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateGRPCFromProto(GRPCOptions{ProtoFile: noService, Dir: dir}); err == nil || !strings.Contains(err.Error(), "proto service is required") {
		t.Fatalf("GenerateGRPCFromProto no service error = %v, want service required", err)
	}
	protoPath := filepath.Join(dir, "greeter.proto")
	protoContent := `syntax = "proto3";
package demo.v1;
option go_package = "example.com/demo/v1;demov1";
message PingReq { string name = 1; }
message PingResp { string message = 1; }
service Greeter { rpc Ping(PingReq) returns (PingResp); }
`
	if err := os.WriteFile(protoPath, []byte(protoContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateGRPCFromProto(GRPCOptions{ProtoFile: protoPath, Dir: dir}); err != nil {
		t.Fatalf("GenerateGRPCFromProto: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "greeter.grpc.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"package demov1",
		"func NewGreeterGRPCServer",
		"flygrpc.RecoveryUnaryServerInterceptor(nil)",
		"RegisterGreeterServer(server.GRPCServer(), impl)",
		"func DialGreeter(ctx context.Context, target string",
		"return NewGreeterClient(conn.Conn()), conn, nil",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated grpc binding missing %q:\n%s", want, data)
		}
	}
}

func TestParseProtoIgnoresCommonDeclarations(t *testing.T) {
	doc, err := ParseProto(`syntax = "proto3";
package demo.v1;
option go_package = "example.com/demo";
message Event {
  option deprecated = true;
  reserved 2, 15;
  extensions 100 to max;
  string name = 1;
}
enum Status {
  STATUS_UNKNOWN = 0;
  STATUS_LEGACY = -1;
}
service Streamer {
  rpc Watch(Event) returns (stream Event);
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 1 || len(doc.Messages[0].Fields) != 1 || doc.Messages[0].Fields[0].Name != "name" {
		t.Fatalf("parsed messages = %#v, want one data field", doc.Messages)
	}
	if len(doc.Enums) != 1 || len(doc.Enums[0].Values) != 2 || doc.Enums[0].Values[1].Number != -1 {
		t.Fatalf("parsed enums = %#v, want negative enum value", doc.Enums)
	}
	if len(doc.Services) != 1 || len(doc.Services[0].Methods) != 1 || !doc.Services[0].Methods[0].ServerStream {
		t.Fatalf("parsed services = %#v, want server stream method", doc.Services)
	}
}

func TestParseThriftAndRPCTooling(t *testing.T) {
	thrift := `namespace go example.com/greeter
include "base.thrift"

struct SayHelloReq {
  1: required string name
}

struct SayHelloResp {
  1: string message
}

service Greeter {
  SayHelloResp SayHello(1: SayHelloReq req)
}`
	doc, err := ParseThrift(thrift)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "thrift" || doc.GoPackage != "example.com/greeter" || len(doc.Imports) != 1 || len(doc.Messages) != 2 || len(doc.Services) != 1 {
		t.Fatalf("parsed thrift doc = %+v", doc)
	}
	if err := LintRPCIDL(doc); err != nil {
		t.Fatal(err)
	}
	report := RPCIDLReportFor(doc)
	if report.Methods != 1 || report.Services != 1 || report.Messages != 2 {
		t.Fatalf("rpc idl report = %+v", report)
	}
	out, err := FormatRPCIDLReport(doc, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"kind": "thrift"`) || !strings.Contains(string(out), `"methods": 1`) {
		t.Fatalf("rpc idl json report = %s", out)
	}
	proto := string(ThriftAsProto(doc))
	for _, want := range []string{"syntax = \"proto3\";", "message SayHelloReq", "service Greeter", "rpc SayHello(SayHelloReq) returns (SayHelloResp);"} {
		if !strings.Contains(proto, want) {
			t.Fatalf("thrift proto missing %q:\n%s", want, proto)
		}
	}
}

func TestRPCToolingTextThriftGenerationAndNewScaffold(t *testing.T) {
	dir := t.TempDir()
	thriftPath := filepath.Join(dir, "chat.thrift")
	thrift := `namespace go example.com/chat
struct ChatReq {
  1: required string text
  2: list<i64> ids
}
struct ChatResp {
  1: bool ok
}
service Chat {
  ChatResp Talk(1: ChatReq req)
}`
	if err := os.WriteFile(thriftPath, []byte(thrift), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := ReadRPCIDL(thriftPath)
	if err != nil {
		t.Fatalf("ReadRPCIDL thrift: %v", err)
	}
	textReport, err := FormatRPCIDLReport(doc, "text")
	if err != nil {
		t.Fatalf("FormatRPCIDLReport text: %v", err)
	}
	for _, want := range []string{"kind: thrift", "go_package: example.com/chat", "messages: 2", "methods: 1"} {
		if !strings.Contains(string(textReport), want) {
			t.Fatalf("text report missing %q:\n%s", want, textReport)
		}
	}
	if _, err := FormatRPCIDLReport(doc, "xml"); err == nil || !strings.Contains(err.Error(), "unsupported rpc idl report format") {
		t.Fatalf("unsupported report format error = %v", err)
	}

	outDir := filepath.Join(dir, "out")
	if err := GenerateProtoFromThrift(RPCScaffoldOptions{IDLFile: thriftPath, Dir: outDir}); err != nil {
		t.Fatalf("GenerateProtoFromThrift: %v", err)
	}
	protoData, err := os.ReadFile(filepath.Join(outDir, "chat.proto"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package example.com/chat;", "repeated int64 ids", "rpc Talk(ChatReq) returns (ChatResp);"} {
		if !strings.Contains(string(protoData), want) {
			t.Fatalf("generated thrift proto missing %q:\n%s", want, protoData)
		}
	}

	if err := GenerateRPCNew(RPCNewOptions{Name: "greeter", Module: "example.com/greeter", Dir: filepath.Join(dir, "newrpc")}); err != nil {
		t.Fatalf("GenerateRPCNew: %v", err)
	}
	newProto, err := os.ReadFile(filepath.Join(dir, "newrpc", "greeter.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newProto), "package greeter;") || !strings.Contains(string(newProto), "service Greeter") {
		t.Fatalf("new rpc proto = %s", newProto)
	}
	if err := GenerateRPCNew(RPCNewOptions{Name: "", Module: "example.com/greeter", Dir: filepath.Join(dir, "bad")}); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("GenerateRPCNew missing name error = %v", err)
	}
}

func TestGenerateRPCClientServerAndMiddleware(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateRPCClient(RPCScaffoldOptions{IDLFile: protoPath, Dir: outDir, Package: "rpcclient"}); err != nil {
		t.Fatal(err)
	}
	clientData, err := os.ReadFile(filepath.Join(outDir, "greeter_client.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientData), "type GreeterServiceEndpointClient struct") || !strings.Contains(string(clientData), "func GreeterServiceDescriptor() rpc.ServiceDesc") || !strings.Contains(string(clientData), `c.desc.MethodPath("SayHello")`) || !strings.Contains(string(clientData), "func NewGreeterServiceHTTPClient(target string, opts ...rpc.ClientOption) (*GreeterServiceEndpointClient, error)") || !strings.Contains(string(clientData), "func (c *GreeterServiceEndpointClient) RuntimeDescriptor() rpc.Descriptor") {
		t.Fatalf("generated rpc client = %s", clientData)
	}
	if !strings.Contains(string(clientData), "type GreeterServiceGenericEndpointClient struct") || !strings.Contains(string(clientData), "func NewGreeterServiceGenericEndpointClient") || !strings.Contains(string(clientData), "func NewGreeterServiceGenericHTTPClient(target string, opts ...rpc.ClientOption) (*GreeterServiceGenericEndpointClient, error)") || !strings.Contains(string(clientData), "func (c *GreeterServiceGenericEndpointClient) RuntimeDescriptor() rpc.Descriptor") || !strings.Contains(string(clientData), "c.invoker.InvokeMethod(ctx, c.desc, method, req)") {
		t.Fatalf("generated generic rpc client = %s", clientData)
	}
	if err := GenerateRPCServer(RPCScaffoldOptions{IDLFile: protoPath, Dir: outDir, Package: "rpcimpl"}); err != nil {
		t.Fatal(err)
	}
	serverData, err := os.ReadFile(filepath.Join(outDir, "greeter_server.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serverData), "type GreeterServiceServer struct") || !strings.Contains(string(serverData), "func RegisterGreeterServiceEndpointServer") || !strings.Contains(string(serverData), "func NewGreeterServiceHTTPServer(impl GreeterServiceEndpoint, opts ...rpc.ServerOption) (*rpc.HTTPServer, error)") || !strings.Contains(string(serverData), "func (s *GreeterServiceServer) RuntimeDescriptor() rpc.Descriptor") || !strings.Contains(string(serverData), "func BindGreeterServiceGenericHandlers") || !strings.Contains(string(serverData), "rpc.CodeUnimplemented") {
		t.Fatalf("generated rpc server = %s", serverData)
	}
	if !strings.Contains(string(serverData), "typed, ok := req.(*SayHelloRequest)") || !strings.Contains(string(serverData), `return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected request type for SayHello")`) {
		t.Fatalf("generated rpc server handler is not defensive = %s", serverData)
	}
	if err := GenerateRPCMiddleware(RPCMiddlewareOptions{Name: "auth", Dir: outDir}); err != nil {
		t.Fatal(err)
	}
	mwData, err := os.ReadFile(filepath.Join(outDir, "internal", "rpc", "middleware", "auth.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mwData), "func AuthUnaryInterceptor() grpc.UnaryServerInterceptor") {
		t.Fatalf("generated rpc middleware = %s", mwData)
	}
}

func TestParseAPI(t *testing.T) {
	doc, err := ParseAPI(testAPI)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 2 || len(doc.Services) != 1 {
		t.Fatalf("messages=%d services=%d, want 2 and 1", len(doc.Messages), len(doc.Services))
	}
	method := doc.Services[0].Methods[0]
	if method.HTTPMethod != "POST" || method.HTTPPath != "/api/login" || method.Handler != "login" {
		t.Fatalf("method = %+v, want POST /api/login handler login", method)
	}
}

func TestGenerateRPCCode(t *testing.T) {
	doc, err := ParseProto(testProto)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateRPCCode(doc, "greeterv1")
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		"type HelloStatus int32",
		"HELLOSTATUSOK",
		"HelloStatus = 1",
		"type SayHelloRequest struct",
		"Labels map[string]string",
		"Tags",
		"[]string",
		"type GreeterService interface",
		"func GreeterServiceDescriptor() rpc.ServiceDesc",
		"func GreeterServiceServiceDesc(impl GreeterService) rpc.ServiceDesc",
		"func BindGreeterServiceGenericHandlers(handlers map[string]rpc.GenericHandler) (rpc.ServiceDesc, error)",
		"func RegisterGreeterServiceServer",
		"func NewGreeterServiceHTTPServer(impl GreeterService, opts ...rpc.ServerOption) (*rpc.HTTPServer, error)",
		"RuntimeDescriptor() rpc.Descriptor",
		"func NewGreeterServiceClient",
		"func NewGreeterServiceHTTPClient(target string, opts ...rpc.ClientOption) (*GreeterServiceClient, error)",
		"func (c *GreeterServiceClient) Descriptor() rpc.ServiceDesc",
		"func (c *GreeterServiceClient) RuntimeDescriptor() rpc.Descriptor",
		"type GreeterServiceGenericClient struct",
		"func NewGreeterServiceGenericClient(client rpc.GenericClient) (*GreeterServiceGenericClient, error)",
		"func NewGreeterServiceGenericHTTPClient(target string, opts ...rpc.ClientOption) (*GreeterServiceGenericClient, error)",
		"func (c *GreeterServiceGenericClient) RuntimeDescriptor() rpc.Descriptor",
		"c.invoker.InvokeMethod(ctx, c.desc, method, req)",
		"type MockGreeterService struct",
		"typed, ok := req.(*SayHelloRequest)",
		`return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected request type for SayHello")`,
		"return impl.SayHello(ctx, typed)",
		`c.desc.MethodPath("SayHello")`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated code missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateRPCCodeSupportsStreaming(t *testing.T) {
	doc, err := ParseProto(`syntax = "proto3";
package greeter.v1;
message Req {
  string name = 1;
}
message Resp {
  string message = 1;
}
service Greeter { 
  rpc Watch(Req) returns (stream Resp);
}
`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateRPCCode(doc, "greeterv1")
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		"Watch(ctx context.Context, stream *rpc.Stream) error",
		"Streams: []rpc.StreamDesc",
		`Name: "Watch"`,
		`Metadata: map[string]string{"request": "Req", "response": "Resp", "clientStream": "false", "serverStream": "true"}`,
		"desc.Streams[0].Handler",
		`c.desc.StreamPath("Watch")`,
		"return c.cc.Stream(ctx, method)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated streaming rpc code missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateRPCCodeWithMiddlewareRecoveryValidator(t *testing.T) {
	doc, err := ParseProto(testProto)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateRPCCodeWithOptions(doc, "greeterv1", RPCCodeOptions{WithMiddleware: true, WithRecovery: true, WithValidator: true})
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		`"github.com/gofly/gofly/rpc/endpoint"`,
		"type GreeterServiceValidator interface",
		"ValidateSayHello(ctx context.Context, req *SayHelloRequest) error",
		"func NewGreeterServiceBizError(code rpc.Code, message string) error",
		"func NewGreeterServiceValidationError(message string) error",
		"type GreeterServiceServerOptions struct",
		"Middlewares []endpoint.Middleware",
		"func WithGreeterServiceMiddleware(middlewares ...endpoint.Middleware) GreeterServiceServerOption",
		"func GreeterServiceInterceptorChain(middlewares ...endpoint.Middleware) endpoint.Middleware",
		"return endpoint.Chain(middlewares...)",
		"func WithGreeterServiceRecovery() GreeterServiceServerOption",
		"rpc.WithServerMiddleware(rpc.RecoverMiddleware())",
		"func WithGreeterServiceValidator(validator GreeterServiceValidator) GreeterServiceServerOption",
		"func GreeterServiceServiceDescWithOptions(impl GreeterService, options ...GreeterServiceServerOption) rpc.ServiceDesc",
		"cfg.Validator.ValidateSayHello(ctx, typed)",
		"func NewGreeterServiceHTTPServerWithOptions(impl GreeterService, options ...GreeterServiceServerOption) (*rpc.HTTPServer, error)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated option rpc code missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateRPCFromProtoMultipleAndStreamVariants(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "chat.proto")
	proto := `syntax = "proto3";
package chat.v1;
message UploadReq {
  string data = 1;
}
message UploadResp {
  string id = 1;
}
message ChatMsg {
  string text = 1;
}
service Uploader {
  rpc Upload(stream UploadReq) returns (UploadResp);
}
service Chat {
  rpc Talk(stream ChatMsg) returns (stream ChatMsg);
}`
	if err := os.WriteFile(protoPath, []byte(proto), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := GenerateRPCFromProto(RPCOptions{
		ProtoFile:      protoPath,
		Dir:            dir,
		Multiple:       true,
		WithMiddleware: true,
		WithRecovery:   true,
		NoClient:       true,
	}); err != nil {
		t.Fatalf("GenerateRPCFromProto multiple: %v", err)
	}

	uploadData, err := os.ReadFile(filepath.Join(dir, "uploader", "chat.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func UploaderDescriptor() rpc.ServiceDesc",
		"Streams: []rpc.StreamDesc",
		`Metadata: map[string]string{"request": "UploadReq", "response": "UploadResp", "clientStream": "true", "serverStream": "false"}`,
		"func UploaderRPCServerOptions(options ...UploaderServerOption) []rpc.ServerOption",
		"rpc.WithServerMiddleware(rpc.RecoverMiddleware())",
	} {
		if !strings.Contains(string(uploadData), want) {
			t.Fatalf("generated uploader streaming code missing %q:\n%s", want, uploadData)
		}
	}
	if strings.Contains(string(uploadData), "type UploaderClient struct") {
		t.Fatalf("NoClient generated client code unexpectedly:\n%s", uploadData)
	}

	chatData, err := os.ReadFile(filepath.Join(dir, "chat", "chat.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func ChatDescriptor() rpc.ServiceDesc",
		`NewMessage: func() any { return new(ChatMsg) }`,
		`Metadata: map[string]string{"request": "ChatMsg", "response": "ChatMsg", "clientStream": "true", "serverStream": "true"}`,
	} {
		if !strings.Contains(string(chatData), want) {
			t.Fatalf("generated bidirectional streaming code missing %q:\n%s", want, chatData)
		}
	}
}

func TestGenerateRPCCodeInfersPackageAndOmitsOptions(t *testing.T) {
	doc, err := ParseProto(`syntax = "proto3";
package billing.v1;
message PayReq {
  int64 cents = 1;
}
message PayResp {
  bool ok = 1;
}
service Billing {
  rpc Pay(PayReq) returns (PayResp);
}`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateRPCCodeWithOptions(doc, "", RPCCodeOptions{NoClient: true})
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		"package billingv1",
		`return rpc.ServiceDesc{Name: "billing.v1.Billing"`,
		"func RegisterBillingServer",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("inferred rpc code missing %q:\n%s", want, out)
		}
	}
	for _, notWant := range []string{"type BillingClient struct", "type BillingServerOptions struct"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("inferred rpc code unexpectedly contains %q:\n%s", notWant, out)
		}
	}
}

func TestGenerateRESTCode(t *testing.T) {
	doc, err := ParseAPI(testAPI)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateRESTCode(doc, "handler")
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		"type LoginReq struct",
		"type UserApi interface",
		"func RegisterUserApiRoutes",
		`Path: "/api/login"`,
		"ctx.BindRequest(&req)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated REST code missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateAPIDocOpenAPIIncludesSchemasAndParameters(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type ListUsersReq {
	  Id string
  Page int
  Tags []string
}
type UserResp {
  Id string
  Age int64
}
service user-api {
  @handler listUsers
  get /users/{id} (ListUsersReq) returns (UserResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "openapi.json")
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: output, Format: "openapi"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	get := paths["/users/{id}"].(map[string]any)["get"].(map[string]any)
	params := get["parameters"].([]any)
	if len(params) != 3 {
		t.Fatalf("parameters = %#v, want path id and query fields", params)
	}
	pathParam := params[0].(map[string]any)
	pageParam := params[1].(map[string]any)
	tagsParam := params[2].(map[string]any)
	if pathParam["in"] != "path" || pageParam["name"] != "page" || tagsParam["name"] != "tags" {
		t.Fatalf("parameters = %#v, want path id and query fields", params)
	}
	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	user := schemas["UserResp"].(map[string]any)
	props := user["properties"].(map[string]any)
	if props["age"].(map[string]any)["format"] != "int64" {
		t.Fatalf("UserResp schema = %#v, want int64 age", user)
	}
}

func TestGenerateAPIDocOpenAPIYAML(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type ListUsersReq {
  Page int
}
type UserResp {
  Id string
}
service user-api {
  @handler listUsers
  get /users/{id} (ListUsersReq) returns (UserResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Dir: dir, Format: "yaml"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "user.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"openapi: 3.0.3",
		"title: UserApi API",
		"/users/{id}:",
		"operationId: ListUsers",
		"$ref: '#/components/schemas/UserResp'",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("openapi yaml missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateAPIDocOpenAPIAdvancedServerAuthAndExamples(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "admin.api")
	api := `type CreateUserReq {
  Id int64 ` + "`json:\"id\" example:\"42\"`" + `
  Name string ` + "`json:\"name\" example:\"Ada\"`" + `
  Password string ` + "`json:\"-\"`" + `
}
type UserResp {
  Id int64 ` + "`json:\"id\" example:\"42\"`" + `
  Name string ` + "`json:\"name\" example:\"Ada\"`" + `
}
@server(group: admin prefix: /api/v1 jwt: Auth)
service user-api {
  @handler createUser
  post /users/{id} (CreateUserReq) returns (UserResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "openapi.json")
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: output, Format: "oas3"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi = %v", spec["openapi"])
	}
	tags := spec["tags"].([]any)
	if tags[0].(map[string]any)["name"] != "Admin" {
		t.Fatalf("tags = %#v", tags)
	}
	components := spec["components"].(map[string]any)
	securitySchemes := components["securitySchemes"].(map[string]any)
	if securitySchemes["BearerAuth"] == nil {
		t.Fatalf("securitySchemes = %#v", securitySchemes)
	}
	paths := spec["paths"].(map[string]any)
	post := paths["/api/v1/users/{id}"].(map[string]any)["post"].(map[string]any)
	if post["tags"].([]any)[0] != "Admin" || post["security"] == nil {
		t.Fatalf("operation = %#v", post)
	}
	params := post["parameters"].([]any)
	if params[0].(map[string]any)["schema"].(map[string]any)["format"] != "int64" {
		t.Fatalf("path params = %#v", params)
	}
	bodyMedia := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	if bodyMedia["example"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("request example = %#v", bodyMedia["example"])
	}
	schemas := components["schemas"].(map[string]any)
	createReq := schemas["CreateUserReq"].(map[string]any)
	props := createReq["properties"].(map[string]any)
	if _, ok := props["-"]; ok {
		t.Fatalf("schema should ignore json '-' field: %#v", props)
	}
	if props["name"].(map[string]any)["example"] != "Ada" {
		t.Fatalf("schema props = %#v", props)
	}
}

func TestGenerateAPIClientPathAndQueryParams(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type ListUsersReq {
  Id string
  Page int
  Tags []string
}
type UserResp {
  Id string
}
service user-api {
  @handler listUsers
  get /users/{id} (ListUsersReq) returns (UserResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	tsOutput := filepath.Join(dir, "client.ts")
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Output: tsOutput, Language: "typescript"}); err != nil {
		t.Fatal(err)
	}
	tsData, err := os.ReadFile(tsOutput)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(tsData)
	for _, want := range []string{
		`let path = "/users/{id}";`,
		`path = path.replace("{id}", encodeURIComponent(String(req.id ?? '')));`,
		`const query = new URLSearchParams();`,
		`query.append("page", String(req.page));`,
		`for (const item of req.tags) query.append("tags", String(item));`,
		`const url = this.baseURL + path + (qs ? '?' + qs : '');`,
	} {
		if !strings.Contains(ts, want) {
			t.Fatalf("typescript client missing %q:\n%s", want, ts)
		}
	}

	jsOutput := filepath.Join(dir, "client.js")
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Output: jsOutput, Language: "javascript"}); err != nil {
		t.Fatal(err)
	}
	jsData, err := os.ReadFile(jsOutput)
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsData)
	for _, want := range []string{
		`let path = "/users/{id}";`,
		`path = path.replace("{id}", encodeURIComponent(String(req.id ?? '')));`,
		`const query = new URLSearchParams();`,
		`query.append("page", String(req.page));`,
		`for (const item of req.tags) query.append("tags", String(item));`,
		`const url = this.baseURL + path + (qs ? '?' + qs : '');`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("javascript client missing %q:\n%s", want, js)
		}
	}

	dartOutput := filepath.Join(dir, "client.dart")
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Output: dartOutput, Language: "dart"}); err != nil {
		t.Fatal(err)
	}
	dartData, err := os.ReadFile(dartOutput)
	if err != nil {
		t.Fatal(err)
	}
	dart := string(dartData)
	for _, want := range []string{
		`var path = "/users/{id}";`,
		`path = path.replaceAll("{id}", Uri.encodeComponent((req.id ?? '').toString()));`,
		`final query = <String, List<String>>{};`,
		`addQuery("page", req.page);`,
		`addQuery("tags", req.tags);`,
		`final uri = Uri.parse('$baseURL$path${qs.isNotEmpty ? '?$qs' : ''}');`,
	} {
		if !strings.Contains(dart, want) {
			t.Fatalf("dart client missing %q:\n%s", want, dart)
		}
	}

	javaOutput := filepath.Join(dir, "APIClient.java")
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Output: javaOutput, Language: "java"}); err != nil {
		t.Fatal(err)
	}
	javaData, err := os.ReadFile(javaOutput)
	if err != nil {
		t.Fatal(err)
	}
	java := string(javaData)
	for _, want := range []string{
		`String path = "/users/{id}";`,
		`path = path.replace("{id}", URLEncoder.encode(String.valueOf(req.id == null ? "" : req.id), StandardCharsets.UTF_8));`,
		`StringBuilder query = new StringBuilder();`,
		`appendQuery(query, "page", req.page);`,
		`appendQuery(query, "tags", req.tags);`,
		`String url = baseURL + path + (query.length() > 0 ? "?" + query : "");`,
	} {
		if !strings.Contains(java, want) {
			t.Fatalf("java client missing %q:\n%s", want, java)
		}
	}

	kotlinOutput := filepath.Join(dir, "APIClient.kt")
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiPath, Output: kotlinOutput, Language: "kotlin"}); err != nil {
		t.Fatal(err)
	}
	kotlinData, err := os.ReadFile(kotlinOutput)
	if err != nil {
		t.Fatal(err)
	}
	kotlin := string(kotlinData)
	for _, want := range []string{
		`var path = "/users/{id}"`,
		`path = path.replace("{id}", URLEncoder.encode((req.id ?: "").toString(), StandardCharsets.UTF_8))`,
		`val query = StringBuilder()`,
		`appendQuery(query, "page", req.page)`,
		`appendQuery(query, "tags", req.tags)`,
		`val url = baseURL.trimEnd('/') + path + if (query.isNotEmpty()) "?$query" else ""`,
	} {
		if !strings.Contains(kotlin, want) {
			t.Fatalf("kotlin client missing %q:\n%s", want, kotlin)
		}
	}
}

func TestGenerateAPIRoutes(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "routes.json")
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: apiPath, Output: output, Format: "json"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var routes []APIRouteInfo
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Method != "GET" || routes[0].Path != "/ping" || routes[0].Handler != "Ping" {
		t.Fatalf("routes = %#v, want GET /ping Ping", routes)
	}
}

func TestGenerateAPIFromOpenAPI(t *testing.T) {
	dir := t.TempDir()
	openAPIPath := filepath.Join(dir, "openapi.json")
	spec := `{
  "openapi": "3.0.3",
  "info": {"title": "User API"},
  "paths": {
    "/users/{id}": {
      "get": {
        "operationId": "getUser",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "verbose", "in": "query", "schema": {"type": "boolean"}}
        ],
        "responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/UserResp"}}}}}
      }
    },
    "/users": {
      "post": {
        "operationId": "createUser",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/CreateUserReq"}}}},
        "responses": {"201": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/UserResp"}}}}}
      }
    }
  },
  "components": {
    "schemas": {
      "CreateUserReq": {"type": "object", "properties": {"name": {"type": "string"}, "age": {"type": "integer", "format": "int32"}}},
      "UserResp": {"type": "object", "properties": {"id": {"type": "string"}, "age": {"type": "integer", "format": "int64"}, "tags": {"type": "array", "items": {"type": "string"}}}}
    }
  }
}`
	if err := os.WriteFile(openAPIPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "imported.api")
	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: openAPIPath, Output: output, Service: "user-api"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"type CreateUserReq {",
		"Age int",
		"type GetUserReq {",
		"Verbose bool",
		"type UserResp {",
		"Age int64",
		"Tags []string",
		"service user_api {",
		"@handler GetUser",
		"get /users/{id} (GetUserReq) returns (UserResp)",
		"post /users (CreateUserReq) returns (UserResp)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("imported api missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateAPIFromOpenAPIYAML(t *testing.T) {
	dir := t.TempDir()
	openAPIPath := filepath.Join(dir, "openapi.yaml")
	spec := `openapi: 3.0.0
info:
  title: inventory api
paths:
  /items/{id}:
    get:
      operationId: getItem
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: verbose
          in: query
          schema:
            type: boolean
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ItemResp"
  /items:
    post:
      operationId: createItem
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/CreateItemReq"
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ItemResp"
components:
  schemas:
    CreateItemReq:
      type: object
      properties:
        name:
          type: string
        count:
          type: integer
          format: int32
    ItemResp:
      type: object
      properties:
        id:
          type: string
        count:
          type: integer
          format: int64
        labels:
          type: array
          items:
            type: string
`
	if err := os.WriteFile(openAPIPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "imported_yaml.api")
	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: openAPIPath, Output: output}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"type CreateItemReq {",
		"Count int",
		"type GetItemReq {",
		"Verbose bool",
		"type ItemResp {",
		"Count int64",
		"Labels []string",
		"service inventory_api {",
		"@handler GetItem",
		"get /items/{id} (GetItemReq) returns (ItemResp)",
		"post /items (CreateItemReq) returns (ItemResp)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("imported yaml api missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateAPIFromOpenAPIPathParametersAndRefs(t *testing.T) {
	dir := t.TempDir()
	openAPIPath := filepath.Join(dir, "openapi.yaml")
	spec := `openapi: 3.0.0
info:
  title: inventory api
paths:
  /orgs/{orgId}/items/{id}:
    parameters:
      - $ref: "#/components/parameters/OrgIDParam"
      - $ref: "#/components/parameters/ItemIDParam"
    get:
      operationId: getItem
      parameters:
        - name: verbose
          in: query
          schema:
            type: boolean
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ItemResp"
    put:
      operationId: updateItem
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/UpdateItemBody"
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ItemResp"
components:
  parameters:
    OrgIDParam:
      name: orgId
      in: path
      required: true
      schema:
        type: string
    ItemIDParam:
      name: id
      in: path
      required: true
      schema:
        type: string
  schemas:
    UpdateItemBody:
      type: object
      properties:
        name:
          type: string
        count:
          type: integer
          format: int32
    ItemResp:
      type: object
      properties:
        id:
          type: string
        name:
          type: string
`
	if err := os.WriteFile(openAPIPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "imported_params.api")
	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: openAPIPath, Output: output}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"type GetItemReq {",
		"Id string",
		"OrgId string",
		"Verbose bool",
		"type UpdateItemReq {",
		"Count int",
		"Name string",
		"put /orgs/{orgId}/items/{id} (UpdateItemReq) returns (ItemResp)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("imported api with parameters missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateAPIDiff(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.api")
	targetPath := filepath.Join(dir, "target.api")
	base := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
}`
	target := `type PingReq {
  Name string
  Age int
}
type PingResp {
  Message string
}
type PongResp {
  Ok bool
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
  @handler pong
  post /pong returns (PongResp)
}`
	if err := os.WriteFile(basePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "diff.json")
	if err := GenerateAPIDiff(APIDiffOptions{Base: basePath, Target: targetPath, Output: output, Format: "json"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var diff APIDiffResult
	if err := json.Unmarshal(data, &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.AddedRoutes) != 1 || diff.AddedRoutes[0].Path != "/pong" {
		t.Fatalf("added routes = %#v, want /pong", diff.AddedRoutes)
	}
	if len(diff.ChangedTypes) != 1 || diff.ChangedTypes[0].Name != "PingReq" {
		t.Fatalf("changed types = %#v, want PingReq", diff.ChangedTypes)
	}
	if len(diff.AddedTypes) != 1 || diff.AddedTypes[0].Name != "PongResp" {
		t.Fatalf("added types = %#v, want PongResp", diff.AddedTypes)
	}
}

func TestDiffAPIUsesServerPrefixInRouteContract(t *testing.T) {
	base := IDLDocument{Services: []IDLService{{
		Name:   "user-api",
		Server: IDLServerAnnotation{Prefix: "/api/v1"},
		Methods: []IDLMethod{{
			Name:       "Ping",
			HTTPMethod: http.MethodGet,
			HTTPPath:   "/ping",
			Response:   "PingResp",
		}},
	}}}
	target := IDLDocument{Services: []IDLService{{
		Name:   "user-api",
		Server: IDLServerAnnotation{Prefix: "/api/v2"},
		Methods: []IDLMethod{{
			Name:       "Ping",
			HTTPMethod: http.MethodGet,
			HTTPPath:   "/ping",
			Response:   "PingResp",
		}},
	}}}

	diff := DiffAPI(base, target)
	if len(diff.RemovedRoutes) != 1 || diff.RemovedRoutes[0].Path != "/api/v1/ping" {
		t.Fatalf("removed routes = %#v, want /api/v1/ping", diff.RemovedRoutes)
	}
	if len(diff.AddedRoutes) != 1 || diff.AddedRoutes[0].Path != "/api/v2/ping" {
		t.Fatalf("added routes = %#v, want /api/v2/ping", diff.AddedRoutes)
	}
}

func TestBreakingDetectionForAPIAndProtoContracts(t *testing.T) {
	dir := t.TempDir()
	baseAPIPath := filepath.Join(dir, "base.api")
	targetAPIPath := filepath.Join(dir, "target.api")
	baseAPI := `type PingReq {
  Name string
  Age int
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
}`
	targetAPI := `type PingReq {
  Name int64
  Trace string
}
type PingResp {
  Message string
}
type AuditResp {
  Ok bool
}
service user-api {
  @handler ping
  post /ping/v2 (PingReq) returns (PingResp)
  @handler audit
  get /audit returns (AuditResp)
}`
	if err := os.WriteFile(baseAPIPath, []byte(baseAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAPIPath, []byte(targetAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	apiReport, err := DetectAPIChanges(APIBreakingOptions{Base: baseAPIPath, Target: targetAPIPath})
	if err != nil {
		t.Fatalf("DetectAPIChanges: %v", err)
	}
	if apiReport.IsEmpty() || !apiReport.HasBreaking() {
		t.Fatalf("api report = %+v, want breaking changes", apiReport)
	}
	apiText := string(FormatBreakingText(apiReport))
	for _, want := range []string{"[BREAKING] route", "GET /ping → POST /ping/v2", "[BREAKING] field", "PingReq.Name", "[INFO] method", "UserApi.Audit", "[INFO] type", "type AuditResp"} {
		if !strings.Contains(apiText, want) {
			t.Fatalf("api breaking text missing %q:\n%s", want, apiText)
		}
	}
	if _, err := DetectAPIChanges(APIBreakingOptions{}); err == nil || !strings.Contains(err.Error(), "base and target api files are required") {
		t.Fatalf("DetectAPIChanges missing paths error = %v", err)
	}
	if text := string(FormatBreakingText(BreakingChangesReport{})); !strings.Contains(text, "No breaking changes") {
		t.Fatalf("empty breaking text = %q", text)
	}

	baseProto, err := ParseProto(`syntax = "proto3";
package old.v1;
enum Status { STATUS_UNSPECIFIED = 0; STATUS_OK = 1; }
message PingReq { string name = 1; int64 age = 2; }
message PingResp { string message = 1; }
service Greeter { rpc Ping(PingReq) returns (PingResp); }
service Legacy { rpc Old(PingReq) returns (PingResp); }`)
	if err != nil {
		t.Fatal(err)
	}
	targetProto, err := ParseProto(`syntax = "proto3";
package new.v1;
enum Status { STATUS_UNSPECIFIED = 0; STATUS_OK = 2; STATUS_NEW = 3; }
message PingReq { int64 name = 3; string trace = 4; }
message PingResp { string message = 1; }
message ExtraResp { bool ok = 1; }
service Greeter { rpc Ping(stream PingReq) returns (PingResp); rpc Extra(PingReq) returns (ExtraResp); }
service Audit { rpc Check(PingReq) returns (PingResp); }`)
	if err != nil {
		t.Fatal(err)
	}
	protoReport := reportIDLChanges(baseProto, targetProto)
	if !protoReport.HasBreaking() {
		t.Fatalf("proto report = %+v, want breaking changes", protoReport)
	}
	protoText := string(FormatBreakingText(protoReport))
	for _, want := range []string{"package \"old.v1\" → \"new.v1\"", "service Legacy", "Greeter.Ping streaming", "PingReq.name", "字段 number 从 1 变更为 3", "Status.STATUS_OK", "[INFO] service"} {
		if !strings.Contains(protoText, want) {
			t.Fatalf("proto breaking text missing %q:\n%s", want, protoText)
		}
	}
}

func TestProtoRuntimeDescriptorMapSeparatesStreams(t *testing.T) {
	doc, err := ParseProto(`syntax = "proto3";
package demo.v1;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
service Greeter {
  rpc Ping(PingReq) returns (PingResp);
  rpc Watch(PingReq) returns (stream PingResp);
  rpc Chat(stream PingReq) returns (stream PingResp);
}`)
	if err != nil {
		t.Fatal(err)
	}

	descriptors := protoRuntimeDescriptorMap(doc)
	desc := descriptors["Greeter"]
	if len(desc.Methods) != 1 || desc.Methods[0].Name != "Ping" {
		t.Fatalf("methods = %#v, want unary Ping only", desc.Methods)
	}
	if len(desc.Streams) != 2 {
		t.Fatalf("streams = %#v, want Watch and Chat", desc.Streams)
	}
	if desc.Streams[0].Name != "Chat" || desc.Streams[0].Mode != rpc.StreamModeBidiStream {
		t.Fatalf("first stream = %#v, want Chat bidi stream", desc.Streams[0])
	}
	if desc.Streams[1].Name != "Watch" || desc.Streams[1].Mode != rpc.StreamModeServerStream {
		t.Fatalf("second stream = %#v, want Watch server stream", desc.Streams[1])
	}
	report := reportProtoDescriptorChanges(
		IDLDocument{Services: []IDLService{{Name: "Greeter", Methods: []IDLMethod{{Name: "Watch", Request: "PingReq", Response: "PingResp"}}}}},
		IDLDocument{Services: []IDLService{{Name: "Greeter", Methods: []IDLMethod{{Name: "Watch", Request: "PingReq", Response: "PingResp", ServerStream: true}}}}},
	)
	if !report.HasBreaking() {
		t.Fatalf("stream signature report = %+v, want breaking change", report)
	}
}

func TestAPIToolingFormatsDocsTypesRoutesAndDiffs(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
  Tags []string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	formattedPath := filepath.Join(dir, "formatted", "user.api")
	formatted, err := FormatAPIFromFile(APIFormatOptions{APIFile: apiPath, Output: formattedPath})
	if err != nil {
		t.Fatalf("FormatAPIFromFile output: %v", err)
	}
	if !strings.Contains(string(formatted), "type PingReq") {
		t.Fatalf("formatted api = %s", formatted)
	}
	lastFormatted, err := FormatAPIFromFile(APIFormatOptions{Dir: dir, Write: true})
	if err != nil {
		t.Fatalf("FormatAPIFromFile dir: %v", err)
	}
	if len(lastFormatted) == 0 {
		t.Fatal("format api dir returned empty formatted content")
	}

	markdownPath := filepath.Join(dir, "user.md")
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: markdownPath, Format: "markdown"}); err != nil {
		t.Fatalf("GenerateAPIDoc markdown: %v", err)
	}
	markdownData, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# UserApi API", "| GET | `/ping` | `Ping` | `PingReq` | `PingResp` |", "### PingReq"} {
		if !strings.Contains(string(markdownData), want) {
			t.Fatalf("api markdown missing %q:\n%s", want, markdownData)
		}
	}

	typesPath := filepath.Join(dir, "types", "types.go")
	if err := GenerateAPITypes(APITypesOptions{APIFile: apiPath, Output: typesPath, Package: "types"}); err != nil {
		t.Fatalf("GenerateAPITypes: %v", err)
	}
	typesData, err := os.ReadFile(typesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typesData), "type PingReq struct") || !strings.Contains(string(typesData), "Tags []string") {
		t.Fatalf("generated api types = %s", typesData)
	}

	for _, tt := range []struct {
		format string
		file   string
		want   string
	}{
		{format: "text", file: "routes.txt", want: "METHOD\tPATH\tHANDLER\tREQUEST\tRESPONSE\tSERVICE"},
		{format: "markdown", file: "routes.md", want: "| GET | `/ping` | `Ping` | `PingReq` | `PingResp` | `user-api` |"},
	} {
		t.Run("routes "+tt.format, func(t *testing.T) {
			output := filepath.Join(dir, tt.file)
			if err := GenerateAPIRoutes(APIRouteOptions{APIFile: apiPath, Output: output, Format: tt.format}); err != nil {
				t.Fatalf("GenerateAPIRoutes(%s): %v", tt.format, err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tt.want) {
				t.Fatalf("routes %s missing %q:\n%s", tt.format, tt.want, data)
			}
		})
	}

	targetPath := filepath.Join(dir, "target.api")
	target := `type PingReq {
  Name string
  Age int
}
type PingResp {
  Message string
}
type PongResp {
  Ok bool
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
  @handler pong
  post /pong returns (PongResp)
}`
	if err := os.WriteFile(targetPath, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		format string
		file   string
		want   []string
	}{
		{format: "text", file: "diff.txt", want: []string{"Added routes", "+ user-api POST /pong", "Changed types"}},
		{format: "markdown", file: "diff.md", want: []string{"# API Diff", "## Added routes", "## Changed types"}},
	} {
		t.Run("diff "+tt.format, func(t *testing.T) {
			output := filepath.Join(dir, tt.file)
			if err := GenerateAPIDiff(APIDiffOptions{Base: apiPath, Target: targetPath, Output: output, Format: tt.format}); err != nil {
				t.Fatalf("GenerateAPIDiff(%s): %v", tt.format, err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(data), want) {
					t.Fatalf("diff %s missing %q:\n%s", tt.format, want, data)
				}
			}
		})
	}

	noChangePath := filepath.Join(dir, "no_change.diff.txt")
	if err := GenerateAPIDiff(APIDiffOptions{Base: apiPath, Target: apiPath, Output: noChangePath, Format: "text"}); err != nil {
		t.Fatalf("GenerateAPIDiff no change: %v", err)
	}
	noChangeData, err := os.ReadFile(noChangePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(noChangeData), "No API changes") {
		t.Fatalf("no-change diff = %s", noChangeData)
	}
}

func TestGenerateRESTFromAPIWritesGatewayTypeGroupsAndRouteTests(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "gateway.api")
	api := `type LoginReq {
  Username string
}
type LoginResp {
  Token string
}
@server(prefix: /api/v1)
service user-api {
  @handler login
  post /login (LoginReq) returns (LoginResp)
}`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := GenerateRESTFromAPI(APIOptions{
		APIFile:    apiPath,
		Dir:        dir,
		Package:    "handler",
		RPCPackage: "example.com/hello/internal/rpc",
		Test:       true,
		TypeGroup:  true,
	}); err != nil {
		t.Fatalf("GenerateRESTFromAPI: %v", err)
	}

	baseDir := filepath.Join(dir, "internal", "api", "v1")
	serviceDir := filepath.Join(baseDir, "user_api")
	checks := []struct {
		path string
		want []string
	}{
		{path: filepath.Join(baseDir, "types_login_req.go"), want: []string{"type LoginReq struct", "Username string"}},
		{path: filepath.Join(baseDir, "converters.go"), want: []string{"func toRPCLoginReq", "func fromRPCLoginResp"}},
		{path: filepath.Join(serviceDir, "gateway.go"), want: []string{"type UserApiGateway struct", "func NewUserApiGateway"}},
		{path: filepath.Join(serviceDir, "login_gateway.go"), want: []string{"func (g *UserApiGateway) Login", "g.client.Login(ctx, toRPCLoginReq(req))"}},
		{path: filepath.Join(serviceDir, "routes.go"), want: []string{"func RegisterUserApiGatewayRoutes", "NewUserApiGateway(cc)"}},
		{path: filepath.Join(serviceDir, "routes_test.go"), want: []string{"func TestUserApiRoutesGenerated"}},
	}
	for _, check := range checks {
		data, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("read generated file %s: %v", check.path, err)
		}
		for _, want := range check.want {
			if !strings.Contains(string(data), want) {
				t.Fatalf("generated file %s missing %q:\n%s", check.path, want, data)
			}
		}
	}
}

func TestDiffAPIServiceScopedRoutes(t *testing.T) {
	base := IDLDocument{Services: []IDLService{
		{
			Name: "public-api",
			Methods: []IDLMethod{{
				Name:       "Ping",
				HTTPMethod: http.MethodGet,
				HTTPPath:   "/ping",
				Response:   "PingResp",
			}},
		},
		{
			Name: "admin-api",
			Methods: []IDLMethod{{
				Name:       "PingAdmin",
				HTTPMethod: http.MethodGet,
				HTTPPath:   "/ping",
				Response:   "PingResp",
			}},
		},
	}}
	target := IDLDocument{Services: []IDLService{
		{
			Name: "public-api",
			Methods: []IDLMethod{{
				Name:       "Ping",
				HTTPMethod: http.MethodGet,
				HTTPPath:   "/ping",
				Response:   "PingResp",
			}},
		},
		{
			Name: "admin-api",
			Methods: []IDLMethod{{
				Name:       "PingAdmin",
				HTTPMethod: http.MethodGet,
				HTTPPath:   "/ping",
				Response:   "AdminPingResp",
			}},
		},
	}}

	diff := DiffAPI(base, target)
	if len(diff.ChangedRoutes) != 1 {
		t.Fatalf("changed routes = %#v, want exactly one admin route change", diff.ChangedRoutes)
	}
	change := diff.ChangedRoutes[0]
	if change.Key != "admin-api GET /ping" || change.Target.Response != "AdminPingResp" {
		t.Fatalf("route change = %#v, want admin-api GET /ping response change", change)
	}
}

func TestValidateAPI(t *testing.T) {
	valid := IDLDocument{
		Messages: []IDLMessage{
			{
				Name: "PingReq",
				Fields: []IDLField{
					{Name: "Name", Type: "string"},
					{Name: "Tags", Type: "[]string"},
				},
			},
			{Name: "PingResp", Fields: []IDLField{{Name: "Message", Type: "string"}}},
		},
		Services: []IDLService{{
			Name: "user-api",
			Methods: []IDLMethod{{
				Name:       "Ping",
				Handler:    "ping",
				Request:    "PingReq",
				Response:   "PingResp",
				HTTPMethod: http.MethodGet,
				HTTPPath:   "/ping",
			}},
		}},
	}
	if err := ValidateAPI(valid); err != nil {
		t.Fatalf("ValidateAPI(valid) = %v", err)
	}

	invalid := IDLDocument{
		Messages: []IDLMessage{
			{Name: "PingReq", Fields: []IDLField{{Name: "Name", Type: "MissingType"}}},
			{Name: "PingReq", Fields: []IDLField{{Name: "Other", Type: "string"}}},
			{Name: "DupField", Fields: []IDLField{{Name: "Name", Type: "string"}, {Name: "Name", Type: "string"}}},
		},
		Services: []IDLService{{
			Name: "user-api",
			Methods: []IDLMethod{
				{
					Name:       "Ping",
					Handler:    "ping",
					Request:    "MissingReq",
					Response:   "MissingResp",
					HTTPMethod: http.MethodGet,
					HTTPPath:   "/ping",
				},
				{
					Name:       "PingAgain",
					Handler:    "ping",
					Response:   "PingReq",
					HTTPMethod: http.MethodGet,
					HTTPPath:   "/ping",
				},
			},
		}},
	}
	err := ValidateAPI(invalid)
	if err == nil {
		t.Fatal("ValidateAPI(invalid) succeeded, want semantic validation error")
	}
	for _, want := range []string{
		"duplicate type PingReq",
		"duplicate field DupField.Name",
		"unknown field type PingReq.Name MissingType",
		"route Ping references unknown request type MissingReq",
		"route Ping references unknown response type MissingResp",
		"duplicate route GET /ping",
		"duplicate handler Ping",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ValidateAPI error missing %q:\n%v", want, err)
		}
	}
}

func TestValidateAPIRejectsUnsafePathParams(t *testing.T) {
	invalid := IDLDocument{
		Messages: []IDLMessage{
			{Name: "PingReq", Fields: []IDLField{{Name: "ID", Type: "string"}}},
			{Name: "PingResp", Fields: []IDLField{{Name: "Message", Type: "string"}}},
		},
		Services: []IDLService{{
			Name: "user-api",
			Methods: []IDLMethod{{
				Name:       "Ping",
				Handler:    "ping",
				Request:    "PingReq",
				Response:   "PingResp",
				HTTPMethod: http.MethodGet,
				HTTPPath:   `/users/{id);console.log("x");//}`,
			}},
		}},
	}

	err := ValidateAPI(invalid)
	if err == nil || !strings.Contains(err.Error(), "invalid path parameter") {
		t.Fatalf("ValidateAPI unsafe path param err = %v, want invalid path parameter", err)
	}
}

func TestOpenAPIPathParamNamesNormalizesCatchAll(t *testing.T) {
	got := openAPIPathParamNames("/files/{file...}/meta/{id}")
	want := []string{"file", "id"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("openAPIPathParamNames catch-all = %v, want %v", got, want)
	}
	if got := openAPIPathParamNames("/{path...}"); len(got) != 0 {
		t.Fatalf("openAPIPathParamNames path catch-all = %v, want empty", got)
	}
}

func TestOpenAPIMediaSchema(t *testing.T) {
	schema := openAPISpecSchema{Type: "object"}
	if got, ok := openAPIMediaSchema(nil); ok {
		t.Fatalf("nil media = %v, want not ok", got)
	}
	if got, ok := openAPIMediaSchema(map[string]openAPIMediaType{"application/json": {Schema: schema}}); !ok || got.Type != "object" {
		t.Fatalf("json media = %v, want ok", got)
	}
	if got, ok := openAPIMediaSchema(map[string]openAPIMediaType{"text/plain": {Schema: schema}}); !ok || got.Type != "object" {
		t.Fatalf("fallback media = %v, want ok", got)
	}
}

func TestUnmarshalOpenAPI(t *testing.T) {
	var spec openAPIDocument
	if err := unmarshalOpenAPI([]byte(""), &spec); err == nil {
		t.Fatal("expected error for empty content")
	}
	if err := unmarshalOpenAPI([]byte("{bad json"), &spec); err == nil {
		t.Fatal("expected error for invalid json")
	}
	if err := unmarshalOpenAPI([]byte(`{"openapi":"3.0.0"}`), &spec); err != nil {
		t.Fatalf("valid json: %v", err)
	}
	if err := unmarshalOpenAPI([]byte("openapi: \"3.0.0\"\n"), &spec); err != nil {
		t.Fatalf("valid yaml: %v", err)
	}
}

func TestOpenAPISchemaType(t *testing.T) {
	tests := []struct {
		schema openAPISpecSchema
		want   string
	}{
		{openAPISpecSchema{Ref: "#/components/schemas/User"}, "User"},
		{openAPISpecSchema{Type: "array"}, "[]string"},
		{openAPISpecSchema{Type: "array", Items: &openAPISpecSchema{Type: "integer"}}, "[]int64"},
		{openAPISpecSchema{Type: "integer"}, "int64"},
		{openAPISpecSchema{Type: "integer", Format: "int32"}, "int"},
		{openAPISpecSchema{Type: "number"}, "float64"},
		{openAPISpecSchema{Type: "number", Format: "float"}, "float32"},
		{openAPISpecSchema{Type: "boolean"}, "bool"},
		{openAPISpecSchema{Type: "object"}, "string"},
		{openAPISpecSchema{Type: "string"}, "string"},
	}
	for _, tt := range tests {
		if got := openAPISchemaType(tt.schema, nil); got != tt.want {
			t.Fatalf("openAPISchemaType(%+v) = %q, want %q", tt.schema, got, tt.want)
		}
	}
}

func TestOpenAPIOperationName(t *testing.T) {
	if got := openAPIOperationName("GET", "/users", openAPIOperation{OperationID: "listUsers"}); got != "ListUsers" {
		t.Fatalf("operationID = %q, want ListUsers", got)
	}
	if got := openAPIOperationName("GET", "/users", openAPIOperation{}); got != "GetUsers" {
		t.Fatalf("no operationID = %q, want GetUsers", got)
	}
}

func TestOpenAPIResponseName(t *testing.T) {
	if got := openAPIResponseName("GetUser", openAPIOperation{}); got != "" {
		t.Fatalf("empty response = %q, want empty", got)
	}
	if got := openAPIResponseName("GetUser", openAPIOperation{Responses: map[string]openAPIResponse{"200": {Content: map[string]openAPIMediaType{"application/json": {Schema: openAPISpecSchema{Ref: "#/components/schemas/User"}}}}}}); got != "User" {
		t.Fatalf("ref response = %q, want User", got)
	}
	if got := openAPIResponseName("GetUser", openAPIOperation{Responses: map[string]openAPIResponse{"200": {Content: map[string]openAPIMediaType{"application/json": {Schema: openAPISpecSchema{Properties: map[string]openAPISpecSchema{"id": {}}}}}}}}); got != "GetUserResp" {
		t.Fatalf("properties response = %q, want GetUserResp", got)
	}
}

func TestGenerateRESTGatewayCode(t *testing.T) {
	api := `type SayHelloRequest {
    Name string
}

type SayHelloResponse {
    Message string
}

service greeter-service {
    @handler SayHello
    post /api/hello (SayHelloRequest) returns (SayHelloResponse)
}
`
	doc, err := ParseAPI(api)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateRESTCodeWithOptions(doc, RESTCodeOptions{
		Package:    "handler",
		RPCPackage: "example.com/hello/api/greeter/v1/greeterv1",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		`greeterv1 "example.com/hello/api/greeter/v1/greeterv1"`,
		`"github.com/gofly/gofly/rpc"`,
		"type GreeterServiceGateway struct",
		"client *greeterv1.GreeterServiceClient",
		"func NewGreeterServiceGateway(cc rpc.Client) *GreeterServiceGateway",
		"greeterv1.NewGreeterServiceClient(cc)",
		"resp, err := g.client.SayHello(ctx, toRPCSayHelloRequest(req))",
		"func toRPCSayHelloRequest(in *SayHelloRequest) *greeterv1.SayHelloRequest",
		"func fromRPCSayHelloResponse(in *greeterv1.SayHelloResponse) *SayHelloResponse",
		"func RegisterGreeterServiceGatewayRoutes(s *rest.Server, cc rpc.Client)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated gateway code missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateRESTFromAPI(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	if err := os.WriteFile(apiPath, []byte(testAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateRESTFromAPI(APIOptions{APIFile: apiPath, Dir: outDir, Package: "handler"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "internal", "api", "v1", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package handler") {
		t.Fatalf("generated file = %s", data)
	}
}

func TestGenerateRESTFromAPITypeGroup(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	if err := os.WriteFile(apiPath, []byte(testAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateRESTFromAPI(APIOptions{APIFile: apiPath, Dir: outDir, Package: "handler", TypeGroup: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "internal", "api", "v1", "types_login_req.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package handler") {
		t.Fatalf("generated grouped type file = %s", data)
	}
}

func TestGenerateModelCode(t *testing.T) {
	tables, err := ParseSQLModels(`CREATE TABLE users (
  id BIGINT PRIMARY KEY,
  name VARCHAR(64) NOT NULL,
  age INT,
  created_at TIMESTAMP
);`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateModelCode(tables, "model")
	if err != nil {
		t.Fatal(err)
	}
	out := string(code)
	for _, want := range []string{
		"type User struct",
		"ID        int64",
		"Age       *int",
		"CreatedAt *time.Time",
		`"github.com/gofly/gofly/core/storage"`,
		"type UserModel struct",
		"func NewUserModel",
		"func NewCachedUserModel",
		"cache.ModelCache[*User, int64]",
		"func (m *UserModel) FindOne",
		"storage.SelectByID(userTable, userColumns, \"id\", m.dialect)",
		"func (m *UserModel) Insert",
		"func (m *UserModel) Update",
		"func (m *UserModel) Delete",
		"func (m *UserModel) List",
		"func (m *UserModel) Count",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated model code missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"github.com/gofly/gofly/storage"`) {
		t.Fatalf("generated model code imports legacy storage package:\n%s", out)
	}
}

func TestGenerateModelFromDDL(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE orders (
  id bigint,
  buyer_name varchar(128),
  PRIMARY KEY (id)
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "order_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "type Order struct") {
		t.Fatalf("generated model file = %s", data)
	}
	repo, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(repo), "func NewCachedOrderRepo") || strings.Contains(string(repo), "cache.ModelCache[*entity.Order, int64]") || strings.Contains(string(repo), `"github.com/gofly/gofly/cache"`) {
		t.Fatalf("generated repo should not include cache helpers unless cache is enabled = %s", repo)
	}
	for _, want := range []string{
		"tx      *sql.Tx",
		"func (r *OrderRepo) WithTx(tx *sql.Tx) *OrderRepo",
		"func (r *OrderRepo) Transact(ctx context.Context, opts *sql.TxOptions, fn func(context.Context, *OrderRepo) error) error",
		"func (r *OrderRepo) FindWhere(ctx context.Context, where *storage.Where) ([]entity.Order, error)",
		"func (r *OrderRepo) CountWhere(ctx context.Context, where *storage.Where) (int64, error)",
		"func (r *OrderRepo) InsertMany(ctx context.Context, items []*entity.Order) error",
		"func (r *OrderRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error",
		`"sort"`,
		"sort.Strings(fieldNames)",
		"for _, column := range fieldNames",
		"func (r *OrderRepo) ListAfter(ctx context.Context, after int64, limit int) ([]entity.Order, error)",
		"storage.SelectWhere(entity.OrderTable, entity.OrderColumns, where, r.dialect)",
	} {
		if !strings.Contains(string(repo), want) {
			t.Fatalf("generated repo missing %q:\n%s", want, repo)
		}
	}
	if !strings.Contains(string(repo), `"github.com/gofly/gofly/core/storage"`) || strings.Contains(string(repo), `"github.com/gofly/gofly/storage"`) {
		t.Fatalf("generated repo should import core/storage only:\n%s", repo)
	}
}

func TestGenerateModelFromDDLCacheOptionControlsSQLRepoHelpers(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE orders (
  id bigint primary key,
  buyer_name varchar(128)
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model", Cache: true}); err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	repoOut := string(repo)
	for _, want := range []string{
		`"github.com/gofly/gofly/cache"`,
		`"github.com/gofly/gofly/core/kv/redis"`,
		"func NewCachedOrderRepo",
		"cache.ModelCache[*entity.Order, int64]",
		"type CachedOrderRepo struct",
		"func NewConsistentCachedOrderRepo",
		"type RedisCachedOrderRepo struct",
		"func NewRedisCachedOrderRepo",
		"cache.RedisModelCache[*entity.Order, int64]",
		"cache.WithRedisModelNotFound[*entity.Order, int64](redis.ErrNil)",
	} {
		if !strings.Contains(repoOut, want) {
			t.Fatalf("generated cached repo missing %q:\n%s", want, repoOut)
		}
	}
}

func TestGenerateModelFromDDLRedisCacheCompilesInTempModule(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE orders (
  id bigint primary key,
  buyer_name varchar(128)
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGeneratedModule(t, outDir, "example.com/shop")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model", Module: "example.com/shop", Cache: true}); err != nil {
		t.Fatal(err)
	}
	runGoCommand(t, outDir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, outDir, 3*time.Minute, "test", "./...")
}

func TestGenerateModelFromDDLCompositeUniqueDoesNotCreateSingleColumnFinders(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE users (
  id bigint primary key,
  tenant_id bigint not null,
  email varchar(128) not null,
  UNIQUE KEY uk_tenant_email (tenant_id, email)
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model"}); err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	repoOut := string(repo)
	for _, unexpected := range []string{
		"func (r *UserRepo) FindByTenantID",
		"func (r *UserRepo) FindByEmail",
	} {
		if strings.Contains(repoOut, unexpected) {
			t.Fatalf("generated repo should not treat composite unique index as single-column unique finder %q:\n%s", unexpected, repoOut)
		}
	}
}

func TestGenerateModelFromDDLGoctlOptions(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE pre_users (
  id bigint primary key,
  name varchar(64) not null,
  deleted_at datetime
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateModelFromDDL(ModelOptions{
		DDLFile:       ddlPath,
		Dir:           outDir,
		Tables:        []string{"pre_users"},
		Prefix:        "pre_",
		IgnoreColumns: []string{"Deleted_At"},
		Strict:        true,
		Cache:         true,
	}); err != nil {
		t.Fatal(err)
	}
	entityData, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "user_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	entityOut := string(entityData)
	if !strings.Contains(entityOut, `const UserTable = "users"`) || strings.Contains(entityOut, "DeletedAt") || strings.Contains(entityOut, `"deleted_at"`) {
		t.Fatalf("generated entity should trim table prefix and ignore deleted_at:\n%s", entityOut)
	}
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: filepath.Join(dir, "strict"), Tables: []string{"missing"}, Strict: true}); err == nil || !strings.Contains(err.Error(), "requested table not found") {
		t.Fatalf("strict missing table error = %v", err)
	}
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: filepath.Join(dir, "pk"), IgnoreColumns: []string{"id"}, Strict: true}); err == nil || !strings.Contains(err.Error(), "primary key column") {
		t.Fatalf("strict ignored primary key error = %v", err)
	}
}

func TestGenerateModelFromDDLSoftDeleteStrictAndTabler(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE users (
  id bigint primary key,
  name varchar(64) not null,
  deleted_at datetime,
  UNIQUE KEY uk_users_name (name)
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model", Module: "example.com/usersvc", Strict: true}); err != nil {
		t.Fatal(err)
	}
	tablerData, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "tabler_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tablerData), "type Tabler interface") || !strings.Contains(string(tablerData), "TableName() string") {
		t.Fatalf("generated tabler file = %s", tablerData)
	}
	entityData, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "user_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entityData), "var _ Tabler = (*User)(nil)") || !strings.Contains(string(entityData), "func (User) TableName() string") {
		t.Fatalf("generated entity should include Tabler assertion and TableName:\n%s", entityData)
	}
	repoData, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	repoOut := string(repoData)
	for _, want := range []string{
		`"time"`,
		`AND deleted_at IS NULL LIMIT 1`,
		`query += " AND deleted_at IS NULL"`,
		`UPDATE " + entity.UserTable + " SET deleted_at = " + storage.Placeholder(r.dialect, 1)`,
		`time.Now()`,
		`WHERE deleted_at IS NULL ORDER BY id LIMIT`,
		`SELECT COUNT(*) FROM " + entity.UserTable + " WHERE deleted_at IS NULL`,
		`where = where.IsNull("deleted_at")`,
		`func (r *UserRepo) FindByName(ctx context.Context, name string) (*entity.User, error)`,
	} {
		if !strings.Contains(repoOut, want) {
			t.Fatalf("generated soft-delete repo missing %q:\n%s", want, repoOut)
		}
	}
	if strings.Contains(repoOut, `"deleted_at": in.DeletedAt`) {
		t.Fatalf("generated update should not write soft-delete column from input:\n%s", repoOut)
	}

	badDDLPath := filepath.Join(dir, "bad.sql")
	badDDL := `CREATE TABLE events (
  id bigint primary key,
  shape geometry not null
);`
	if err := os.WriteFile(badDDLPath, []byte(badDDL), 0o644); err != nil {
		t.Fatal(err)
	}
	err = GenerateModelFromDDL(ModelOptions{DDLFile: badDDLPath, Dir: filepath.Join(dir, "bad"), Strict: true})
	if err == nil || !strings.Contains(err.Error(), `unknown column type "geometry" for events.shape`) {
		t.Fatalf("strict unknown type error = %v", err)
	}
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: badDDLPath, Dir: filepath.Join(dir, "mapped"), Strict: true, TypesMap: map[string]string{"geometry": "string"}}); err != nil {
		t.Fatalf("strict generation with types map should pass: %v", err)
	}
}

func TestGenerateModelFromDDLInfersModuleImport(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE users (
  id bigint primary key,
  name varchar(64) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "go.mod"), []byte("module example.com/shop\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir}); err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repo), `"example.com/shop/model/entity"`) {
		t.Fatalf("generated repo import = %s", repo)
	}
}

func TestGenerateModelFromDDLGORMStyle(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE users (
  id bigint primary key,
  name varchar(64) not null,
  age int
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "go.mod"), []byte("module example.com/shop\n\nrequire github.com/gofly/gofly v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model", Module: "example.com/shop", Style: "gorm"}); err != nil {
		t.Fatal(err)
	}
	goModData, err := os.ReadFile(filepath.Join(outDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goModData), "require gorm.io/gorm ") {
		t.Fatalf("generated gorm go.mod should include gorm dependency:\n%s", goModData)
	}
	entityData, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "user_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	entityOut := string(entityData)
	for _, want := range []string{
		`gorm:"column:id;primaryKey"`,
		`gorm:"column:name"`,
		"func (User) TableName() string { return UserTable }",
	} {
		if !strings.Contains(entityOut, want) {
			t.Fatalf("generated gorm entity missing %q:\n%s", want, entityOut)
		}
	}
	repoData, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	repoOut := string(repoData)
	for _, want := range []string{
		`"gorm.io/gorm"`,
		`"github.com/gofly/gofly/core/storage"`,
		`"example.com/shop/model/entity"`,
		"type UserRepo struct",
		"db *gorm.DB",
		"func NewUserRepo(db *gorm.DB) *UserRepo",
		"func (r *UserRepo) WithDB(db *gorm.DB) *UserRepo",
		"func (r *UserRepo) Transact(ctx context.Context, fn func(context.Context, *UserRepo) error) error",
		"func (r *UserRepo) dbWithContext(ctx context.Context) (*gorm.DB, error)",
		`return r.db.WithContext(ctx).Table(entity.UserTable), nil`,
		`db.Where("id = ?", id).First(&out).Error`,
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return nil, storage.ErrNotFound",
		`return db.Create(in).Error`,
		`Updates(map[string]any{"name": in.Name, "age": in.Age}).Error`,
		`db.Order("id ASC").Limit(limit).Offset(offset).Find(&out).Error`,
		`db.Model(&entity.User{}).Count(&count).Error`,
		"func (r *UserRepo) FindWhere(ctx context.Context, where any, args ...any) ([]entity.User, error)",
		"func (r *UserRepo) CountWhere(ctx context.Context, where any, args ...any) (int64, error)",
	} {
		if !strings.Contains(repoOut, want) {
			t.Fatalf("generated gorm repo missing %q:\n%s", want, repoOut)
		}
	}
}

func TestGenerateModelFromDDLGoZeroStyleDoesNotRequireGORM(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE users (
  id bigint primary key,
  name varchar(64) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(outDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module example.com/shop\n\nrequire github.com/gofly/gofly v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateModelFromDDL(ModelOptions{
		DDLFile: ddlPath,
		Dir:     outDir,
		Package: "model",
		Module:  "example.com/shop",
		Style:   "go_zero",
	}); err != nil {
		t.Fatal(err)
	}
	goModData, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goModData), "gorm.io/gorm") {
		t.Fatalf("go_zero generated go.mod should not include gorm dependency:\n%s", goModData)
	}
}

func TestGenerateModelFromDDLGORMStyleFindsParentGoMod(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE orders (
  id bigint primary key,
  user_id bigint not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "internal")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model", Style: "gorm"}); err != nil {
		t.Fatal(err)
	}
	goModData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goModData), "require gorm.io/gorm ") {
		t.Fatalf("parent go.mod should include gorm dependency:\n%s", goModData)
	}
	repoData, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "order.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repoData), `"example.com/shop/internal/model/entity"`) {
		t.Fatalf("repo should import entity relative to parent module root:\n%s", repoData)
	}
}

func TestGenerateMongoModelDriverStyle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\nrequire github.com/gofly/gofly v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateMongoModel(MongoModelOptions{Type: "UserProfile", Dir: dir, Package: "model", Cache: true, Style: "driver"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		`"go.mongodb.org/mongo-driver/bson"`,
		`"go.mongodb.org/mongo-driver/bson/primitive"`,
		`"go.mongodb.org/mongo-driver/mongo"`,
		`"go.mongodb.org/mongo-driver/mongo/options"`,
		"type UserProfileRepo struct",
		"collection *mongo.Collection",
		"func NewCachedUserProfileRepo(repo *UserProfileRepo, opts ...cache.ModelOption[*UserProfile, string]) *cache.ModelCache[*UserProfile, string]",
		"func (r *UserProfileRepo) FindByHexID(ctx context.Context, id string) (*UserProfile, error)",
		"primitive.ObjectIDFromHex(id)",
		"collection.Find(ctx, filter, findOpts)",
		"collection.UpdateOne(ctx, bson.M{\"_id\": id}, bson.M{\"$set\": value})",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated mongo driver model missing %q:\n%s", want, out)
		}
	}
	goModData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goModData), "require go.mongodb.org/mongo-driver ") {
		t.Fatalf("mongo driver go.mod should include mongo dependency:\n%s", goModData)
	}
}

func TestGenerateModelFromDDLGORMSoftDeleteAndLegacyMongo(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE user_profiles (
  id bigint primary key,
  name varchar(64) not null,
  deleted_at datetime,
  created_at datetime
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "gorm_soft")
	if err := GenerateModelFromDDL(ModelOptions{DDLFile: ddlPath, Dir: outDir, Package: "model", Module: "example.com/usersvc/gorm_soft", Style: "gorm", Strict: true}); err != nil {
		t.Fatalf("GenerateModelFromDDL gorm soft delete: %v", err)
	}
	entityData, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "user_profile_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DeletedAt *time.Time", `gorm:"column:deleted_at"`, "var _ Tabler = (*UserProfile)(nil)"} {
		if !strings.Contains(string(entityData), want) {
			t.Fatalf("gorm soft entity missing %q:\n%s", want, entityData)
		}
	}
	repoData, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"time"`,
		`db = db.Where("deleted_at IS NULL")`,
		`return db.Model(&entity.UserProfile{}).Where("id = ?", id).Where("deleted_at IS NULL").Update("deleted_at", time.Now().UTC()).Error`,
		`func (r *UserProfileRepo) FindWhere(ctx context.Context, where any, args ...any) ([]entity.UserProfile, error)`,
		`func (r *UserProfileRepo) CountWhere(ctx context.Context, where any, args ...any) (int64, error)`,
	} {
		if !strings.Contains(string(repoData), want) {
			t.Fatalf("gorm soft repo missing %q:\n%s", want, repoData)
		}
	}

	legacyDir := filepath.Join(dir, "legacy_mongo")
	if err := GenerateMongoModel(MongoModelOptions{Type: "UserProfile", Dir: legacyDir, Package: "model", Cache: true}); err != nil {
		t.Fatalf("GenerateMongoModel legacy: %v", err)
	}
	legacyData, err := os.ReadFile(filepath.Join(legacyDir, "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"github.com/gofly/gofly/cache"`,
		"type MongoCollection[T any] interface",
		"func NewCachedUserProfileRepo",
		"func (r *UserProfileRepo) FindMany",
		"func (r *UserProfileRepo) Delete",
	} {
		if !strings.Contains(string(legacyData), want) {
			t.Fatalf("legacy mongo model missing %q:\n%s", want, legacyData)
		}
	}

	if datasourceDriverName("postgres") != "pgx" || datasourceDriverName("postgresql") != "pgx" || datasourceDriverName("mysql") != "mysql" {
		t.Fatalf("datasource driver aliases are not normalized")
	}
}

func TestDatasourceColumnsQuery(t *testing.T) {
	query, args, err := datasourceColumnsQuery("mysql", []string{"users", "orders", "users"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "table_schema = DATABASE()") || !strings.Contains(query, "table_name IN (?,?)") {
		t.Fatalf("mysql query = %s", query)
	}
	if len(args) != 2 || args[0] != "orders" || args[1] != "users" {
		t.Fatalf("mysql args = %#v, want sorted unique tables", args)
	}

	query, args, err = datasourceColumnsQuery("postgres", []string{"accounts", "users"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "current_schema()") || !strings.Contains(query, "c.table_name IN ($1,$2)") {
		t.Fatalf("postgres query = %s", query)
	}
	if len(args) != 2 || args[0] != "accounts" || args[1] != "users" {
		t.Fatalf("postgres args = %#v, want sorted tables", args)
	}

	if _, _, err := datasourceColumnsQuery("sqlite", nil); err == nil || !strings.Contains(err.Error(), "unsupported datasource driver") {
		t.Fatalf("unsupported driver error = %v", err)
	}
}

func TestGenerateMigrationAndCompletionScripts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations")
	when := time.Date(2026, 6, 14, 9, 8, 7, 0, time.UTC)
	if err := GenerateMigration(MigrationOptions{Name: "Create Users!", Dir: dir, Time: when}); err != nil {
		t.Fatalf("GenerateMigration: %v", err)
	}
	up := filepath.Join(dir, "20260614090807_create_users.up.sql")
	down := filepath.Join(dir, "20260614090807_create_users.down.sql")
	for _, path := range []string{up, down} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if !strings.Contains(string(data), "migration SQL") {
			t.Fatalf("migration file %s = %s", path, data)
		}
	}
	if err := GenerateMigration(MigrationOptions{Name: "   "}); err == nil || !strings.Contains(err.Error(), "migration name is required") {
		t.Fatalf("blank migration error = %v", err)
	}
	if migrationName(" --- ") != "migration" || migrationName("Add-User Table") != "add_user_table" {
		t.Fatalf("migrationName normalization failed")
	}

	for _, tt := range []struct {
		shell string
		want  []string
	}{
		{shell: "bash", want: []string{`commands="version new gen generate handler rpc api model docker kube template quickstart migrate migration env bug upgrade config feature plugin completion complete release doctor example examples ai tools"`, `plugin) commands="list ls install uninstall remove rm run"`, `ai|tools) commands="manifest complete stream doctor"`}},
		{shell: "zsh", want: []string{`'plugin:list, install or run gofly plugins'`, `plugin) commands=('list:list plugins'`, `ai|tools) commands=('manifest:print AI tool manifest' 'complete:run governed noop completion' 'stream:run governed streaming completion' 'doctor:run AI subsystem diagnostics')`}},
		{shell: "fish", want: []string{`complete -c gofly -n '__fish_seen_subcommand_from plugin' -a "list\tList plugins`, `complete -c gofly -n '__fish_seen_subcommand_from ai' -a "manifest\tPrint AI tool manifest\ncomplete\tRun governed noop completion\nstream\tRun governed streaming completion\ndoctor\tRun AI subsystem diagnostics"`}},
		{shell: "powershell", want: []string{`"plugin" { $commands = @("list", "ls", "install", "uninstall", "remove", "rm", "run") }`, `"ai" { $commands = @("manifest", "complete", "stream", "doctor") }`}},
		{shell: "pwsh", want: []string{`Register-ArgumentCompleter -Native -CommandName gofly`}},
	} {
		t.Run(tt.shell, func(t *testing.T) {
			script, err := GenerateCompletion(tt.shell)
			if err != nil {
				t.Fatalf("GenerateCompletion(%q): %v", tt.shell, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(script, want) {
					t.Fatalf("completion %s missing %q:\n%s", tt.shell, want, script)
				}
			}
		})
	}
	if _, err := GenerateCompletion("elvish"); err == nil || !strings.Contains(err.Error(), "unsupported completion shell") {
		t.Fatalf("unsupported completion error = %v", err)
	}
}

func TestDatasourceColumnsQueryWithScope(t *testing.T) {
	query, args, err := datasourceColumnsQueryWithScope(datasourceIntrospectionOptions{Driver: "mysql", Database: "app", Tables: []string{"users"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "table_schema = ?") || strings.Contains(query, "DATABASE()") {
		t.Fatalf("mysql scoped query = %s", query)
	}
	if len(args) != 2 || args[0] != "app" || args[1] != "users" {
		t.Fatalf("mysql scoped args = %#v", args)
	}

	query, args, err = datasourceColumnsQueryWithScope(datasourceIntrospectionOptions{Driver: "postgres", Schema: "public", Tables: []string{"accounts", "users"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "c.table_schema = $1") || !strings.Contains(query, "c.table_name IN ($2,$3)") {
		t.Fatalf("postgres scoped query = %s", query)
	}
	if len(args) != 3 || args[0] != "public" || args[1] != "accounts" || args[2] != "users" {
		t.Fatalf("postgres scoped args = %#v", args)
	}
}

func TestNormalizeDatasourceType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "postgres varchar", in: "character varying", want: "varchar"},
		{name: "postgres timestamptz", in: "timestamp with time zone", want: "timestamptz"},
		{name: "postgres timestamp", in: "timestamp without time zone", want: "timestamp"},
		{name: "postgres double", in: "double precision", want: "double"},
		{name: "mysql passthrough", in: "BIGINT", want: "bigint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDatasourceType(tt.in); got != tt.want {
				t.Fatalf("normalizeDatasourceType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGenerateRPCFromProto(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateRPCFromProto(RPCOptions{ProtoFile: protoPath, Dir: outDir, Package: "greeterv1"}); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(outDir, "greeter.gofly.go")
	data, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package greeterv1") {
		t.Fatalf("generated file = %s", data)
	}
}

func TestGenerateRPCFromProtoNoClientAndMultiple(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "multi.proto")
	protoContent := `syntax = "proto3";
package demo.v1;
message PingReq {
  string name = 1;
}
message PingResp {
  string message = 1;
}
service Greeter {
  rpc Ping(PingReq) returns (PingResp);
}
service Health {
  rpc Check(PingReq) returns (PingResp);
}
`
	if err := os.WriteFile(protoPath, []byte(protoContent), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateRPCFromProto(RPCOptions{ProtoFile: protoPath, Dir: outDir, Package: "demov1", NoClient: true, Multiple: true}); err != nil {
		t.Fatal(err)
	}
	greeterData, err := os.ReadFile(filepath.Join(outDir, "greeter", "multi.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(greeterData), "type Greeter interface") || strings.Contains(string(greeterData), "GreeterClient") {
		t.Fatalf("greeter multiple/no-client output:\n%s", greeterData)
	}
	healthData, err := os.ReadFile(filepath.Join(outDir, "health", "multi.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(healthData), "type Health interface") || strings.Contains(string(healthData), "HealthClient") {
		t.Fatalf("health multiple/no-client output:\n%s", healthData)
	}
}

func TestProtocArgs(t *testing.T) {
	args, err := ProtocArgs(ProtocOptions{ProtoFile: "api/greeter.proto", ProtoPath: []string{"api", "third_party"}, GoOut: "gen", GoGRPCOut: "grpcgen", ExtraArgs: []string{"--experimental_allow_proto3_optional"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-I", "api", "-I", "third_party",
		"--go_out=gen", "--go_opt=paths=source_relative",
		"--go-grpc_out=grpcgen", "--go-grpc_opt=paths=source_relative",
		"--experimental_allow_proto3_optional", "api/greeter.proto",
	}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestGenerateStandardProtoTimesOutHungProtoc(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := GenerateStandardProto(context.Background(), ProtocOptions{
		ProtoFile: protoPath,
		GoOut:     dir,
		GoGRPCOut: dir,
		Protoc:    fakeProtoc,
		Timeout:   20 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("GenerateStandardProto timeout err = %v, want deadline exceeded timeout", err)
	}
}

func TestGenerateStandardProtoRespectsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(testProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := GenerateStandardProto(ctx, ProtocOptions{
		ProtoFile: protoPath,
		GoOut:     dir,
		GoGRPCOut: dir,
		Protoc:    fakeProtoc,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateStandardProto canceled err = %v, want context canceled", err)
	}
}

func TestProtocArgsUserPathOptionsOverrideDefaults(t *testing.T) {
	args, err := ProtocArgs(ProtocOptions{
		ProtoFile: "api/greeter.proto",
		GoOut:     "gen",
		GoGRPCOut: "grpcgen",
		ExtraArgs: []string{
			"--go_opt=module=example.com/app",
			"--go-grpc_opt=paths=import",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	argsText := strings.Join(args, "\n")
	for _, unexpected := range []string{
		"--go_opt=paths=source_relative",
		"--go-grpc_opt=paths=source_relative",
	} {
		if strings.Contains(argsText, unexpected) {
			t.Fatalf("args should not contain default %q when user overrides paths/module:\n%s", unexpected, argsText)
		}
	}
	for _, want := range []string{
		"--go_opt=module=example.com/app",
		"--go-grpc_opt=paths=import",
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("args missing %q:\n%s", want, argsText)
		}
	}
}

func TestGenerateAPIFromOpenAPIImport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "petstore.json")
	jsonSpec := `{
  "openapi": "3.0.0",
  "info": {"title": "PetStore"},
  "paths": {
    "/pets/{id}": {
      "get": {
        "operationId": "getPet",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}}}
      }
    }
  }
}`
	if err := os.WriteFile(src, []byte(jsonSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: src, Dir: outDir, Service: "pet-store"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "pet_store.api"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "service pet_store") || !strings.Contains(out, "GetPet") {
		t.Fatalf("generated api missing service or handler:\n%s", out)
	}

	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: ""}); err == nil || !strings.Contains(err.Error(), "openapi source file is required") {
		t.Fatalf("empty source error = %v", err)
	}
	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("expected error for missing source file")
	}

	yamlSrc := filepath.Join(dir, "minimal.yaml")
	if err := os.WriteFile(yamlSrc, []byte("openapi: \"3.0.0\"\ninfo:\n  title: Minimal\npaths:\n  /ok:\n    get:\n      operationId: ok\n      responses:\n        '200':\n          description: OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIFromOpenAPI(APIImportOptions{Source: yamlSrc, Dir: outDir, Service: ""}); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateAPIDiffAndRoutes(t *testing.T) {
	dir := t.TempDir()
	baseAPI := filepath.Join(dir, "base.api")
	targetAPI := filepath.Join(dir, "target.api")
	if err := os.WriteFile(baseAPI, []byte(testAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	target := testAPI + `
type Extra {
    Value string
}
`
	if err := os.WriteFile(targetAPI, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "out")
	if err := GenerateAPIDiff(APIDiffOptions{Base: baseAPI, Target: targetAPI, Dir: outDir, Format: "json"}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIDiff(APIDiffOptions{Base: baseAPI, Target: targetAPI, Dir: outDir, Format: "md"}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIDiff(APIDiffOptions{Base: baseAPI, Target: targetAPI, Dir: outDir, Format: "text"}); err != nil {
		t.Fatal(err)
	}

	if err := GenerateAPIDiff(APIDiffOptions{Base: "", Target: targetAPI}); err == nil || !strings.Contains(err.Error(), "base api file is required") {
		t.Fatalf("empty base error = %v", err)
	}
	if err := GenerateAPIDiff(APIDiffOptions{Base: baseAPI, Target: "", Format: "bad"}); err == nil || !strings.Contains(err.Error(), "target api file is required") {
		t.Fatalf("empty target error = %v", err)
	}
	if err := GenerateAPIDiff(APIDiffOptions{Base: baseAPI, Target: targetAPI, Format: "xml"}); err == nil || !strings.Contains(err.Error(), "unsupported api diff format") {
		t.Fatalf("bad format error = %v", err)
	}

	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: baseAPI, Dir: outDir, Format: "json"}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: baseAPI, Dir: outDir, Format: "md"}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: baseAPI, Dir: outDir, Format: "text"}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: "", Format: "bad"}); err == nil || !strings.Contains(err.Error(), "api file is required") {
		t.Fatalf("empty api file error = %v", err)
	}
	badAPI := filepath.Join(dir, "bad.api")
	if err := os.WriteFile(badAPI, []byte("service s {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: badAPI, Format: "text"}); err == nil || !strings.Contains(err.Error(), "api route is required") {
		t.Fatalf("no routes error = %v", err)
	}
	if err := GenerateAPIRoutes(APIRouteOptions{APIFile: baseAPI, Format: "xml"}); err == nil || !strings.Contains(err.Error(), "unsupported api route format") {
		t.Fatalf("bad route format error = %v", err)
	}
}

func TestGenerateAPITypesAndClient(t *testing.T) {
	dir := t.TempDir()
	apiFile := filepath.Join(dir, "types.api")
	api := `type UserReq {
    Name string
}

type UserResp {
    Id int64
}

service user-api {
    @handler GetUser
    get /users/:id (UserReq) returns (UserResp)
}
`
	if err := os.WriteFile(apiFile, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")

	if err := GenerateAPITypes(APITypesOptions{APIFile: apiFile, Dir: outDir, Package: "mypkg"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package mypkg") {
		t.Fatalf("types package = %s", data)
	}

	if err := GenerateAPITypes(APITypesOptions{APIFile: "", Package: "x"}); err == nil || !strings.Contains(err.Error(), "api file is required") {
		t.Fatalf("empty api file error = %v", err)
	}
	emptyAPI := filepath.Join(dir, "empty.api")
	if err := os.WriteFile(emptyAPI, []byte("service s {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPITypes(APITypesOptions{APIFile: emptyAPI, Package: "x"}); err == nil || !strings.Contains(err.Error(), "api type is required") {
		t.Fatalf("empty types error = %v", err)
	}

	for _, lang := range []string{"typescript", "ts", "javascript", "js", "dart", "java", "kotlin", "kt"} {
		if err := GenerateAPIClient(APIClientOptions{APIFile: apiFile, Dir: outDir, Language: lang}); err != nil {
			t.Fatalf("client %s: %v", lang, err)
		}
	}
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiFile, Dir: outDir, Language: ""}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIClient(APIClientOptions{APIFile: "", Language: "ts"}); err == nil || !strings.Contains(err.Error(), "api file is required") {
		t.Fatalf("empty client api error = %v", err)
	}
	if err := GenerateAPIClient(APIClientOptions{APIFile: apiFile, Dir: outDir, Language: "ruby"}); err == nil || !strings.Contains(err.Error(), "unsupported api client language") {
		t.Fatalf("bad lang error = %v", err)
	}
}

func TestGenerateAPIDocFormats(t *testing.T) {
	dir := t.TempDir()
	apiFile := filepath.Join(dir, "doc.api")
	if err := os.WriteFile(apiFile, []byte(testAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	for _, format := range []string{"markdown", "md", "openapi", "openapi3", "yaml", "yml"} {
		if err := GenerateAPIDoc(APIDocOptions{APIFile: apiFile, Dir: outDir, Format: format}); err != nil {
			t.Fatalf("doc format %s: %v", format, err)
		}
	}
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiFile, Dir: outDir, Format: ""}); err != nil {
		t.Fatal(err)
	}
	if err := GenerateAPIDoc(APIDocOptions{APIFile: "", Format: "md"}); err == nil || !strings.Contains(err.Error(), "api file is required") {
		t.Fatalf("empty doc api error = %v", err)
	}
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiFile, Dir: outDir, Format: "xml"}); err == nil || !strings.Contains(err.Error(), "unsupported api doc format") {
		t.Fatalf("bad doc format error = %v", err)
	}
}

func TestOpenAPIFieldNameAndRequired(t *testing.T) {
	f := IDLField{Name: "UserName", Tag: `json:"user_name,omitempty"`}
	if got := openAPIFieldName(f); got != "user_name" {
		t.Fatalf("openAPIFieldName json = %q, want user_name", got)
	}
	if openAPIFieldRequired(f) {
		t.Fatal("expected not required with omitempty")
	}
	f2 := IDLField{Name: "ID", Tag: `path:"id"`}
	if got := openAPIFieldName(f2); got != "id" {
		t.Fatalf("openAPIFieldName path = %q, want id", got)
	}
	f3 := IDLField{Name: "Query", Tag: `form:"q"`}
	if got := openAPIFieldName(f3); got != "q" {
		t.Fatalf("openAPIFieldName form = %q, want q", got)
	}
	f4 := IDLField{Name: "RawName"}
	if got := openAPIFieldName(f4); got != "rawName" {
		t.Fatalf("openAPIFieldName default = %q, want rawName", got)
	}
	f5 := IDLField{Name: "Required", Tag: `json:"req"`}
	if !openAPIFieldRequired(f5) {
		t.Fatal("expected required without omitempty")
	}
	f6 := IDLField{Name: "Optional", Tag: `json:"opt" optional:"true"`}
	if openAPIFieldRequired(f6) {
		t.Fatal("expected not required with optional tag")
	}
}

func TestAPIDocExt(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{"openapi", ".json"},
		{"swagger", ".json"},
		{"yaml", ".yaml"},
		{"yml", ".yaml"},
		{"markdown", ".md"},
		{"", ".md"},
	}
	for _, tt := range tests {
		if got := apiDocExt(tt.format); got != tt.want {
			t.Fatalf("apiDocExt(%q) = %q, want %q", tt.format, got, tt.want)
		}
	}
}

func TestJavaBoxedType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"string", "String"},
		{"bool", "Boolean"},
		{"int", "Integer"},
		{"int8", "Integer"},
		{"int16", "Integer"},
		{"int32", "Integer"},
		{"int64", "Long"},
		{"uint", "Long"},
		{"uint8", "Integer"},
		{"uint16", "Integer"},
		{"uint32", "Integer"},
		{"uint64", "Long"},
		{"float32", "Float"},
		{"float64", "Double"},
		{"User", "User"},
	}
	for _, tt := range tests {
		if got := javaBoxedType(tt.in); got != tt.want {
			t.Fatalf("javaBoxedType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestKotlinType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"string", "String"},
		{"bool", "Boolean"},
		{"int", "Int"},
		{"int64", "Long"},
		{"uint", "Long"},
		{"float32", "Float"},
		{"float64", "Double"},
		{"[]string", "List<String>"},
		{"User", "User"},
	}
	for _, tt := range tests {
		if got := kotlinType(tt.in); got != tt.want {
			t.Fatalf("kotlinType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAPIGoType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"string", "string"},
		{"bool", "bool"},
		{"int", "int"},
		{"int32", "int32"},
		{"uint64", "uint64"},
		{"float32", "float32"},
		{"float64", "float64"},
		{"[]string", "[]string"},
		{"User", "User"},
	}
	for _, tt := range tests {
		if got := apiGoType(tt.in); got != tt.want {
			t.Fatalf("apiGoType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatAPIFromFileBranches(t *testing.T) {
	dir := t.TempDir()
	apiFile := filepath.Join(dir, "fmt.api")
	if err := os.WriteFile(apiFile, []byte(testAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(dir, "out.api")

	_, err := FormatAPIFromFile(APIFormatOptions{APIFile: apiFile, Output: outFile})
	if err != nil {
		t.Fatal(err)
	}
	_, err = FormatAPIFromFile(APIFormatOptions{APIFile: apiFile, Write: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = FormatAPIFromFile(APIFormatOptions{Dir: dir, Write: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := FormatAPIFromFile(APIFormatOptions{}); err == nil || !strings.Contains(err.Error(), "api file is required") {
		t.Fatalf("empty opts error = %v", err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{APIFile: apiFile, Dir: dir, Output: outFile}); err == nil || !strings.Contains(err.Error(), "api format output cannot be used with dir") {
		t.Fatalf("output+dir error = %v", err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{Dir: dir, Output: outFile}); err == nil || !strings.Contains(err.Error(), "api format output cannot be used with dir") {
		t.Fatalf("dir+output error = %v", err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{Dir: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("expected error for missing dir")
	}
	notDir := filepath.Join(dir, "notdir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FormatAPIFromFile(APIFormatOptions{Dir: notDir}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("not dir error = %v", err)
	}
}

func TestGenerateRESTFromAPIValidation(t *testing.T) {
	if err := GenerateRESTFromAPI(APIOptions{}); err == nil || !strings.Contains(err.Error(), "api file is required") {
		t.Fatalf("empty api file error = %v", err)
	}
	if err := GenerateRESTFromAPI(APIOptions{APIFile: "/missing/file.api"}); err == nil {
		t.Fatal("expected error for missing api file")
	}
}

func TestServiceFilesystemSinkWriteRenderedGoFormatError(t *testing.T) {
	dir := t.TempDir()
	sink := serviceFilesystemSink{Dir: dir}
	badGo := scaffoldRenderedFile{Path: "bad.go", Content: "package main\nfunc {\n"}
	if err := sink.WriteRendered([]scaffoldRenderedFile{badGo}); err == nil {
		t.Fatal("expected format error for bad Go file")
	}
}

func TestServiceFilesystemSinkRunPluginsStderr(t *testing.T) {
	dir := t.TempDir()
	var buf strings.Builder
	sink := serviceFilesystemSink{Dir: dir, Stderr: &buf}
	ir := serviceScaffoldIR{Name: "test", Module: "example.com/test", Dir: dir, Kind: "service", Plugins: []string{}}
	if err := sink.RunPlugins(ir); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPIDefaultExampleAndExampleValue(t *testing.T) {
	names := map[string]struct{}{"User": {}}
	if got := openAPIDefaultExample("[]string", names); !reflect.DeepEqual(got, []any{"string"}) {
		t.Fatalf("default example []string = %v", got)
	}
	if got := openAPIDefaultExample("User", names); !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("default example User = %v", got)
	}
	if got := openAPIDefaultExample("bool", names); got != true {
		t.Fatalf("default example bool = %v", got)
	}
	if got := openAPIExampleValue("[]int", "3", names); !reflect.DeepEqual(got, []any{1}) {
		t.Fatalf("example value []int = %v", got)
	}
	if got := openAPIExampleValue("User", "x", names); !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("example value User = %v", got)
	}
	if got := openAPIExampleValue("bool", "true", names); got != true {
		t.Fatalf("example value bool true = %v", got)
	}
	if got := openAPIExampleValue("bool", "false", names); got != false {
		t.Fatalf("example value bool false = %v", got)
	}
}

func TestAPIEmptyDashAndDiffListPrefix(t *testing.T) {
	if got := apiEmptyDash(""); got != "-" {
		t.Fatalf("apiEmptyDash(\"\") = %q, want -", got)
	}
	if got := apiEmptyDash("x"); got != "x" {
		t.Fatalf("apiEmptyDash(\"x\") = %q, want x", got)
	}
	if got := apiDiffListPrefix("Added routes"); got != "+" {
		t.Fatalf("apiDiffListPrefix added = %q, want +", got)
	}
	if got := apiDiffListPrefix("Removed routes"); got != "-" {
		t.Fatalf("apiDiffListPrefix removed = %q, want -", got)
	}
}

func TestOpenAPISchema(t *testing.T) {
	names := map[string]struct{}{"User": {}}
	if got := openAPISchema("[]string", names); !reflect.DeepEqual(got, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}) {
		t.Fatalf("openAPISchema []string = %v", got)
	}
	if got := openAPISchema("User", names); !reflect.DeepEqual(got, map[string]any{"$ref": "#/components/schemas/User"}) {
		t.Fatalf("openAPISchema User = %v", got)
	}
	if got := openAPISchema("int32", names); !reflect.DeepEqual(got, map[string]any{"type": "integer", "format": "int32"}) {
		t.Fatalf("openAPISchema int32 = %v", got)
	}
	if got := openAPISchema("float64", names); !reflect.DeepEqual(got, map[string]any{"type": "number", "format": "double"}) {
		t.Fatalf("openAPISchema float64 = %v", got)
	}
}

func TestOpenAPITypeAndDartType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"bool", "boolean"},
		{"int", "integer"},
		{"int32", "integer"},
		{"float32", "number"},
		{"float64", "number"},
		{"string", "string"},
		{"[]int", "integer"},
	}
	for _, tt := range tests {
		if got := openAPIType(tt.in); got != tt.want {
			t.Fatalf("openAPIType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	dartTests := []struct {
		in   string
		want string
	}{
		{"bool", "bool"},
		{"int", "int"},
		{"float32", "double"},
		{"[]string", "List<String>"},
		{"User", "User"},
	}
	for _, tt := range dartTests {
		if got := dartType(tt.in); got != tt.want {
			t.Fatalf("dartType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	if got := dartFromJSON("[]string", "x"); got != `(x as List<dynamic>?)?.map((e) => e as String?).toList()` {
		t.Fatalf("dartFromJSON []string = %q", got)
	}
	if got := dartFromJSON("User", "x"); got != `User.fromJson(x as Map<String, dynamic>)` {
		t.Fatalf("dartFromJSON User = %q", got)
	}

	if got := typeScriptType("[]User"); got != "User[]" {
		t.Fatalf("typeScriptType []User = %q, want User[]", got)
	}
	if got := typeScriptType("float32"); got != "number" {
		t.Fatalf("typeScriptType float32 = %q, want number", got)
	}
}

func TestValidIdentifier(t *testing.T) {
	if !ValidIdentifier("hello") {
		t.Fatal("expected hello to be valid")
	}
	if ValidIdentifier("123") {
		t.Fatal("expected 123 to be invalid")
	}
	if ValidIdentifier("") {
		t.Fatal("expected empty to be invalid")
	}
	if ValidIdentifier("hello-world") {
		t.Fatal("expected hello-world to be invalid")
	}
}

func TestDefaultConfigAndLoadConfig(t *testing.T) {
	cfg := DefaultConfig("svc", "example.com/svc")
	if cfg.ServiceName != "svc" || cfg.Module != "example.com/svc" {
		t.Fatalf("DefaultConfig = %+v", cfg)
	}
	features := DefaultConfigFeatures()
	if len(features) == 0 {
		t.Fatal("expected default features")
	}

	cfg2, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.ServiceName != "" {
		t.Fatalf("LoadConfig empty = %+v", cfg2)
	}

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	cfg3, err := LoadConfig(missing)
	if err != nil {
		t.Fatal(err)
	}
	if cfg3.ServiceName != "" {
		t.Fatalf("LoadConfig missing = %+v", cfg3)
	}

	badJSON := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(badJSON); err == nil {
		t.Fatal("expected error for bad json")
	}

	goodJSON := filepath.Join(dir, "good.json")
	if err := os.WriteFile(goodJSON, []byte(`{"serviceName":"x","module":"example.com/x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg4, err := LoadConfig(goodJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cfg4.ServiceName != "x" {
		t.Fatalf("LoadConfig good = %+v", cfg4)
	}

	if cfg4.String() == "" {
		t.Fatal("expected non-empty String()")
	}
	if (&Config{}).String() == "" {
		t.Fatal("expected non-empty String() for empty config")
	}

	opts := cfg4.ResolveServiceOptions("", "", "/tmp", "")
	if opts.Name != "x" || opts.Module != "example.com/x" || opts.Dir != "/tmp" {
		t.Fatalf("ResolveServiceOptions = %+v", opts)
	}
}

func TestProtocArgsWithGoflyPlugin(t *testing.T) {
	args, err := ProtocArgs(ProtocOptions{
		ProtoFile:    "api/greeter.proto",
		ProtoPath:    []string{"api"},
		GoOut:        "gen",
		GoGRPCOut:    "grpcgen",
		GoflyOut:     "zrpc",
		GoflyPlugin:  "/tmp/gofly",
		GoflyOptions: []string{"paths=source_relative", "module=example.com/app", "name_from_filename=true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-I", "api",
		"--go_out=gen", "--go_opt=paths=source_relative",
		"--go-grpc_out=grpcgen", "--go-grpc_opt=paths=source_relative",
		"--plugin=protoc-gen-gofly=/tmp/gofly",
		"--gofly_out=zrpc",
		"--gofly_opt=paths=source_relative",
		"--gofly_opt=module=example.com/app",
		"--gofly_opt=name_from_filename=true",
		"api/greeter.proto",
	}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
