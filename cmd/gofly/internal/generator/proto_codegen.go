package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

type RPCOptions struct {
	ProtoFile      string
	Dir            string
	Package        string
	Profile        string
	NoClient       bool
	Multiple       bool
	WithMiddleware bool
	WithRecovery   bool
	WithValidator  bool
}

type RPCCodeOptions struct {
	Profile        GenerationProfile
	NoClient       bool
	WithMiddleware bool
	WithRecovery   bool
	WithValidator  bool
}

func GenerateRPCFromProto(opts RPCOptions) error {
	if opts.ProtoFile == "" {
		return errors.New("proto file is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	content, err := os.ReadFile(opts.ProtoFile)
	if err != nil {
		return fmt.Errorf("read proto file: %w", err)
	}
	doc, err := ParseProto(string(content))
	if err != nil {
		return err
	}
	codeOpts, err := rpcCodeOptionsFromRPCOptions(opts)
	if err != nil {
		return err
	}
	if opts.Multiple {
		for _, svc := range doc.Services {
			serviceDoc := doc
			serviceDoc.Services = []IDLService{svc}
			code, err := GenerateRPCCodeWithOptions(serviceDoc, opts.Package, codeOpts)
			if err != nil {
				return err
			}
			name := strings.TrimSuffix(filepath.Base(opts.ProtoFile), filepath.Ext(opts.ProtoFile)) + ".gofly.go"
			path := filepath.Join(opts.Dir, lowerSnake(svc.Name), name)
			if err := writeRPCGeneratedFile(path, code); err != nil {
				return err
			}
		}
		return nil
	}
	code, err := GenerateRPCCodeWithOptions(doc, opts.Package, codeOpts)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(opts.ProtoFile), filepath.Ext(opts.ProtoFile)) + ".gofly.go"
	path := filepath.Join(opts.Dir, name)
	if err := writeRPCGeneratedFile(path, code); err != nil {
		return err
	}
	return nil
}

func GenerateRPCCode(doc IDLDocument, packageName string) ([]byte, error) {
	return GenerateRPCCodeWithOptions(doc, packageName, RPCCodeOptions{})
}

func rpcCodeOptionsFromRPCOptions(opts RPCOptions) (RPCCodeOptions, error) {
	profile, err := normalizeGenerationProfile(opts.Profile)
	if err != nil {
		return RPCCodeOptions{}, err
	}
	codeOpts := RPCCodeOptions{
		Profile:        profile,
		NoClient:       opts.NoClient,
		WithMiddleware: opts.WithMiddleware,
		WithRecovery:   opts.WithRecovery,
		WithValidator:  opts.WithValidator,
	}
	if profile == ProfileKitexCompatible {
		codeOpts.WithMiddleware = true
	}
	return codeOpts, nil
}

func GenerateRPCCodeWithOptions(doc IDLDocument, packageName string, opts RPCCodeOptions) ([]byte, error) {
	if len(doc.Services) == 0 {
		return nil, errors.New("proto service is required")
	}
	if opts.Profile == ProfileKitexCompatible {
		opts.WithMiddleware = true
	}
	if packageName == "" {
		packageName = inferGoPackageName(doc)
	}
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", packageName)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	if opts.WithMiddleware {
		fprintf(&b, "\n\t\"github.com/imajinyun/gofly/rpc/endpoint\"\n")
	}
	fprintf(&b, "\t\"github.com/imajinyun/gofly/rpc\"\n")
	fprintf(&b, ")\n\n")
	for _, enum := range doc.Enums {
		writeEnum(&b, enum)
	}
	for _, msg := range doc.Messages {
		writeMessage(&b, msg)
	}
	for _, svc := range doc.Services {
		writeService(&b, doc, svc, opts)
	}
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated rpc code: %w", err)
	}
	return out, nil
}

func writeRPCGeneratedFile(path string, code []byte) error {
	if err := writeGeneratedFile(path, code); err != nil {
		return fmt.Errorf("write rpc generated file: %w", err)
	}
	return nil
}

func writeEnum(b *bytes.Buffer, enum IDLEnum) {
	enumName := exportName(enum.Name)
	fprintf(b, "type %s int32\n\n", enumName)
	if len(enum.Values) == 0 {
		return
	}
	fprintf(b, "const (\n")
	for _, value := range enum.Values {
		fprintf(b, "\t%s %s = %d\n", exportName(value.Name), enumName, value.Number)
	}
	fprintf(b, ")\n\n")
}

func writeMessage(b *bytes.Buffer, msg IDLMessage) {
	fprintf(b, "type %s struct {\n", exportName(msg.Name))
	for _, field := range msg.Fields {
		fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", exportName(field.Name), protoGoType(field.Type), lowerCamel(field.Name))
	}
	fprintf(b, "}\n\n")
}

func writeService(b *bytes.Buffer, doc IDLDocument, svc IDLService, opts RPCCodeOptions) {
	serviceName := exportName(svc.Name)
	fprintf(b, "type %s interface {\n", serviceName)
	for _, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			fprintf(b, "\t%s(ctx context.Context, stream *rpc.Stream) error\n", exportName(method.Name))
			continue
		}
		fprintf(b, "\t%s(ctx context.Context, req *%s) (*%s, error)\n", exportName(method.Name), exportName(method.Request), exportName(method.Response))
	}
	fprintf(b, "}\n\n")
	if opts.WithValidator {
		writeRPCValidatorScaffold(b, svc)
	}
	if opts.WithMiddleware || opts.WithRecovery || opts.WithValidator {
		writeRPCServerOptionScaffold(b, svc, opts)
	}
	fprintf(b, "func %sDescriptor() rpc.ServiceDesc {\n", serviceName)
	fprintf(b, "\treturn rpc.ServiceDesc{Name: %q", serviceFullName(doc, svc.Name))
	if hasUnaryMethods(svc) {
		fprintf(b, ", Methods: []rpc.MethodDesc{\n")
	} else if hasStreamMethods(svc) {
		fprintf(b, ",\n")
	}
	for _, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			continue
		}
		methodName := exportName(method.Name)
		requestName := exportName(method.Request)
		responseName := exportName(method.Response)
		fprintf(b, "\t\t{Name: %q, NewRequest: func() any { return new(%s) }, Request: %q, Response: %q", methodName, requestName, requestName, responseName)
		if strings.TrimSpace(method.HTTPMethod) != "" || strings.TrimSpace(method.HTTPPath) != "" {
			fprintf(b, ", HTTP: rpc.HTTPBinding{Method: %q, Path: %q}", strings.ToUpper(strings.TrimSpace(method.HTTPMethod)), strings.TrimSpace(method.HTTPPath))
		}
		fprintf(b, "},\n")
	}
	if hasUnaryMethods(svc) {
		fprintf(b, "\t}")
	}
	if hasStreamMethods(svc) {
		if hasUnaryMethods(svc) {
			fprintf(b, ", Streams: []rpc.StreamDesc{\n")
		} else {
			fprintf(b, "\tStreams: []rpc.StreamDesc{\n")
		}
		for _, method := range svc.Methods {
			if !method.ClientStream && !method.ServerStream {
				continue
			}
			methodName := exportName(method.Name)
			requestName := exportName(method.Request)
			responseName := exportName(method.Response)
			messageName := streamMessageName(method)
			fprintf(b, "\t\t{Name: %q, NewMessage: func() any { return new(%s) }, Message: %q, Mode: %s, Metadata: map[string]string{\"request\": %q, \"response\": %q, \"clientStream\": %q, \"serverStream\": %q}},\n", methodName, messageName, messageName, rpcStreamModeLiteral(method), requestName, responseName, boolString(method.ClientStream), boolString(method.ServerStream))
		}
		fprintf(b, "\t}")
	}
	fprintf(b, "}\n")
	fprintf(b, "}\n\n")
	fprintf(b, "func %sServiceDesc(impl %s) rpc.ServiceDesc {\n", serviceName, serviceName)
	fprintf(b, "\tdesc := %sDescriptor()\n", serviceName)
	for i, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			continue
		}
		methodName := exportName(method.Name)
		requestName := exportName(method.Request)
		writeDefensiveHandlerBinding(b, i, methodName, requestName)
	}
	for i, method := range svc.Methods {
		if !method.ClientStream && !method.ServerStream {
			continue
		}
		methodName := exportName(method.Name)
		fprintf(b, "\tdesc.Streams[%d].Handler = func(ctx context.Context, stream *rpc.Stream) error {\n", streamIndex(svc, i))
		fprintf(b, "\t\treturn impl.%s(ctx, stream)\n", methodName)
		fprintf(b, "\t}\n")
	}
	fprintf(b, "\treturn desc\n")
	fprintf(b, "}\n\n")
	if opts.WithValidator {
		writeRPCServiceDescWithOptions(b, svc)
	}
	fprintf(b, "func Bind%sGenericHandlers(handlers map[string]rpc.GenericHandler) (rpc.ServiceDesc, error) {\n", serviceName)
	fprintf(b, "\treturn rpc.BindGenericHandlers(%sDescriptor(), handlers)\n", serviceName)
	fprintf(b, "}\n\n")
	fprintf(b, "func Register%sServer(s *rpc.HTTPServer, impl %s) error {\n", serviceName, serviceName)
	fprintf(b, "\treturn s.RegisterService(%sServiceDesc(impl), impl)\n", serviceName)
	fprintf(b, "}\n\n")
	fprintf(b, "func New%sHTTPServer(impl %s, opts ...rpc.ServerOption) (*rpc.HTTPServer, error) {\n", serviceName, serviceName)
	fprintf(b, "\tserver := rpc.NewServer(opts...)\n")
	fprintf(b, "\tif err := Register%sServer(server, impl); err != nil {\n\t\treturn nil, err\n\t}\n", serviceName)
	fprintf(b, "\treturn server, nil\n")
	fprintf(b, "}\n\n")
	if opts.WithMiddleware || opts.WithRecovery || opts.WithValidator {
		writeRPCServerWithOptions(b, svc, opts)
	}
	if opts.NoClient {
		return
	}
	if hasStreamMethods(svc) {
		fprintf(b, "type %sRPCClient interface {\n\trpc.Client\n\tStream(ctx context.Context, method string) (*rpc.Stream, error)\n}\n\n", serviceName)
		fprintf(b, "type %sClient struct {\n\tcc   %sRPCClient\n\tdesc rpc.ServiceDesc\n}\n\n", serviceName, serviceName)
		fprintf(b, "func New%sClient(cc %sRPCClient) *%sClient {\n\treturn &%sClient{cc: cc, desc: %sDescriptor()}\n}\n\n", serviceName, serviceName, serviceName, serviceName, serviceName)
	} else {
		fprintf(b, "type %sClient struct {\n\tcc   rpc.Client\n\tdesc rpc.ServiceDesc\n}\n\n", serviceName)
		fprintf(b, "func New%sClient(cc rpc.Client) *%sClient {\n\treturn &%sClient{cc: cc, desc: %sDescriptor()}\n}\n\n", serviceName, serviceName, serviceName, serviceName)
	}
	fprintf(b, "func New%sHTTPClient(target string, opts ...rpc.ClientOption) (*%sClient, error) {\n", serviceName, serviceName)
	fprintf(b, "\tclient, err := rpc.NewClient(target, opts...)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn New%sClient(client), nil\n", serviceName)
	fprintf(b, "}\n\n")
	fprintf(b, "func (c *%sClient) Descriptor() rpc.ServiceDesc {\n\treturn rpc.CloneServiceDesc(c.desc)\n}\n\n", serviceName)
	fprintf(b, "func (c *%sClient) RuntimeDescriptor() rpc.Descriptor {\n\treturn c.desc.Descriptor()\n}\n\n", serviceName)
	for _, method := range svc.Methods {
		methodName := exportName(method.Name)
		if method.ClientStream || method.ServerStream {
			fprintf(b, "func (c *%sClient) %s(ctx context.Context) (*rpc.Stream, error) {\n", serviceName, methodName)
			fprintf(b, "\tmethod, err := c.desc.StreamPath(%q)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", methodName)
			fprintf(b, "\treturn c.cc.Stream(ctx, method)\n}\n\n")
			continue
		}
		requestName := exportName(method.Request)
		responseName := exportName(method.Response)
		fprintf(b, "func (c *%sClient) %s(ctx context.Context, req *%s) (*%s, error) {\n", serviceName, methodName, requestName, responseName)
		fprintf(b, "\tvar resp %s\n", responseName)
		fprintf(b, "\tmethod, err := c.desc.MethodPath(%q)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", methodName)
		fprintf(b, "\tif err := c.cc.Call(ctx, method, req, &resp); err != nil {\n\t\treturn nil, err\n\t}\n")
		fprintf(b, "\treturn &resp, nil\n}\n\n")
	}
	fprintf(b, "type %sGenericClient struct {\n\tinvoker *rpc.GenericInvoker\n\tdesc    rpc.ServiceDesc\n}\n\n", serviceName)
	fprintf(b, "func New%sGenericClient(client rpc.GenericClient) (*%sGenericClient, error) {\n", serviceName, serviceName)
	fprintf(b, "\tinvoker, err := rpc.NewGenericInvoker(client)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn &%sGenericClient{invoker: invoker, desc: %sDescriptor()}, nil\n", serviceName, serviceName)
	fprintf(b, "}\n\n")
	fprintf(b, "func New%sGenericHTTPClient(target string, opts ...rpc.ClientOption) (*%sGenericClient, error) {\n", serviceName, serviceName)
	fprintf(b, "\tclient, err := rpc.NewClient(target, opts...)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn New%sGenericClient(client)\n", serviceName)
	fprintf(b, "}\n\n")
	fprintf(b, "func (c *%sGenericClient) Descriptor() rpc.ServiceDesc {\n\treturn rpc.CloneServiceDesc(c.desc)\n}\n\n", serviceName)
	fprintf(b, "func (c *%sGenericClient) RuntimeDescriptor() rpc.Descriptor {\n\treturn c.desc.Descriptor()\n}\n\n", serviceName)
	fprintf(b, "func (c *%sGenericClient) Invoke(ctx context.Context, method string, req any) (rpc.GenericResponse, error) {\n", serviceName)
	fprintf(b, "\treturn c.invoker.InvokeMethod(ctx, c.desc, method, req)\n")
	fprintf(b, "}\n\n")
	fprintf(b, "type Mock%s struct {\n", serviceName)
	for _, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			fprintf(b, "\t%sFunc func(ctx context.Context, stream *rpc.Stream) error\n", exportName(method.Name))
			continue
		}
		fprintf(b, "\t%sFunc func(ctx context.Context, req *%s) (*%s, error)\n", exportName(method.Name), exportName(method.Request), exportName(method.Response))
	}
	fprintf(b, "}\n\n")
	for _, method := range svc.Methods {
		methodName := exportName(method.Name)
		if method.ClientStream || method.ServerStream {
			fprintf(b, "func (m *Mock%s) %s(ctx context.Context, stream *rpc.Stream) error {\n", serviceName, methodName)
			fprintf(b, "\tif m.%sFunc == nil {\n\t\treturn rpc.NewError(rpc.CodeInternal, %q)\n\t}\n", methodName, "mock stream "+methodName+" is not implemented")
			fprintf(b, "\treturn m.%sFunc(ctx, stream)\n}\n\n", methodName)
			continue
		}
		requestName := exportName(method.Request)
		responseName := exportName(method.Response)
		fprintf(b, "func (m *Mock%s) %s(ctx context.Context, req *%s) (*%s, error) {\n", serviceName, methodName, requestName, responseName)
		fprintf(b, "\tif m.%sFunc == nil {\n\t\treturn nil, rpc.NewError(rpc.CodeInternal, %q)\n\t}\n", methodName, "mock method "+methodName+" is not implemented")
		fprintf(b, "\treturn m.%sFunc(ctx, req)\n}\n\n", methodName)
	}
}

func writeRPCValidatorScaffold(b *bytes.Buffer, svc IDLService) {
	serviceName := exportName(svc.Name)
	fprintf(b, "type %sValidator interface {\n", serviceName)
	for _, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			continue
		}
		fprintf(b, "\tValidate%s(ctx context.Context, req *%s) error\n", exportName(method.Name), exportName(method.Request))
	}
	fprintf(b, "}\n\n")
	fprintf(b, "func New%sBizError(code rpc.Code, message string) error {\n\treturn rpc.NewError(code, message)\n}\n\n", serviceName)
	fprintf(b, "func New%sValidationError(message string) error {\n\treturn rpc.NewError(rpc.CodeInvalidArgument, message)\n}\n\n", serviceName)
}

func writeRPCServerOptionScaffold(b *bytes.Buffer, svc IDLService, opts RPCCodeOptions) {
	serviceName := exportName(svc.Name)
	fprintf(b, "type %sServerOptions struct {\n", serviceName)
	if opts.WithMiddleware {
		fprintf(b, "\tMiddlewares []endpoint.Middleware\n")
	}
	if opts.WithRecovery {
		fprintf(b, "\tRecovery bool\n")
	}
	if opts.WithValidator {
		fprintf(b, "\tValidator %sValidator\n", serviceName)
	}
	fprintf(b, "}\n\n")
	fprintf(b, "type %sServerOption func(*%sServerOptions)\n\n", serviceName, serviceName)
	if opts.WithMiddleware {
		fprintf(b, "func With%sMiddleware(middlewares ...endpoint.Middleware) %sServerOption {\n", serviceName, serviceName)
		fprintf(b, "\treturn func(o *%sServerOptions) {\n\t\to.Middlewares = append(o.Middlewares, middlewares...)\n\t}\n", serviceName)
		fprintf(b, "}\n\n")
		fprintf(b, "func %sInterceptorChain(middlewares ...endpoint.Middleware) endpoint.Middleware {\n", serviceName)
		fprintf(b, "\treturn endpoint.Chain(middlewares...)\n")
		fprintf(b, "}\n\n")
		fprintf(b, "func With%sKitexInterceptors(interceptors ...rpc.KitexInterceptor) %sServerOption {\n", serviceName, serviceName)
		fprintf(b, "\treturn func(o *%sServerOptions) {\n\t\to.Middlewares = append(o.Middlewares, rpc.KitexInterceptorMiddleware(interceptors...))\n\t}\n", serviceName)
		fprintf(b, "}\n\n")
		fprintf(b, "func %sKitexEndpointChain(middlewares ...rpc.KitexMiddleware) rpc.KitexMiddleware {\n", serviceName)
		fprintf(b, "\treturn rpc.KitexEndpointChain(middlewares...)\n")
		fprintf(b, "}\n\n")
		fprintf(b, "func %sObservabilityInterceptor(name string) rpc.KitexInterceptor {\n", serviceName)
		fprintf(b, "\treturn rpc.KitexObservabilityInterceptor(name, nil, nil)\n")
		fprintf(b, "}\n\n")
	}
	if opts.WithRecovery {
		fprintf(b, "func With%sRecovery() %sServerOption {\n", serviceName, serviceName)
		fprintf(b, "\treturn func(o *%sServerOptions) {\n\t\to.Recovery = true\n\t}\n", serviceName)
		fprintf(b, "}\n\n")
	}
	if opts.WithValidator {
		fprintf(b, "func With%sValidator(validator %sValidator) %sServerOption {\n", serviceName, serviceName, serviceName)
		fprintf(b, "\treturn func(o *%sServerOptions) {\n\t\to.Validator = validator\n\t}\n", serviceName)
		fprintf(b, "}\n\n")
	}
	fprintf(b, "func %sRPCServerOptions(options ...%sServerOption) []rpc.ServerOption {\n", serviceName, serviceName)
	fprintf(b, "\tvar cfg %sServerOptions\n", serviceName)
	fprintf(b, "\tfor _, option := range options {\n\t\tif option != nil {\n\t\t\toption(&cfg)\n\t\t}\n\t}\n")
	if opts.WithMiddleware || opts.WithRecovery {
		fprintf(b, "\tserverOptions := make([]rpc.ServerOption, 0")
		if opts.WithMiddleware {
			fprintf(b, "+len(cfg.Middlewares)")
		}
		if opts.WithRecovery {
			fprintf(b, "+1")
		}
		fprintf(b, ")\n")
		if opts.WithRecovery {
			fprintf(b, "\tif cfg.Recovery {\n\t\tserverOptions = append(serverOptions, rpc.WithServerMiddleware(rpc.RecoverMiddleware()))\n\t}\n")
		}
		if opts.WithMiddleware {
			fprintf(b, "\tfor _, middleware := range cfg.Middlewares {\n\t\tif middleware != nil {\n\t\t\tserverOptions = append(serverOptions, rpc.WithServerMiddleware(middleware))\n\t\t}\n\t}\n")
		}
		fprintf(b, "\treturn serverOptions\n")
	} else {
		fprintf(b, "\treturn nil\n")
	}
	fprintf(b, "}\n\n")
}

func writeRPCServiceDescWithOptions(b *bytes.Buffer, svc IDLService) {
	serviceName := exportName(svc.Name)
	fprintf(b, "func %sServiceDescWithOptions(impl %s, options ...%sServerOption) rpc.ServiceDesc {\n", serviceName, serviceName, serviceName)
	fprintf(b, "\tvar cfg %sServerOptions\n", serviceName)
	fprintf(b, "\tfor _, option := range options {\n\t\tif option != nil {\n\t\t\toption(&cfg)\n\t\t}\n\t}\n")
	fprintf(b, "\tdesc := %sDescriptor()\n", serviceName)
	for i, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			continue
		}
		methodName := exportName(method.Name)
		requestName := exportName(method.Request)
		fprintf(b, "\tdesc.Methods[%d].Handler = func(ctx context.Context, req any) (any, error) {\n", i)
		fprintf(b, "\t\ttyped, ok := req.(*%s)\n", requestName)
		fprintf(b, "\t\tif !ok || typed == nil {\n")
		fprintf(b, "\t\t\treturn nil, rpc.NewError(rpc.CodeInvalidArgument, %q)\n", "unexpected request type for "+methodName)
		fprintf(b, "\t\t}\n")
		fprintf(b, "\t\tif cfg.Validator != nil {\n")
		fprintf(b, "\t\t\tif err := cfg.Validator.Validate%s(ctx, typed); err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n", methodName)
		fprintf(b, "\t\t}\n")
		fprintf(b, "\t\treturn impl.%s(ctx, typed)\n", methodName)
		fprintf(b, "\t}\n")
	}
	for i, method := range svc.Methods {
		if !method.ClientStream && !method.ServerStream {
			continue
		}
		methodName := exportName(method.Name)
		fprintf(b, "\tdesc.Streams[%d].Handler = func(ctx context.Context, stream *rpc.Stream) error {\n", streamIndex(svc, i))
		fprintf(b, "\t\treturn impl.%s(ctx, stream)\n", methodName)
		fprintf(b, "\t}\n")
	}
	fprintf(b, "\treturn desc\n")
	fprintf(b, "}\n\n")
}

func writeRPCServerWithOptions(b *bytes.Buffer, svc IDLService, opts RPCCodeOptions) {
	serviceName := exportName(svc.Name)
	if opts.WithValidator {
		fprintf(b, "func Register%sServerWithOptions(s *rpc.HTTPServer, impl %s, options ...%sServerOption) error {\n", serviceName, serviceName, serviceName)
		fprintf(b, "\treturn s.RegisterService(%sServiceDescWithOptions(impl, options...), impl)\n", serviceName)
		fprintf(b, "}\n\n")
	}
	fprintf(b, "func New%sHTTPServerWithOptions(impl %s, options ...%sServerOption) (*rpc.HTTPServer, error) {\n", serviceName, serviceName, serviceName)
	fprintf(b, "\tserver := rpc.NewServer(%sRPCServerOptions(options...)...)\n", serviceName)
	if opts.WithValidator {
		fprintf(b, "\tif err := Register%sServerWithOptions(server, impl, options...); err != nil {\n\t\treturn nil, err\n\t}\n", serviceName)
	} else {
		fprintf(b, "\tif err := Register%sServer(server, impl); err != nil {\n\t\treturn nil, err\n\t}\n", serviceName)
	}
	fprintf(b, "\treturn server, nil\n")
	fprintf(b, "}\n\n")
}

func hasUnaryMethods(svc IDLService) bool {
	for _, method := range svc.Methods {
		if !method.ClientStream && !method.ServerStream {
			return true
		}
	}
	return false
}

func hasStreamMethods(svc IDLService) bool {
	for _, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			return true
		}
	}
	return false
}

func streamIndex(svc IDLService, methodIndex int) int {
	idx := 0
	for i, method := range svc.Methods {
		if method.ClientStream || method.ServerStream {
			if i == methodIndex {
				return idx
			}
			idx++
		}
	}
	return idx
}

func streamMessageName(method IDLMethod) string {
	if method.ClientStream || method.Request == method.Response {
		return exportName(method.Request)
	}
	return exportName(method.Response)
}

func rpcStreamModeLiteral(method IDLMethod) string {
	switch {
	case method.ClientStream && method.ServerStream:
		return "rpc.StreamModeBidiStream"
	case method.ClientStream:
		return "rpc.StreamModeClientStream"
	case method.ServerStream:
		return "rpc.StreamModeServerStream"
	default:
		return "rpc.StreamModeUnary"
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func protoGoType(protoType string) string {
	if strings.HasPrefix(protoType, "map<") && strings.HasSuffix(protoType, ">") {
		inner := strings.TrimSuffix(strings.TrimPrefix(protoType, "map<"), ">")
		parts := strings.SplitN(inner, ",", 2)
		if len(parts) == 2 {
			return "map[" + protoGoType(strings.TrimSpace(parts[0])) + "]" + protoGoType(strings.TrimSpace(parts[1]))
		}
	}
	switch protoType {
	case "string":
		return "string"
	case "bool":
		return "bool"
	case "int32", "sint32", "sfixed32":
		return "int32"
	case "int64", "sint64", "sfixed64":
		return "int64"
	case "uint32", "fixed32":
		return "uint32"
	case "uint64", "fixed64":
		return "uint64"
	case "float":
		return "float32"
	case "double":
		return "float64"
	case "bytes":
		return "[]byte"
	default:
		if strings.HasPrefix(protoType, "repeated ") {
			return "[]" + protoGoType(strings.TrimSpace(strings.TrimPrefix(protoType, "repeated ")))
		}
		return "*" + exportName(lastIdent(protoType))
	}
}

func inferGoPackageName(doc IDLDocument) string {
	if doc.GoPackage != "" {
		name := doc.GoPackage
		if idx := strings.LastIndex(name, ";"); idx >= 0 {
			name = name[idx+1:]
		} else if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		name = strings.ReplaceAll(name, "-", "_")
		if name != "" {
			return name
		}
	}
	if doc.Package != "" {
		parts := strings.FieldsFunc(doc.Package, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
		var b strings.Builder
		for _, part := range parts {
			b.WriteString(exportName(part))
		}
		name := strings.ToLower(b.String())
		if name != "" {
			return name
		}
	}
	return "pb"
}

func serviceFullName(doc IDLDocument, service string) string {
	if doc.Package == "" {
		return exportName(service)
	}
	return doc.Package + "." + exportName(service)
}
