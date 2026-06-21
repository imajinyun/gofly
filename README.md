# 🪽 gofly

**English** | [简体中文](README.CN.md)

`gofly` is an **AI-native Go microservice framework** for teams that want more than an HTTP router: **codegen + runtime governance + control-plane** in one Go-native toolkit.

It is designed for platform, backend, and AI-agent-assisted engineering teams who need to generate services quickly, run them with production defaults, and keep runtime behavior observable and governable after deployment.

- 📦 **Module:** `github.com/gofly/gofly`
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
                 ┌──────────────────────────────┐
                 │          AI agent             │
                 │ plan / generate / inspect /   │
                 │ contract diff / release check │
                 └───────────────┬──────────────┘
                                 │ machine-readable CLI + manifests
┌────────────────┐     ┌─────────▼─────────┐     ┌────────────────────┐
│  CLI / codegen │────▶│      runtime      │────▶│    control-plane    │
│ quickstart     │     │ REST / RPC / MQ   │     │ descriptors /       │
│ api/rpc/model  │     │ discovery / app   │     │ snapshots / admin   │
│ diff/breaking  │     │ observability     │     │ diagnostics         │
└────────────────┘     └─────────┬─────────┘     └────────────────────┘
                                 │
                       ┌─────────▼─────────┐
                       │    governance     │
                       │ retry / breaker / │
                       │ rate-limit /      │
                       │ policies / checks │
                       └───────────────────┘
```

## ⚡ 5-minute start

```sh
go install github.com/gofly/gofly/cmd/gofly@latest

gofly quickstart hello --module github.com/me/hello --dir hello
cd hello && go run .
```

This matches the CLI contract: `quickstart <name> --module <module> [--dir <dir>] [--style minimal|basic|production]`.

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

📖 Need runnable examples or full code snippets? See [Quickstart Examples](docs/doc/quickstart.md) and the [Examples Catalog](examples/README.md).

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

## 📚 Documentation

| Topic | Link |
| --- | --- |
| 📘 Documentation home | [docs/index.md](docs/index.md) |
| 🧭 From zero to production | [docs/tutorials/zero-to-production.md](docs/tutorials/zero-to-production.md) |
| 🚀 Golden-path quickstart | [docs/getting-started/quickstart.md](docs/getting-started/quickstart.md) |
| 🧩 API stability | [docs/reference/api-surface.md](docs/reference/api-surface.md), [docs/reference/compatibility.md](docs/reference/compatibility.md), [CLI JSON](docs/reference/cli-json-contracts.md), [control-plane](docs/reference/control-plane-contracts.md) |
| 📊 Benchmark matrix | [docs/reference/benchmark-matrix.md](docs/reference/benchmark-matrix.md), [bench/evidence.md](bench/evidence.md), [benchmarks/README.md](benchmarks/README.md) |
| 📈 P1 growth roadmap | [docs/reference/p1-growth-roadmap.md](docs/reference/p1-growth-roadmap.md) |
| 🧭 Concepts | [docs/concepts/architecture.md](docs/concepts/architecture.md) |
| 🧠 Adoption model | [docs/explanation/adoption-model.md](docs/explanation/adoption-model.md) |
| 🌐 REST / RPC / Gateway guides | [docs/guides/rest.md](docs/guides/rest.md), [docs/guides/rpc.md](docs/guides/rpc.md), [docs/guides/gateway.md](docs/guides/gateway.md) |
| ⚙️ Production operations | [docs/operations/production-checklist.md](docs/operations/production-checklist.md) |
| 📦 Stable releases | [docs/releases/stable.md](docs/releases/stable.md) |
| 🧪 Runnable examples | [examples/README.md](examples/README.md) |
| 📚 Case studies | [orders service](docs/case-studies/build-orders-service.md), [AI drift](docs/case-studies/ai-control-plane-drift.md), [Gin migration](docs/case-studies/migrate-from-gin.md) |
| 🔁 Migration guides | [docs/comparisons/gin.md](docs/comparisons/gin.md), [docs/comparisons/go-zero.md](docs/comparisons/go-zero.md), [docs/comparisons/kratos.md](docs/comparisons/kratos.md), [docs/comparisons/kitex.md](docs/comparisons/kitex.md) |
| 🧪 Local development | [Development](#-development) |

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
make bench-trend    # write bench/summary.md for release trend notes
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

Third-party framework names such as go-zero and Kitex, when mentioned in docs, tests, or generated compatibility adapters, are used only for ecosystem compatibility and migration context. gofly does not include or depend on their source code and is not endorsed by or affiliated with those projects.
