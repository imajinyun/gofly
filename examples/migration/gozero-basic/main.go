// Command gozero-basic describes the recommended go-zero to gofly migration
// entry path without importing go-zero or generating temporary files.
package main

import (
	"encoding/json"
	"os"
)

const schema = "gofly.gozero_basic_migration.v1"

type report struct {
	Schema           string            `json:"schema"`
	SourceFramework  string            `json:"sourceFramework"`
	TargetProfile    string            `json:"targetProfile"`
	ExampleInputs    []string          `json:"exampleInputs"`
	LayoutMapping    map[string]string `json:"layoutMapping"`
	Commands         []string          `json:"commands"`
	Evidence         []string          `json:"evidence"`
	Boundaries       []string          `json:"boundaries"`
	Rollback         string            `json:"rollback"`
	AdopterChecklist []string          `json:"adopterChecklist"`
}

func main() {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(buildReport()); err != nil {
		panic(err)
	}
}

func buildReport() report {
	return report{
		Schema:          schema,
		SourceFramework: "go-zero",
		TargetProfile:   "gozero-compatible",
		ExampleInputs: []string{
			"service.api",
			"service.proto",
			"schema.sql",
			"etc/service-api.yaml",
		},
		LayoutMapping: map[string]string{
			".api":                  "gofly api gen --profile gozero-compatible",
			"handler":               "internal/handler/routes.go and generated handlers",
			"logic":                 "internal/logic/*logic.go",
			"svc.ServiceContext":    "internal/svc/servicecontext.go",
			"types":                 "internal/types/types.go",
			"etc/service-api.yaml":  "generated etc/<service>.json plus explicit rest.preset",
			"model/cache":           "gofly model gen --style go_zero",
			"zrpc proto":            "gofly rpc gen or rpc protoc with zrpc compatibility review",
			"multi-language client": "gofly api client --language <typescript|javascript|dart|java|kotlin>",
		},
		Commands: []string{
			"gofly new api orders --module example.com/orders --profile gozero-compatible",
			"gofly api gen --file service.api --dir . --profile gozero-compatible",
			"gofly model gen --ddl schema.sql --dir . --style go_zero",
			"gofly rpc gen --file service.proto --dir internal/rpc --transport gofly",
			"go test ./...",
		},
		Evidence: []string{
			"docs/reference/from-go-zero-migration.md",
			"docs/reference/goctl-generator-compatibility.json",
			"docs/reference/goctl-real-project-replay.json",
			"docs/reference/zrpc-proto-compatibility.json",
			"docs/reference/rest-middleware-profiles.md",
			"docs/reference/api-client-generation.md",
		},
		Boundaries: []string{
			"go-zero compatibility covers scaffold layout and goctl-style generation behavior, not full framework runtime parity",
			"local external proto imports are recursively merged into generated DTOs",
			"common google well-known protobuf types map to stable Go shapes",
			"standard REST profile does not silently enable rate limiters, breakers, adaptive limiters, or max-concurrency guards",
		},
		Rollback: "Keep the original go-zero service routable, pin the last gozero-compatible generator behavior, and discard generated output if replay or smoke evidence fails.",
		AdopterChecklist: []string{
			"capture .api, .proto, SQL DDL, and config inputs",
			"generate with gozero-compatible profile",
			"compare generated handler/logic/svc/types layout",
			"review zRPC compatibility matrix rows before moving RPC traffic",
			"run go test ./... in the generated module",
			"keep rollback routing to the source go-zero service until evidence passes",
		},
	}
}
