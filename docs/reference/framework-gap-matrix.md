# Framework Gap Matrix

Schema: `gofly.framework_gap_matrix.v1`

This matrix compares gofly with top Go frameworks on product and engineering
capabilities only. Community size, brand awareness, and ecosystem popularity are
intentionally out of scope.

Machine-readable source:
[`framework-gap-matrix.json`](framework-gap-matrix.json)

Validation:

```sh
make framework-gap-check
```

## Current Position

gofly is strongest where generation, governance, runtime control-plane state,
upgrade evidence, and release evidence need to be one workflow. Gin, Echo,
Fiber, and Hertz remain stronger for immediate HTTP middleware familiarity and
low-friction handler adoption. go-zero and Kratos remain stronger in
microservice scaffold convention maturity and adopter expectations. Kitex and
gRPC-Go remain stronger for specialized RPC transport depth and latency-critical
IDL workflows.

The recommended next work therefore prioritizes machine-verifiable adoption
confidence rather than broad rewrites.

## Executable TODO Order

The P3 baseline below is complete enough to serve as the current governed
foundation. The next-wave P4 roadmap is captured in
[`framework-gap-next-wave.json`](framework-gap-next-wave.json) and keeps the
comparison focused on product capability, engineering maturity, DX,
performance evidence, release trust, production proof, and machine-verifiable
governance. Community size and popularity remain out of scope.
The P5 adoption wave is captured in
[`framework-gap-adoption-wave.json`](framework-gap-adoption-wave.json) and
focuses on adopter operations: example health, release evidence consumption,
operator drills, template/profile trust, and adoption risk registration.

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-P3-1-FRAMEWORK-GAP-MATRIX` | Keep the framework gap analysis and TODO list as a governed source of truth. | `make framework-gap-check` |
| 2 | `GOFLY-P3-2-RPC-TIER1-EVIDENCE` | Convert `rpc`, `gateway`, and `app` Tier 1 promotion criteria into machine-readable evidence. | `make rpc-boundary-check` |
| 3 | `GOFLY-P3-3-OPENAPI-INVALID-SMOKE` | Make invalid request behavior and OpenAPI/runtime binding alignment visible to adopters. | `make openapi-validation-check` |
| 4 | `GOFLY-P3-4-MIDDLEWARE-ECOSYSTEM-MATRIX` | Make JWT, CORS, CSRF, session, Prometheus, Otel, SSE, and WebSocket coverage discoverable. | `make p1-growth-check` |
| 5 | `GOFLY-P3-5-REFERENCE-APP-TOPOLOGY` | Index production-orders as a full production topology proof. | `make reference-app-smoke` |
| 6 | `GOFLY-P3-FOLLOWUP-RELEASE-READINESS-SCORE` | Turn release evidence into a compact adopter-facing readiness score. | `make governance-report-check` |
| 7 | `GOFLY-P3-FOLLOWUP-PLUGIN-PUBLISHING-UX` | Improve plugin permission review and third-party template publishing UX. | `make plugin-conformance-check` |
| 8 | `GOFLY-P3-FOLLOWUP-BENCH-BUDGET-RATCHET` | Promote selected performance budgets from report-only to blocking after trend confidence. | `make bench-regression-check` |

## Next-Wave TODO Order

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-P4-1-NEXT-WAVE-GAP-ROADMAP` | Refresh the post-P3 gap analysis as a machine-readable roadmap. | `make framework-gap-check` |
| 2 | `GOFLY-P4-2-RPC-LATENCY-RATCHET` | Promote RPC latency budgets only where benchmark trend confidence is strong enough. | `make bench-regression-check` |
| 3 | `GOFLY-P4-3-GENERATED-MIGRATION-FIDELITY` | Tie framework migration paths to deterministic regeneration, diff categories, rollback notes, and smoke gates. | `make generated-upgrade-dry-run-check` |
| 4 | `GOFLY-P4-4-CLOUD-NATIVE-POLICY-CONFORMANCE` | Turn Helm/Kustomize policy checks and fallback status into release evidence. | `make cloud-native-render-check` |
| 5 | `GOFLY-P4-5-DX-SUPPORT-BUNDLE` | Productize doctor/release JSON, support bundles, and generated failure reports. | `make dx-troubleshooting-check` |
| 6 | `GOFLY-P4-6-GOVERNANCE-DASHBOARD-PRODUCTIZATION` | Make the governance report an adopter-facing dashboard contract. | `make governance-report-check` |

## Adoption-Wave TODO Order

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-P5-0-ADOPTION-WAVE-ROADMAP` | Keep the post-P4 adoption operations roadmap as a machine-readable contract. | `make framework-gap-check` |
| 2 | `GOFLY-P5-1-EXAMPLES-HEALTH-INDEX` | Make copyable example health, smoke commands, ports, schemas, and risk notes visible in one index. | `make api-example-consistency-check` |
| 3 | `GOFLY-P5-2-RELEASE-EVIDENCE-CONSUMPTION` | Map release evidence to adopter upgrade and publish decisions. | `make governance-report-check` |
| 4 | `GOFLY-P5-3-OPERATOR-RUNBOOK-DRILLS` | Tie runtime symptoms to health, metrics, traces, resilience, drift, and rollback checks. | `make runtime-slo-check` |
| 5 | `GOFLY-P5-4-TEMPLATE-PROFILE-TRUST` | Index template/profile purpose, generated-output guarantees, dependencies, and verification commands. | `make doc-manifest-sync-check` |
| 6 | `GOFLY-P5-5-ADOPTION-RISK-REGISTER` | Separate production-ready, candidate, report-only, and rollback-required surfaces for adopters. | `make framework-gap-check` |

## Gap Summary

| Dimension | Compared with | Current evidence | Next action |
| --- | --- | --- | --- |
| HTTP DX | Gin, Echo, Fiber, Hertz | `docs/reference/openapi-validation-envelope.md`, `make openapi-validation-check` | Add invalid request smoke and middleware ecosystem matrices. |
| Microservice scaffold | go-zero, Kratos | `make generated-version-compat-check`, `make generated-upgrade-dry-run-check` | Link scaffold trust to the framework gap matrix and adopter docs. |
| RPC Tier 1 readiness | Kitex, gRPC-Go, go-zero | `docs/reference/rpc-boundary.md`, `make rpc-boundary-check` | Add a Tier 1 evidence manifest and gate. |
| Production proof | Kratos, go-zero, Kitex | `examples/production-orders`, `make reference-app-smoke` | Add memory and Docker-backed topology evidence. |
| Release trust | Kratos, go-zero | `docs/releases/evidence-index.json`, `make release-evidence-index-check` | Publish a concise readiness summary for adopters. |
| Plugin ecosystem | go-zero, Kratos | `examples/plugin-ecosystem`, `make plugin-conformance-check` | Add permission review and publishing checklist evidence. |
| Performance credibility | Gin, Echo, Fiber, Hertz, Kitex | `bench/evidence.md`, `make bench-evidence-check` | Ratchet selected hot-path budgets after trend confidence. |
| Adopter DX | Gin, go-zero, Kratos, Kitex, Beego | `docs/explanation/adopter-decision-guide.md`, `make adopter-decision-check` | Keep this gap matrix linked and machine checked. |

## Out Of Scope

- GitHub stars, downloads, contributor counts, and community growth.
- Marketing copy or claims unsupported by local gates.
- One-shot rewrites of `rpc`, `gateway`, `app`, or generated project layout.
