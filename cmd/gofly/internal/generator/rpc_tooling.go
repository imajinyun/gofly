package generator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type RPCIDLOptions struct {
	File   string
	Format string
}

type RPCScaffoldOptions struct {
	IDLFile string
	Dir     string
	Package string
}

type RPCMiddlewareOptions struct {
	Name string
	Dir  string
}

type RPCIDLReport struct {
	Kind      string   `json:"kind"`
	Package   string   `json:"package,omitempty"`
	GoPackage string   `json:"goPackage,omitempty"`
	Imports   []string `json:"imports,omitempty"`
	Messages  int      `json:"messages"`
	Enums     int      `json:"enums"`
	Services  int      `json:"services"`
	Methods   int      `json:"methods"`
	Streaming int      `json:"streaming"`
}

var (
	thriftNamespaceRE = regexp.MustCompile(`^namespace\s+([A-Za-z_][A-Za-z0-9_.]*)\s+([A-Za-z_][A-Za-z0-9_./-]*)`)
	thriftIncludeRE   = regexp.MustCompile(`^include\s+"([^"]+)"`)
	thriftStructRE    = regexp.MustCompile(`^(?:struct|exception)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	thriftServiceRE   = regexp.MustCompile(`^service\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:extends\s+[A-Za-z_][A-Za-z0-9_.]*)?\s*\{`)
	thriftFieldRE     = regexp.MustCompile(`^[0-9]+\s*:\s*(?:required\s+|optional\s+)?(.+?)\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:[=,;]|$)`)
	thriftMethodRE    = regexp.MustCompile(`^(?:oneway\s+)?([A-Za-z_][A-Za-z0-9_.<>]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)`)
)

func ReadRPCIDL(path string) (IDLDocument, error) {
	if strings.TrimSpace(path) == "" {
		return IDLDocument{}, errors.New("idl file is required")
	}
	// #nosec G304 -- RPC IDL files are explicit proto/thrift generator inputs from CLI flags or caller options.
	content, err := os.ReadFile(path)
	if err != nil {
		return IDLDocument{}, fmt.Errorf("read idl file: %w", err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".thrift":
		return ParseThrift(string(content))
	default:
		return ParseProto(string(content))
	}
}

func ParseThrift(content string) (IDLDocument, error) {
	doc := IDLDocument{Kind: "thrift"}
	lines := strings.Split(stripBlockComments(content), "\n")
	var currentStruct *IDLMessage
	var currentService *IDLService
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(stripLineComment(raw))
		line = strings.TrimRight(line, ",;")
		if line == "" {
			continue
		}
		if currentStruct != nil {
			if line == "}" {
				doc.Messages = append(doc.Messages, *currentStruct)
				currentStruct = nil
				continue
			}
			match := thriftFieldRE.FindStringSubmatch(line)
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse thrift line %d: invalid field", lineNo)
			}
			currentStruct.Fields = append(currentStruct.Fields, IDLField{Name: match[2], Type: thriftTypeToProto(match[1])})
			continue
		}
		if currentService != nil {
			if line == "}" {
				doc.Services = append(doc.Services, *currentService)
				currentService = nil
				continue
			}
			match := thriftMethodRE.FindStringSubmatch(line)
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse thrift line %d: invalid service method", lineNo)
			}
			currentService.Methods = append(currentService.Methods, IDLMethod{Name: match[2], Request: thriftMethodRequest(match[2], match[3]), Response: exportName(lastIdent(match[1]))})
			continue
		}
		if match := thriftNamespaceRE.FindStringSubmatch(line); match != nil {
			if match[1] == "go" {
				doc.GoPackage = match[2]
			} else if doc.Package == "" {
				doc.Package = match[2]
			}
			continue
		}
		if match := thriftIncludeRE.FindStringSubmatch(line); match != nil {
			doc.Imports = append(doc.Imports, match[1])
			continue
		}
		if match := thriftStructRE.FindStringSubmatch(line); match != nil {
			currentStruct = &IDLMessage{Name: match[1]}
			continue
		}
		if match := thriftServiceRE.FindStringSubmatch(line); match != nil {
			currentService = &IDLService{Name: match[1]}
			continue
		}
	}
	if currentStruct != nil {
		return IDLDocument{}, fmt.Errorf("parse thrift: struct %s is not closed", currentStruct.Name)
	}
	if currentService != nil {
		return IDLDocument{}, fmt.Errorf("parse thrift: service %s is not closed", currentService.Name)
	}
	if len(doc.Services) == 0 && len(doc.Messages) == 0 {
		return IDLDocument{}, errors.New("parse thrift: no struct or service found")
	}
	return doc, nil
}

func RPCIDLReportFor(doc IDLDocument) RPCIDLReport {
	report := RPCIDLReport{Kind: doc.Kind, Package: doc.Package, GoPackage: doc.GoPackage, Imports: append([]string(nil), doc.Imports...), Messages: len(doc.Messages), Enums: len(doc.Enums), Services: len(doc.Services)}
	for _, svc := range doc.Services {
		report.Methods += len(svc.Methods)
		for _, method := range svc.Methods {
			if method.ClientStream || method.ServerStream {
				report.Streaming++
			}
		}
	}
	sort.Strings(report.Imports)
	return report
}

func FormatRPCIDLReport(doc IDLDocument, formatName string) ([]byte, error) {
	report := RPCIDLReportFor(doc)
	switch strings.ToLower(strings.TrimSpace(formatName)) {
	case "", "text":
		var b bytes.Buffer
		fprintf(&b, "kind: %s\n", report.Kind)
		if report.Package != "" {
			fprintf(&b, "package: %s\n", report.Package)
		}
		if report.GoPackage != "" {
			fprintf(&b, "go_package: %s\n", report.GoPackage)
		}
		fprintf(&b, "messages: %d\n", report.Messages)
		fprintf(&b, "enums: %d\n", report.Enums)
		fprintf(&b, "services: %d\n", report.Services)
		fprintf(&b, "methods: %d\n", report.Methods)
		fprintf(&b, "streaming: %d\n", report.Streaming)
		for _, imp := range report.Imports {
			fprintf(&b, "import: %s\n", imp)
		}
		return b.Bytes(), nil
	case "json":
		return json.MarshalIndent(report, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported rpc idl report format %q", formatName)
	}
}

func LintRPCIDL(doc IDLDocument) error {
	if len(doc.Services) == 0 {
		return errors.New("rpc idl service is required")
	}
	messages := make(map[string]struct{}, len(doc.Messages))
	for _, msg := range doc.Messages {
		if strings.TrimSpace(msg.Name) == "" {
			return errors.New("rpc idl message name is required")
		}
		messages[exportName(msg.Name)] = struct{}{}
	}
	for _, svc := range doc.Services {
		if len(svc.Methods) == 0 {
			return fmt.Errorf("rpc idl service %s method is required", svc.Name)
		}
		for _, method := range svc.Methods {
			if method.Request == "" || method.Response == "" {
				return fmt.Errorf("rpc idl method %s.%s request and response are required", svc.Name, method.Name)
			}
			if doc.Kind == "proto" {
				if _, ok := messages[exportName(method.Request)]; !ok {
					return fmt.Errorf("rpc idl method %s.%s request message %s not found", svc.Name, method.Name, method.Request)
				}
				if _, ok := messages[exportName(method.Response)]; !ok {
					return fmt.Errorf("rpc idl method %s.%s response message %s not found", svc.Name, method.Name, method.Response)
				}
			}
		}
	}
	return nil
}

func GenerateRPCClient(opts RPCScaffoldOptions) error {
	doc, err := ReadRPCIDL(opts.IDLFile)
	if err != nil {
		return err
	}
	if err := LintRPCIDL(doc); err != nil {
		return err
	}
	code, err := GenerateRPCClientCode(doc, opts.Package)
	if err != nil {
		return err
	}
	return writeRPCScaffoldFile(opts, "client.go", code)
}

func GenerateRPCServer(opts RPCScaffoldOptions) error {
	doc, err := ReadRPCIDL(opts.IDLFile)
	if err != nil {
		return err
	}
	if err := LintRPCIDL(doc); err != nil {
		return err
	}
	code, err := GenerateRPCServerCode(doc, opts.Package)
	if err != nil {
		return err
	}
	return writeRPCScaffoldFile(opts, "server.go", code)
}

func GenerateRPCClientCode(doc IDLDocument, packageName string) ([]byte, error) {
	if packageName == "" {
		packageName = inferGoPackageName(doc)
	}
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", packageName)
	fprintf(&b, "import (\n\t\"context\"\n\n\t\"github.com/gofly/gofly/rpc\"\n)\n\n")
	for _, svc := range doc.Services {
		serviceName := exportName(svc.Name)
		writeRPCServiceDescriptor(&b, doc, svc)
		fprintf(&b, "type %sEndpointClient struct {\n\tclient rpc.Client\n\tdesc   rpc.ServiceDesc\n}\n\n", serviceName)
		fprintf(&b, "func New%sEndpointClient(client rpc.Client) *%sEndpointClient {\n\treturn &%sEndpointClient{client: client, desc: %sDescriptor()}\n}\n\n", serviceName, serviceName, serviceName, serviceName)
		fprintf(&b, "func New%sHTTPClient(target string, opts ...rpc.ClientOption) (*%sEndpointClient, error) {\n", serviceName, serviceName)
		fprintf(&b, "\tclient, err := rpc.NewClient(target, opts...)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(&b, "\treturn New%sEndpointClient(client), nil\n", serviceName)
		fprintf(&b, "}\n\n")
		fprintf(&b, "func (c *%sEndpointClient) Descriptor() rpc.ServiceDesc {\n\treturn rpc.CloneServiceDesc(c.desc)\n}\n\n", serviceName)
		fprintf(&b, "func (c *%sEndpointClient) RuntimeDescriptor() rpc.Descriptor {\n\treturn c.desc.Descriptor()\n}\n\n", serviceName)
		for _, method := range svc.Methods {
			methodName := exportName(method.Name)
			fprintf(&b, "func (c *%sEndpointClient) %s(ctx context.Context, req *%s) (*%s, error) {\n", serviceName, methodName, exportName(method.Request), exportName(method.Response))
			fprintf(&b, "\tvar resp %s\n", exportName(method.Response))
			fprintf(&b, "\tmethod, err := c.desc.MethodPath(%q)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", methodName)
			fprintf(&b, "\tif err := c.client.Call(ctx, method, req, &resp); err != nil {\n\t\treturn nil, err\n\t}\n")
			fprintf(&b, "\treturn &resp, nil\n}\n\n")
		}
		fprintf(&b, "type %sGenericEndpointClient struct {\n\tinvoker *rpc.GenericInvoker\n\tdesc    rpc.ServiceDesc\n}\n\n", serviceName)
		fprintf(&b, "func New%sGenericEndpointClient(client rpc.GenericClient) (*%sGenericEndpointClient, error) {\n", serviceName, serviceName)
		fprintf(&b, "\tinvoker, err := rpc.NewGenericInvoker(client)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(&b, "\treturn &%sGenericEndpointClient{invoker: invoker, desc: %sDescriptor()}, nil\n", serviceName, serviceName)
		fprintf(&b, "}\n\n")
		fprintf(&b, "func New%sGenericHTTPClient(target string, opts ...rpc.ClientOption) (*%sGenericEndpointClient, error) {\n", serviceName, serviceName)
		fprintf(&b, "\tclient, err := rpc.NewClient(target, opts...)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(&b, "\treturn New%sGenericEndpointClient(client)\n", serviceName)
		fprintf(&b, "}\n\n")
		fprintf(&b, "func (c *%sGenericEndpointClient) Descriptor() rpc.ServiceDesc {\n\treturn rpc.CloneServiceDesc(c.desc)\n}\n\n", serviceName)
		fprintf(&b, "func (c *%sGenericEndpointClient) RuntimeDescriptor() rpc.Descriptor {\n\treturn c.desc.Descriptor()\n}\n\n", serviceName)
		fprintf(&b, "func (c *%sGenericEndpointClient) Invoke(ctx context.Context, method string, req any) (rpc.GenericResponse, error) {\n", serviceName)
		fprintf(&b, "\treturn c.invoker.InvokeMethod(ctx, c.desc, method, req)\n")
		fprintf(&b, "}\n\n")
	}
	return formatRPCGenerated(b.Bytes(), "format rpc client code")
}

func GenerateRPCServerCode(doc IDLDocument, packageName string) ([]byte, error) {
	if packageName == "" {
		packageName = inferGoPackageName(doc)
	}
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", packageName)
	fprintf(&b, "import (\n\t\"context\"\n\n\t\"github.com/gofly/gofly/rpc\"\n)\n\n")
	for _, svc := range doc.Services {
		serviceName := exportName(svc.Name)
		writeRPCServiceInterface(&b, svc)
		writeRPCServiceDescriptor(&b, doc, svc)
		writeRPCServiceDescBinding(&b, serviceName, svc)
		fprintf(&b, "func Register%sEndpointServer(s *rpc.HTTPServer, impl %sEndpoint) error {\n", serviceName, serviceName)
		fprintf(&b, "\treturn s.RegisterService(%sServiceDesc(impl), impl)\n", serviceName)
		fprintf(&b, "}\n\n")
		fprintf(&b, "func New%sHTTPServer(impl %sEndpoint, opts ...rpc.ServerOption) (*rpc.HTTPServer, error) {\n", serviceName, serviceName)
		fprintf(&b, "\tserver := rpc.NewServer(opts...)\n")
		fprintf(&b, "\tif err := Register%sEndpointServer(server, impl); err != nil {\n\t\treturn nil, err\n\t}\n", serviceName)
		fprintf(&b, "\treturn server, nil\n")
		fprintf(&b, "}\n\n")
		fprintf(&b, "type %sServer struct{}\n\n", serviceName)
		fprintf(&b, "func New%sServer() *%sServer {\n\treturn &%sServer{}\n}\n\n", serviceName, serviceName, serviceName)
		fprintf(&b, "func (s *%sServer) Descriptor() rpc.ServiceDesc {\n\treturn %sDescriptor()\n}\n\n", serviceName, serviceName)
		fprintf(&b, "func (s *%sServer) RuntimeDescriptor() rpc.Descriptor {\n\treturn %sDescriptor().Descriptor()\n}\n\n", serviceName, serviceName)
		fprintf(&b, "func (s *%sServer) RegisterRPC(server *rpc.HTTPServer) error {\n\treturn Register%sEndpointServer(server, s)\n}\n\n", serviceName, serviceName)
		for _, method := range svc.Methods {
			methodName := exportName(method.Name)
			fprintf(&b, "func (s *%sServer) %s(ctx context.Context, req *%s) (*%s, error) {\n", serviceName, methodName, exportName(method.Request), exportName(method.Response))
			fprintf(&b, "\treturn nil, rpc.NewError(rpc.CodeUnimplemented, %q)\n}\n\n", strings.ToLower(serviceName+"."+methodName)+" is not implemented")
		}
	}
	return formatRPCGenerated(b.Bytes(), "format rpc server code")
}

func writeRPCServiceInterface(b *bytes.Buffer, svc IDLService) {
	serviceName := exportName(svc.Name)
	fprintf(b, "type %sEndpoint interface {\n", serviceName)
	for _, method := range svc.Methods {
		fprintf(b, "\t%s(ctx context.Context, req *%s) (*%s, error)\n", exportName(method.Name), exportName(method.Request), exportName(method.Response))
	}
	fprintf(b, "}\n\n")
}

func writeRPCServiceDescriptor(b *bytes.Buffer, doc IDLDocument, svc IDLService) {
	serviceName := exportName(svc.Name)
	fprintf(b, "func %sDescriptor() rpc.ServiceDesc {\n", serviceName)
	fprintf(b, "\treturn rpc.ServiceDesc{Name: %q, Methods: []rpc.MethodDesc{\n", serviceFullName(doc, svc.Name))
	for _, method := range svc.Methods {
		methodName := exportName(method.Name)
		requestName := exportName(method.Request)
		responseName := exportName(method.Response)
		fprintf(b, "\t\t{Name: %q, NewRequest: func() any { return new(%s) }, Request: %q, Response: %q},\n", methodName, requestName, requestName, responseName)
	}
	fprintf(b, "\t}}\n")
	fprintf(b, "}\n\n")
}

func writeRPCServiceDescBinding(b *bytes.Buffer, serviceName string, svc IDLService) {
	fprintf(b, "func %sServiceDesc(impl %sEndpoint) rpc.ServiceDesc {\n", serviceName, serviceName)
	fprintf(b, "\tdesc := %sDescriptor()\n", serviceName)
	for i, method := range svc.Methods {
		methodName := exportName(method.Name)
		requestName := exportName(method.Request)
		writeDefensiveHandlerBinding(b, i, methodName, requestName)
	}
	fprintf(b, "\treturn desc\n")
	fprintf(b, "}\n\n")
	fprintf(b, "func Bind%sGenericHandlers(handlers map[string]rpc.GenericHandler) (rpc.ServiceDesc, error) {\n", serviceName)
	fprintf(b, "\treturn rpc.BindGenericHandlers(%sDescriptor(), handlers)\n", serviceName)
	fprintf(b, "}\n\n")
}

func GenerateRPCMiddleware(opts RPCMiddlewareOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return errors.New("rpc middleware name is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	typeName := exportName(opts.Name) + "UnaryInterceptor"
	var b bytes.Buffer
	fprintf(&b, "package middleware\n\n")
	fprintf(&b, "import (\n\t\"context\"\n\n\t\"google.golang.org/grpc\"\n)\n\n")
	fprintf(&b, "func %s() grpc.UnaryServerInterceptor {\n", typeName)
	fprintf(&b, "\treturn func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {\n")
	fprintf(&b, "\t\treturn handler(ctx, req)\n\t}\n}\n")
	formatted, err := formatRPCGenerated(b.Bytes(), "format rpc middleware code")
	if err != nil {
		return err
	}
	path := filepath.Join(opts.Dir, "internal", "rpc", "middleware", lowerSnake(opts.Name)+".go")
	return writeGeneratedFile(path, formatted)
}

func GenerateProtoFromThrift(opts RPCScaffoldOptions) error {
	doc, err := ReadRPCIDL(opts.IDLFile)
	if err != nil {
		return err
	}
	if doc.Kind != "thrift" {
		return errors.New("thrift idl file is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	proto := ThriftAsProto(doc)
	name := strings.TrimSuffix(filepath.Base(opts.IDLFile), filepath.Ext(opts.IDLFile)) + ".proto"
	path := filepath.Join(opts.Dir, name)
	return writeGeneratedFile(path, proto)
}

func ThriftAsProto(doc IDLDocument) []byte {
	var b bytes.Buffer
	fprintf(&b, "syntax = \"proto3\";\n\n")
	pkg := doc.Package
	if pkg == "" {
		pkg = doc.GoPackage
	}
	if pkg != "" {
		fprintf(&b, "package %s;\n\n", strings.ReplaceAll(pkg, "-", "_"))
	}
	for _, msg := range doc.Messages {
		fprintf(&b, "message %s {\n", exportName(msg.Name))
		for i, field := range msg.Fields {
			fprintf(&b, "  %s %s = %d;\n", field.Type, lowerSnake(field.Name), i+1)
		}
		fprintf(&b, "}\n\n")
	}
	for _, svc := range doc.Services {
		fprintf(&b, "service %s {\n", exportName(svc.Name))
		for _, method := range svc.Methods {
			fprintf(&b, "  rpc %s(%s) returns (%s);\n", exportName(method.Name), exportName(method.Request), exportName(method.Response))
		}
		fprintf(&b, "}\n")
	}
	return b.Bytes()
}

func writeRPCScaffoldFile(opts RPCScaffoldOptions, suffix string, code []byte) error {
	if opts.Dir == "" {
		opts.Dir = "."
	}
	base := strings.TrimSuffix(filepath.Base(opts.IDLFile), filepath.Ext(opts.IDLFile))
	if base == "" || base == "." {
		base = "rpc"
	}
	path := filepath.Join(opts.Dir, base+"_"+suffix)
	return writeGeneratedFile(path, code)
}

func formatRPCGenerated(src []byte, context string) ([]byte, error) {
	out, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", context, err)
	}
	return out, nil
}

func thriftMethodRequest(methodName string, params string) string {
	match := thriftFieldRE.FindStringSubmatch(strings.TrimSpace(params))
	if match == nil {
		return exportName(methodName) + "Request"
	}
	return exportName(lastIdent(match[1]))
}

func thriftTypeToProto(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "list<") && strings.HasSuffix(value, ">") {
		return "repeated " + thriftTypeToProto(strings.TrimSuffix(strings.TrimPrefix(value, "list<"), ">"))
	}
	switch strings.ToLower(lastIdent(value)) {
	case "string", "binary":
		return "string"
	case "bool":
		return "bool"
	case "byte", "i8", "i16", "i32":
		return "int32"
	case "i64":
		return "int64"
	case "double":
		return "double"
	case "void":
		return "Empty"
	default:
		return exportName(lastIdent(value))
	}
}
