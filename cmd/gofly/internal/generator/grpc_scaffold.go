package generator

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

type GRPCScaffoldOptions struct {
	ProtoFile string
	ProtoPath []string
	Dir       string
	Module    string
	Name      string
	Timeout   time.Duration
}

func GenerateGRPCScaffold(ctx context.Context, opts GRPCScaffoldOptions) error {
	if opts.ProtoFile == "" {
		return errors.New("proto file is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	moduleTarget, err := safeRelativeTarget(opts.Dir, "go.mod", "grpc scaffold")
	if err != nil {
		return err
	}
	if err := rejectExistingSymlinkTarget(moduleTarget, "grpc scaffold module"); err != nil {
		return err
	}
	if _, err := os.Stat(moduleTarget); err == nil {
		existing, err := inferModule(opts.Dir)
		if err != nil {
			return err
		}
		if opts.Module != "" && opts.Module != existing {
			return errors.New("scaffold module differs from existing go.mod")
		}
		opts.Module = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if opts.Module == "" {
		module, err := inferModule(opts.Dir)
		if err != nil {
			return err
		}
		opts.Module = module
	}
	if !isSafeProjectFeatureModulePath(opts.Module) {
		return fmt.Errorf("invalid scaffold module %q", opts.Module)
	}
	if opts.Name == "" {
		opts.Name = strings.TrimSuffix(filepath.Base(opts.ProtoFile), filepath.Ext(opts.ProtoFile))
	}
	if !token.IsIdentifier(opts.Name) {
		return fmt.Errorf("invalid scaffold name %q", opts.Name)
	}
	serverTarget, err := safeRelativeTarget(opts.Dir, "internal/server/"+opts.Name+"_grpc.gen.go", "grpc scaffold")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(serverTarget))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_grpc.gen.go") && entry.Name() != filepath.Base(serverTarget) {
			return errors.New("another grpc scaffold already owns this output root")
		}
	}
	if err := validateNativeGRPCToolchain(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "gofly-grpc-scaffold-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	input, err := filepath.Abs(opts.ProtoFile)
	if err != nil {
		return err
	}
	includes := append([]string{filepath.Dir(input)}, opts.ProtoPath...)
	descriptorPath := filepath.Join(stage, "descriptor.pb")
	if err := GenerateStandardProto(ctx, ProtocOptions{
		ProtoFile: input, ProtoPath: includes, GoOut: stage, GoGRPCOut: stage, Timeout: opts.Timeout,
		ExtraArgs: []string{"--go_opt=module=" + opts.Module, "--go-grpc_opt=module=" + opts.Module, "--descriptor_set_out=" + descriptorPath, "--include_imports"},
	}); err != nil {
		return err
	}
	data, err := ReadFileUnderRoot(stage, "descriptor.pb", "grpc descriptors")
	if err != nil {
		return err
	}
	var descriptors descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &descriptors); err != nil {
		return fmt.Errorf("decode proto descriptors: %w", err)
	}
	plugin, err := (protogen.Options{}).New(&pluginpb.CodeGeneratorRequest{ProtoFile: descriptors.File, FileToGenerate: []string{filepath.Base(input)}})
	if err != nil {
		return fmt.Errorf("resolve proto Go types: %w", err)
	}
	file := plugin.FilesByPath[filepath.Base(input)]
	if file == nil || len(file.Services) == 0 {
		return errors.New("proto service is required")
	}
	if !strings.HasPrefix(string(file.GoImportPath), opts.Module+"/") {
		return errors.New("proto go_package must be a package under the scaffold module")
	}
	for _, dependency := range plugin.Files {
		if dependency == file || !strings.HasPrefix(string(dependency.GoImportPath), opts.Module+"/") {
			continue
		}
		if err := GenerateStandardProto(ctx, ProtocOptions{ProtoFile: dependency.Desc.Path(), ProtoPath: includes, GoOut: stage, GoGRPCOut: stage, Timeout: opts.Timeout, ExtraArgs: []string{"--go_opt=module=" + opts.Module, "--go-grpc_opt=module=" + opts.Module}}); err != nil {
			return err
		}
	}
	files := make(map[string][]byte)
	if err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == descriptorPath {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular protoc output %s", path)
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		content, err := ReadFileUnderRoot(stage, rel, "protoc output")
		if err != nil {
			return err
		}
		files[rel] = content
		return nil
	}); err != nil {
		return err
	}
	owned := make(map[string]bool)
	if err := renderGRPCScaffold(plugin, file, opts, files, owned); err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for name, content := range files {
		target, err := safeRelativeTarget(opts.Dir, name, "grpc scaffold")
		if err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("grpc scaffold target %q must be a regular file", name)
			}
			if !owned[name] {
				existing, err := ReadFileUnderRoot(opts.Dir, name, "grpc scaffold")
				if err != nil {
					return err
				}
				header, _, _ := strings.Cut(string(content), "\n")
				if !strings.HasPrefix(string(existing), header+"\n") {
					return fmt.Errorf("refusing to overwrite non-generated file %q", name)
				}
				for line := range strings.SplitSeq(string(content), "\n") {
					if strings.HasPrefix(line, "// source: ") && !strings.Contains(string(existing), "\n"+line+"\n") {
						return fmt.Errorf("grpc scaffold output %q belongs to another proto", name)
					}
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if filepath.Ext(name) == ".go" {
			formatted, err := format.Source(content)
			if err != nil {
				return fmt.Errorf("format %s: %w", name, err)
			}
			files[name] = formatted
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		if owned[name] {
			if err := writeGeneratedExtensionFile(opts.Dir, name, files[name]); err != nil {
				return err
			}
		} else if err := writeGeneratedFileUnder(opts.Dir, name, files[name]); err != nil {
			return err
		}
	}
	return nil
}

func renderGRPCScaffold(plugin *protogen.Plugin, file *protogen.File, opts GRPCScaffoldOptions, files map[string][]byte, owned map[string]bool) error {
	data := serviceScaffoldData(ServiceScaffoldOptions{Name: opts.Name, Module: opts.Module, Dir: opts.Dir, Kind: "rpc", Profile: string(ProfileGoZeroCompatible)})
	main := strings.ReplaceAll(goZeroRPCMainTemplate, "\t\"{{.Module}}/internal/pb\"\n", "")
	main = strings.ReplaceAll(main, "pb.RegisterGreeterServer(grpcServer.GRPCServer(), appserver.NewGreeterServer(stx))", "appserver.RegisterServices(grpcServer.GRPCServer(), stx)")
	config := strings.ReplaceAll(goZeroRPCConfigTemplate, "\"greeter-timeout\"", "\"default-timeout\"")
	config = strings.ReplaceAll(config, ", \"method\": \"SayHello\"", "")
	for path, template := range map[string]string{
		"go.mod": goModTemplate,
		filepath.Join("cmd", opts.Name, "main.go"): main,
		filepath.Join("etc", opts.Name+".json"):    config,
		"internal/config/config.go":                goZeroRPCConfigGoTemplate,
		"internal/discovery/registry.go":           goZeroRPCDiscoveryTemplate,
		"internal/svc/servicecontext.go":           goZeroRPCSvcTemplate,
	} {
		files[path] = []byte(render(template, data))
		owned[path] = true
	}
	serverPath := "internal/server/" + opts.Name + "_grpc.gen.go"
	server := plugin.NewGeneratedFile(serverPath, protogen.GoImportPath(opts.Module+"/internal/server"))
	server.P("// Code generated by gofly. DO NOT EDIT.")
	server.P("// source: ", file.Desc.Path())
	server.P("package server")
	ident := func(path, name string) protogen.GoIdent {
		return protogen.GoIdent{GoImportPath: protogen.GoImportPath(path), GoName: name}
	}
	stx := ident(opts.Module+"/internal/svc", "ServiceContext")
	server.P("func RegisterServices(registrar ", ident("google.golang.org/grpc", "ServiceRegistrar"), ", stx *", stx, ") {")
	for _, service := range file.Services {
		server.P(ident(string(file.GoImportPath), "Register"+service.GoName+"Server"), "(registrar, &", service.GoName, "Server{stx: stx})")
	}
	server.P("}")
	for _, service := range file.Services {
		server.P("type ", service.GoName, "Server struct {", ident(string(file.GoImportPath), "Unimplemented"+service.GoName+"Server"), "; stx *", stx, "}")
		for _, method := range service.Methods {
			logicPath := opts.Module + "/internal/logic/" + strings.ToLower(service.GoName)
			path := "internal/logic/" + strings.ToLower(service.GoName) + "/" + strings.ToLower(method.GoName) + "logic.go"
			if _, exists := files[path]; exists {
				return fmt.Errorf("duplicate RPC logic output %q", path)
			}
			logic := plugin.NewGeneratedFile(path, protogen.GoImportPath(logicPath))
			logic.P("package ", strings.ToLower(service.GoName))
			logic.P("type ", method.GoName, "Logic struct {ctx ", ident("context", "Context"), "; stx *", stx, "}")
			logic.P("func New", method.GoName, "Logic(ctx ", ident("context", "Context"), ", stx *", stx, ") *", method.GoName, "Logic {return &", method.GoName, "Logic{ctx:ctx,stx:stx}}")
			var request, serverRequest, result, serverResult string
			args, ctx := "req", "ctx"
			stub := "return nil, " + logic.QualifiedGoIdent(ident("google.golang.org/grpc/status", "Error")) + "(" + logic.QualifiedGoIdent(ident("google.golang.org/grpc/codes", "Unimplemented")) + ", \"not implemented\")"
			if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
				streamType := ident(string(file.GoImportPath), service.GoName+"_"+method.GoName+"Server")
				request = "stream " + logic.QualifiedGoIdent(streamType)
				serverRequest = "stream " + server.QualifiedGoIdent(streamType)
				args, ctx = "stream", "stream.Context()"
				if !method.Desc.IsStreamingClient() {
					request = "req *" + logic.QualifiedGoIdent(method.Input.GoIdent) + ", " + request
					serverRequest = "req *" + server.QualifiedGoIdent(method.Input.GoIdent) + ", " + serverRequest
					args = "req, stream"
				}
				result, serverResult = "error", "error"
				stub = strings.Replace(stub, "return nil, ", "return ", 1)
			} else {
				request = "req *" + logic.QualifiedGoIdent(method.Input.GoIdent)
				serverRequest = "ctx " + server.QualifiedGoIdent(ident("context", "Context")) + ", req *" + server.QualifiedGoIdent(method.Input.GoIdent)
				result = "(*" + logic.QualifiedGoIdent(method.Output.GoIdent) + ", error)"
				serverResult = "(*" + server.QualifiedGoIdent(method.Output.GoIdent) + ", error)"
			}
			logic.P("func (l *", method.GoName, "Logic) ", method.GoName, "(", request, ") ", result, " {", stub, "}")
			server.P("func (s *", service.GoName, "Server) ", method.GoName, "(", serverRequest, ") ", serverResult, " {return ", ident(logicPath, "New"+method.GoName+"Logic"), "(", ctx, ", s.stx).", method.GoName, "(", args, ")}")
			content, err := logic.Content()
			if err != nil {
				return err
			}
			files[path], owned[path] = content, true
		}
	}
	content, err := server.Content()
	if err != nil {
		return err
	}
	files[serverPath] = content
	clientPath := "pkg/client/" + opts.Name + "_grpc.gen.go"
	client := plugin.NewGeneratedFile(clientPath, protogen.GoImportPath(opts.Module+"/pkg/client"))
	client.P("// Code generated by gofly. DO NOT EDIT.")
	client.P("// source: ", file.Desc.Path())
	client.P("package client")
	for _, service := range file.Services {
		client.P("func New", service.GoName, "(ctx ", ident("context", "Context"), ", target string, rules *", ident("github.com/imajinyun/gofly/core/governance", "RuleSet"), ", opts ...", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientOption"), ") (", ident(string(file.GoImportPath), service.GoName+"Client"), ", *", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientConn"), ", error) {")
		client.P("conn, err := ", ident("github.com/imajinyun/gofly/rpc/grpc", "NewDefaultClient"), "(ctx, target, ", fmt.Sprintf("%q", service.Desc.FullName()), ", rules, nil, opts...)")
		client.P("if err != nil {return nil,nil,err}; return ", ident(string(file.GoImportPath), "New"+service.GoName+"Client"), "(conn.Conn()),conn,nil }")
	}
	content, err = client.Content()
	if err != nil {
		return err
	}
	files[clientPath] = content
	return nil
}
