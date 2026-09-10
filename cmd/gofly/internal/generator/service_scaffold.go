package generator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// GenerateServiceScaffold 是配置驱动的脚手架入口，按 IR、renderer、filesystem sink 三层编排生成流程。
func GenerateServiceScaffold(opts ServiceScaffoldOptions) error {
	ir, err := buildServiceScaffoldIR(opts)
	if err != nil {
		return err
	}
	if ir.Profile == ProfileGoZeroCompatible && ir.Kind == "rpc" {
		if err := validateNativeGRPCToolchain(); err != nil {
			return err
		}
	}
	if err := cleanupLegacyServiceFilesForProfile(ir.Dir, ir.Profile); err != nil {
		return err
	}

	rendered := serviceScaffoldRenderer{}.Render(ir)
	sink := serviceFilesystemSink{Dir: ir.Dir, Stderr: os.Stderr}
	if err := sink.WriteRendered(rendered); err != nil {
		return err
	}
	if ir.Profile == ProfileGoZeroCompatible && ir.Kind == "rpc" {
		moduleOpt := "module=" + ir.Module
		if err := GenerateStandardProto(context.Background(), ProtocOptions{
			ProtoFile: filepath.Join(ir.Dir, ir.Name+".proto"),
			ProtoPath: []string{ir.Dir},
			GoOut:     ir.Dir,
			GoGRPCOut: ir.Dir,
			ExtraArgs: []string{"--go_opt=" + moduleOpt, "--go-grpc_opt=" + moduleOpt},
			Timeout:   30 * time.Second,
		}); err != nil {
			return err
		}
	}
	if err := sink.RunPlugins(ir); err != nil {
		return err
	}

	return nil
}

func validateNativeGRPCToolchain() error {
	for _, tool := range []string{"protoc", "protoc-gen-go", "protoc-gen-go-grpc"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("generate gozero-compatible gRPC scaffold: %s is required: %w", tool, err)
		}
	}
	return nil
}
