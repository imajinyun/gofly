package command

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type newServiceContractInputs struct {
	APIFile     string
	OpenAPIFile string
	ProtoFile   string
	ThriftFile  string
}

func applyNewServiceContractInputs(inputs newServiceContractInputs, serviceName, dir string) error {
	if err := validateNewServiceContractInputs(inputs); err != nil {
		return err
	}

	apiContract, err := materializeNewServiceAPIContract(inputs, serviceName, dir)
	if err != nil {
		return err
	}
	if apiContract != "" {
		if err := generator.GenerateRESTFromAPI(generator.APIOptions{APIFile: apiContract, Dir: dir, Package: "api", Test: true, TypeGroup: true}); err != nil {
			return fmt.Errorf("generate REST from API contract: %w", err)
		}
	}

	protoContract, err := materializeNewServiceRPCContract(inputs, serviceName, dir)
	if err != nil {
		return err
	}
	if protoContract != "" {
		if err := generator.GenerateRPCFromProto(generator.RPCOptions{ProtoFile: protoContract, Dir: filepath.Join(dir, "internal", "rpc"), Package: "rpc", WithMiddleware: true, WithRecovery: true, WithValidator: true}); err != nil {
			return fmt.Errorf("generate RPC from proto contract: %w", err)
		}
	}
	return nil
}

func validateNewServiceContractInputs(inputs newServiceContractInputs) error {
	if strings.TrimSpace(inputs.APIFile) != "" && strings.TrimSpace(inputs.OpenAPIFile) != "" {
		return fmt.Errorf("%w: --api and --openapi are mutually exclusive", errUsage)
	}
	if strings.TrimSpace(inputs.ProtoFile) != "" && strings.TrimSpace(inputs.ThriftFile) != "" {
		return fmt.Errorf("%w: --proto and --thrift are mutually exclusive", errUsage)
	}
	return nil
}

func materializeNewServiceAPIContract(inputs newServiceContractInputs, serviceName, dir string) (string, error) {
	apiFile := strings.TrimSpace(inputs.APIFile)
	openAPIFile := strings.TrimSpace(inputs.OpenAPIFile)
	if apiFile == "" && openAPIFile == "" {
		return "", nil
	}
	apiOut, err := newServiceContractOutputPath(dir, serviceName, ".api")
	if err != nil {
		return "", err
	}
	if openAPIFile != "" {
		if err := generator.GenerateAPIFromOpenAPI(generator.APIImportOptions{Source: openAPIFile, Output: apiOut, Service: serviceName}); err != nil {
			return "", fmt.Errorf("import OpenAPI contract: %w", err)
		}
		return apiOut, nil
	}
	if err := copyNewServiceContractFile(apiFile, apiOut, dir); err != nil {
		return "", fmt.Errorf("copy API contract: %w", err)
	}
	return apiOut, nil
}

func materializeNewServiceRPCContract(inputs newServiceContractInputs, serviceName, dir string) (string, error) {
	protoFile := strings.TrimSpace(inputs.ProtoFile)
	thriftFile := strings.TrimSpace(inputs.ThriftFile)
	if protoFile == "" && thriftFile == "" {
		return "", nil
	}
	protoOut, err := newServiceContractOutputPath(dir, serviceName, ".proto")
	if err != nil {
		return "", err
	}
	if thriftFile != "" {
		if err := generator.GenerateProtoFromThrift(generator.RPCScaffoldOptions{IDLFile: thriftFile, Dir: dir}); err != nil {
			return "", fmt.Errorf("convert thrift contract: %w", err)
		}
		generatedProto := filepath.Join(dir, strings.TrimSuffix(filepath.Base(thriftFile), filepath.Ext(thriftFile))+".proto")
		if generatedProto != protoOut {
			if err := copyNewServiceContractFile(generatedProto, protoOut, dir); err != nil {
				return "", fmt.Errorf("copy thrift-derived proto contract: %w", err)
			}
		}
		return protoOut, nil
	}
	if err := copyNewServiceContractFile(protoFile, protoOut, dir); err != nil {
		return "", fmt.Errorf("copy proto contract: %w", err)
	}
	return protoOut, nil
}

func copyNewServiceContractFile(src, dst, root string) error {
	return generator.CopyFileToRoot(src, root, dst, 0o600, 0o750, "contract target")
}

func newServiceContractOutputPath(dir, serviceName, ext string) (string, error) {
	name := strings.TrimSpace(serviceName)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", errUsage)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%w: service name %q cannot be used as a contract filename", errUsage, serviceName)
	}
	return filepath.Join(dir, name+ext), nil
}

func sameFilePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}
