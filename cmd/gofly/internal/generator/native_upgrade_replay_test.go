package generator

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoZeroCompatibleNativeAPIUpgradeReplay(t *testing.T) {
	root := repositoryRoot(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/orders\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generate := func(fixture string) {
		t.Helper()
		if err := GenerateRESTFromAPI(APIOptions{
			APIFile: filepath.Join(root, "testdata", "generated-compat", fixture, "orders.api"),
			Dir:     project,
			Profile: string(ProfileGoZeroCompatible),
		}); err != nil {
			t.Fatalf("generate API fixture %s: %v", fixture, err)
		}
	}

	generate("v0.1")
	logicPath := filepath.Join(project, "internal", "app", "orders", "createorder.go")
	logic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	const businessMarker = "// nativeUpgradeBusinessMarker keeps adopter logic.\n"
	logic = append(logic, []byte("\n"+businessMarker)...)
	if err := os.WriteFile(logicPath, logic, 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(project, "etc", "orders.yaml")
	const adopterConfig = "Name: orders\nHost: 127.0.0.2\nPort: 9091\nCustomValue: adopter-owned\n"
	if err := os.WriteFile(configPath, []byte(adopterConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	generate("current")
	preservedLogic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(preservedLogic, []byte(businessMarker)) {
		t.Fatalf("API upgrade overwrote adopter logic:\n%s", preservedLogic)
	}
	for _, rel := range []string{
		"internal/app/orders/getorder.go",
		"internal/api/http/v1/orders/getorder.go",
		"internal/app/model/types.go",
		"internal/routes/routes.go",
		"internal/svc/service_context.go",
	} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("upgraded API artifact %s: %v", rel, err)
		}
	}
	routes, err := os.ReadFile(filepath.Join(project, "internal", "routes", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(routes), "orders.GetOrderHandler(stx)") {
		t.Fatalf("upgraded API routes omit GetOrder:\n%s", routes)
	}
	preservedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(preservedConfig) != adopterConfig {
		t.Fatalf("API upgrade changed adopter config:\n%s", preservedConfig)
	}
	for _, rel := range []string{"internal/logic", "internal/server", "internal/types", "pkg/client"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("API upgrade created forbidden goctl layout path %s: %v", rel, err)
		}
	}
	assertGeneratedProjectCompiles(t, project)
}

func TestGoZeroCompatibleNativeRPCUpgradeReplay(t *testing.T) {
	if err := validateNativeGRPCToolchain(); err != nil {
		t.Skip(err)
	}
	root := repositoryRoot(t)
	project := t.TempDir()
	generate := func(fixture string) {
		t.Helper()
		if err := GenerateGRPCScaffold(t.Context(), GRPCScaffoldOptions{
			ProtoFile: filepath.Join(root, "testdata", "generated-compat", fixture, "greeter.proto"),
			Dir:       project,
			Module:    "example.com/orders",
			Name:      "greeter",
		}); err != nil {
			t.Fatalf("generate RPC fixture %s: %v", fixture, err)
		}
	}

	generate("v0.1")
	logicPath := filepath.Join(project, "internal", "app", "greeter", "sayhello.go")
	logic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	const businessMarker = "// nativeUpgradeBusinessMarker keeps adopter RPC logic.\n"
	logic = append(logic, []byte("\n"+businessMarker)...)
	if err := os.WriteFile(logicPath, logic, 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(project, "etc", "greeter.json")
	legacyConfig := []byte(`{
  "name": "compat.v1.Greeter",
  "listenOn": "127.0.0.1:18081",
  "loadBalancing": {"policy": "round_robin"},
  "adopterExtension": {"enabled": true}
}
`)
	if err := os.WriteFile(configPath, legacyConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		t.Fatal(err)
	}

	generate("current")
	preservedLogic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(preservedLogic, []byte(businessMarker)) {
		t.Fatalf("RPC upgrade overwrote adopter logic:\n%s", preservedLogic)
	}
	if _, err := os.Stat(filepath.Join(project, "internal", "app", "greeter", "watchhello.go")); err != nil {
		t.Fatalf("RPC upgrade did not add WatchHello logic: %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatalf("decode upgraded RPC config: %v\n%s", err, configData)
	}
	if info, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("RPC upgrade changed config permissions to %o, want 600", info.Mode().Perm())
	}
	if config["listenOn"] != "127.0.0.1:18081" {
		t.Fatalf("RPC upgrade changed adopter listenOn: %#v", config["listenOn"])
	}
	loadBalancing, _ := config["loadBalancing"].(map[string]any)
	if loadBalancing["policy"] != "round_robin" {
		t.Fatalf("RPC upgrade changed adopter load-balancing policy: %#v", loadBalancing)
	}
	extension, _ := config["adopterExtension"].(map[string]any)
	if extension["enabled"] != true {
		t.Fatalf("RPC upgrade dropped adopter extension: %#v", extension)
	}
	for _, field := range []string{"adaptiveLimit", "ruleFile", "discovery", "clients", "etcd"} {
		if _, ok := config[field]; !ok {
			t.Fatalf("RPC upgrade did not add config field %q: %s", field, configData)
		}
	}
	afterUpgrade := append([]byte(nil), configData...)
	generate("current")
	repeated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterUpgrade, repeated) {
		t.Fatalf("RPC config migration is not deterministic:\nfirst:\n%s\nsecond:\n%s", afterUpgrade, repeated)
	}
	for _, rel := range []string{"internal/logic", "internal/server", "internal/types", "pkg/client"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("RPC upgrade created forbidden goctl layout path %s: %v", rel, err)
		}
	}
	assertGeneratedProjectCompiles(t, project)
}

func TestMergeGRPCScaffoldJSONDefaultsRejectsInvalidExistingConfig(t *testing.T) {
	project := t.TempDir()
	configPath := filepath.Join(project, "etc", "greeter.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const invalid = "{not-json}\n"
	if err := os.WriteFile(configPath, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	err := mergeGRPCScaffoldJSONDefaults(project, "etc/greeter.json", []byte(`{"name":"greeter"}`))
	if err == nil || !strings.Contains(err.Error(), "decode existing grpc scaffold config") {
		t.Fatalf("merge invalid existing config error = %v", err)
	}
	preserved, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(preserved) != invalid {
		t.Fatalf("invalid adopter config was changed: %q", preserved)
	}
}
