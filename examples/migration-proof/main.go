// Command migration-proof emits runnable migration evidence for common Go
// framework adoption paths without importing those frameworks into this module.
package main

import (
	"encoding/json"
	"os"
	"sort"
)

const schema = "gofly.migration_proof.v1"

type report struct {
	Schema     string          `json:"schema"`
	Cases      []migrationCase `json:"cases"`
	Smoke      []string        `json:"smoke"`
	Rollbacks  []rollbackNote  `json:"rollbacks"`
	References []string        `json:"references"`
}

type migrationCase struct {
	Source        string   `json:"source"`
	Example       string   `json:"example"`
	Contract      string   `json:"contract"`
	Validation    []string `json:"validation"`
	Rollback      string   `json:"rollback"`
	Compatibility []string `json:"compatibility"`
}

type rollbackNote struct {
	Source string `json:"source"`
	Note   string `json:"note"`
}

func main() {
	out := buildReport()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}

func buildReport() report {
	cases := []migrationCase{
		{
			Source:   "gin",
			Example:  "examples/restserver",
			Contract: "route parity, OpenAPI schema, health, metrics, and stable error envelope",
			Validation: []string{
				"go test -C examples/restserver ./...",
				"go run -C examples/restserver .",
				"curl -s localhost:8080/openapi.json",
			},
			Rollback:      "keep Gin route active behind the existing router until sampled responses and metrics match",
			Compatibility: []string{"docs/comparisons/gin.md", "docs/case-studies/migrate-from-gin.md"},
		},
		{
			Source:   "go-zero",
			Example:  "examples/production-orders",
			Contract: "generated REST/RPC service shape, governance policy, release checks, and admin control-plane",
			Validation: []string{
				"go test -C examples/production-orders ./...",
				"go run -C examples/production-orders .",
				"gofly release check --strict",
			},
			Rollback:      "keep go-zero and gofly services addressable through discovery and switch routing back to go-zero",
			Compatibility: []string{"docs/comparisons/go-zero.md"},
		},
		{
			Source:   "kratos",
			Example:  "examples/microshop",
			Contract: "app lifecycle, gateway topology, health checks, discovery, and control-plane visibility",
			Validation: []string{
				"go test -C examples/microshop ./...",
				"go run -C examples/microshop describe",
			},
			Rollback:      "restore the previous Kratos deployment target while keeping shared domain services unchanged",
			Compatibility: []string{"docs/comparisons/kratos.md"},
		},
		{
			Source:   "kitex",
			Example:  "examples/rpc-idl-matrix",
			Contract: "IDL-first RPC, unary and streaming contracts, middleware, resolver, and load-balancing evidence",
			Validation: []string{
				"go test -C examples/rpc-idl-matrix ./...",
				"go run -C examples/rpc-idl-matrix .",
				"BENCH_PATTERN=BenchmarkRPCUnary make bench-stat",
			},
			Rollback:      "route latency-critical methods back to Kitex and retain gofly for REST ingress or governance surfaces",
			Compatibility: []string{"docs/comparisons/kitex.md", "docs/guides/rpc.md"},
		},
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Source < cases[j].Source })

	rollbacks := make([]rollbackNote, 0, len(cases))
	for _, item := range cases {
		rollbacks = append(rollbacks, rollbackNote{Source: item.Source, Note: item.Rollback})
	}

	return report{
		Schema:    schema,
		Cases:     cases,
		Smoke:     []string{"make examples-smoke", "make migration-docs-check"},
		Rollbacks: rollbacks,
		References: []string{
			"docs/comparisons/gin.md",
			"docs/comparisons/go-zero.md",
			"docs/comparisons/kratos.md",
			"docs/comparisons/kitex.md",
			"docs/case-studies/migrate-from-gin.md",
		},
	}
}
