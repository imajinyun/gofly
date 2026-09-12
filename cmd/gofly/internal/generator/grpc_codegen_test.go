package generator

import (
	"bytes"
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateGRPCScaffold(t *testing.T) {
	inputDir, outputDir := t.TempDir(), t.TempDir()
	input := filepath.Join(inputDir, "chat.proto")
	if err := writeGeneratedFileUnder(inputDir, "google/protobuf/empty.proto", []byte(`syntax = "proto3"; package google.protobuf; option go_package = "google.golang.org/protobuf/types/known/emptypb"; message Empty {}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFileUnder(inputDir, "common/types.proto", []byte(`syntax = "proto3"; package common; option go_package = "example.com/chat/internal/common;common"; message Request { string id = 1; }`)); err != nil {
		t.Fatal(err)
	}
	content := `syntax = "proto3";
package chat.v1;
option go_package = "example.com/chat/internal/pb;pb";
import "google/protobuf/empty.proto";
import "common/types.proto";
message Envelope { message Payload { string text = 1; } }
service Chat {
 rpc Send(Envelope.Payload) returns (google.protobuf.Empty);
 rpc Upload(stream Envelope.Payload) returns (google.protobuf.Empty);
 rpc Watch(google.protobuf.Empty) returns (stream Envelope.Payload);
 rpc Talk(stream Envelope.Payload) returns (stream Envelope.Payload);
}
service Admin { rpc Ping(common.Request) returns (google.protobuf.Empty); }
`
	if err := os.WriteFile(input, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := GRPCScaffoldOptions{ProtoFile: input, Dir: outputDir, Module: "example.com/chat", Name: "chat"}
	if err := GenerateGRPCScaffold(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	logicPath := filepath.Join(outputDir, "internal/logic/chat/sendlogic.go")
	logic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	logic = bytes.Replace(logic, []byte("not implemented"), []byte("business implementation"), 1)
	if err := os.WriteFile(logicPath, logic, 0o600); err != nil {
		t.Fatal(err)
	}
	content = strings.Replace(content, "service Chat {", "service Chat { rpc Added(google.protobuf.Empty) returns (google.protobuf.Empty);", 1)
	if err := os.WriteFile(input, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := GenerateGRPCScaffold(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(logic, preserved) {
		t.Fatal("business logic overwritten")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "internal/logic/chat/addedlogic.go")); err != nil {
		t.Fatal(err)
	}
	snapshot := func(dir string) map[string]string {
		t.Helper()
		files := make(map[string]string)
		if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				files[rel] = "symlink:" + target
				return err
			}
			data, err := os.ReadFile(path)
			files[rel] = string(data)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot(outputDir)
	if err := GenerateGRPCScaffold(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(before, snapshot(outputDir)) {
		t.Fatal("regeneration changed identical output")
	}
	t.Run("infer existing module and application name", func(t *testing.T) {
		inferred := opts
		inferred.Module, inferred.Name = "", ""
		if err := GenerateGRPCScaffold(t.Context(), inferred); err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(before, snapshot(outputDir)) {
			t.Fatal("inferred options changed existing output")
		}
	})
	assertGeneratedProjectCompiles(t, outputDir)
	for _, name := range []string{"missing proto", "missing module", "invalid module", "malformed module", "module mismatch", "invalid name", "missing toolchain", "canceled context", "another application", "no service", "root go package", "logic collision", "symlink root", "symlink parent", "symlink target", "symlink module", "user output", "foreign generator", "foreign proto", "directory target"} {
		t.Run(name, func(t *testing.T) {
			current := opts
			current.Dir = t.TempDir()
			outside := t.TempDir()
			ctx := t.Context()
			switch name {
			case "missing proto":
				current.ProtoFile = ""
			case "missing module":
				current.Module = ""
			case "invalid module":
				current.Module = "https://invalid"
			case "malformed module":
				if err := writeGeneratedFileUnder(current.Dir, "go.mod", []byte("go 1.26\n")); err != nil {
					t.Fatal(err)
				}
			case "missing toolchain":
				t.Setenv("PATH", t.TempDir())
			case "canceled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "another application":
				if err := writeGeneratedFileUnder(current.Dir, "internal/server/other_grpc.gen.go", []byte("package server\n")); err != nil {
					t.Fatal(err)
				}
			case "no service", "root go package", "logic collision":
				proto := `syntax = "proto3"; package check; option go_package = "example.com/chat/internal/pb;pb"; message Request {}`
				if name == "root go package" {
					proto = strings.Replace(proto, "example.com/chat/internal/pb", "example.com/chat", 1)
					proto += ` service Check { rpc Get(Request) returns (Request); }`
				}
				if name == "logic collision" {
					proto += ` service Check { rpc Get(Request) returns (Request); } service CHECK { rpc Get(Request) returns (Request); }`
				}
				current.ProtoFile = filepath.Join(t.TempDir(), "check.proto")
				if err := os.WriteFile(current.ProtoFile, []byte(proto), 0o600); err != nil {
					t.Fatal(err)
				}
			case "module mismatch":
				if err := writeGeneratedFileUnder(current.Dir, "go.mod", []byte("module example.com/other\n")); err != nil {
					t.Fatal(err)
				}
			case "invalid name":
				current.Name = "../escape"
			case "symlink root":
				current.Dir = filepath.Join(current.Dir, "linked")
				if err := os.Symlink(outside, current.Dir); err != nil {
					t.Fatal(err)
				}
			case "symlink parent":
				if err := os.Symlink(outside, filepath.Join(current.Dir, "internal")); err != nil {
					t.Fatal(err)
				}
			case "symlink target", "symlink module":
				path := "internal/pb/chat.pb.go"
				if name == "symlink module" {
					path = "go.mod"
				}
				if err := os.MkdirAll(filepath.Dir(filepath.Join(current.Dir, path)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "untouched"), filepath.Join(current.Dir, path)); err != nil {
					t.Fatal(err)
				}
			case "directory target":
				if err := os.MkdirAll(filepath.Join(current.Dir, "internal/pb/chat.pb.go"), 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				data := "package pb\n"
				if name == "foreign generator" {
					data = "// Code generated by another tool. DO NOT EDIT.\npackage pb\n"
				}
				if name == "foreign proto" {
					data = strings.Replace(before["internal/pb/chat.pb.go"], "// source: chat.proto", "// source: other.proto", 1)
				}
				if err := writeGeneratedFileUnder(current.Dir, "internal/pb/chat.pb.go", []byte(data)); err != nil {
					t.Fatal(err)
				}
			}
			before, outsideBefore := snapshot(current.Dir), snapshot(outside)
			if err := GenerateGRPCScaffold(ctx, current); err == nil {
				t.Fatal("unsafe output accepted")
			}
			if !maps.Equal(before, snapshot(current.Dir)) || !maps.Equal(outsideBefore, snapshot(outside)) {
				t.Fatal("rejected generation modified files")
			}
		})
	}
}

func TestGenerateGRPCBindingCodeSupportsStreaming(t *testing.T) {
	doc, err := ParseProto(`syntax = "proto3";
package chat.v1;
message ChatRequest { string text = 1; }
message ChatResponse { string text = 1; }
service Chat { rpc Talk(stream ChatRequest) returns (stream ChatResponse); }
`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateGRPCBindingCode(doc, "chatv1")
	if err != nil {
		t.Fatal(err)
	}
	text := string(code)
	for _, want := range []string{"func NewChatGRPCServer", "RegisterChatServer", "func DialChat"} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated grpc binding missing %q: %s", want, text)
		}
	}
}

func TestGenerateRPCNewGoZeroCompatibleProducesRunnableGRPCProject(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateRPCNew(RPCNewOptions{
		Name:          "Greeter",
		Module:        "example.com/greeter",
		Dir:           dir,
		Profile:       string(ProfileGoZeroCompatible),
		FrameworkPath: repositoryRoot(t),
	}); err != nil {
		t.Fatalf("GenerateRPCNew: %v", err)
	}
	for _, rel := range []string{
		filepath.Join("cmd", "Greeter", "main.go"),
		filepath.Join("etc", "Greeter.json"),
		filepath.Join("internal", "config", "config.go"),
		filepath.Join("internal", "discovery", "registry.go"),
		filepath.Join("internal", "logic", "sayhellologic.go"),
		filepath.Join("internal", "server", "greeterserver.go"),
		filepath.Join("internal", "svc", "servicecontext.go"),
		filepath.Join("internal", "pb", "Greeter.pb.go"),
		filepath.Join("internal", "pb", "Greeter_grpc.pb.go"),
		filepath.Join("pkg", "client", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("generated runnable gRPC file %s: %v", rel, err)
		}
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "Greeter", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"flygrpc.NewDefaultServer", "flygrpc.WithDiscovery", "pb.RegisterGreeterServer", "app.Run"} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("generated main missing %q: %s", want, mainData)
		}
	}
	if err := GenerateGRPCFromProto(GRPCOptions{ProtoFile: filepath.Join(dir, "Greeter.proto"), Dir: filepath.Join(dir, "internal", "pb"), Package: "pb"}); err != nil {
		t.Fatal(err)
	}
	bindingTest := `package pb

import (
 "context"
 "net"
 "testing"
 "time"
 "github.com/imajinyun/gofly/core/governance"
 flygrpc "github.com/imajinyun/gofly/rpc/grpc"
 grpc "google.golang.org/grpc"
 "google.golang.org/grpc/codes"
 "google.golang.org/grpc/credentials/insecure"
 "google.golang.org/grpc/status"
 "google.golang.org/grpc/test/bufconn"
)

type bindingGreeter struct { UnimplementedGreeterServer }
func (bindingGreeter) SayHello(context.Context, *SayHelloRequest) (*SayHelloResponse, error) { return &SayHelloResponse{Message: "bound"}, nil }
func TestBindingDefaults(t *testing.T) {
 for _, side := range []string{"server", "client"} {
  t.Run(side, func(t *testing.T) {
   rules := governance.NewRuleSet()
   var serverOpts []flygrpc.ServerOption
   var clientOpts []flygrpc.ClientOption
   if side == "server" { serverOpts = append(serverOpts, flygrpc.WithRules(rules)) } else { clientOpts = append(clientOpts, flygrpc.WithClientRules(rules)) }
   server := NewGreeterGRPCServer(bindingGreeter{}, serverOpts...)
   listener := bufconn.Listen(1024*1024)
   done := make(chan error, 1)
   go func() { done <- server.GRPCServer().Serve(listener) }()
   t.Cleanup(func() { server.GRPCServer().Stop(); if err := <-done; err != nil { t.Error(err) } })
   clientOpts = append(clientOpts, flygrpc.WithDialOptions(grpc.WithContextDialer(func(context.Context,string)(net.Conn,error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials())))
   client, conn, err := DialGreeter(t.Context(), "passthrough:///bufnet", clientOpts...)
   if err != nil { t.Fatal(err) }; defer conn.Close()
   ctx, cancel := context.WithTimeout(t.Context(), time.Second); defer cancel()
   if response, err := client.SayHello(ctx, &SayHelloRequest{}); err != nil || response.GetMessage() != "bound" { t.Fatalf("response=%v err=%v", response, err) }
   rules.Replace(governance.Rule{Method:"SayHello", Policy:governance.Policy{RateLimit:governance.RateLimitPolicy{Rate:1,Burst:1}}})
   if _, err := client.SayHello(ctx, &SayHelloRequest{}); err != nil { t.Fatal(err) }
   if _, err := client.SayHello(ctx, &SayHelloRequest{}); status.Code(err) != codes.ResourceExhausted { t.Fatalf("rule ignored: %v", err) }
  })
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "internal", "pb", "binding_test.go"), []byte(bindingTest), 0o600); err != nil {
		t.Fatal(err)
	}
	assertGeneratedProjectCompiles(t, dir)
}
