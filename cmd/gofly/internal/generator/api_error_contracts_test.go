package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIGeneratorInputErrorContracts(t *testing.T) {
	dir := t.TempDir()
	validAPI := writeGeneratorContractFile(t, dir, "service.api", `
type Request {
  ID string
}
type Response {
  Name string
}
service UserService {
  @handler getUser
  get /users/{id} (Request) returns (Response)
}
`)
	typesOnlyAPI := writeGeneratorContractFile(t, dir, "types.api", `
type User {
  ID string
}
`)
	serviceOnlyAPI := writeGeneratorContractFile(t, dir, "service-only.api", `
service HealthService {
  @handler health
  get /health returns (Response)
}
`)
	invalidAPI := writeGeneratorContractFile(t, dir, "invalid.api", `type {`)
	validProto := writeGeneratorContractFile(t, dir, "service.proto", `
syntax = "proto3";
package demo;
message Request { string id = 1; }
message Response { string name = 1; }
service UserService {
  rpc GetUser(Request) returns (Response);
}
`)
	invalidProto := writeGeneratorContractFile(t, dir, "invalid.proto", `message {`)
	validOpenAPI := writeGeneratorContractFile(t, dir, "openapi.json", `{
  "openapi":"3.0.3",
  "info":{"title":"demo","version":"1.0.0"},
  "paths":{"/users/{id}":{"get":{
    "operationId":"getUser",
    "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
    "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"}}}}}}}
  }}}
}`)
	invalidOpenAPI := writeGeneratorContractFile(t, dir, "invalid-openapi.json", `{`)
	outputDirectory := filepath.Join(dir, "output-directory")
	if err := os.Mkdir(outputDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.input")

	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "REST missing API",
			run:  func() error { return GenerateRESTFromAPI(APIOptions{}) },
			want: "api file is required",
		},
		{
			name: "REST missing file",
			run:  func() error { return GenerateRESTFromAPI(APIOptions{APIFile: missing}) },
			want: "read api file",
		},
		{
			name: "REST invalid API",
			run:  func() error { return GenerateRESTFromAPI(APIOptions{APIFile: invalidAPI}) },
			want: "parse",
		},
		{
			name: "REST invalid profile",
			run: func() error {
				return GenerateRESTFromAPI(APIOptions{APIFile: validAPI, Dir: dir, Profile: "unknown"})
			},
			want: "unknown generation profile",
		},
		{
			name: "OpenAPI import missing source",
			run:  func() error { return GenerateAPIFromOpenAPI(APIImportOptions{}) },
			want: "openapi source file is required",
		},
		{
			name: "OpenAPI import missing file",
			run:  func() error { return GenerateAPIFromOpenAPI(APIImportOptions{Source: missing}) },
			want: "read openapi file",
		},
		{
			name: "OpenAPI import malformed",
			run:  func() error { return GenerateAPIFromOpenAPI(APIImportOptions{Source: invalidOpenAPI}) },
			want: "openapi",
		},
		{
			name: "OpenAPI import output is directory",
			run: func() error {
				return GenerateAPIFromOpenAPI(APIImportOptions{Source: validOpenAPI, Output: outputDirectory})
			},
			want: "write imported api file",
		},
		{
			name: "API diff missing base",
			run:  func() error { return GenerateAPIDiff(APIDiffOptions{}) },
			want: "base api file is required",
		},
		{
			name: "API diff missing target",
			run:  func() error { return GenerateAPIDiff(APIDiffOptions{Base: validAPI}) },
			want: "target api file is required",
		},
		{
			name: "API diff missing base file",
			run:  func() error { return GenerateAPIDiff(APIDiffOptions{Base: missing, Target: validAPI}) },
			want: "read base api",
		},
		{
			name: "API diff missing target file",
			run:  func() error { return GenerateAPIDiff(APIDiffOptions{Base: validAPI, Target: missing}) },
			want: "read target api",
		},
		{
			name: "API diff unsupported format",
			run: func() error {
				return GenerateAPIDiff(APIDiffOptions{Base: validAPI, Target: validAPI, Format: "xml"})
			},
			want: "unsupported api diff format",
		},
		{
			name: "API diff output is directory",
			run: func() error {
				return GenerateAPIDiff(APIDiffOptions{Base: validAPI, Target: validAPI, Output: outputDirectory})
			},
			want: "write api diff",
		},
		{
			name: "API doc missing API",
			run:  func() error { return GenerateAPIDoc(APIDocOptions{}) },
			want: "api file is required",
		},
		{
			name: "API doc missing file",
			run:  func() error { return GenerateAPIDoc(APIDocOptions{APIFile: missing}) },
			want: "read api file",
		},
		{
			name: "API doc malformed",
			run:  func() error { return GenerateAPIDoc(APIDocOptions{APIFile: invalidAPI}) },
			want: "parse",
		},
		{
			name: "API doc unsupported format",
			run:  func() error { return GenerateAPIDoc(APIDocOptions{APIFile: validAPI, Format: "xml"}) },
			want: "unsupported api doc format",
		},
		{
			name: "API doc output is directory",
			run: func() error {
				return GenerateAPIDoc(APIDocOptions{APIFile: validAPI, Output: outputDirectory})
			},
			want: "write api doc",
		},
		{
			name: "proto doc missing proto",
			run:  func() error { return GenerateProtoDoc(ProtoDocOptions{}) },
			want: "proto file is required",
		},
		{
			name: "proto doc missing file",
			run:  func() error { return GenerateProtoDoc(ProtoDocOptions{ProtoFile: missing}) },
			want: "read proto file",
		},
		{
			name: "proto doc malformed",
			run:  func() error { return GenerateProtoDoc(ProtoDocOptions{ProtoFile: invalidProto}) },
			want: "parse",
		},
		{
			name: "proto doc unsupported format",
			run:  func() error { return GenerateProtoDoc(ProtoDocOptions{ProtoFile: validProto, Format: "xml"}) },
			want: "unsupported proto doc format",
		},
		{
			name: "proto doc output is directory",
			run: func() error {
				return GenerateProtoDoc(ProtoDocOptions{ProtoFile: validProto, Output: outputDirectory})
			},
			want: "write proto doc",
		},
		{
			name: "API client missing API",
			run:  func() error { return GenerateAPIClient(APIClientOptions{}) },
			want: "api file is required",
		},
		{
			name: "API client missing file",
			run:  func() error { return GenerateAPIClient(APIClientOptions{APIFile: missing}) },
			want: "read api file",
		},
		{
			name: "API client malformed",
			run:  func() error { return GenerateAPIClient(APIClientOptions{APIFile: invalidAPI}) },
			want: "parse",
		},
		{
			name: "API client unsupported language",
			run: func() error {
				return GenerateAPIClient(APIClientOptions{APIFile: validAPI, Language: "python"})
			},
			want: "unsupported api client language",
		},
		{
			name: "API client output is directory",
			run: func() error {
				return GenerateAPIClient(APIClientOptions{APIFile: validAPI, Output: outputDirectory})
			},
			want: "write api client",
		},
		{
			name: "API types missing API",
			run:  func() error { return GenerateAPITypes(APITypesOptions{}) },
			want: "api file is required",
		},
		{
			name: "API types missing file",
			run:  func() error { return GenerateAPITypes(APITypesOptions{APIFile: missing}) },
			want: "read api file",
		},
		{
			name: "API types malformed",
			run:  func() error { return GenerateAPITypes(APITypesOptions{APIFile: invalidAPI}) },
			want: "parse",
		},
		{
			name: "API types requires type",
			run:  func() error { return GenerateAPITypes(APITypesOptions{APIFile: serviceOnlyAPI}) },
			want: "api type is required",
		},
		{
			name: "API types output is directory",
			run: func() error {
				return GenerateAPITypes(APITypesOptions{APIFile: typesOnlyAPI, Output: outputDirectory})
			},
			want: "write api types",
		},
		{
			name: "API routes missing API",
			run:  func() error { return GenerateAPIRoutes(APIRouteOptions{}) },
			want: "api file is required",
		},
		{
			name: "API routes missing file",
			run:  func() error { return GenerateAPIRoutes(APIRouteOptions{APIFile: missing}) },
			want: "read api file",
		},
		{
			name: "API routes malformed",
			run:  func() error { return GenerateAPIRoutes(APIRouteOptions{APIFile: invalidAPI}) },
			want: "parse",
		},
		{
			name: "API routes requires route",
			run:  func() error { return GenerateAPIRoutes(APIRouteOptions{APIFile: typesOnlyAPI}) },
			want: "api route is required",
		},
		{
			name: "API routes unsupported format",
			run: func() error {
				return GenerateAPIRoutes(APIRouteOptions{APIFile: validAPI, Format: "xml"})
			},
			want: "unsupported api route format",
		},
		{
			name: "API routes output is directory",
			run: func() error {
				return GenerateAPIRoutes(APIRouteOptions{APIFile: validAPI, Output: outputDirectory})
			},
			want: "write api routes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestAPIFormatErrorContracts(t *testing.T) {
	dir := t.TempDir()
	validAPI := writeGeneratorContractFile(t, dir, "valid.api", `
type User { ID string }
service UserService { get /users returns (User) }
`)
	invalidAPI := writeGeneratorContractFile(t, dir, "invalid.api", `type {`)
	plainFile := writeGeneratorContractFile(t, dir, "plain.txt", "plain")
	missing := filepath.Join(dir, "missing.api")

	tests := []struct {
		name string
		opts APIFormatOptions
		want string
	}{
		{name: "missing input", opts: APIFormatOptions{}, want: "api file is required"},
		{name: "dir and output conflict", opts: APIFormatOptions{Dir: dir, Output: filepath.Join(dir, "out.api")}, want: "output cannot be used with dir"},
		{name: "file dir and output conflict", opts: APIFormatOptions{APIFile: validAPI, Dir: dir, Output: filepath.Join(dir, "out.api")}, want: "output cannot be used with dir"},
		{name: "missing file", opts: APIFormatOptions{APIFile: missing}, want: "read api file"},
		{name: "malformed file", opts: APIFormatOptions{APIFile: invalidAPI}, want: "parse"},
		{name: "missing directory", opts: APIFormatOptions{Dir: filepath.Join(dir, "missing")}, want: "stat api format directory"},
		{name: "path is file", opts: APIFormatOptions{Dir: plainFile}, want: "not a directory"},
		{name: "directory contains malformed API", opts: APIFormatOptions{Dir: dir}, want: "format api directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := FormatAPIFromFile(test.opts)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestGenerationProfilePublicContract(t *testing.T) {
	for _, test := range []struct {
		input string
		want  GenerationProfile
	}{
		{input: "", want: ProfileGoflyAI},
		{input: " GOZERO-COMPATIBLE ", want: ProfileGoZeroCompatible},
		{input: "kitex-compatible", want: ProfileKitexCompatible},
	} {
		got, err := NormalizeGenerationProfile(test.input)
		if err != nil || got != test.want {
			t.Fatalf("NormalizeGenerationProfile(%q) = %q, %v, want %q", test.input, got, err, test.want)
		}
	}
	if _, err := NormalizeGenerationProfile("unknown"); err == nil {
		t.Fatal("unknown generation profile should fail")
	}
}

func writeGeneratorContractFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
