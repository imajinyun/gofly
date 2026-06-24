# P1 Growth Roadmap

This roadmap turns the P1 growth gaps into productized, testable workstreams. Each stream must ship an example or deployable asset, a stable validation command, and a rollback or migration note before it is considered complete.

## Execution order

| Phase | Gap | Deliverable | Gate |
| --- | --- | --- | --- |
| P1-1 | HTTP middleware ecosystem | JWT, CORS, CSRF, sessions, OpenTelemetry, Prometheus, SSE/WebSocket, and request validation examples in a single middleware matrix. | `make p1-growth-check` |
| P1-2 | Binding and validation experience | Unified error response contract, validator adapter guidance, and OpenAPI schema linkage for generated services. | `make docs-check` plus generated service smoke tests |
| P1-3 | CLI DX | Stable `gofly new service`, `api gen`, `rpc gen`, and `release check` JSON/CI output examples. | CLI JSON contract tests and `gofly release check --strict` |
| P1-4 | RPC IDL ecosystem | Proto/thrift compatibility, streaming, interceptor, resolver, and load-balancing example matrix. | `make examples-smoke` plus RPC contract checks |
| P1-5 | Cloud-native deployment assets | Helm chart, Kustomize overlay, Kubernetes probes, ServiceMonitor, HPA, and PodDisruptionBudget assets. | `make p1-growth-check` |
| P1-6 | Plugin ecosystem | SPI registry, plugin examples, version compatibility matrix, and third-party template directory contract. | plugin protocol tests plus `make docs-check` |

## P1-1 HTTP middleware ecosystem

The growth goal is to make gofly comparable with Gin, Echo, and Fiber for common web middleware discovery. The product surface should include:

- authentication examples: JWT bearer middleware and signed request middleware;
- browser safety examples: CORS, CSRF, security headers, and session-cookie guidance;
- observability examples: OpenTelemetry trace propagation and Prometheus metrics;
- realtime examples: SSE and WebSocket integration patterns that preserve request IDs and trace IDs;
- request validation examples: binding, validation, and standard error responses.

Definition of done: every example must be copyable, referenced from `examples/README.md`, and covered by `make examples-copyable-check`.

## P1-2 Binding and validation experience

The growth goal is to make request decoding errors predictable for humans, clients, and AI agents.

- Keep `rest.ErrorResponse` as the standard JSON error object.
- Document validator adapters as the extension point for `Validate() error` and generated DTOs.
- Link OpenAPI schema generation to binding tags so request examples and schemas do not drift.
- Keep unknown JSON fields rejected in production examples unless a route explicitly allows extension fields.

Definition of done: invalid body, invalid query, missing path parameter, and validation failure examples all return stable machine-readable errors.

## P1-3 CLI DX

The growth goal is predictable automation output for go-zero-style scaffold workflows.

- `gofly new service --plan --output json` must remain stable for CI preview.
- `gofly api gen --output json` and `gofly rpc gen --output json` should report generated file counts and plugin effects.
- `gofly release check --json --strict` should remain a single report with deterministic check names.
- Human output should stay concise and keep diagnostics on stderr.

Definition of done: CLI contract docs include every command family that CI or agents should parse.

## P1-4 RPC IDL ecosystem

The growth goal is to reduce adoption risk for teams comparing gofly with Kitex and go-zero.

- Add standalone proto and thrift fixtures with compatibility checks.
- Demonstrate unary, server streaming, client streaming, and bidirectional streaming paths.
- Show unary and stream interceptors for recovery, tracing, logging, timeout, retry, breaker, and validation.
- Show resolver and load-balancing behavior with round-robin, weighted round-robin, P2C, consistent hash, and health-aware balancing.

Definition of done: the RPC example matrix can be copied out of the repository and still run tests with a released gofly module.

## P1-5 Cloud-native deployment assets

The growth goal is a deployable baseline similar to Kratos and go-zero starter charts.

- Static YAML assets live under `k8s/` for direct `kubectl apply` and Kustomize users.
- Helm assets live under `charts/gofly/` for release packaging.
- Probes must include liveness, readiness, and startup checks.
- Observability must include Prometheus scrape annotations and ServiceMonitor support.
- Availability must include HPA and PodDisruptionBudget support.

Definition of done: `make p1-growth-check` validates the static and Helm cloud-native assets exist and contain the expected resource kinds.

## P1-6 Plugin ecosystem

The growth goal is to make third-party extension safer to discover and maintain.

- Maintain the SPI registry contract with name, version, protocol, compatible versions, capabilities, permissions, checksum, and source.
- Provide plugin examples for code generation, post-generation patching, and third-party template directories.
- Add compatibility tests for old, current, and future protocol declarations.
- Keep remote plugin installation behind HTTPS, digest validation, path safety, and explicit permissions.

Definition of done: plugin examples and registry entries pass protocol validation and copy-out checks without adding dependencies to the root module.

Current shipped slice:

- `examples/plugin-ecosystem` emits `gofly.plugin_ecosystem.v1` with registry fields, protocol compatibility cases, file-generation output, patch output, and third-party template directory metadata.
- `cmd/gofly/internal/generator/plugin.go` validates `protocol`, `checksum`, and `source` for registry entries.
- `make p1-growth-check` and `make examples-smoke` include the plugin ecosystem assets.

## First execution slice

The first slice in this change lands the P1 roadmap, cloud-native deployment assets, and a P1 growth validation gate. Follow-up slices should implement the HTTP middleware example matrix, validation adapter examples, CLI JSON expansions, RPC fixture examples, and plugin example modules in the order listed above.
