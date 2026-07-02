package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerateProtocPluginResponse(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"hello.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("hello.proto"),
			Package: proto.String("demo.hello"),
			Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/demo/hello;hellopb")},
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("PingRequest")},
				{Name: proto.String("PingResponse")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:       proto.String("Ping"),
					InputType:  proto.String(".demo.hello.PingRequest"),
					OutputType: proto.String(".demo.hello.PingResponse"),
				}},
			}},
		}},
	}
	resp, err := GenerateProtocPluginResponse(req, ProtocPluginOptions{Module: "example.com/app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.File) != 1 {
		t.Fatalf("files=%d", len(resp.File))
	}
	if got := resp.File[0].GetName(); got != "hello.gofly.go" {
		t.Fatalf("file name=%q", got)
	}
	content := resp.File[0].GetContent()
	for _, want := range []string{
		"package hellopb",
		`"context"`,
		`"github.com/imajinyun/gofly/rpc"`,
		"// Module: example.com/app",
		"func GreeterGoflyDescriptor() rpc.ServiceDesc",
		`Name: "demo.hello.Greeter"`,
		`Request: "PingRequest", Response: "PingResponse"`,
		"func GreeterGoflyRuntimeDescriptor() rpc.Descriptor",
		"type GreeterGoflyService interface",
		"func GreeterGoflyServiceDesc(impl GreeterGoflyService) rpc.ServiceDesc",
		"desc.Methods[0].Handler = func(ctx context.Context, req any) (any, error)",
		"typed, ok := req.(*PingRequest)",
		`return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected request type for Ping")`,
		"return impl.Ping(ctx, typed)",
		"func BindGreeterGoflyGenericHandlers(handlers map[string]rpc.GenericHandler) (rpc.ServiceDesc, error)",
		"func RegisterGreeterGoflyServer(s *rpc.HTTPServer, impl GreeterGoflyService) error",
		"func NewGreeterGoflyHTTPServer(impl GreeterGoflyService, opts ...rpc.ServerOption) (*rpc.HTTPServer, error)",
		"type GreeterGoflyClient interface",
		"type GreeterGoflyRPCClient struct",
		"func NewGreeterGoflyClient(cc rpc.Client) *GreeterGoflyRPCClient",
		"func NewGreeterGoflyHTTPClient(target string, opts ...rpc.ClientOption) (*GreeterGoflyRPCClient, error)",
		"func (c *GreeterGoflyRPCClient) Descriptor() rpc.ServiceDesc",
		"func (c *GreeterGoflyRPCClient) RuntimeDescriptor() rpc.Descriptor",
		`method, err := c.desc.MethodPath("Ping")`,
		"if err := c.cc.Call(ctx, method, req, &resp); err != nil",
		"type GreeterGoflyGenericClient struct",
		"func NewGreeterGoflyGenericClient(client rpc.GenericClient) (*GreeterGoflyGenericClient, error)",
		"func NewGreeterGoflyGenericHTTPClient(target string, opts ...rpc.ClientOption) (*GreeterGoflyGenericClient, error)",
		"func (c *GreeterGoflyGenericClient) Invoke(ctx context.Context, method string, req any) (rpc.GenericResponse, error)",
		"Ping(ctx context.Context, req *PingRequest) (*PingResponse, error)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestGenerateProtocPluginResponseSupportsStreaming(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"stream.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("stream.proto"),
			Package: proto.String("demo.stream"),
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("ChatRequest")},
				{Name: proto.String("ChatResponse")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:            proto.String("Chat"),
					InputType:       proto.String(".demo.stream.ChatRequest"),
					OutputType:      proto.String(".demo.stream.ChatResponse"),
					ClientStreaming: proto.Bool(true),
				}},
			}},
		}},
	}
	resp, err := GenerateProtocPluginResponse(req, ProtocPluginOptions{})
	if err != nil {
		t.Fatalf("streaming response err = %v", err)
	}
	if len(resp.File) != 1 {
		t.Fatalf("files=%d, want 1", len(resp.File))
	}
	content := resp.File[0].GetContent()
	for _, want := range []string{
		"Streams: []rpc.StreamDesc",
		`Name: "Chat"`,
		`NewMessage: func() any { return new(ChatRequest) }`,
		`Mode: rpc.StreamModeClientStream`,
		`Metadata: map[string]string{"request": "ChatRequest", "response": "ChatResponse", "clientStream": "true", "serverStream": "false"}`,
		"Chat(ctx context.Context, stream *rpc.Stream) error",
		"desc.Streams[0].Handler = func(ctx context.Context, stream *rpc.Stream) error",
		"return impl.Chat(ctx, stream)",
		"Chat(ctx context.Context) (*rpc.Stream, error)",
		`method, err := c.desc.StreamPath("Chat")`,
		"return streamer.Stream(ctx, method)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("streaming content missing %q:\n%s", want, content)
		}
	}
}

func TestGenerateProtocPluginResponseRejectsExternalMessageType(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"hello.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("hello.proto"),
			Package: proto.String("demo.hello"),
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:       proto.String("Ping"),
					InputType:  proto.String(".demo.common.PingRequest"),
					OutputType: proto.String(".demo.hello.PingResponse"),
				}},
			}},
		}},
	}
	_, err := GenerateProtocPluginResponse(req, ProtocPluginOptions{})
	if err == nil || !strings.Contains(err.Error(), `external proto type "demo.common.PingRequest" is not supported`) {
		t.Fatalf("external type error = %v", err)
	}
}

func TestGenerateProtocPluginResponseSanitizesModuleComment(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"hello.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("hello.proto"),
			Package: proto.String("demo.hello"),
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:       proto.String("Ping"),
					InputType:  proto.String(".demo.hello.PingRequest"),
					OutputType: proto.String(".demo.hello.PingResponse"),
				}},
			}},
		}},
	}
	resp, err := GenerateProtocPluginResponse(req, ProtocPluginOptions{Module: "example.com/app\nconst injected = true"})
	if err != nil {
		t.Fatal(err)
	}
	content := resp.File[0].GetContent()
	if !strings.Contains(content, "// Module: example.com/app const injected = true") || strings.Contains(content, "\nconst injected") {
		t.Fatalf("module comment was not sanitized:\n%s", content)
	}
}

func TestProtocPluginOptionParsingBoundaries(t *testing.T) {
	truthy := []string{"1", "t", "true", "y", "yes", " TRUE "}
	for _, raw := range truthy {
		t.Run("truthy "+raw, func(t *testing.T) {
			if !parseProtocBool(raw) {
				t.Fatalf("parseProtocBool(%q) = false, want true", raw)
			}
		})
	}
	falsy := []string{"", "0", "false", "no", "anything"}
	for _, raw := range falsy {
		t.Run("falsy "+raw, func(t *testing.T) {
			if parseProtocBool(raw) {
				t.Fatalf("parseProtocBool(%q) = true, want false", raw)
			}
		})
	}
}

func TestProtocLocalGoType(t *testing.T) {
	file := &descriptorpb.FileDescriptorProto{Package: proto.String("demo.hello")}
	tests := []struct {
		name      string
		protoType string
		want      string
		wantErr   string
	}{
		{name: "same package message", protoType: ".demo.hello.PingRequest", want: "PingRequest"},
		{name: "same package nested message", protoType: ".demo.hello.Outer.Inner", want: "Outer_Inner"},
		{name: "relative local message", protoType: "PingRequest", want: "PingRequest"},
		{name: "external package", protoType: ".demo.common.PingRequest", wantErr: `external proto type "demo.common.PingRequest" is not supported`},
		{name: "package without message", protoType: ".demo.hello", wantErr: `proto type "demo.hello" is invalid`},
		{name: "empty type", protoType: " ", wantErr: "proto type is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := protocLocalGoType(file, tt.protoType)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("protocLocalGoType error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("protocLocalGoType: %v", err)
			}
			if got != tt.want {
				t.Fatalf("protocLocalGoType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProtocPluginPackageNameBoundaries(t *testing.T) {
	tests := []struct {
		name string
		file *descriptorpb.FileDescriptorProto
		want string
	}{
		{name: "go package semicolon", file: &descriptorpb.FileDescriptorProto{Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/demo;demopb")}}, want: "demopb"},
		{name: "go package path", file: &descriptorpb.FileDescriptorProto{Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/demo/user-pb")}}, want: "user_pb"},
		{name: "go package raw", file: &descriptorpb.FileDescriptorProto{Options: &descriptorpb.FileOptions{GoPackage: proto.String("123 bad-pkg")}}, want: "_23_bad_pkg"},
		{name: "proto package", file: &descriptorpb.FileDescriptorProto{Package: proto.String("demo.v1")}, want: "v1"},
		{name: "default", file: &descriptorpb.FileDescriptorProto{}, want: "proto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := goPackageName(tt.file); got != tt.want {
				t.Fatalf("goPackageName = %q, want %q", got, tt.want)
			}
		})
	}

	serviceTests := []struct {
		name string
		file *descriptorpb.FileDescriptorProto
		svc  *descriptorpb.ServiceDescriptorProto
		want string
	}{
		{name: "without package", file: &descriptorpb.FileDescriptorProto{}, svc: &descriptorpb.ServiceDescriptorProto{Name: proto.String("Greeter")}, want: "Greeter"},
		{name: "with package", file: &descriptorpb.FileDescriptorProto{Package: proto.String("demo.v1")}, svc: &descriptorpb.ServiceDescriptorProto{Name: proto.String("Greeter")}, want: "demo.v1.Greeter"},
	}
	for _, tt := range serviceTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protocServiceFullName(tt.file, tt.svc); got != tt.want {
				t.Fatalf("protocServiceFullName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateProtocPluginResponseGeneratedCodeCompiles(t *testing.T) {
	repoRoot := repositoryRoot(t)
	dir := t.TempDir()
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"hello.proto"},
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("hello.proto"),
			Package: proto.String("demo.hello"),
			Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/demo/hello;hellopb")},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:       proto.String("Ping"),
					InputType:  proto.String(".demo.hello.PingRequest"),
					OutputType: proto.String(".demo.hello.PingResponse"),
				}},
			}},
		}},
	}
	resp, err := GenerateProtocPluginResponse(req, ProtocPluginOptions{Module: "example.com/app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.File) != 1 {
		t.Fatalf("files=%d", len(resp.File))
	}
	goMod := fmt.Sprintf(`module example.com/protocplugintest

go 1.26

require github.com/imajinyun/gofly v0.0.0

replace github.com/imajinyun/gofly => %s
`, filepath.ToSlash(repoRoot))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	goSum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), goSum, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, resp.File[0].GetName()), []byte(resp.File[0].GetContent()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(`package hellopb

type PingRequest struct {
	Message string
}

type PingResponse struct {
	Message string
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated_usage_test.go"), []byte(`package hellopb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc"
)

type fakeService struct{}

func (fakeService) Ping(ctx context.Context, req *PingRequest) (*PingResponse, error) {
	return &PingResponse{Message: req.Message}, nil
}

type fakeUnaryClient struct{}

func (fakeUnaryClient) Call(ctx context.Context, method string, request any, response any) error {
	if method != "demo.hello.Greeter/Ping" {
		return rpc.NewError(rpc.CodeInvalidArgument, "unexpected method")
	}
	out, ok := response.(*PingResponse)
	if !ok || out == nil {
		return rpc.NewError(rpc.CodeInvalidArgument, "unexpected response type")
	}
	out.Message = "pong"
	return nil
}

type fakeGenericClient struct{}

func (fakeGenericClient) CallRaw(ctx context.Context, method string, request any) (json.RawMessage, metadata.MD, error) {
	if method != "demo.hello.Greeter/Ping" {
		return nil, nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected method")
	}
	return json.RawMessage(`+"`"+`{"message":"pong"}`+"`"+`), nil, nil
}

func TestGeneratedPluginUsage(t *testing.T) {
	desc := GreeterGoflyDescriptor()
	if desc.Name != "demo.hello.Greeter" {
		t.Fatalf("descriptor name = %q", desc.Name)
	}
	runtimeDesc := GreeterGoflyRuntimeDescriptor()
	if err := runtimeDesc.Validate(); err != nil {
		t.Fatalf("validate runtime descriptor: %v", err)
	}
	bound := GreeterGoflyServiceDesc(fakeService{})
	if _, err := bound.Methods[0].Handler(context.Background(), PingRequest{}); rpc.CodeOf(err) != rpc.CodeInvalidArgument {
		t.Fatalf("handler error code = %v, want %v", rpc.CodeOf(err), rpc.CodeInvalidArgument)
	}
	client := NewGreeterGoflyClient(fakeUnaryClient{})
	resp, err := client.Ping(context.Background(), &PingRequest{Message: "ping"})
	if err != nil {
		t.Fatalf("typed client ping: %v", err)
	}
	if resp.Message != "pong" {
		t.Fatalf("typed client response = %#v", resp)
	}
	genericClient, err := NewGreeterGoflyGenericClient(fakeGenericClient{})
	if err != nil {
		t.Fatalf("new generic client: %v", err)
	}
	if _, err := genericClient.Invoke(context.Background(), "Ping", &PingRequest{Message: "ping"}); err != nil {
		t.Fatalf("generic invoke: %v", err)
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGoCommand(t, dir, 2*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 2*time.Minute, "test", "./...")
}

func TestMarshalProtocPluginResponse(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"hello.proto"},
		Parameter:      proto.String("module=example.com/plugin,name_from_filename=true"),
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("hello.proto"),
			Package: proto.String("demo"),
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: proto.String("Echo"),
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name:       proto.String("Say"),
					InputType:  proto.String(".demo.SayRequest"),
					OutputType: proto.String(".demo.SayResponse"),
				}},
			}},
		}},
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalProtocPluginResponse(data, ProtocPluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resp := &pluginpb.CodeGeneratorResponse{}
	if err := proto.Unmarshal(out, resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.File) != 1 || !strings.Contains(resp.File[0].GetContent(), "HelloEchoGoflyService") || !strings.Contains(resp.File[0].GetContent(), "// Module: example.com/plugin") {
		t.Fatalf("unexpected response: %+v", resp.File)
	}
}

func TestGenerateProtocPluginResponseNoClientMultiple(t *testing.T) {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"multi.proto"},
		Parameter:      proto.String("no_client=true,multiple=true"),
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("multi.proto"),
			Package: proto.String("demo.multi"),
			MessageType: []*descriptorpb.DescriptorProto{
				{Name: proto.String("PingRequest")},
				{Name: proto.String("PingResponse")},
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("Greeter"),
					Method: []*descriptorpb.MethodDescriptorProto{{
						Name:       proto.String("Ping"),
						InputType:  proto.String(".demo.multi.PingRequest"),
						OutputType: proto.String(".demo.multi.PingResponse"),
					}},
				},
				{
					Name: proto.String("Health"),
					Method: []*descriptorpb.MethodDescriptorProto{{
						Name:       proto.String("Check"),
						InputType:  proto.String(".demo.multi.PingRequest"),
						OutputType: proto.String(".demo.multi.PingResponse"),
					}},
				},
			},
		}},
	}
	resp, err := GenerateProtocPluginResponse(req, ProtocPluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.File) != 2 {
		t.Fatalf("files=%d, want 2", len(resp.File))
	}
	got := []string{resp.File[0].GetName(), resp.File[1].GetName()}
	want := []string{"greeter/multi.gofly.go", "health/multi.gofly.go"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
	for _, file := range resp.File {
		content := file.GetContent()
		if strings.Contains(content, "GoflyClient interface") || strings.Contains(content, "GoflyRPCClient") || strings.Contains(content, "GoflyGenericClient") {
			t.Fatalf("no_client output should omit client interface in %s:\n%s", file.GetName(), content)
		}
	}
	if !strings.Contains(resp.File[0].GetContent(), "type GreeterGoflyService interface") || !strings.Contains(resp.File[1].GetContent(), "type HealthGoflyService interface") {
		t.Fatalf("split service output missing expected service interfaces: %+v", resp.File)
	}
	if !strings.Contains(resp.File[0].GetContent(), "func GreeterGoflyRuntimeDescriptor() rpc.Descriptor") || !strings.Contains(resp.File[1].GetContent(), "func HealthGoflyRuntimeDescriptor() rpc.Descriptor") {
		t.Fatalf("split service output missing runtime descriptors: %+v", resp.File)
	}
	if !strings.Contains(resp.File[0].GetContent(), "func RegisterGreeterGoflyServer") || !strings.Contains(resp.File[1].GetContent(), "func RegisterHealthGoflyServer") {
		t.Fatalf("split service output missing registration helpers: %+v", resp.File)
	}
}
