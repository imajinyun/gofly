package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	assertGeneratedProjectCompiles(t, dir)
}
