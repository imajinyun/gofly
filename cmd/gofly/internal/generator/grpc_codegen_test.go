package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/governance"
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
	logicPath := filepath.Join(outputDir, "internal/app/chat/send.go")
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
	if _, err := os.Stat(filepath.Join(outputDir, "internal/app/chat/added.go")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("etc", "governance.json"),
		filepath.Join("bin", "production-check.sh"),
		filepath.Join("internal", "config", "production_check.go"),
		filepath.Join("internal", "config", "governance_recovery_test.go"),
	} {
		if _, err := os.Stat(filepath.Join(outputDir, rel)); err != nil {
			t.Fatalf("generated descriptor scaffold file %s: %v", rel, err)
		}
	}
	configData, err := os.ReadFile(filepath.Join(outputDir, "etc", "chat.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"policy": "gofly_p2c_ewma"`) {
		t.Fatalf("descriptor scaffold config missing load-balancing default: %s", configData)
	}
	configSource, err := os.ReadFile(filepath.Join(outputDir, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configSource), "flygrpc.WithClientCallTimeout(timeout)") {
		t.Fatalf("descriptor scaffold client timeout does not cover unary and streaming RPCs: %s", configSource)
	}
	wantRules := []governance.Rule{
		{Name: "chat-v1-chat-send-timeout", Priority: -1000, Transport: governance.TransportRPC, Service: "chat.v1.Chat", Method: "Send", Policy: governance.Policy{Timeout: 2 * time.Second}},
		{Name: "chat-v1-chat-upload-timeout", Priority: -1000, Transport: governance.TransportRPC, Service: "chat.v1.Chat", Method: "Upload", Policy: governance.Policy{Timeout: 2 * time.Second}},
		{Name: "chat-v1-chat-watch-timeout", Priority: -1000, Transport: governance.TransportRPC, Service: "chat.v1.Chat", Method: "Watch", Policy: governance.Policy{Timeout: 2 * time.Second}},
		{Name: "chat-v1-chat-talk-timeout", Priority: -1000, Transport: governance.TransportRPC, Service: "chat.v1.Chat", Method: "Talk", Policy: governance.Policy{Timeout: 2 * time.Second}},
		{Name: "chat-v1-admin-ping-timeout", Priority: -1000, Transport: governance.TransportRPC, Service: "chat.v1.Admin", Method: "Ping", Policy: governance.Policy{Timeout: 2 * time.Second}},
	}
	var appConfig struct {
		Rules []governance.Rule `json:"rules"`
	}
	if err := json.Unmarshal(configData, &appConfig); err != nil {
		t.Fatalf("decode generated RPC config: %v", err)
	}
	if !reflect.DeepEqual(appConfig.Rules, wantRules) {
		t.Fatalf("generated config rules = %#v, want %#v", appConfig.Rules, wantRules)
	}
	governanceData, err := os.ReadFile(filepath.Join(outputDir, "etc", "governance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persistedRules []governance.Rule
	if err := json.Unmarshal(governanceData, &persistedRules); err != nil {
		t.Fatalf("decode generated governance rules: %v", err)
	}
	if !reflect.DeepEqual(persistedRules, wantRules) {
		t.Fatalf("generated governance rules = %#v, want %#v", persistedRules, wantRules)
	}
	override := governance.Rule{
		Name: "operator-send-timeout", Transport: governance.TransportRPC,
		Service: "chat.v1.Chat", Method: "Send", Policy: governance.Policy{Timeout: 7 * time.Second},
	}
	merged := governance.NewRuleSet(append(wantRules, override)...)
	decision := merged.Match(governance.Request{Transport: governance.TransportRPC, Service: "chat.v1.Chat", Method: "Send"})
	if !decision.Matched || decision.RuleName != override.Name || decision.Policy.Timeout != 7*time.Second {
		t.Fatalf("operator rule did not override generated default: %#v", decision)
	}
	methodDefaults, err := os.ReadFile(filepath.Join(outputDir, "internal", "config", "rpc_methods.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"chat.v1.Chat", `Method: "Added"`, `Method: "Upload"`, `Method: "Watch"`, `Method: "Talk"`, "chat.v1.Admin", `Method: "Ping"`, "Priority: -1000"} {
		if !strings.Contains(string(methodDefaults), want) {
			t.Fatalf("generated method defaults missing %q: %s", want, methodDefaults)
		}
	}
	clientData, err := os.ReadFile(filepath.Join(outputDir, "internal", "api", "rpc", "chat_client.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientData), "NewConfiguredChat") || !strings.Contains(string(clientData), "NewConfiguredAdmin") {
		t.Fatalf("descriptor scaffold client missing configured constructors: %s", clientData)
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
	configuredClientTest := `package rpc

import (
 "context"
 "testing"
 "time"

 "github.com/imajinyun/gofly/core/discovery"
 flygrpc "github.com/imajinyun/gofly/rpc/grpc"
 stdgrpc "google.golang.org/grpc"
 "google.golang.org/grpc/codes"
 "google.golang.org/grpc/status"
 "google.golang.org/protobuf/types/known/emptypb"
 "example.com/chat/internal/config"
 "example.com/chat/internal/pb"
)

type configuredChatServer struct { pb.UnimplementedChatServer }

func (configuredChatServer) Send(context.Context, *pb.Envelope_Payload) (*emptypb.Empty, error) {
 return &emptypb.Empty{}, nil
}

func (configuredChatServer) Watch(_ *emptypb.Empty, stream stdgrpc.ServerStreamingServer[pb.Envelope_Payload]) error {
 <-stream.Context().Done()
 return stream.Context().Err()
}

func TestConfiguredLoadBalancing(t *testing.T) {
 registry := discovery.NewMemoryRegistry()
 server := flygrpc.NewDefaultServer("127.0.0.1:0", "chat.v1.Chat", nil, nil,
  flygrpc.WithDiscovery(registry, discovery.Instance{ID: "configured-client-test", Service: "chat.v1.Chat"}),
 )
 pb.RegisterChatServer(server.GRPCServer(), configuredChatServer{})
 started := make(chan error, 1)
 go func() { started <- server.Start() }()
 t.Cleanup(func() {
  _ = server.Shutdown(context.Background())
  select { case <-started: case <-time.After(time.Second): t.Error("timed out waiting for server shutdown") }
 })
 deadline := time.Now().Add(time.Second)
 for {
  if instances, err := registry.Resolve(t.Context(), "chat.v1.Chat"); err == nil && len(instances) == 1 { break }
  if time.Now().After(deadline) { t.Fatal("service was not registered") }
  time.Sleep(time.Millisecond)
 }
 for _, policy := range []string{"", "round_robin", flygrpc.P2CEWMABalancerName, flygrpc.ConsistentHashBalancerName} {
  t.Run("policy "+policy, func(t *testing.T) {
   cfg := config.RPCClientConfig{Etcd: config.EtcdConfig{Hosts: []string{"memory"}, Key: "chat.v1.Chat"}, BalancerName: policy}
   client, conn, err := NewConfiguredChat(t.Context(), registry, cfg, nil, flygrpc.WithWaitForReady())
   if err != nil { t.Fatalf("NewConfiguredChat policy %q: %v", policy, err) }
   defer conn.Close()
   callCtx := t.Context()
   if policy == flygrpc.ConsistentHashBalancerName { callCtx = flygrpc.WithHashKey(callCtx, "tenant-42") }
   if _, err := client.Send(callCtx, &pb.Envelope_Payload{Text: "ready"}); err != nil {
    t.Fatalf("Send policy %q: %v", policy, err)
   }
  })
 }
 invalid := config.RPCClientConfig{Target: "127.0.0.1:1", NonBlock: true, BalancerName: "least_request"}
 if _, _, err := NewConfiguredChat(t.Context(), registry, invalid, nil); err == nil {
  t.Fatal("unsupported configured load-balancing policy was accepted")
 }
 timeoutClient, timeoutConn, err := NewConfiguredChat(t.Context(), registry, config.RPCClientConfig{
  Etcd: config.EtcdConfig{Hosts: []string{"memory"}, Key: "chat.v1.Chat"}, Timeout: 20, NonBlock: true,
 }, nil, flygrpc.WithWaitForReady())
 if err != nil { t.Fatal(err) }
 defer timeoutConn.Close()
 watch, err := timeoutClient.Watch(t.Context(), &emptypb.Empty{})
 if err != nil { t.Fatal(err) }
 if _, err := watch.Recv(); status.Code(err) != codes.DeadlineExceeded {
  t.Fatalf("configured stream timeout = %v, want DeadlineExceeded", err)
 }
}
`
	if err := os.WriteFile(filepath.Join(outputDir, "internal", "api", "rpc", "configured_balancing_test.go"), []byte(configuredClientTest), 0o600); err != nil {
		t.Fatal(err)
	}
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
				if err := writeGeneratedFileUnder(current.Dir, "internal/api/rpc/other_grpc.gen.go", []byte("package rpc\n")); err != nil {
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

func TestGenerateGRPCScaffoldGoctlOptions(t *testing.T) {
	if err := validateNativeGRPCToolchain(); err != nil {
		t.Skip(err)
	}
	tests := []struct {
		name       string
		proto      string
		opts       GRPCScaffoldOptions
		want       []string
		wantAbsent []string
		wantErr    string
	}{
		{
			name: "client disabled and package-derived name",
			proto: `syntax = "proto3";
package catalog.v1;
option go_package = "example.com/catalog/internal/pb;pb";
message Request {}
message Response {}
service Catalog { rpc Get(Request) returns (Response); }
`,
			opts: GRPCScaffoldOptions{NameFromPackage: true, NoClient: true},
			want: []string{
				"cmd/catalogv1/main.go",
				"etc/catalogv1.json",
				"internal/api/rpc/catalogv1_grpc.gen.go",
				"internal/app/catalog/get.go",
			},
			wantAbsent: []string{"internal/api/rpc/catalogv1_client.gen.go"},
		},
		{
			name: "multiple groups server logic and clients by service",
			proto: `syntax = "proto3";
package platform;
option go_package = "example.com/platform/internal/pb;pb";
message Request {}
message Response {}
service Catalog { rpc Get(Request) returns (Response); }
service Inventory { rpc Check(Request) returns (Response); }
`,
			opts: GRPCScaffoldOptions{Module: "example.com/platform", NameFromPackage: true, Multiple: true, RequireMultiple: true},
			want: []string{
				"internal/api/rpc/register.gen.go",
				"internal/api/rpc/catalog/catalog_grpc.gen.go",
				"internal/api/rpc/inventory/inventory_grpc.gen.go",
				"internal/app/catalog/get.go",
				"internal/app/inventory/check.go",
				"internal/api/rpc/catalog/catalog_client.gen.go",
				"internal/api/rpc/inventory/inventory_client.gen.go",
			},
		},
		{
			name: "multiple services require explicit multiple mode",
			proto: `syntax = "proto3";
package platform;
option go_package = "example.com/platform/internal/pb;pb";
message Request {}
service Catalog { rpc Get(Request) returns (Request); }
service Inventory { rpc Check(Request) returns (Request); }
`,
			opts:    GRPCScaffoldOptions{Module: "example.com/platform", NameFromPackage: true, RequireMultiple: true},
			wantErr: "rerun with --multiple",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputDir, outputDir := t.TempDir(), t.TempDir()
			input := filepath.Join(inputDir, "service.proto")
			if err := os.WriteFile(input, []byte(tt.proto), 0o600); err != nil {
				t.Fatal(err)
			}
			opts := tt.opts
			opts.ProtoFile, opts.Dir = input, outputDir
			err := GenerateGRPCScaffold(t.Context(), opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("GenerateGRPCScaffold error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, rel := range tt.want {
				if _, err := os.Stat(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil {
					t.Fatalf("expected generated file %s: %v", rel, err)
				}
			}
			for _, rel := range tt.wantAbsent {
				if _, err := os.Stat(filepath.Join(outputDir, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected generated file %s: %v", rel, err)
				}
			}
			assertGeneratedProjectCompiles(t, outputDir)
		})
	}
}

func TestGenerateGRPCScaffoldMultipleProtoProject(t *testing.T) {
	if err := validateNativeGRPCToolchain(); err != nil {
		t.Skip(err)
	}
	inputDir, outputDir := t.TempDir(), t.TempDir()
	protoFiles := []string{
		filepath.Join(inputDir, "catalog.proto"),
		filepath.Join(inputDir, "inventory.proto"),
	}
	contents := []string{
		`syntax = "proto3";
package catalog.v1;
option go_package = "example.com/platform/internal/catalogpb;catalogpb";
message GetRequest {}
message GetResponse {}
service Catalog { rpc Get(GetRequest) returns (GetResponse); }
`,
		`syntax = "proto3";
package inventory.v1;
option go_package = "example.com/platform/internal/inventorypb;inventorypb";
message CheckRequest {}
message CheckResponse {}
service Inventory { rpc Check(CheckRequest) returns (CheckResponse); }
`,
	}
	for index, name := range protoFiles {
		if err := os.WriteFile(name, []byte(contents[index]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := GRPCScaffoldOptions{
		ProtoFiles: protoFiles, Dir: outputDir, Module: "example.com/platform",
		NameFromPackage: true, Multiple: true, RequireMultiple: true,
	}
	if err := GenerateGRPCScaffold(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"internal/catalogpb/catalog.pb.go",
		"internal/inventorypb/inventory.pb.go",
		"internal/api/rpc/catalog/catalog_grpc.gen.go",
		"internal/api/rpc/inventory/inventory_grpc.gen.go",
		"internal/app/catalog/get.go",
		"internal/app/inventory/check.go",
		"internal/api/rpc/catalog/catalog_client.gen.go",
		"internal/api/rpc/inventory/inventory_client.gen.go",
	} {
		if _, err := os.Stat(filepath.Join(outputDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected multi-proto scaffold file %s: %v", rel, err)
		}
	}
	register, err := os.ReadFile(filepath.Join(outputDir, "internal/api/rpc/register.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"// source: catalog.proto", "// source: inventory.proto"} {
		if !bytes.Contains(register, []byte(source)) {
			t.Fatalf("register output missing %q: %s", source, register)
		}
	}
	logicPath := filepath.Join(outputDir, "internal/app/catalog/get.go")
	logic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	logic = bytes.Replace(logic, []byte("not implemented"), []byte("catalog business implementation"), 1)
	if err := os.WriteFile(logicPath, logic, 0o600); err != nil {
		t.Fatal(err)
	}
	billingProto := filepath.Join(inputDir, "billing.proto")
	if err := os.WriteFile(billingProto, []byte(`syntax = "proto3";
package billing.v1;
option go_package = "example.com/platform/internal/billingpb;billingpb";
message ChargeRequest {}
message ChargeResponse {}
service Billing { rpc Charge(ChargeRequest) returns (ChargeResponse); }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.ProtoFiles = append(opts.ProtoFiles, billingProto)
	if err := GenerateGRPCScaffold(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(logic, preserved) {
		t.Fatal("multi-proto regeneration overwrote business logic")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "internal/app/billing/charge.go")); err != nil {
		t.Fatalf("incremental multi-proto service was not generated: %v", err)
	}
	mainFiles, err := filepath.Glob(filepath.Join(outputDir, "cmd", "*", "main.go"))
	if err != nil || len(mainFiles) != 1 {
		t.Fatalf("generated main files = %v, err = %v", mainFiles, err)
	}
	discoveryTest := `package main

import (
 "context"
 "testing"
 "time"

 "github.com/imajinyun/gofly/core/discovery"
 flygrpc "github.com/imajinyun/gofly/rpc/grpc"
 apprpc "example.com/platform/internal/api/rpc"
)

func TestAllDescriptorServicesAreDiscoverable(t *testing.T) {
 registry := discovery.NewMemoryRegistry()
 server := flygrpc.NewDefaultServer("127.0.0.1:0", "platform-process", nil, nil,
  flygrpc.WithDiscovery(registry, discovery.Instance{ID: "platform-test", Service: "platform-process"}),
  flygrpc.WithDiscoveryAliases(apprpc.DiscoveryAliases()...),
 )
 started := make(chan error, 1)
 go func() { started <- server.Start() }()
 t.Cleanup(func() {
  _ = server.Shutdown(context.Background())
  select { case err := <-started: if err != nil { t.Error(err) }; case <-time.After(time.Second): t.Error("timed out waiting for server shutdown") }
 })
 for _, service := range []string{"catalog.v1.Catalog", "inventory.v1.Inventory", "billing.v1.Billing"} {
  deadline := time.Now().Add(time.Second)
  for {
   instances, resolveErr := registry.Resolve(t.Context(), service)
   if resolveErr == nil && len(instances) == 1 { break }
   if time.Now().After(deadline) { t.Fatalf("service %q was not discoverable: %v", service, resolveErr) }
   time.Sleep(time.Millisecond)
  }
 }
}
`
	if err := os.WriteFile(filepath.Join(filepath.Dir(mainFiles[0]), "discovery_test.go"), []byte(discoveryTest), 0o600); err != nil {
		t.Fatal(err)
	}
	assertGeneratedProjectCompiles(t, outputDir)
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

func TestGenerateRPCNewGoZeroCompatibleProducesRunnableGoflyProject(t *testing.T) {
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
		filepath.Join("etc", "governance.json"),
		filepath.Join("bin", "production-check.sh"),
		filepath.Join("internal", "config", "config.go"),
		filepath.Join("internal", "config", "production_check.go"),
		filepath.Join("internal", "config", "governance_recovery_test.go"),
		filepath.Join("internal", "discovery", "registry.go"),
		filepath.Join("internal", "app", "greeter", "sayhello.go"),
		filepath.Join("internal", "api", "rpc", "greeter.go"),
		filepath.Join("internal", "api", "rpc", "greeter_client.go"),
		filepath.Join("internal", "svc", "service_context.go"),
		filepath.Join("internal", "pb", "Greeter.pb.go"),
		filepath.Join("internal", "pb", "Greeter_grpc.pb.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("generated runnable gRPC file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{
		filepath.Join("internal", "logic"),
		filepath.Join("internal", "server"),
		filepath.Join("internal", "types"),
		filepath.Join("internal", "svc", "servicecontext.go"),
		filepath.Join("pkg", "client"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("generated project unexpectedly uses goctl layout path %s", rel)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect forbidden generated path %s: %v", rel, err)
		}
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "Greeter", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"flygrpc.NewDefaultServer", "flygrpc.WithDiscovery", "flygrpc.WithDiscoveryAliases", "appdiscovery.NewZRPCRegistrar", "serverService = c.Etcd.Key", "serverAliases = nil", "flygrpc.WithAdaptiveLimiter", "limit.NewRuntimeCPUReader", "stx.InitRPCClients(ctx, registry)", "stx.Close()", "pb.RegisterGreeterServer", "app.Run"} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("generated main missing %q: %s", want, mainData)
		}
	}
	svcData, err := os.ReadFile(filepath.Join(dir, "internal", "svc", "service_context.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rpcClients   map[string]rpcClientResource", "rpcResolvers map[rpcResolverKey]rpcResolverResource", "func (s *ServiceContext) InitRPCClients", "appdiscovery.NewZRPCResolver", "func (s *ServiceContext) RPCClient", "func (s *ServiceContext) Close() error"} {
		if !strings.Contains(string(svcData), want) {
			t.Fatalf("generated service context missing %q: %s", want, svcData)
		}
	}
	configData, err := os.ReadFile(filepath.Join(dir, "etc", "Greeter.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"loadBalancing"`, `"policy": "gofly_p2c_ewma"`, `"adaptiveLimit"`, `"enabled": true`, `"cpuThresholdPermille": 800`} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("generated config missing %q: %s", want, configData)
		}
	}
	clientData, err := os.ReadFile(filepath.Join(dir, "internal", "api", "rpc", "greeter_client.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NewDiscoveredGreeter", "flygrpc.WithP2CEWMAResolver()", "NewConfiguredGreeter", "c.TargetAndOptions"} {
		if !strings.Contains(string(clientData), want) {
			t.Fatalf("generated client missing %q: %s", want, clientData)
		}
	}
	checkInfo, err := os.Stat(filepath.Join(dir, "bin", "production-check.sh"))
	if err != nil || checkInfo.Mode()&0o111 == 0 {
		t.Fatalf("production check mode=%v err=%v", checkInfo.Mode(), err)
	}
	unsafeCheck := exec.Command("sh", filepath.Join("bin", "production-check.sh"))
	unsafeCheck.Dir = dir
	if output, err := unsafeCheck.CombinedOutput(); err == nil || !strings.Contains(string(output), "environment must be production") {
		t.Fatalf("development production check err=%v output=%s", err, output)
	}
	productionConfig := strings.Replace(string(configData), `"environment": "development"`, `"environment": "production"`, 1)
	productionConfig = strings.Replace(productionConfig, `"tls": {}`, `"tls": {"certFile":"server.crt","keyFile":"server.key"}`, 1)
	if err := os.WriteFile(filepath.Join(dir, "etc", "Greeter.json"), []byte(productionConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	runGoCommand(t, dir, 2*time.Minute, "mod", "edit", "-replace", "github.com/imajinyun/gofly="+repositoryRoot(t))
	runGoCommand(t, dir, 2*time.Minute, "mod", "tidy")
	check := exec.Command("sh", filepath.Join("bin", "production-check.sh"))
	check.Dir = dir
	check.Env = append(os.Environ(), "GOFLAGS=-count=1")
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("production check: %v\n%s", err, output)
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
