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
	Source               string        `json:"source"`
	Example              string        `json:"example"`
	Contract             string        `json:"contract"`
	Validation           []string      `json:"validation"`
	GateCommands         []string      `json:"gateCommands"`
	Rollback             string        `json:"rollback"`
	SupportBundle        string        `json:"supportBundle"`
	FailureReport        string        `json:"failureReport"`
	Compatibility        []string      `json:"compatibility"`
	CompatibilityCaveats []string      `json:"compatibilityCaveats"`
	PerformanceBoundary  string        `json:"performanceBoundary"`
	GovernanceBoundary   string        `json:"governanceBoundary"`
	DecisionTable        decisionTable `json:"decisionTable"`
}

type decisionTable struct {
	ChooseWhen      string `json:"chooseWhen"`
	KeepSourceWhen  string `json:"keepSourceWhen"`
	AdopterAction   string `json:"adopterAction"`
	RollbackTrigger string `json:"rollbackTrigger"`
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
			GateCommands: []string{
				"go test -C examples/restserver ./...",
				"make examples-smoke",
			},
			Rollback:             "keep Gin route active behind the existing router until sampled responses and metrics match",
			SupportBundle:        "attach gofly bug --json only after redaction and include sampled response, OpenAPI, and error-envelope drift evidence",
			FailureReport:        "capture route, status-code, JSON-field, OpenAPI-schema, and stable error envelope drift before changing traffic",
			Compatibility:        []string{"docs/comparisons/gin.md", "docs/case-studies/migrate-from-gin.md"},
			CompatibilityCaveats: []string{"Gin :id routes become gofly {id} routes", "compare status codes, JSON field names, and error envelopes before switching traffic"},
			PerformanceBoundary:  "treat HTTP latency as report-only until route, binding, middleware, and OpenAPI overhead are measured against the adopter workload",
			GovernanceBoundary:   "use gofly for OpenAPI, stable error envelopes, request IDs, metrics, and control-plane evidence while Gin can keep serving traffic during comparison",
			DecisionTable: decisionTable{
				ChooseWhen:      "the HTTP service needs OpenAPI, generated contracts, runtime governance, or control-plane state",
				KeepSourceWhen:  "the service is a focused HTTP API without generated-contract or governance needs",
				AdopterAction:   "mirror routes through gofly, compare sampled responses, and promote only after examples-smoke passes",
				RollbackTrigger: "sampled response, metric, status-code, or OpenAPI schema drift appears during rollout",
			},
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
			GateCommands: []string{
				"make generated-version-compat-check",
				"make reference-app-smoke",
			},
			Rollback:             "keep go-zero and gofly services addressable through discovery and switch routing back to go-zero",
			SupportBundle:        "attach gofly bug --json with generated-project failure reports and release check output when scaffold compatibility fails",
			FailureReport:        "capture generated diff category, dependency boundary, OpenAPI mismatch, release-check output, and /admin/control-plane drift",
			Compatibility:        []string{"docs/comparisons/go-zero.md"},
			CompatibilityCaveats: []string{"preserve .api request and response field names", "verify generated OpenAPI and /admin/control-plane before changing discovery"},
			PerformanceBoundary:  "keep go-zero hot paths active until generated service smoke, reference-app evidence, and benchmark budgets cover the migrated traffic",
			GovernanceBoundary:   "use gofly when generated projects need release checks, discovery evidence, control-plane snapshots, and a support bundle for failed upgrades",
			DecisionTable: decisionTable{
				ChooseWhen:      "the team wants generated services plus governance files, discovery, release gates, and admin diagnostics",
				KeepSourceWhen:  "existing go-zero generated code owns stable production routing and generated compatibility evidence is incomplete",
				AdopterAction:   "run generated compatibility and reference-app smoke before changing discovery or traffic routing",
				RollbackTrigger: "generated compatibility, reference app smoke, release check, or control-plane evidence fails",
			},
		},
		{
			Source:   "kratos",
			Example:  "examples/microshop",
			Contract: "app lifecycle, gateway topology, health checks, discovery, and control-plane visibility",
			Validation: []string{
				"go test -C examples/microshop ./...",
				"go run -C examples/microshop describe",
			},
			GateCommands: []string{
				"make cloud-native-render-check",
				"go test -C examples/microshop ./...",
			},
			Rollback:             "restore the previous Kratos deployment target while keeping shared domain services unchanged",
			SupportBundle:        "attach gofly bug --json with rendered cloud-native assets, topology output, and health or discovery drift evidence",
			FailureReport:        "capture Helm or Kustomize rendering, topology, health, discovery, and lifecycle hook differences before replacing deployment targets",
			Compatibility:        []string{"docs/comparisons/kratos.md"},
			CompatibilityCaveats: []string{"keep domain services separate from transport wiring", "compare lifecycle hooks, health checks, discovery registration, and topology output"},
			PerformanceBoundary:  "do not treat cloud-native rendering evidence as runtime performance proof; pair rollout evidence with service-specific latency and resource metrics",
			GovernanceBoundary:   "use gofly for rendered deployment evidence, topology reports, health checks, runtime SLOs, and rollback notes before changing the serving target",
			DecisionTable: decisionTable{
				ChooseWhen:      "cloud-native operations remain important and generated governance contracts or AI-readable runtime state are needed",
				KeepSourceWhen:  "Kratos lifecycle, deployment, and service registration behavior is the production source of truth",
				AdopterAction:   "start with control-plane comparison or non-critical service slices before replacing the serving deployment",
				RollbackTrigger: "rendered policy, topology, health, discovery, or lifecycle behavior diverges from the previous Kratos deployment",
			},
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
			GateCommands: []string{
				"make rpc-boundary-check",
				"make bench-evidence-check",
			},
			Rollback:             "route latency-critical methods back to Kitex and retain gofly for REST ingress or governance surfaces",
			SupportBundle:        "attach gofly bug --json with RPC boundary, resolver, balancer, stream, trace, auth, and benchmark evidence before escalating",
			FailureReport:        "capture unary, stream, resolver, balancer, tracing, auth, and benchmark drift before moving latency-critical methods",
			Compatibility:        []string{"docs/comparisons/kitex.md", "docs/guides/rpc.md"},
			CompatibilityCaveats: []string{"do not migrate hot methods without bench evidence", "compare unary, stream, resolver, balancer, tracing, auth, and rollback behavior"},
			PerformanceBoundary:  "keep Kitex on latency-critical transports until gofly RPC unary and stream budgets move from report-only to blocking evidence",
			GovernanceBoundary:   "use gofly around REST ingress, descriptor comparison, release checks, control-plane snapshots, and governed non-hot-path service glue",
			DecisionTable: decisionTable{
				ChooseWhen:      "Kitex keeps latency-critical RPC while gofly owns REST ingress, governance, release checks, or non-hot-path service glue",
				KeepSourceWhen:  "the method is latency-critical or depends on Kitex IDL/runtime behavior without gofly benchmark evidence",
				AdopterAction:   "keep Kitex for hot RPC and add gofly around ingress, governance, descriptor comparison, or generated service surfaces",
				RollbackTrigger: "RPC boundary, stream contract, resolver, balancer, tracing, auth, or benchmark evidence fails",
			},
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
