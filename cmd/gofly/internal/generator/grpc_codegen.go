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

type GRPCOptions struct {
	ProtoFile string
	Dir       string
	Package   string
}

func GenerateGRPCFromProto(opts GRPCOptions) error {
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
	code, err := GenerateGRPCBindingCode(doc, opts.Package)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(opts.ProtoFile), filepath.Ext(opts.ProtoFile)) + ".grpc.gofly.go"
	path := filepath.Join(opts.Dir, name)
	return writeGeneratedFile(path, code)
}

func GenerateGRPCBindingCode(doc IDLDocument, packageName string) ([]byte, error) {
	if len(doc.Services) == 0 {
		return nil, errors.New("proto service is required")
	}
	if packageName == "" {
		packageName = inferGoPackageName(doc)
	}
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", packageName)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\tflygrpc \"github.com/gofly/gofly/rpc/grpc\"\n")
	fprintf(&b, ")\n\n")
	for _, svc := range doc.Services {
		writeGRPCServiceBinding(&b, svc)
	}
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated grpc binding code: %w", err)
	}
	return out, nil
}

func writeGRPCServiceBinding(b *bytes.Buffer, svc IDLService) {
	serviceName := exportName(svc.Name)
	fprintf(b, "func New%sGRPCServer(impl %sServer, opts ...flygrpc.ServerOption) *flygrpc.Server {\n", serviceName, serviceName)
	fprintf(b, "\tbase := []flygrpc.ServerOption{\n")
	fprintf(b, "\t\tflygrpc.WithUnaryServerInterceptors(flygrpc.RecoveryUnaryServerInterceptor(nil), flygrpc.ObservabilityUnaryServerInterceptor(%q, nil, nil)),\n", svc.Name)
	fprintf(b, "\t\tflygrpc.WithStreamServerInterceptors(flygrpc.ObservabilityStreamServerInterceptor(%q, nil, nil)),\n", svc.Name)
	fprintf(b, "\t}\n")
	fprintf(b, "\tserver := flygrpc.NewServer(append(base, opts...)...)\n")
	fprintf(b, "\tRegister%sServer(server.GRPCServer(), impl)\n", serviceName)
	fprintf(b, "\treturn server\n")
	fprintf(b, "}\n\n")
	fprintf(b, "func Dial%s(ctx context.Context, target string, opts ...flygrpc.ClientOption) (%sClient, *flygrpc.ClientConn, error) {\n", serviceName, serviceName)
	fprintf(b, "\tconn, err := flygrpc.Dial(ctx, target, opts...)\n")
	fprintf(b, "\tif err != nil {\n\t\treturn nil, nil, err\n\t}\n")
	fprintf(b, "\treturn New%sClient(conn.Conn()), conn, nil\n", serviceName)
	fprintf(b, "}\n\n")
}
