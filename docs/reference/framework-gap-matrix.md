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
The adopter-facing risk split is captured in
[`adoption-risk-register.json`](adoption-risk-register.json) with schema
`gofly.adoption_risk_register.v1`.
The P6 long-term adoption wave is captured in
[`framework-gap-long-term-adoption.json`](framework-gap-long-term-adoption.json)
with schema `gofly.framework_gap_long_term_adoption.v1`. It focuses on support
lifecycle, integration matrix ownership, dependency ownership, required-check
drift, release prerequisite drift, and production readiness scorecards.
The P7 adopter proof wave is captured in
[`framework-gap-adopter-proof.json`](framework-gap-adopter-proof.json) with
schema `gofly.framework_gap_adopter_proof.v1`. It focuses on runnable adopter
proof, evidence traceability, upgrade rehearsal, incident drill evidence, and
capability claim provenance.
The `gofly.capability_claim_provenance.v1` section in that file ties each
framework comparison or production-readiness claim to source evidence, a gate,
risk class, adopter action, and unsupported-claim handling.
The post-R5 R6 roadmap is captured in
[`framework-gap-post-r5-roadmap.json`](framework-gap-post-r5-roadmap.json) with
schema `gofly.framework_gap_post_r5_roadmap.v1`. It focuses on the remaining
non-community gaps after the R5 convergence batch: HTTP migration DX, generated
scaffold compatibility, RPC transport boundaries, integration ownership,
production topology proof, release supply-chain trust, performance credibility,
adopter decision evidence, and convergence traceability.
The post-R6 R7 roadmap is captured in
[`framework-gap-post-r6-roadmap.json`](framework-gap-post-r6-roadmap.json) with
schema `gofly.framework_gap_post_r6_roadmap.v1`. It focuses on deeper runnable
adoption proof after the R6 convergence batch: migration smoke depth, RPC Tier 1
promotion evidence, importer compatibility, DB/cache/discovery adapter proof,
plugin/template supply-chain hardening, release CI enforcement, performance
trend promotion, support-bundle remediation, and convergence traceability.

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

## Long-Term Adoption TODO Order

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-P6-0-LONG-TERM-ADOPTION-ROADMAP` | Keep the post-P5 long-term adoption roadmap as a machine-readable contract. | `make framework-gap-check` |
| 2 | `GOFLY-P6-1-SUPPORT-LIFECYCLE` | Turn deprecation metadata into an adopter-facing support lifecycle playbook. | `make deprecation-lifecycle-check` |
| 3 | `GOFLY-P6-2-INTEGRATION-MATRIX` | Map storage, discovery, MQ, gateway, RPC, and observability integrations to owners, gates, and release prerequisites. | `make required-checks-drift-check` |
| 4 | `GOFLY-P6-3-DEPENDENCY-OWNERSHIP-PLAYBOOK` | Separate root dependencies, generated-project dependencies, security response, and integration delegation. | `make dependency-upgrade-evidence-check` |
| 5 | `GOFLY-P6-4-REQUIRED-CHECK-DRIFT` | Block drift between documented gates, CI jobs, branch protection, and release prerequisites. | `make required-checks-drift-check` |
| 6 | `GOFLY-P6-5-PRODUCTION-READINESS-SCORECARD` | Summarize stable, candidate, report-only, and rollback-required surfaces across production adoption evidence. | `make governance-report-check` |

## Adopter Proof TODO Order

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-P7-0-ADOPTER-PROOF-ROADMAP` | Keep the post-P6 adopter proof roadmap as a machine-readable contract. | `make framework-gap-check` |
| 2 | `GOFLY-P7-1-EVIDENCE-TRACEABILITY` | Link top-level claims to source manifests, report fields, gates, and rollback or escalation actions. | `make governance-report-check` |
| 3 | `GOFLY-P7-2-UPGRADE-REHEARSAL` | Tie generated dry-run, dependency ownership, release evidence, smoke gates, and rollback steps into one rehearsal path. | `make generated-upgrade-dry-run-check` |
| 4 | `GOFLY-P7-3-INCIDENT-DRILL-EVIDENCE` | Map runtime symptoms to SLO signals, required artifacts, rollback triggers, and post-incident evidence. | `make runtime-slo-check` |
| 5 | `GOFLY-P7-4-CAPABILITY-CLAIM-PROVENANCE` | Prevent unsupported framework comparison or production-readiness claims from drifting beyond local evidence. | `make framework-gap-check` |

## Post-R5 R6 TODO Order

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-GOV-10R6-01` | Keep the post-R5 gap analysis, non-community scope, and aiflow task order as a machine-readable contract. | `make framework-gap-check` |
| 2 | `GOFLY-GOV-10R6-02` | Make HTTP migration from Gin, Echo, Fiber, and Hertz easier to evaluate through route, binding, middleware, error envelope, and OpenAPI evidence. | `make openapi-validation-check` |
| 3 | `GOFLY-GOV-10R6-03` | Continue closing go-zero and Kratos scaffold trust gaps with generated fixture, diff, dependency, and rollback evidence. | `make generated-upgrade-dry-run-check` |
| 4 | `GOFLY-GOV-10R6-04` | Keep Kitex and gRPC-Go transport depth gaps explicit without claiming transport parity. | `make rpc-boundary-check` |
| 5 | `GOFLY-GOV-10R6-05` | Tie SQL, Redis, MQ, discovery, gateway, RPC, and observability integrations to owners, gates, dependency triggers, and rollback notes. | `make required-checks-drift-check` |
| 6 | `GOFLY-GOV-10R6-06` | Link reference-app topology, runtime SLO, cloud-native rollout, incident drills, and rollback evidence. | `make reference-app-smoke` |
| 7 | `GOFLY-GOV-10R6-07` | Connect release artifacts, checksums, SBOM, Docker digest, provenance, Trivy, and required checks to adopter release decisions. | `make governance-report-check` |
| 8 | `GOFLY-GOV-10R6-08` | Promote performance claims only through benchmark trend evidence, allocation budgets, and report-only latency boundaries. | `make bench-regression-check` |
| 9 | `GOFLY-GOV-10R6-09` | Tie framework choice guidance to claim provenance, support bundles, dashboard evidence, caveats, gates, and rollback actions. | `make adopter-decision-check` |
| 10 | `GOFLY-GOV-10R6-10` | Record convergence evidence for all R6 tasks, commits, gates, known risks, and ignored runtime paths. | `make governance-10-rounds` |

## Post-R6 R7 TODO Order

| Order | Task | Gap | Acceptance gate |
| --- | --- | --- | --- |
| 1 | `GOFLY-GOV-10R7-01` | Keep the post-R6 gap analysis, active aiflow batch, and non-community scope as a machine-readable contract. | `make framework-gap-check` |
| 2 | `GOFLY-GOV-10R7-02` | Deepen runnable Gin, go-zero, Kratos, and Kitex migration proof with smoke, support bundle, caveat, and rollback evidence. | `make adopter-decision-check` |
| 3 | `GOFLY-GOV-10R7-03` | Turn RPC Tier 1 promotion blockers into explicit stream, resolver, balancer, retry, deadline, auth, tracing, benchmark, and rollback rows. | `make rpc-boundary-check` |
| 4 | `GOFLY-GOV-10R7-04` | Extend generated importer compatibility across API/proto imports, aliases, generated-only dependencies, repeat diffs, and rollback. | `make generated-upgrade-dry-run-check` |
| 5 | `GOFLY-GOV-10R7-05` | Add DB/cache/discovery proof rows for adapters, generated-project boundaries, dependency triggers, fallback behavior, and rollback. | `make required-checks-drift-check` |
| 6 | `GOFLY-GOV-10R7-06` | Harden plugin and template supply-chain evidence across registry, manifest, digest, permission, compatibility, malicious paths, and partial writes. | `make plugin-conformance-check` |
| 7 | `GOFLY-GOV-10R7-07` | Tighten release CI supply-chain enforcement across artifact producers, required checks, security scans, provenance, and publish/block decisions. | `make governance-report-check` |
| 8 | `GOFLY-GOV-10R7-08` | Promote performance evidence only through multi-run trend confidence, allocation budgets, report-only latency, and unsupported-surface handling. | `make bench-regression-check` |
| 9 | `GOFLY-GOV-10R7-09` | Link doctor, release check, support bundle, generated failure report, dashboard fields, and next actions into a remediation loop. | `make dx-troubleshooting-check` |
| 10 | `GOFLY-GOV-10R7-10` | Record convergence evidence for all R7 tasks, commits, gates, known risks, and ignored runtime paths. | `make governance-10-rounds` |

## Adoption Risk Register

`adoption-risk-register.json` separates adoption surfaces into four classes:

| Risk class | Meaning |
| --- | --- |
| production-ready | Has blocking local gates and can be used by adopters when the listed guardrail passes. |
| candidate | Has concrete guardrails but still requires adopter review or local promotion evidence. |
| report-only | Provides trend or comparison evidence, but should not be treated as a blocking claim unless a ratchet promotes it. |
| rollback-required | Indicates a failed gate or breaking candidate where the recommended action is to pin, rollback, or block promotion. |

## Capability Claim Provenance

Capability claims are governed by
`gofly.capability_claim_provenance.v1` in
[`framework-gap-adopter-proof.json`](framework-gap-adopter-proof.json). Claims
without local source evidence, a runnable gate, risk class, adopter action, and
unsupported-claim handling must be downgraded to `report-only` or removed before
release.

| Claim | Risk class | Gate |
| --- | --- | --- |
| `http-dx-openapi-envelope` | `production-ready` | `make openapi-validation-check` |
| `generated-scaffold-upgrade` | `production-ready` | `make generated-upgrade-dry-run-check` |
| `rpc-boundary-tier1` | `candidate` | `make rpc-boundary-check` |
| `production-reference-proof` | `candidate` | `make reference-app-smoke` |
| `release-trust-evidence` | `production-ready` | `make governance-report-check` |
| `plugin-publishing-protocol` | `candidate` | `make plugin-conformance-check` |
| `performance-credibility` | `report-only` | `make bench-regression-check` |

Adopter-facing docs that make capability or framework comparison claims include
hidden `claim-provenance` markers. `make framework-gap-check` verifies that each
marker references a known claim id and that `docs/superpowers/` remains ignored
and outside the scan target set.

<!-- claim-provenance: http-dx-openapi-envelope -->
<!-- claim-provenance: generated-scaffold-upgrade -->
<!-- claim-provenance: rpc-boundary-tier1 -->
<!-- claim-provenance: production-reference-proof -->
<!-- claim-provenance: release-trust-evidence -->
<!-- claim-provenance: plugin-publishing-protocol -->
<!-- claim-provenance: performance-credibility -->

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
