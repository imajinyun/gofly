# gofly

`gofly` is a goctl/flycli-style toolkit and runtime for building Go microservices.
It pairs a code-generation CLI (`gofly`) with a batteries-included set of runtime
packages — REST and RPC servers, observability, resilience, storage, and service
governance — so you can go from an IDL file to a production-ready service quickly.

- **Module:** `github.com/gofly/gofly`
- **Go:** 1.26+

## Quickstart

Install the CLI and scaffold a runnable REST service from a single command:

```sh
go install github.com/gofly/gofly/cmd/gofly@latest

# scaffold + generate handlers/routes from a goctl-style .api spec
gofly quickstart hello --module github.com/me/hello --dir hello
cd hello && go run .
```

Prefer to see working code first? Run the bundled example:

```sh
go run ./examples/restserver
# then:
curl localhost:8080/healthz
curl localhost:8080/users/42
curl -XPOST localhost:8080/users -d '{"name":"ada"}'
open  http://localhost:8080/docs        # Swagger UI
```

### Minimal REST server

```go
package main

import (
	"net/http"

	"github.com/gofly/gofly/rest"
)

func main() {
	srv := rest.MustNewServer(rest.Config{
		Name: "hello",
		Port: 8080,
		Middlewares: rest.MiddlewaresConfig{
			Recover: true, Log: true, Metrics: true, Health: true, RequestID: true,
		},
	})
	srv.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/users/{id}",
		Handler: func(c *rest.Context) { c.JSON(http.StatusOK, map[string]string{"id": c.PathValue("id")}) },
	})
	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "hello", Version: "1.0.0"})
	_ = srv.Start()
}
```

See [examples/restserver/main.go](examples/restserver/main.go) for the full
example with graceful shutdown.

## CLI commands

Run `gofly help` for the complete usage. The most common commands:

| Command | Purpose |
| --- | --- |
| `gofly new api\|rpc <name> --module <m>` | Scaffold a new REST or RPC service |
| `gofly quickstart <name> --module <m>` | Scaffold + generate from a `.api` spec in one step |
| `gofly gen handler\|middleware\|model\|gateway` | Generate individual components |
| `gofly api gen --file <s.api> --dir <d>` | Generate REST server code from a `.api` IDL |
| `gofly api check\|format\|doc\|swagger\|route` | Validate, format, and document a `.api` IDL |
| `gofly api diff --base <a> --target <b>` | Diff two `.api` IDLs |
| `gofly api breaking --base <a> --target <b>` | Detect **breaking** API changes (non-zero exit on break) |
| `gofly api client --file <s.api> --dir <d> --language ts\|js\|dart\|java\|kotlin` | Generate typed API clients |
| `gofly rpc gen --file <s.proto> --out <d>` | Generate RPC server code from a `.proto` |
| `gofly rpc check --file <s.proto>` | Validate a `.proto` IDL |
| `gofly rpc breaking --base <a> --target <b>` | Detect **breaking** RPC changes (non-zero exit on break) |
| `gofly model gen --ddl <schema.sql> --dir <d>` | Generate models from DDL |
| `gofly release check [--strict] [--json]` | Run release readiness gates before publishing |
| `gofly docker\|kube` | Generate Dockerfile / Kubernetes manifests |
| `gofly migrate create <name>` | Create a migration file |
| `gofly config\|feature\|plugin\|template` | Project config, features, plugins, templates |
| `gofly env\|bug\|upgrade\|version\|completion` | Diagnostics and tooling |

## Engineering capabilities

The framework ships with an end-to-end engineering toolchain, organized in tiers:

- **Tier A — Delivery chain.** Reproducible builds via [Makefile](Makefile) (version
  metadata embedded with `-ldflags`), multi-stage [Dockerfile](Dockerfile), and
  [`.goreleaser.yml`](.goreleaser.yml) for cross-platform releases.
- **Tier B — Quality gates.** [`.golangci.yml`](.golangci.yml) linting,
  `go vet`, race-enabled tests, and a [CI pipeline](.github/workflows/ci.yml)
  with [Dependabot](.github/dependabot.yml).
- **Tier C — Operational readiness.** Health probes (`/healthz`, `/readyz`,
  `/startupz`), Prometheus metrics (`/metrics`), structured logging and tracing
  via [core/observability](core/observability), and a production
  [Kubernetes manifest](k8s/deployment.yaml).
- **Tier D — Contracts & API engineering.** Breaking-change detection
  (`gofly api breaking` / `gofly rpc breaking`), runtime OpenAPI 3.0 contract
  (`/openapi.json`) plus Swagger UI (`/docs`), enforced by a `contract-check`
  CI gate.
- **Tier E — Docs & usability.** This README, runnable [examples](examples),
  godoc Example tests, and a maintained [CHANGELOG](CHANGELOG.md).

## Documentation

Use the topic guides when wiring a concrete service capability:

| Topic | Guide | Runnable example |
| --- | --- | --- |
| REST server | [docs/rest.md](docs/rest.md) | [examples/restserver](examples/restserver) |
| RPC server | [docs/rpc.md](docs/rpc.md) | [examples/rpcserver](examples/rpcserver) |
| Configuration and discovery | [docs/config.md](docs/config.md), [docs/discovery.md](docs/discovery.md) | [examples/config-discovery](examples/config-discovery) |
| Message queues | [docs/mq.md](docs/mq.md) | [examples/mq-worker](examples/mq-worker) |
| Observability | [docs/observability.md](docs/observability.md) | [examples/observability](examples/observability) |
| Reliability patterns | [docs/outbox.md](docs/outbox.md), [docs/saga.md](docs/saga.md) | [examples/outbox-mq](examples/outbox-mq), [examples/saga](examples/saga) |
| Production composition | REST/RPC, config, discovery, MQ, outbox, saga, resilience, observability | [examples/production-orders](examples/production-orders) |
| Gateway | [docs/gateway.md](docs/gateway.md) | [examples/gateway-discovery-rpc](examples/gateway-discovery-rpc) |

## Layout

Top-level packages are the framework's primary, user-facing surface — the
servers, runners, and gateways you import directly to build a service.
`core/` holds the runtime building blocks they are composed from: lower-level,
reusable constructs that callers rarely touch in isolation. The rule of thumb:
**if it's a headline API you wire up in `main`, it lives at the top level; if
it's an internal capability the headline APIs depend on, it lives under
`core/`.** Sub-packages of a domain are nested under their owner (e.g. the
Redis KV implementation lives at `core/kv/redis`, RPC endpoint middleware at
`rpc/endpoint`) so dependency direction always flows top-level → `core/` and
parent → child, never the reverse.

```
cmd/gofly/        # the gofly CLI (commands + code generators)
rest/             # REST server: routing, middleware, OpenAPI, health, SSE, websocket
rpc/              # RPC server/client: balancing, discovery, streaming, governance
  endpoint/       #   endpoint middleware chains (hedging, etc.)
  grpc/           #   gRPC server/client adapters
app/              # lifecycle runner: start/stop multiple servers with hooks
gateway/          # API gateway
cache/            # in-process & tiered cache
core/             # runtime building blocks:
  observability/  #   logging, metrics, tracing
  breaker/ limit/ retry/ saga/ outbox/  # resilience & reliability
  discovery/ config/ governance/        # service registry, config, rules
  auth/ security/ metadata/             # auth, security, request metadata
  kv/             #   key-value abstraction (Memory/Redis/Etcd/Consul stores)
    redis/        #     RESP protocol client (a kv backend)
  storage/        #   sql helpers (sharding, cluster routing)
  mq/             #   message queue abstraction (Kafka/RabbitMQ/Redis Stream)
examples/         # runnable example services
k8s/              # deployment manifests
```

## Development

```sh
make build      # build the CLI with embedded version metadata
make test       # go test -race ./...
make lint       # golangci-lint
make bench      # run benchmarks
make governance # tidy + coverage gate + public Go API compatibility
make examples-check # build and vet runnable examples

# run the bundled examples; see examples/README.md for the full matrix
go run ./examples/restserver
go run ./examples/config-discovery
go run ./examples/mq-worker
go run ./examples/observability
go run ./examples/production-orders
go run ./examples/resilience
```

Performance-sensitive packages ship benchmarks (`rest`, `rpc`, `gateway`,
`core/limit`, `core/breaker`, `core/governance`, `cmd/gofly/internal/generator`)
and parsers/binders ship fuzz targets (`FuzzParseAPI`,
`FuzzParseProto`, `FuzzBindJSON`, `FuzzBindQuery`), both exercised in CI.
External adapters include lightweight unit coverage that does not require
running Consul, etcd, Nacos, Kafka, RabbitMQ, or Redis. Integration tests should
use the `integration` build tag and run through the dedicated CI job:

```sh
go test -tags=integration -count=1 ./...
```

The current Docker-backed integration suite covers Redis Stream MQ, RabbitMQ MQ,
etcd config source, and etcd discovery.

Governance gates keep the public surface and repository state stable:

```sh
COVERAGE_THRESHOLD=60 make cover-check
API_BASE_REF=origin/main make api-compat
make tidy
```

`api-compat` uses `apidiff` to fail on incompatible public Go API changes.
`tidy` fails if `go mod tidy` changes `go.mod` or `go.sum`.

## License

gofly is released under the [MIT License](./LICENSE).

Third-party framework names such as go-zero and Kitex, when mentioned in docs,
tests, or generated compatibility adapters, are used only for ecosystem
compatibility and migration context. gofly does not include or depend on their
source code and is not endorsed by or affiliated with those projects.
