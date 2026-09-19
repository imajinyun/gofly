package generator

import (
	"context"
	"encoding/json"
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
	ProtoFile       string
	ProtoFiles      []string
	ProtoPath       []string
	Dir             string
	Module          string
	Name            string
	NameFromPackage bool
	NoClient        bool
	Multiple        bool
	RequireMultiple bool
	Protoc          string
	Timeout         time.Duration
}

func GenerateGRPCScaffold(ctx context.Context, opts GRPCScaffoldOptions) error {
	protoFiles := grpcScaffoldProtoFiles(opts)
	if len(protoFiles) == 0 {
		return errors.New("proto file is required")
	}
	opts.ProtoFile = protoFiles[0]
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
			module, err = inferGRPCScaffoldModule(opts)
			if err != nil {
				return err
			}
		}
		opts.Module = module
	}
	if !isSafeProjectFeatureModulePath(opts.Module) {
		return fmt.Errorf("invalid scaffold module %q", opts.Module)
	}
	if err := validateNativeGRPCToolchain(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "gofly-grpc-scaffold-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	protoNames, includes, err := resolveGRPCScaffoldInputs(protoFiles, opts.ProtoPath)
	if err != nil {
		return err
	}
	descriptorPath := filepath.Join(stage, "descriptor.pb")
	if err := GenerateStandardProto(ctx, ProtocOptions{
		ProtoFiles: protoNames, ProtoPath: includes, GoOut: stage, GoGRPCOut: stage, Protoc: opts.Protoc, Timeout: opts.Timeout,
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
	plugin, err := (protogen.Options{}).New(&pluginpb.CodeGeneratorRequest{ProtoFile: descriptors.File, FileToGenerate: protoNames})
	if err != nil {
		return fmt.Errorf("resolve proto Go types: %w", err)
	}
	primaryFiles := make([]*protogen.File, 0, len(protoNames))
	serviceCount := 0
	for _, name := range protoNames {
		file := plugin.FilesByPath[name]
		if file == nil {
			return fmt.Errorf("generated proto descriptor %q is missing", name)
		}
		if !strings.HasPrefix(string(file.GoImportPath), opts.Module+"/") {
			return fmt.Errorf("proto %q go_package must be a package under the scaffold module", name)
		}
		primaryFiles = append(primaryFiles, file)
		serviceCount += len(file.Services)
	}
	if serviceCount == 0 {
		return errors.New("proto service is required")
	}
	if opts.RequireMultiple && serviceCount > 1 && !opts.Multiple {
		return errors.New("proto inputs define multiple services; rerun with --multiple")
	}
	if opts.Name == "" {
		opts.Name = strings.TrimSuffix(filepath.Base(opts.ProtoFile), filepath.Ext(opts.ProtoFile))
		if opts.NameFromPackage && strings.TrimSpace(string(primaryFiles[0].Desc.Package())) != "" {
			opts.Name = lowerName(string(primaryFiles[0].Desc.Package()))
		}
	}
	if !token.IsIdentifier(opts.Name) {
		return fmt.Errorf("invalid scaffold name %q", opts.Name)
	}
	serverTarget := "internal/api/rpc/" + opts.Name + "_grpc.gen.go"
	if opts.Multiple {
		serverTarget = "internal/api/rpc/register.gen.go"
	}
	serverTargetPath, err := safeRelativeTarget(opts.Dir, serverTarget, "grpc scaffold")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(serverTargetPath))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_grpc.gen.go") && entry.Name() != filepath.Base(serverTargetPath) {
			return errors.New("another grpc scaffold already owns this output root")
		}
	}
	primaryPaths := make(map[string]struct{}, len(primaryFiles))
	for _, file := range primaryFiles {
		primaryPaths[file.Desc.Path()] = struct{}{}
	}
	for _, dependency := range plugin.Files {
		if _, primary := primaryPaths[dependency.Desc.Path()]; primary || !strings.HasPrefix(string(dependency.GoImportPath), opts.Module+"/") {
			continue
		}
		if err := GenerateStandardProto(ctx, ProtocOptions{ProtoFile: dependency.Desc.Path(), ProtoPath: includes, GoOut: stage, GoGRPCOut: stage, Protoc: opts.Protoc, Timeout: opts.Timeout, ExtraArgs: []string{"--go_opt=module=" + opts.Module, "--go-grpc_opt=module=" + opts.Module}}); err != nil {
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
	if err := renderGRPCScaffold(plugin, primaryFiles, opts, files, owned); err != nil {
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
				for line := range strings.SplitSeq(string(existing), "\n") {
					if strings.HasPrefix(line, "// source: ") && !strings.Contains(string(content), "\n"+line+"\n") {
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
			var err error
			if filepath.Clean(name) == filepath.Join("etc", opts.Name+".json") {
				err = mergeGRPCScaffoldJSONDefaults(opts.Dir, name, files[name])
			} else {
				err = writeGeneratedExtensionFile(opts.Dir, name, files[name])
			}
			if err != nil {
				return err
			}
		} else if err := writeGeneratedFileUnder(opts.Dir, name, files[name]); err != nil {
			return err
		}
	}
	return nil
}

func mergeGRPCScaffoldJSONDefaults(root, name string, defaults []byte) error {
	existing, err := ReadFileUnderRoot(root, name, "grpc scaffold config")
	if errors.Is(err, os.ErrNotExist) {
		return writeGeneratedExtensionFile(root, name, defaults)
	}
	if err != nil {
		return err
	}
	target, err := SafeTarget(root, name, "grpc scaffold config")
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("stat existing grpc scaffold config %s: %w", name, err)
	}
	var currentValues, defaultValues map[string]any
	if err := json.Unmarshal(existing, &currentValues); err != nil {
		return fmt.Errorf("decode existing grpc scaffold config %s: %w", name, err)
	}
	if err := json.Unmarshal(defaults, &defaultValues); err != nil {
		return fmt.Errorf("decode generated grpc scaffold config %s: %w", name, err)
	}
	if !mergeMissingJSONValues(currentValues, defaultValues) {
		return nil
	}
	merged, err := json.MarshalIndent(currentValues, "", "  ")
	if err != nil {
		return fmt.Errorf("encode merged grpc scaffold config %s: %w", name, err)
	}
	return WriteFileUnderRoot(root, name, append(merged, '\n'), info.Mode().Perm(), generatedDirMode, "grpc scaffold config")
}

func mergeMissingJSONValues(current, defaults map[string]any) bool {
	changed := false
	for key, defaultValue := range defaults {
		currentValue, exists := current[key]
		if !exists {
			current[key] = defaultValue
			changed = true
			continue
		}
		currentObject, currentOK := currentValue.(map[string]any)
		defaultObject, defaultOK := defaultValue.(map[string]any)
		if currentOK && defaultOK && mergeMissingJSONValues(currentObject, defaultObject) {
			changed = true
		}
	}
	return changed
}

func inferGRPCScaffoldModule(opts GRPCScaffoldOptions) (string, error) {
	doc, err := ParseProtoFileWithIncludes(opts.ProtoFile, opts.ProtoPath)
	if err != nil {
		return "", fmt.Errorf("infer scaffold module from proto: %w", err)
	}
	goPackage := strings.TrimSpace(protoGoImportPath(doc.GoPackage))
	for _, marker := range []string{"/internal/", "/pkg/"} {
		if index := strings.Index(goPackage, marker); index > 0 {
			return goPackage[:index], nil
		}
	}
	if base := strings.TrimSpace(filepath.Base(goPackage)); goPackage != "" && base != "." && base != string(filepath.Separator) {
		if module := strings.TrimSuffix(goPackage, "/"+base); module != "" {
			return module, nil
		}
	}
	base := lowerName(filepath.Base(filepath.Clean(opts.Dir)))
	if base != "" && base != "." {
		return base, nil
	}
	return "", errors.New("module is required; pass --module or declare option go_package under the project module")
}

func grpcScaffoldProtoFiles(opts GRPCScaffoldOptions) []string {
	values := make([]string, 0, 1+len(opts.ProtoFiles))
	if strings.TrimSpace(opts.ProtoFile) != "" {
		values = append(values, strings.TrimSpace(opts.ProtoFile))
	}
	for _, value := range opts.ProtoFiles {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func resolveGRPCScaffoldInputs(protoFiles, includePaths []string) ([]string, []string, error) {
	absIncludes := make([]string, 0, len(includePaths)+len(protoFiles))
	for _, path := range includePaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, err
		}
		absIncludes = append(absIncludes, filepath.Clean(abs))
	}
	absIncludes = uniqueCleanPaths(absIncludes)
	absFiles := make([]string, 0, len(protoFiles))
	for _, name := range protoFiles {
		abs, err := filepath.Abs(name)
		if err != nil {
			return nil, nil, err
		}
		abs = filepath.Clean(abs)
		absFiles = append(absFiles, abs)
		covered := false
		for _, include := range absIncludes {
			rel, relErr := filepath.Rel(include, abs)
			if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		if !covered {
			absIncludes = append(absIncludes, filepath.Dir(abs))
		}
	}
	absIncludes = uniqueCleanPaths(absIncludes)
	resolved := make([]string, 0, len(absFiles))
	seen := make(map[string]struct{}, len(absFiles))
	for _, abs := range absFiles {
		name := ""
		for _, include := range absIncludes {
			rel, err := filepath.Rel(include, abs)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			name = filepath.ToSlash(rel)
			break
		}
		if name == "" {
			return nil, nil, fmt.Errorf("proto file %q is outside configured include paths", abs)
		}
		if _, ok := seen[name]; ok {
			return nil, nil, fmt.Errorf("duplicate proto input path %q", name)
		}
		seen[name] = struct{}{}
		resolved = append(resolved, name)
	}
	return resolved, absIncludes, nil
}

func uniqueCleanPaths(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = filepath.Clean(value)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func renderGRPCScaffold(plugin *protogen.Plugin, primaryFiles []*protogen.File, opts GRPCScaffoldOptions, files map[string][]byte, owned map[string]bool) error {
	type serviceSource struct {
		file    *protogen.File
		service *protogen.Service
	}
	services := make([]serviceSource, 0)
	seenServices := make(map[string]struct{})
	seenServiceOutputs := make(map[string]string)
	for _, primary := range primaryFiles {
		for _, service := range primary.Services {
			name := string(service.Desc.FullName())
			if _, exists := seenServices[name]; exists {
				return fmt.Errorf("duplicate RPC service %q", name)
			}
			outputName := strings.ToLower(service.GoName)
			if previous, exists := seenServiceOutputs[outputName]; exists {
				return fmt.Errorf("RPC services %q and %q have conflicting output name %q", previous, name, outputName)
			}
			seenServices[name] = struct{}{}
			seenServiceOutputs[outputName] = name
			services = append(services, serviceSource{file: primary, service: service})
		}
	}
	data := serviceScaffoldData(ServiceScaffoldOptions{Name: opts.Name, Module: opts.Module, Dir: opts.Dir, Kind: "rpc", Profile: string(ProfileGoZeroCompatible)})
	data["RPCService"] = string(services[0].service.Desc.FullName())
	methodRules := make([]generatedRPCMethodRule, 0)
	for _, item := range services {
		for _, method := range item.service.Methods {
			methodRules = append(methodRules, generatedRPCMethodRule{
				Service: string(item.service.Desc.FullName()),
				Method:  string(method.Desc.Name()),
			})
		}
	}
	data["RPCMethodRulesJSON"] = generatedRPCMethodTimeoutRulesJSON(methodRules)
	data["GovernanceRulesJSON"] = data["RPCMethodRulesJSON"]
	defaultRules := plugin.NewGeneratedFile("internal/config/rpc_methods.gen.go", protogen.GoImportPath(opts.Module+"/internal/config"))
	defaultRules.P("// Code generated by gofly. DO NOT EDIT.")
	writeGRPCScaffoldSources(defaultRules, primaryFiles)
	defaultRules.P("package config")
	defaultRules.P("func DefaultRPCMethodRules() []", protogen.GoIdent{GoImportPath: "github.com/imajinyun/gofly/core/governance", GoName: "Rule"}, " { return []", protogen.GoIdent{GoImportPath: "github.com/imajinyun/gofly/core/governance", GoName: "Rule"}, "{")
	for _, rule := range methodRules {
		defaultRules.P("{Name:", fmt.Sprintf("%q", generatedRPCMethodTimeoutRuleName(rule)), ", Priority:-1000, Transport:", protogen.GoIdent{GoImportPath: "github.com/imajinyun/gofly/core/governance", GoName: "TransportRPC"}, ", Service:", fmt.Sprintf("%q", rule.Service), ", Method:", fmt.Sprintf("%q", rule.Method), ", Policy:", protogen.GoIdent{GoImportPath: "github.com/imajinyun/gofly/core/governance", GoName: "Policy"}, "{Timeout:2*", protogen.GoIdent{GoImportPath: "time", GoName: "Second"}, "}},")
	}
	defaultRules.P("} }")
	defaultRules.P("func DefaultRPCMethodGovernance() ", protogen.GoIdent{GoImportPath: "github.com/imajinyun/gofly/core/governance", GoName: "Plugin"}, " { return ", protogen.GoIdent{GoImportPath: "github.com/imajinyun/gofly/core/governance", GoName: "NewPlugin"}, "(\"descriptor-rpc-method-defaults\", DefaultRPCMethodRules()...) }")
	defaultRulesContent, err := defaultRules.Content()
	if err != nil {
		return err
	}
	files["internal/config/rpc_methods.gen.go"] = defaultRulesContent
	main := strings.ReplaceAll(goZeroRPCMainTemplate, "\t\"{{.Module}}/internal/pb\"\n", "")
	main = strings.ReplaceAll(main, "pb.RegisterGreeterServer(grpcServer.GRPCServer(), apprpc.NewGreeterServer(stx))", "apprpc.RegisterServices(grpcServer.GRPCServer(), stx)")
	for path, template := range map[string]string{
		"go.mod":   goModTemplate,
		"Makefile": makefileTemplate,
		filepath.Join("cmd", opts.Name, "main.go"):    main,
		filepath.Join("etc", opts.Name+".json"):       goZeroRPCConfigTemplate,
		filepath.Join("etc", "governance.json"):       governanceTemplate,
		"bin/production-check.sh":                     goZeroRPCProductionCheckScriptTemplate,
		"internal/config/config.go":                   goZeroRPCConfigGoTemplate,
		"internal/config/production_check.go":         goZeroRPCProductionCheckGoTemplate,
		"internal/config/governance_recovery_test.go": goZeroRPCGovernanceRecoveryTestTemplate,
		"internal/discovery/registry.go":              goZeroRPCDiscoveryTemplate,
		"internal/svc/service_context.go":             goZeroRPCSvcTemplate,
	} {
		files[path] = []byte(render(template, data))
		owned[path] = true
	}
	ident := func(path, name string) protogen.GoIdent {
		return protogen.GoIdent{GoImportPath: protogen.GoImportPath(path), GoName: name}
	}
	stx := ident(opts.Module+"/internal/svc", "ServiceContext")
	var combinedServer *protogen.GeneratedFile
	if opts.Multiple {
		registerPath := "internal/api/rpc/register.gen.go"
		combinedServer = plugin.NewGeneratedFile(registerPath, protogen.GoImportPath(opts.Module+"/internal/api/rpc"))
		combinedServer.P("// Code generated by gofly. DO NOT EDIT.")
		writeGRPCScaffoldSources(combinedServer, primaryFiles)
		combinedServer.P("package rpc")
		combinedServer.P("func RegisterServices(registrar ", ident("google.golang.org/grpc", "ServiceRegistrar"), ", stx *", stx, ") {")
		for _, item := range services {
			service := item.service
			combinedServer.P(ident(opts.Module+"/internal/api/rpc/"+strings.ToLower(service.GoName), "Register"), "(registrar, stx)")
		}
		combinedServer.P("}")
	} else {
		serverPath := "internal/api/rpc/" + opts.Name + "_grpc.gen.go"
		combinedServer = plugin.NewGeneratedFile(serverPath, protogen.GoImportPath(opts.Module+"/internal/api/rpc"))
		combinedServer.P("// Code generated by gofly. DO NOT EDIT.")
		writeGRPCScaffoldSources(combinedServer, primaryFiles)
		combinedServer.P("package rpc")
		combinedServer.P("func RegisterServices(registrar ", ident("google.golang.org/grpc", "ServiceRegistrar"), ", stx *", stx, ") {")
		for _, item := range services {
			service := item.service
			combinedServer.P(ident(string(item.file.GoImportPath), "Register"+service.GoName+"Server"), "(registrar, &", service.GoName, "Server{stx: stx})")
		}
		combinedServer.P("}")
	}
	combinedServer.P("func DiscoveryAliases() []string { return []string{")
	for _, item := range services {
		combinedServer.P(fmt.Sprintf("%q,", item.service.Desc.FullName()))
	}
	combinedServer.P("} }")
	for _, item := range services {
		file, service := item.file, item.service
		server := combinedServer
		serverPath := "internal/api/rpc/" + opts.Name + "_grpc.gen.go"
		if opts.Multiple {
			serviceDir := strings.ToLower(service.GoName)
			serverPath = "internal/api/rpc/" + serviceDir + "/" + serviceDir + "_grpc.gen.go"
			server = plugin.NewGeneratedFile(serverPath, protogen.GoImportPath(opts.Module+"/internal/api/rpc/"+serviceDir))
			server.P("// Code generated by gofly. DO NOT EDIT.")
			server.P("// source: ", file.Desc.Path())
			server.P("package ", serviceDir, "rpc")
			server.P("func Register(registrar ", ident("google.golang.org/grpc", "ServiceRegistrar"), ", stx *", stx, ") {")
			server.P(ident(string(file.GoImportPath), "Register"+service.GoName+"Server"), "(registrar, &", service.GoName, "Server{stx: stx})")
			server.P("}")
		}
		server.P("type ", service.GoName, "Server struct {", ident(string(file.GoImportPath), "Unimplemented"+service.GoName+"Server"), "; stx *", stx, "}")
		for _, method := range service.Methods {
			logicPath := opts.Module + "/internal/app/" + strings.ToLower(service.GoName)
			path := "internal/app/" + strings.ToLower(service.GoName) + "/" + strings.ToLower(method.GoName) + ".go"
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
		if opts.Multiple {
			content, err := server.Content()
			if err != nil {
				return err
			}
			files[serverPath] = content
		}
	}
	serverContent, err := combinedServer.Content()
	if err != nil {
		return err
	}
	if opts.Multiple {
		files["internal/api/rpc/register.gen.go"] = serverContent
	} else {
		files["internal/api/rpc/"+opts.Name+"_grpc.gen.go"] = serverContent
	}
	if opts.NoClient {
		return nil
	}
	var combinedClient *protogen.GeneratedFile
	if !opts.Multiple {
		combinedClient = plugin.NewGeneratedFile("internal/api/rpc/"+opts.Name+"_client.gen.go", protogen.GoImportPath(opts.Module+"/internal/api/rpc"))
		combinedClient.P("// Code generated by gofly. DO NOT EDIT.")
		writeGRPCScaffoldSources(combinedClient, primaryFiles)
		combinedClient.P("package rpc")
	}
	for _, item := range services {
		file, service := item.file, item.service
		clientPath := "internal/api/rpc/" + opts.Name + "_client.gen.go"
		client := combinedClient
		if opts.Multiple {
			serviceDir := strings.ToLower(service.GoName)
			clientPath = "internal/api/rpc/" + serviceDir + "/" + serviceDir + "_client.gen.go"
			client = plugin.NewGeneratedFile(clientPath, protogen.GoImportPath(opts.Module+"/internal/api/rpc/"+serviceDir))
			client.P("// Code generated by gofly. DO NOT EDIT.")
			client.P("// source: ", file.Desc.Path())
			client.P("package ", serviceDir, "rpc")
		}
		client.P("func New", service.GoName, "(ctx ", ident("context", "Context"), ", target string, rules *", ident("github.com/imajinyun/gofly/core/governance", "RuleSet"), ", opts ...", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientOption"), ") (", ident(string(file.GoImportPath), service.GoName+"Client"), ", *", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientConn"), ", error) {")
		client.P("conn, err := ", ident("github.com/imajinyun/gofly/rpc/grpc", "NewDefaultClient"), "(ctx, target, ", fmt.Sprintf("%q", service.Desc.FullName()), ", rules, nil, opts...)")
		client.P("if err != nil {return nil,nil,err}; return ", ident(string(file.GoImportPath), "New"+service.GoName+"Client"), "(conn.Conn()),conn,nil }")
		client.P("func NewDiscovered", service.GoName, "(ctx ", ident("context", "Context"), ", resolver ", ident("github.com/imajinyun/gofly/core/discovery", "Resolver"), ", rules *", ident("github.com/imajinyun/gofly/core/governance", "RuleSet"), ", opts ...", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientOption"), ") (", ident(string(file.GoImportPath), service.GoName+"Client"), ", *", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientConn"), ", error) {")
		client.P("opts = append([]", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientOption"), "{", ident("github.com/imajinyun/gofly/rpc/grpc", "WithDiscoveryResolverOptions"), "(resolver, ", fmt.Sprintf("%q", service.Desc.FullName()), ", []", ident("github.com/imajinyun/gofly/rpc/grpc", "ResolverOption"), "{", ident("github.com/imajinyun/gofly/rpc/grpc", "WithP2CEWMAResolver"), "()})}, opts...)")
		client.P("return New", service.GoName, "(ctx, ", ident("github.com/imajinyun/gofly/rpc/grpc", "Target"), "(", fmt.Sprintf("%q", service.Desc.FullName()), "), rules, opts...) }")
		client.P("func NewConfigured", service.GoName, "(ctx ", ident("context", "Context"), ", resolver ", ident("github.com/imajinyun/gofly/core/discovery", "Resolver"), ", cfg ", ident(opts.Module+"/internal/config", "RPCClientConfig"), ", rules *", ident("github.com/imajinyun/gofly/core/governance", "RuleSet"), ", opts ...", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientOption"), ") (", ident(string(file.GoImportPath), service.GoName+"Client"), ", *", ident("github.com/imajinyun/gofly/rpc/grpc", "ClientConn"), ", error) {")
		client.P(fmt.Sprintf("target, configured, err := cfg.TargetAndOptions(resolver, %q); if err != nil {return nil,nil,err}", service.Desc.FullName()))
		client.P("opts = append(configured, opts...)")
		client.P("return New", service.GoName, "(ctx, target, rules, opts...) }")
		if opts.Multiple {
			content, err := client.Content()
			if err != nil {
				return err
			}
			if _, exists := files[clientPath]; exists {
				return fmt.Errorf("duplicate RPC client output %q", clientPath)
			}
			files[clientPath] = content
		}
	}
	if !opts.Multiple {
		content, err := combinedClient.Content()
		if err != nil {
			return err
		}
		files["internal/api/rpc/"+opts.Name+"_client.gen.go"] = content
	}
	return nil
}

func writeGRPCScaffoldSources(file *protogen.GeneratedFile, primaryFiles []*protogen.File) {
	for _, primary := range primaryFiles {
		file.P("// source: ", primary.Desc.Path())
	}
}
