# 🪽 gofly

**English** | [简体中文](README.CN.md)

`gofly` is a Go microservice toolkit that combines a `goctl`-style code-generation CLI with production-ready runtime packages for REST, RPC, gateways, observability, resilience, and governance.

- 📦 **Module:** `github.com/gofly/gofly`
- 🧭 **Go:** 1.26+
- 🚀 **CLI:** `gofly`

---

## ✨ Why gofly?

- 🛠️ **Generate faster** — scaffold REST/RPC services, handlers, gateways, models, Dockerfiles, and Kubernetes manifests.
- 🌐 **Build services** — use ready-to-wire REST, RPC, gateway, cache, MQ, config, discovery, and app lifecycle packages.
- 📊 **Observe by default** — logs, metrics, tracing, health probes, profiling, and admin endpoints are first-class citizens.
- 🛡️ **Ship safely** — rate limiting, circuit breaking, retries, auth helpers, security middleware, and governance rules are built in.
- ✅ **Keep quality high** — lint, vet, race tests, coverage gates, API compatibility checks, security scans, and release checks.

---

## ⚡ Quickstart

```sh
go install github.com/gofly/gofly/cmd/gofly@latest

gofly quickstart hello --module github.com/me/hello --dir hello
cd hello && go run .
```

📖 Need runnable examples or full code snippets? See [Quickstart Examples](docs/doc/quickstart.md).

---

## 🧰 Common commands

| Command | Purpose |
| --- | --- |
| `gofly quickstart <name> --module <m>` | Scaffold and generate a service in one step |
| `gofly new api\|rpc <name> --module <m>` | Create a REST or RPC project |
| `gofly api gen --file <s.api> --dir <d>` | Generate REST code from a `.api` IDL |
| `gofly rpc gen --file <s.proto> --out <d>` | Generate RPC code from a `.proto` file |
| `gofly model gen --ddl <schema.sql> --dir <d>` | Generate data models from DDL |
| `gofly api diff\|breaking` | Compare API contracts and detect breaking changes |
| `gofly rpc check\|breaking` | Validate RPC contracts and detect breaking changes |
| `gofly release check --strict` | Run release readiness checks |
| `gofly env\|doctor\|version\|completion` | Diagnostics and developer tooling |

Run `gofly help` for the full command list.

---

## 📚 Documentation

| Topic | Link |
| --- | --- |
| 🚀 Examples and full snippets | [docs/doc/quickstart.md](docs/doc/quickstart.md) |
| 🌐 REST / RPC examples | [docs/doc/quickstart.md](docs/doc/quickstart.md) |
| 📊 Observability notes | [docs/doc/quickstart.md](docs/doc/quickstart.md) |
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
