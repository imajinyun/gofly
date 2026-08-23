# 🪽 gofly

`gofly` is an **AI-native Go microservice framework** for teams that want more than an HTTP router: **codegen + runtime governance + control-plane** in one Go-native toolkit.

It is designed for platform, backend, and AI-agent-assisted engineering teams who need to generate services quickly, run them with production defaults, and keep runtime behavior observable and governable after deployment.

- 📦 **Module:** `github.com/imajinyun/gofly`
- 🧭 **Go:** 1.26+
- 🚀 **CLI:** `gofly`

---

## ✨ What gofly solves

- 🛠️ **Start from contracts, not boilerplate** — scaffold REST/RPC services, handlers, gateways, models, Dockerfiles, Kubernetes manifests, and compatibility adapters from CLI commands and IDLs.
- 🌐 **Run with batteries included** — wire REST, RPC, gateway, cache, MQ, config, discovery, lifecycle, and admin diagnostics without assembling every package by hand.
- 🛡️ **Govern behavior at runtime** — ship rate limiting, retries, circuit breaking, auth helpers, runtime policy snapshots, and governance rules as first-class service capabilities.
- 🧭 **Expose a control-plane surface** — make descriptors, generated contracts, service discovery state, runtime policies, and diagnostics queryable by operators and AI agents.
- 🤖 **Keep AI agents grounded** — provide machine-readable CLI output, manifest data, contract diffing, and governance checks so agents can generate, inspect, and safely change services.

## 🧩 Capability map

```text
                              ┌──────────────────────────┐
                              │        AI operator        │
                              │ plan / manifest / stream  │
                              │ doctor / JSON envelopes   │
                              └────────────┬─────────────┘
                                           │ machine-readable context
┌──────────────────────────┐      ┌────────▼────────┐      ┌──────────────────────────┐
│ contract-first codegen   │─────▶│ generated app   │─────▶│ runtime control-plane    │
│ quickstart / new         │      │ REST / RPC /    │      │ admin / health / metrics │
│ api / rpc / model        │      │ gateway / jobs  │      │ diagnostics / discovery  │
│ docker / kube / migrate  │      │ cache / MQ / KV │      │ snapshots / descriptors  │
└────────────┬─────────────┘      └────────┬────────┘      └────────────┬─────────────┘
             │                             │                            │
             │ writes stable project       │ emits runtime state         │ validates evidence
             ▼                             ▼                            ▼
┌──────────────────────────┐      ┌─────────────────┐        ┌────────────────────────┐
│ generated layout         │      │ governance      │        │ compatibility gates    │
│ internal/api/http        │◀────▶│ retry / limit   │◀──────▶│ api/rpc/model replay   │
│ internal/api/rpc         │      │ breaker / auth  │        │ release / docs checks  │
│ internal/app             │      │ policy / trace  │        │ upgrade dry-run        │
│ internal/model + repo    │      └─────────────────┘        └────────────────────────┘
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│ extension surface        │
│ templates / plugins      │
│ features / examples      │
│ bug + support bundles    │
└──────────────────────────┘
```

| Capability area | Current surface | Existing entry points |
| --- | --- | --- |
| 🚀 Service scaffolding | Minimal, basic, production, REST-only, RPC-only, quickstart, AI-selected templates, and generated smoke checks | `gofly quickstart`, `gofly new service`, `gofly new api`, `gofly new rpc`, `gofly ai new` |
| 🛠️ REST/API generation | `.api` parsing, imports, formatting, REST route generation, OpenAPI import/export, generated request/response types, route tests, go-zero-compatible API layout | `gofly api gen`, `gofly api go`, `gofly api format`, `gofly api import`, `gofly api doc`, `gofly handler gen` |
| 📡 RPC generation | Protobuf parsing, local import resolution, multiple services, streaming descriptors, WKT mappings, gRPC adapter generation, generic handler binding, client/server stubs | `gofly rpc gen`, `gofly rpc protoc`, `gofly rpc client`, `gofly rpc server`, `gofly rpc middleware` |
| 🧱 Generated layout | HTTP entrypoints in `internal/api/http`, RPC code in `internal/api/rpc`, application orchestration in `internal/app`, go-zero model structs in `model` and repositories in `repo` | `gofly new service`, `gofly api gen --profile gozero-compatible`, `gofly model mysql ddl --style go_zero` |
| 🗄️ Model and storage generation | SQL DDL and datasource introspection, table filters, prefix/ignore-column handling, MySQL/PostgreSQL dialects, cache helpers, GORM style, go-zero-style `model` + `repo` split | `gofly model gen`, `gofly model mysql ddl`, `gofly model pg ddl`, `gofly model mysql datasource`, `gofly model pg datasource` |
| 🌐 Service runtime | REST server/client, RPC server/client, gRPC adapters, gateway runtime, lifecycle bootstrap, config loading, discovery, cache, MQ, KV, storage, scheduler, event bus, saga, outbox | `rest/`, `rpc/`, `rpc/grpc/`, `gateway/`, `app/`, `cache/`, `core/*` |
| 🛡️ Runtime governance | Retries, circuit breakers, token/sliding/adaptive limits, concurrency guards, RPC method policies, auth helpers, request metadata, defensive security helpers | `core/retry`, `core/breaker`, `core/limit`, `core/governance`, `core/auth`, `core/security`, `rpc/policy.go` |
| 🧭 Control-plane and observability | Runtime snapshots, service discovery state, governance rules, admin endpoints, descriptors, health checks, metrics, tracing, profiling-ready observability setup | `core/runtime`, `core/controlplane`, `core/observability`, `ops/admin`, `rest/health.go`, `rpc/admin.go`, `gateway/admin.go` |
| 🔌 Gateway and contract safety | OpenAPI-backed routing, transcoding profiles, BFF aggregation validation, API/RPC diffing, breaking-change checks, release readiness reports | `gofly gateway`, `gofly api diff`, `gofly api breaking`, `gofly rpc check`, `gofly rpc breaking`, `gofly release check` |
| 🤖 AI-governed workflows | Machine-readable command manifest, control-plane snapshots, project planning, governed completion/streaming, provider diagnostics, redaction, failover, token-budget metadata | `gofly ai manifest`, `gofly ai control-plane`, `gofly ai plan`, `gofly ai new`, `gofly ai complete`, `gofly ai stream`, `gofly ai doctor` |
| 🧩 Extension ecosystem | Local/remote templates, cached plugins, feature previews, built-in examples, upgrade guidance, shell completions, environment checks, support bundles | `gofly template`, `gofly plugin`, `gofly feature`, `gofly example`, `gofly upgrade`, `gofly completion`, `gofly env`, `gofly doctor`, `gofly bug` |
| ✅ Governance evidence | Generated-output determinism, goctl replay, generated layout checks, REST profile checks, RPC mux evidence, CLI JSON contracts, control-plane contracts | `make generated-service-layout-check`, `make goctl-real-project-replay-check`, `make contract-docs-check`, `docs/reference/cli-json-contracts.md`, `docs/reference/control-plane-contracts.md` |

## 🏗️ Architecture

```text
┌──────────────────────────┐   contracts / intent   ┌──────────────────────────┐
│ developers + AI agents   │───────────────────────▶│ gofly CLI + codegen      │
│ .api / .proto / SQL      │                        │ plan / scaffold / verify │
│ flags / manifests        │◀───────────────────────│ diff / breaking / doctor │
└──────────────────────────┘   plans + diagnostics  └────────────┬─────────────┘
                                                                 │ generates
                                                                 ▼
┌────────────────────────────────────────────────────────────────────────────────┐
│ generated service                                                              │
│                                                                                │
│  ┌───────────────────────┐   ┌───────────────────────┐   ┌──────────────────┐  │
│  │ inbound data plane    │──▶│ application layer     │──▶│ outbound clients │  │
│  │ REST / RPC / gRPC     │   │ handlers / logic      │   │ REST / RPC / MQ  │  │
│  │ gateway / WebSocket   │   │ app lifecycle / DI    │   │ cache / KV / DB  │  │
│  └───────────┬───────────┘   └───────────┬───────────┘   └────────┬─────────┘  │
│              │                           │                        │            │
│              └───────────────────────────┼────────────────────────┘            │
│                                          │ governed by                         │
│  ┌───────────────────────────────────────▼───────────────────────────────────┐ │
│  │ shared runtime services                                                 │ │ │
│  │ config / discovery / balancing / retry / limit / breaker / auth         │ │ │
│  │ logging / metrics / tracing / scheduling / saga / outbox / security     │ │ │
│  └───────────────────────────────────────┬───────────────────────────────────┘ │
└──────────────────────────────────────────┼─────────────────────────────────────┘
                                           │ runtime I/O + state
                              ┌────────────┴────────────┐
                              │                         │
                              ▼                         ▼
                 ┌──────────────────────────┐   ┌──────────────────────────┐
                 │ external infrastructure  │   │ runtime control-plane    │
                 │ discovery / config       │   │ admin / health / metrics │
                 │ DB / cache / MQ / OTel   │   │ policy / diagnostics     │
                 └──────────────────────────┘   └────────────┬─────────────┘
                                                            │ inspect + act
                                          ┌─────────────────┴────────────────┐
                                          ▼                                  ▼
                              ┌──────────────────────────┐       ┌─────────────────┐
                              │ governance + delivery    │       │ operators +     │
                              │ tests / replay / compat  │       │ AI agents       │
                              │ K8s / Helm / release     │       │                 │
                              └──────────────────────────┘       └─────────────────┘
```

## ⚡ 5-minute start

```sh
go install github.com/imajinyun/gofly/cmd/gofly@latest

gofly quickstart hello --module github.com/me/hello --dir hello
cd hello && go run .
```

Use `github.com/imajinyun/gofly` for both CLI installation and library imports.

This matches the CLI contract: `quickstart <name> --module <module> [--dir <dir>] [--style minimal|basic|production]`.

To scaffold an application-owned mux OTel log sink:

```sh
gofly new service orders --module example.com/orders \
  --feature mux-otel-sink=myorg/telemetry
```

The feature generates a local `internal/observability/muxotelsink` provider,
registers it through a blank import, and delegates profile validation to that
provider. The sink ID is not treated as a module URL: this feature performs no
network download and executes no external plugin.

Generated sink profiles use strict JSON with an application-owned schema. The
starter provider validates `endpoint`, `batchSize`, and `timeout`, while mux
diagnosis export uses a bounded queue with timeout, panic isolation, drop
counters, and queue-depth metrics. Registered sinks and their schemas are
visible through the RPC runtime/control-plane snapshot without exposing active
profile values.

Generated services can also configure a versioned `sinks` list. Configuration
reload builds and validates a complete replacement generation before an atomic
swap, drains the previous exporters after the swap, and preserves the active
generation when validation or construction fails. Each sink has an independent
queue, timeout, circuit breaker, priority, delivery SLO snapshot, and
Prometheus alert path, so one blocked sink cannot stall diagnosis requests or
other sinks.

## 🟡 Golden path: production service in 10 minutes

Use `new service` when you want the full production baseline: REST, RPC, OpenAPI, governance, admin control-plane, in-memory discovery, config tests, and generated smoke tests.

```sh
gofly new service orders --style production --module example.com/orders
cd orders
go test ./...
go run ./cmd/orders
```

In another terminal:

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

For runnable references, use the checked-in example directories and the CLI help output.

---

## 🧰 Common commands

| Command | Purpose |
| --- | --- |
| `gofly quickstart <name> --module <m>` | Scaffold and generate a service in one step |
| `gofly new service <name> --module <m>` | Create the golden-path production service |
| `gofly new api\|rpc <name> --module <m>` | Create a REST or RPC project |
| `gofly api gen --file <s.api> --dir <d>` | Generate REST code from a `.api` IDL |
| `gofly rpc gen --file <s.proto> --out <d>` | Generate RPC code from a `.proto` file |
| `gofly gen model --ddl <schema.sql> --dir <d>` | Generate data models from DDL |
| `gofly api diff\|breaking` | Compare API contracts and detect breaking changes |
| `gofly rpc check\|breaking` | Validate RPC contracts and detect breaking changes |
| `gofly release check --strict` | Run release readiness checks |
| `gofly env\|doctor\|version\|completion` | Diagnostics and developer tooling |

Run `gofly help` for the full command list.

---

## 📚 Documentation

Start at [docs/index.md](docs/index.md).

- Tutorial: [docs/tutorials/zero-to-production.md](docs/tutorials/zero-to-production.md)
- Examples: [examples/README.md](examples/README.md)
- Adoption model: [docs/explanation/adoption-model.md](docs/explanation/adoption-model.md)
- Benchmark claims: [docs/reference/benchmark-matrix.md](docs/reference/benchmark-matrix.md)
- go-zero migration: [docs/reference/from-go-zero-migration.md](docs/reference/from-go-zero-migration.md)
- CLI JSON: [docs/reference/cli-json-contracts.md](docs/reference/cli-json-contracts.md)
- Control-plane: [docs/reference/control-plane-contracts.md](docs/reference/control-plane-contracts.md)
- Contributing: [CONTRIBUTING.md](CONTRIBUTING.md)
- Roadmap: [ROADMAP.md](ROADMAP.md)
- Security: [SECURITY.md](SECURITY.md)

---

## 🆚 How gofly compares

| Compared with | gofly position |
| --- | --- |
| **Gin** | Gin is a focused HTTP router/framework. gofly includes REST serving, but its main value is the surrounding microservice system: code generation, RPC, discovery, governance, observability, and control-plane diagnostics. |
| **go-zero** | go-zero is the closest inspiration for IDL-first service generation. gofly keeps the codegen ergonomics, then adds stronger runtime governance, contract diffing, generated control-plane snapshots, and AI-agent-friendly workflows. |
| **Kratos** | Kratos provides a mature cloud-native application framework. gofly is more opinionated around generated services, governance gates, compatibility checks, and exposing machine-readable runtime state for operators and agents. |
| **Kitex** | Kitex is a high-performance RPC framework. gofly can generate and run RPC services, but it optimizes for end-to-end microservice delivery—codegen, runtime policy, discovery, admin endpoints, and contract safety—rather than pure RPC throughput alone. |

## 🚫 What gofly is not trying to replace

- **Not an MVC full-stack replacement for Beego.** gofly focuses on microservice codegen, runtime governance, and control-plane surfaces, not a batteries-included MVC web application stack.
- **Not a short-term pure-RPC performance fight with Kitex.** gofly values RPC compatibility and service delivery workflows, while specialized RPC frameworks remain the right benchmark for maximum transport performance.
- **Not a replacement for simple stdlib services.** If `net/http` plus a few handlers is enough, keep it simple; gofly is for services that need generated structure, contracts, governance, observability, and operational metadata.

---

## 🗂️ Layout

```text
cmd/gofly/        # CLI commands and code generators
app/              # application lifecycle runner
rest/             # REST server, middleware, OpenAPI, health checks
rpc/              # RPC server/client, discovery, balancing, streaming
gateway/          # API gateway runtime
cache/            # local and tiered cache helpers
ops/admin/        # shared admin/control-plane HTTP primitives
core/             # reusable runtime building blocks
  observability/  # logs, metrics, tracing, profiling
  governance/     # runtime rules and diagnostics
  config/         # local and remote configuration sources
  discovery/      # service discovery adapters
  mq/             # Kafka, RabbitMQ, Redis Stream abstraction
  kv/             # key-value abstraction and Redis backend
```

Rule of thumb: top-level packages are user-facing building blocks; `core/` contains reusable lower-level capabilities.

---

## 🧪 Development

```sh
make build          # build the CLI
make test           # run tests
make lint           # run golangci-lint
make cover-check    # enforce coverage threshold and ratchet
make bench-smoke    # run one benchmark iteration for PR smoke checks
make governance     # run repository governance checks
gofly release check --strict
```

For the full no-cache governance workflow:

```sh
make governance-10-rounds
```

---

## 📄 License

gofly is released under the [MIT License](./LICENSE).

See [CONTRIBUTING.md](CONTRIBUTING.md) for local setup and pull-request
checks, [ROADMAP.md](ROADMAP.md) for the public milestone view, and
[SECURITY.md](SECURITY.md) to report vulnerabilities.

Third-party framework names such as go-zero and Kitex, when mentioned in docs, tests, or generated compatibility adapters, are used only for ecosystem compatibility and migration context. gofly does not include or depend on their source code and is not endorsed by or affiliated with those projects.

<!-- claim-provenance: http-dx-openapi-envelope -->
<!-- claim-provenance: generated-scaffold-upgrade -->
<!-- claim-provenance: rpc-boundary-tier1 -->
<!-- claim-provenance: production-reference-proof -->
<!-- claim-provenance: release-trust-evidence -->
<!-- claim-provenance: performance-credibility -->
