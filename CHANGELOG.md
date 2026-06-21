# Changelog

This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
and [Conventional Commits](https://www.conventionalcommits.org/). The file is
auto-generated on release by [GoReleaser](https://goreleaser.com); edit only the
"Unreleased" section by hand.

## Unreleased

### Added
- **CLI** — `gofly version` now reports commit hash and build time when
  built with `-ldflags` (see [Makefile](Makefile)).
- **Release** — Multi-stage [Dockerfile](Dockerfile), `.dockerignore`, and
  `.goreleaser.yml` for reproducible, cross-platform releases.
- **Contracts** — `gofly api breaking` and `gofly rpc breaking` detect breaking
  IDL changes (classified by severity/category) and exit non-zero on a break,
  enabling a `contract-check` CI gate.
- **REST** — `Server.AddOpenAPIRoutes` serves the OpenAPI 3.0 contract at
  `/openapi.json` and Swagger UI at `/docs`.
- **Docs** — Root [README](README.md), a runnable [examples/restserver](examples/restserver)
  service, and godoc Example tests for the `rest` package.
- **Examples** — [examples/resilience](examples/resilience) demonstrates composing
  the rate limiter, circuit breaker, and retry policy around an unreliable
  downstream call.
- **Benchmarks** — Hot-path benchmarks for `rest` routing, `core/limit`, and
  `core/breaker`.
- **Benchmarks** — Added a reproducible `benchmarks/` suite for HTTP framework
  comparisons, gofly REST feature toggles, gofly RPC, and gRPC-Go.
- **Fuzzing** — Fuzz targets for the `.api`/`.proto` parsers (`FuzzParseAPI`,
  `FuzzParseProto`) and REST request binding (`FuzzBindJSON`, `FuzzBindQuery`).
- **Driver tests** — Lightweight unit coverage for Consul/etcd/Nacos config
  sources, Consul/etcd discovery adapters, and Kafka/RabbitMQ/Redis Stream MQ
  adapters without requiring external services.
- **Integration tests** — Docker-backed testcontainers coverage for Redis
  Stream MQ, RabbitMQ MQ, etcd config source, and etcd discovery under the
  `integration` build tag.
- **Governance scripts** — Added reusable gates for coverage threshold checks,
  `go mod tidy` drift, and public Go API compatibility via `apidiff`.
- **Examples** — Added `examples/config-discovery` and `examples/mq-worker`
  so configuration/discovery and MQ worker patterns have runnable, dependency-free
  entry points.
- **Observability assets** — Added a Grafana dashboard and OTel Collector sample
  for the observability example.
- **Observability stack** — Added local Prometheus, Grafana provisioning, Docker
  Compose, and README instructions for `examples/observability`.
- **Production example** — Added `examples/production-orders`, combining REST/RPC,
  config/discovery, MQ, outbox, saga, limiter, retry, breaker, and observability.
- **Docs** — Added a productized documentation tree under `docs/` covering
  getting started, concepts, module guides, framework migrations, and operations.

### Changed
- **Makefile** — `build` / `install` now embed version metadata via
  `-ldflags`. New targets: `bench`, `cover-html`, `docker`, `release-snapshot`,
  `version`.
- **Makefile** — Added `governance`, `api-compat`, and script-backed `tidy` /
  `cover-check` targets; the default coverage threshold is now 60%.
- **CI** — Added a `contract-check` job (between `security` and `release`) that
  runs IDL validation, diff, and breaking-change detection.
- **CI** — Added `bench-fuzz` and `integration` jobs so benchmarks, fuzz smoke
  tests, examples, and future `integration` build-tagged tests stay wired into
  the release gate.
- **CI** — The `integration` job now has an explicit 20-minute timeout and runs
  Docker-backed integration tests through `go test -tags=integration`.
- **CI** — Added a `governance` job for `go.mod`/`go.sum` cleanliness and public
  Go API compatibility; release now waits for this job.
- **CI** — Benchmark smoke now uploads both raw benchmark output and a Markdown
  trend summary for PR/release performance review.
- **Docs** — Added a topic-to-example documentation index and replaced stale
  config/MQ snippets with APIs that exist in the current implementation.
- **Docs** — Linked the production orders composition example and expanded the
  observability guide with local stack instructions.

### Fixed
_(nothing yet)_

### Security
_(nothing yet)_

---

_Template for new entries — copy, paste, and trim._
