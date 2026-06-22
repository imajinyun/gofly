# 🚀 gofly Agent Workflow

This file defines the default automation and collaboration workflow for this project. Any AI agent, automation script, or human operator performing governance work should follow these constraints first.

## 🎯 Governance goals and priorities

gofly is a Go microservice toolkit and code-generation project. Apply governance priorities in this order:

1. **🛡️ Safety and recoverability first**: for any change involving paths, templates, plugins, external processes, network downloads, secrets, SQL, or command execution, first ensure the change does not expand the attack surface or damage user projects.
2. **🧱 Generated-output determinism first**: code generation, template extensions, and RPC/API/model outputs must be repeatable, gofmt-clean, compilable, and must not write outside the project root.
3. **🧼 Root module hygiene first**: the root module may only keep dependencies actually imported by gofly itself; dependencies required only by generated projects must go into the generated project's own `go.mod`.
4. **✅ Quality gates before feature expansion**: new functionality must include tests, documentation/help text, and governance gates. Do not default to “tests later.”
5. **🔎 Minimal changes and traceability**: fix root causes first. For historical gosec/lint baselines that are intentionally left unfixed, document the reason, scope, and follow-up convergence plan.

## 🧭 Governance change control

Governance changes include `AGENTS.md`, `Makefile`, `bin/scripts/`, `.golangci.yml`, CI workflows, release configuration, `go.mod` tool directives, and security-baseline notes. Follow these rules for such changes:

- **📜 Scripts before prose**: documentation must have a corresponding entry in `Makefile` or `bin/scripts/`. If only text changes, explain that this is a governance-spec update rather than automation rollout.
- **🎯 Single source of truth**: quality-gate commands are authoritative in `Makefile` and `bin/scripts/`; `AGENTS.md` only describes constraints, layering strategy, and exception handling. If inconsistencies are found, fix scripts first or explicitly state the script gap.
- **🧩 Compatibility protection**: evaluate historical baselines before adding new blocking gates. Existing historical issues should first run in audit/report mode and then converge module by module, avoiding a one-shot block on all development.
- **↩️ Rollbackability**: governance scripts must not irreversibly modify user projects. Operations such as `go mod tidy`, generated-output checks, and temporary worktrees must be recoverable or operate only in temporary directories.
- **🧪 Minimal verification**: documentation governance should at least run shell syntax checks and relevant make-target dry-runs. Script governance should run the smallest real path of the target script.

## 🚦 Risk levels and handling SLA

| Level | Trigger | Default handling |
| --- | --- | --- |
| P0 Blocker | Broken builds, all tests failing, root-module dependency pollution, path traversal, command injection, credential leaks, unverifiable release artifacts | Fix immediately; do not only write a report; run the matching L2/L3 gates after the fix |
| P1 High | New high-confidence gosec finding, race, breaking contract change, uncompilable generated output, coverage ratchet regression | Fix in the current task or document the blocker; add regression tests |
| P2 Medium | Historical gosec/lint baseline, non-critical coverage gaps, docs/CLI help inconsistency, observability gap | Recommend for the next governance round; record module, command, and impact |
| P3 Low | Style alignment, comment cleanup, report wording, non-blocking script UX improvement | Fix opportunistically; do not displace P0/P1 work |

Any downgrade must document: original risk, existing protections, downgrade rationale, and follow-up trigger conditions.

## ⚙️ Default execution mode

- Report conclusions, risks, and verification results in Chinese.
- Prefer English for all documentation, including README files, reference docs, governance notes, release notes, and generated documentation unless the user explicitly requests another language.
- Work as an autonomous senior pair-programmer: proactively audit, plan, implement, test, fix, re-test, and report.
- Once the user provides direction, do not ask for confirmation between every round unless there is destructive risk or missing permission.
- Keep the loop closed: **baseline → audit → fix → test → verify → report**.
- When changing Go code, load and follow relevant Go capabilities first: `golang-how-to`, `golang-testing`, `golang-lint`, and `golang-safety`; add specialized capabilities for CLI, concurrency, security, performance, databases, gRPC, Swagger, and similar areas.
- When changing `AGENTS.md`, `Makefile`, `bin/scripts/`, `.golangci.yml`, or CI configuration, treat it as a governance change: align existing scripts first, then update documentation, and finally run at least the smallest verifiable subset of the related script.
- Do not silently ignore external changes. If files were changed by users, scripts, or linters, preserve their intent and avoid rolling back unrelated edits.

## 🧠 Required Go capabilities

Apply these capabilities by scenario when working on Go-related tasks; avoid changing code based only on generic experience:

- **⭐ Default required**: `golang-how-to`, `golang-testing`, `golang-lint`, `golang-safety`.
- **🔐 Security / external input**: for plugins, templates, filesystem, network, TLS, command execution, SQL, or authentication, also load `golang-security`.
- **🧰 CLI / configuration**: for `cmd/gofly`, flags, completion, exit codes, or config loading, also load `golang-cli`, `golang-spf13-cobra`, or `golang-spf13-viper`.
- **📡 RPC/API contracts**: for proto/thrift/OpenAPI/Swagger/REST/gRPC generation, also load `golang-grpc`, `golang-swagger`, and `golang-error-handling`.
- **🔄 Concurrency / lifecycle**: for goroutines, context, streams, watchers, or cache refresh, also load `golang-concurrency` and `golang-context`.
- **📈 Performance governance**: optimize only after a benchmark/profile/production signal identifies a bottleneck; measure with `golang-benchmark`, optimize with `golang-performance`.

## 🧊 Cache strategy

Governance and quality gates disable project-level cache semantics by default:

- For steps that must verify cache bypass or start the gofly runtime, set `GOFLY_CACHE_DISABLED=true` so runtime cache, layered local L1 cache, and remote L2 cache are bypassed.
- For normal cache-component behavior tests, do not inject `GOFLY_CACHE_DISABLED=true` globally; otherwise tests that should prove caching works become bypass-mode tests.
- Go tests use `GOFLAGS=-count=1` to avoid cached results. Governance and CI tests should also use `-shuffle=on` by default to expose order coupling.
- Use temporary `GOCACHE` and `GOTMPDIR`; remove them after execution to avoid reusing persistent local build cache.
- Reuse the Go module download cache by default to avoid exhausting local temporary space. Use `GOVERNANCE_ISOLATE_GOMODCACHE=true` only when strong module-cache isolation is required.
- Use `GOPROXY=direct` to avoid remote module-proxy cache semantics.
- Remote plugin downloads must not reuse `$USER_CACHE_DIR/gofly/plugins`; use one-time temporary files.
- Governance tools (`govulncheck`, `gosec`, `apidiff`) are version-pinned through Go `tool` directives. CI and local scripts should prefer `go tool <name>`.
- Generated-project-only dependencies such as `gorm.io/gorm` must be written only to the generated project's own `go.mod`, never to the root module. `bin/scripts/check-mod-tidy.sh` rejects generated-only dependencies not actually imported by the root module.

## 📦 Dependency and module governance

- Direct dependencies in the root `go.mod` must satisfy the “actually imported by the root module” rule. Dependencies needed only for temporary code generation, example projects, or test fixtures must not remain in the root module.
- Before adding a dependency, confirm whether the standard library or an existing dependency is sufficient. If a new dependency is required, explain its purpose, import site, alternatives, and security impact.
- Run `go mod tidy` only as a check/convergence step. If tidy removes generated-project-only dependencies, keep the removal instead of repeatedly re-adding them to the root module.
- Dependencies for generated projects, such as GORM-style model output, should be written by the generator into the target project's `go.mod` and verified with a temporary project test, not by polluting the gofly root module.
- `go.sum` changes must be explainable by the root module dependency graph. Any unexplained indirect dependency drift should be traced back to the triggering command or external fixture.
- Go 1.24+ tool dependencies use `go.mod` `tool` directives for version pinning. Do not add legacy `tools.go` unless the project explicitly downgrades below Go 1.24.
- Prefer patch/minor dependency upgrades. For dependencies involving networking, serialization, databases, authentication, cryptography, code generation, or CLI behavior, read release notes and run relevant subsystem tests.
- For temporary generated-project dependency verification, create an independent module under `t.TempDir()`, `.tmp-test`, or another external temporary directory. Do not run `go get` in the root module if it leaves generated-project dependencies behind.

## 🔌 Public API and contract governance

- Public Go API compatibility is handled by `make api-compat` / `bin/scripts/check-public-api.sh`. If no git base ref is available, the script may skip; reports must explain the skip reason.
- CLI commands, flags, JSON output, plugin protocol, OpenAPI/proto/thrift/API diffs are external contracts. Field deletion, renaming, or semantic changes require migration notes or a breaking-change report.
- New JSON fields should be backward-compatible. Removing fields or changing types is a P1/P0 risk and requires a compatibility window or explicit version boundary.
- Generator template changes must verify that old inputs still generate successfully. If output diff is expected, describe the diff type: formatting, feature addition, compatibility fix, or breaking change.

## 🧱 7-stage governance roadmap

Project-level quality governance proceeds through these 7 stages in order. Complete each stage before moving to the next:

1. **🧪 Fill test gaps**: identify low-coverage packages, first cover 0% functions with pure unit tests, then add boundary branches; record baseline, target, and final coverage.
2. **🧯 Fill error handling gaps**: check that all returned errors are handled; add missing `if err != nil` and error wrapping; prioritize external interfaces and goroutine boundaries.
3. **📡 Fill logging and observability gaps**: standardize log format, add metrics/traces on key paths, and verify production-default configuration.
4. **📚 Fill docs and comments**: add godoc for public APIs, CLI help text for commands, and inline comments for complex logic.
5. **⚡ Performance and concurrency governance**: identify hot paths, add benchmarks, and check for goroutine leaks and race conditions.
6. **🔐 Security and dependency governance**: run gosec/govulncheck, remove unused dependencies, and validate path safety and input validation.
7. **🏗️ Architecture and contract governance**: check public API compatibility, proto/OpenAPI contract consistency, and generated-output determinism.

Every stage must follow: **baseline → audit → fix → test → verify → report**.

Recommended entry point:

```bash
make governance-10-rounds
```

Smaller scenario-specific loops:

```bash
make tidy
make cover-check
make govulncheck
make gosec
```

## 🔟 10-round architecture and quality governance

Whenever the user asks for “architecture governance,” “quality governance,” “re-plan governance,” or “multi-round governance,” execute the following 10 rounds by default. After each round, continue automatically to the next and produce one final report.

1. **🧭 Baseline and boundary inventory**: confirm modules, commands, generator, runtime, cache, governance, RPC/REST boundaries, and existing quality gates.
2. **🧵 Context and lifecycle governance**: check `context.Context` propagation, timeouts, cancellation, process lifecycle, and goroutine leak risks.
3. **🧰 CLI and configuration governance**: check command arguments, usage errors, exit codes, config loading, hot reload, defaults, and compatibility.
4. **🏭 Code-generation governance**: check proto/API/model/template output determinism, path safety, compatibility, and compilability.
5. **🔌 Plugin and external-process governance**: check plugin protocol, remote downloads, output limits, error semantics, temporary files, command injection, and resource cleanup.
6. **🧊 Cache and remote-dependency governance**: check local cache, remote cache, layered cache, module proxy, remote templates, and external downloads.
7. **📜 REST/RPC/API contract governance**: check OpenAPI, descriptors, admin endpoints, path parameters, error responses, and compatible fields.
8. **🛡️ Security and defensive-coding governance**: check path traversal, symlinks, nil handling, type assertions, body limits, URL schemes, and sensitive data leaks.
9. **📈 Observability and production-default governance**: check logs, metrics, traces, profiles, governance manager, and production/test/development configuration.
10. **✅ Convergence verification and report**: run formatting, tests, race, vet, lint, and tidy; summarize changes, risks, verification commands, and follow-up recommendations.

Every round must produce traceable conclusions: what was found, what changed, why anything is intentionally left unfixed, and which commands verified the result.

## 🧩 5-round productization governance workflow

When the user asks for productization governance, use the following 5 rounds. The goal is to move gofly from “functionally usable” to “adoptable, verifiable, operable, and shareable.”

1. **🤝 Trust and adoption foundation**: audit public APIs, CLI flags/JSON, control-plane snapshots, generator output, and upgrade plans; fill contract inputs, dry-run plans, compatibility tests, and machine-readable verification entry points.
2. **🛣️ REST/DX enhancement**: audit binding, validation, error responses, middleware, OpenAPI/schema, and path/query/header parameter experience; fill stable error envelopes, documented response schemas, generated examples, and regression tests.
3. **🛰️ Microservice completeness**: audit config, discovery, governance rules, admin/control-plane, K8s, Helm, observability, and production check scripts; fill production defaults, observability assets, script permissions, and generated-project smoke tests.
4. **📊 Performance credibility**: keep benchmark work consolidated in `bench/`; maintain baseline/matrix/evidence artifacts; cover REST/RPC/governance/OpenAPI scenarios; run `make bench-evidence-check` and at least one benchmark smoke; avoid making performance claims from a single run.
5. **🌱 Community growth and migration**: audit README, docs taxonomy, quickstart, migration/case studies, P1 roadmap, and release/CI entries; run minimal gates such as `contract-docs-check`, `p1-growth-check`, and `migration-docs-check` so new capabilities are discoverable and reproducible.

Execution constraints:

- Start each round by inventorying already-completed work, avoiding rollbacks of user, script, or linter changes.
- Generator, contract, or production-asset changes must include tests; `gofly new service` changes should preferentially run `make test-generated-matrix`.
- Benchmark-related changes must keep `bench/` as the only public benchmark workspace; do not reintroduce `benchmarks/`.
- Final reports must list by 5 rounds: what changed, key file locations, verification commands, failures or downgrades, and follow-up recommendations.

## 🤖 AI governance pipeline

LLM calls go through a 9-stage governance pipeline. The manifest exposes it through the `governancePipeline` field:

```
request-redaction  →  rate-limit  →  token-budget  →  response-cache  →  circuit-breaker  →  provider-call  →  usage-accounting  →  audit-log  →  telemetry-emit
```

| Stage | Responsibility | Optional |
|---|---|---|
| `request-redaction` | Redact secrets and sensitive metadata from prompts before entering the pipeline | Yes |
| `rate-limit` | Token-bucket rate limiting; returns `ErrRateLimited` when empty | Yes |
| `token-budget` | Check accumulated token budget; returns `token_budget_exceeded` on overflow | Yes |
| `response-cache` | Query response cache; return immediately on hit, coalesce concurrent misses | Yes |
| `circuit-breaker` | Fast-reject when breaker is open; allow probe requests during half-open recovery | No |
| `provider-call` | Forward requests to the LLM provider with timeout, retry, and failover wrapping | No |
| `usage-accounting` | Record token usage (input/output/total) and deduct budget | No |
| `audit-log` | Emit structured audit logs: operation, provider, model, status, duration, tokens, error | No |
| `telemetry-emit` | Emit low-cardinality metric/trace fields: `cache_status`, `error_class`, `provider_status_code` | No |

Verification:

```bash
gofly ai manifest --json | python3 -c "import json,sys; d=json.load(sys.stdin); gp=d['data']['llmGovernance']['governancePipeline']; assert len(gp)==9, f'expected 9 stages, got {len(gp)}'"
```

## 🧪 Layered governance gates

Choose the gate level based on the change scope. The closer the change is to release or cross-module behavior, the higher the level.

| Level | Scope | Required commands |
| --- | --- | --- |
| L0 Docs/comments | Only `AGENTS.md`, comments, or explanatory text | Read related scripts/config to confirm consistency; if governance commands changed, run the relevant script help/minimal check |
| L1 Single-package change | Code or tests within one package | `go test -shuffle=on <pkg>`, `go vet <pkg>`; add `go tool gosec -quiet <pkg>/...` for security-related changes |
| L2 Subsystem change | generator, cmd, cache, rpc, rest, and similar subtrees | `go test -shuffle=on ./subtree/...`, `go test -shuffle=on -race ./subtree/...`, `go vet ./subtree/...`, targeted coverage/security scans |
| L3 Full-repository governance | Cross-module, dependency, script, CI, or pre-release changes | `make governance-10-rounds`, `make cover-check`, `make govulncheck`, `make gosec`, `make tidy` |

If the local environment cannot complete a high-level gate, continue with runnable lower-level commands and document the blocker and reproduction command in the report.

Gate selection rules:

- Changes spanning more than two subsystems default to L3.
- Changes to `go.mod`, `go.sum`, `Makefile`, `bin/scripts/`, `.golangci.yml`, or release configuration default to at least L2; if they affect full-repository commands, escalate to L3.
- Changes to security boundaries (paths, plugins, templates, external processes, network, TLS, authentication, secrets) must add security scans and attack-surface notes even if only one package is touched.
- If only target packages are tested, the report must explain why the full repository was not tested and what risk remains uncovered.

## ✅ Mandatory quality gates

After governance work, run at least:

```bash
make governance-10-rounds
```

This entry point runs with no persistent cache semantics:

- `gofmt` checks
- `go test -shuffle=on ./...`
- `go vet ./...`
- `golangci-lint run ./...`
- `go tool govulncheck -scan=package ./...` / `go tool gosec ...` (the historical gosec baseline is currently zero, so gosec is blocking)
- `go test -shuffle=on -race ./...`
- `bin/scripts/coverage-check.sh`, enforcing both the minimum threshold and `COVERAGE_RATCHET`
- `go mod tidy` / `go mod verify` consistency checks

Quality-gate constraints:

- `TESTFLAGS` includes `-shuffle=on` by default. Do not disable shuffle to bypass order coupling unless the report explains the flaky root cause and temporary exemption scope.
- `GOFLAGS` includes `-count=1` by default. Do not use cached results as governance evidence.
- `bin/scripts/coverage-check.sh` checks both `COVERAGE_THRESHOLD` and `COVERAGE_RATCHET`; after raising coverage to a stable new level, recommend raising the ratchet to prevent future regressions.
- `go tool govulncheck` defaults to `-scan=package`. If source mode fails because of tooling issues, it may be downgraded to package mode, but the downgrade reason must be recorded.
- `go tool gosec` is currently blocking. New code must not introduce security findings. `#nosec` is allowed only for false positives or protocol/generator-required cases and must state the exact rule and rationale.
- If `golangci-lint` fails, fix the root cause first. `//nolint` must name the linter and include a rationale; broad exemptions without reasons are not allowed.
- Round 10 of `bin/scripts/governance-10-rounds.sh` runs the coverage ratchet, `govulncheck`, `gosec`, and the final dependency-list check. Set `GOVERNANCE_SKIP_SECURITY=true` only when tooling or environment limits are clear, and document the skip reason.

Release governance should also include CodeQL, Dependency Review, release checksum provenance attestations, and GoReleaser SBOM generation.

If a gate fails because a local tool is missing, continue running other available gates and clearly report the failure reason, impact scope, and reproduction command.

## 🏁 CI and release governance

- CI must use `make governance-10-rounds` or equivalent steps as the primary quality entry point, explicitly including `-shuffle=on`, `-race`, `go vet`, `golangci-lint`, coverage ratchet, and tidy/verify.
- CI supply-chain gates must cover GitHub Actions workflow syntax, governance-script shellcheck, OSV dependency scanning, OpenSSF Scorecard, CodeQL, Dependency Review, and Trivy.
- GitHub Actions or other CI jobs must use least privilege. Only release jobs may request `contents: write`, `packages: write`, `id-token: write`, or attestations permissions.
- GitHub Actions `uses:` should default to pinned full commit SHAs. Upgrades should be maintained by grouped Dependabot PRs. Do not fall back to floating tags without explicit downgrade rationale.
- Docker images built on PRs must not be pushed. Release images must include SBOM, provenance attestation, and vulnerability scan results. Docker base/builder images should be digest-pinned by default.
- Dependency update bots (Dependabot/Renovate) may merge only after all required gates pass. Auto-merge must rely on branch protection, not only actor checks.
- Before release, record version, commit, build time, checksums, SBOM/provenance location, and Go tool security-scan results.
- Any skipped release gate must have human approval recorded and must be listed as a risk in the final report.

## 🔐 Security governance baseline

- **Path safety**: all code writing to user-provided directories must use relative-path validation, root constraints, symlink-parent rejection, or Go 1.24+ `os.Root` style protections. Do not rely only on `filepath.Clean` plus string-prefix checks.
- **Plugin safety**: remote plugins may use only HTTPS or explicit local test sources; downloads must have size limits, timeouts, one-time temporary files, sha256 digests, and tests proving user cache is not reused.
- **Template safety**: remote template sync must reject dangerous target directories; symlinks inside template directories must not be followed, read, or copied.
- **External processes**: `exec.Command` must receive split arguments. Shell string concatenation is forbidden. Executable paths, plugin arguments, and output size must be bounded.
- **Network and TLS**: configurations that allow `InsecureSkipVerify` must be explicit opt-ins and must be marked as risks in docs/reports.
- **Randomness and cryptography**: security tokens, keys, and nonces must use `crypto/rand`; when `math/rand` is used for non-security purposes, code or reports should state that boundary.
- **Sensitive data**: logs, errors, CLI output, and test snapshots must not leak tokens, secrets, Cookies, Authorization headers, or internal credentials.

## 🕵️ gosec / govulncheck baseline governance

- `gosec` currently runs with `-quiet` as a blocking gate. New code may not introduce unexplained security findings.
- `#nosec` is allowed only for false positives or intentionally required generator/CLI behavior. Format must include rule and reason, for example: `#nosec G306 -- generated source files are intentionally user-readable`.
- Each security-governance report should record scan scope, issue count, resolved count, whether new/regressed findings exist, and the next modular cleanup plan.
- `govulncheck` uses `-scan=package` as the stable default. If switching to source mode, record toolchain version and downgrade path on failure.
- Dependency vulnerabilities should be prioritized by reachability, exposure, and mitigating configuration. Do not overstate an unreachable indirect CVE as P0.

## 🏭 Code-generation governance

- Generator output must satisfy: no path escape, creatable directories, gofmt-clean Go files, deterministic output, and no unrelated diff on repeated runs.
- API/RPC/Model/Template generation changes must add fixtures or temporary-project compile tests proving generated output compiles.
- Contract-related changes must cover breaking detection, diff, format, and compatible output; do not verify only the happy path.
- Soft delete, streaming RPC, OpenAPI schema/path params, plugin protocol, and remote template/cache are high-risk paths. New behavior requires regression tests.
- `go.mod` changes in generated projects must happen in the target directory. Root module dependency hygiene is guarded by `bin/scripts/check-mod-tidy.sh`.
- Generator file writes should reuse centralized safe helpers. New `os.WriteFile`, `os.MkdirAll`, `os.ReadFile`, or `os.Open` calls must explain path source and root-directory constraints.
- Plugin or template-produced file lists must distinguish declared file count from actual written file count. Automation JSON output should report actual written counts.
- Generated-output verification should preferably run `go test` or `go test ./...` in a temporary module. Do not rely only on string-fragment assertions.

## 🧪 Coverage and test governance

- New or fixed functionality should prioritize behavior tests. Do not write fragile implementation-detail tests only for coverage.
- Use temporary directories and independent fixtures for complex generator paths. Tests must not depend on execution order, global HOME, user plugin cache, or persistent build cache.
- Coverage-improvement work must record baseline, target, final coverage, and key new cases. If a new stable level is reached, recommend raising `COVERAGE_RATCHET`.
- Tests involving goroutines, streams, watchers, or cache refresh should preferentially run with `-race`. Diagnose flaky root causes first; do not use lower concurrency or disabling shuffle as a long-term fix.
- New tests must not depend on real user HOME, global plugin cache, system git config, fixed ports, external networks, or local persistent cache. If external resources are required, tests must be skippable and the condition explained.
- Table-driven tests must include readable `name` fields; failure messages should show inputs, actual values, and expected values rather than only “failed.”
- When changing governance scripts, run at least `sh -n` and the related make dry-run. When changing test strategy, run the smallest target-package test that proves the strategy works.

### 🧫 Pure unit-test strategy

During the “fill test gaps” governance stage, prefer pure unit tests and avoid Docker or real network dependencies:

- **gRPC interceptors**: call interceptor closures directly with fake handlers/invokers/streamers to verify metadata, retry, breaker, and rate-limit behavior.
- **Resolver**: use fake `rpc.WatchResolver` and fake `resolver.ClientConn` to test the full `Build`, `ResolveNow`, `watch`, and `update` lifecycle.
- **Server**: use `httptest` requests against admin endpoints to verify `/healthz`, `/metrics`, and `/governance/rules`; use `healthpb.Health_ServiceDesc` + `health.NewServer()` to test `RegisterService`.
- **OTel trace**: build contexts with `metadata.NewIncomingContext` / `metadata.NewOutgoingContext` to verify traceparent extraction and injection; use fake `ClientStream` to test `otelClientStream` error paths.
- **General rule**: first cover 0% functions to establish a behavior baseline, then add boundary branches such as nil guards, empty slices, and error paths.

### 📏 Coverage ratchet practice

- `Makefile` currently sets `COVERAGE_RATCHET` to `90%` and `COVERAGE_THRESHOLD` to `60%`.
- After improving a single subsystem package such as `rpc/grpc`, if coverage rises significantly, the report should recommend whether to raise the global `COVERAGE_RATCHET`.
- Example: `rpc/grpc` baseline 71.9% → 90.3%, with no remaining 0% functions; after the full repository stabilized, the ratchet was converged to 90%.

## 🧾 Review checklist

Before submitting, self-check the following and cover relevant items in the report:

- Are there any path escape, symlink, external-process, network-download, credential-leak, or TLS downgrade risks?
- Were root module dependencies added or removed? Can the change be explained by the root module import graph?
- Does the change affect CLI/API/RPC/plugin/JSON output contracts? Are breaking or migration notes required?
- Were enough behavior tests, shuffle tests, race tests, or temporary generated-output compile tests added?
- Did coverage stay non-regressing? Should `COVERAGE_RATCHET` be raised?
- Were gates matching the change level run? If not, are reasons and reproduction commands documented?

## 🚑 Exception and downgrade handling

- If `utree flush`, `git diff`, or `git status` fails because the current directory is not a git worktree, record it as an environment limitation rather than a code verification failure.
- If security/governance tooling crashes because of a toolchain bug, first try recommended downgrade parameters, such as `govulncheck -scan=package`, and smaller scan scopes. If still failing, record version, command, and error summary.
- If dependency downloads, module proxies, or cache directories are constrained, prefer project-local temporary `GOCACHE/GOTMPDIR`, and reuse the module download cache when needed. Do not write temporary dependencies into the repository.
- If external processes or tests generate temporary projects and cleanup fails, report the path and impact so temporary directories are not mistaken for repository changes.

## 📝 Report format

Final reports include:

- The governance goal for this task
- 10-round execution summary
- Modified file list, using `file_path:line_number` references
- Quality-gate results
- Known risks and unfinished items
- Follow-up recommendations

Report requirements:

- Reference code or script locations with the `file_path:line_number` format.
- For every failed gate, include: command, failure type, whether it is an environment limitation, and whether it affects this task's goal.
- For security scan output, distinguish “new issues,” “resolved issues,” and “historical baseline.” Historical baselines must not hide new risks.
- For dependency changes, state whether they affect the root module, generated projects, or test fixtures.
- Conclusions must clearly state whether the user's goal was met; do not only list the execution process.
