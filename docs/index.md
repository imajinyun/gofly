# gofly Documentation

gofly is a Go microservice framework and code-generation toolchain for teams that need REST, RPC, governance, service discovery, observability, and machine-readable runtime state in one adoption path.

Start with the golden path, then use the module guides when you wire production capabilities into an existing service.

## Getting started

- [Quickstart](getting-started/quickstart.md) — run the golden-path production service.
- [Installation](getting-started/installation.md) — install `gofly`, verify toolchain requirements, and choose local or CI usage.
- [First service](getting-started/first-service.md) — understand the generated project layout and commands.
- [Stable releases](releases/stable.md) — install and verify binary, Docker, checksum, and SBOM artifacts.

## Concepts

- [Architecture](concepts/architecture.md) — package boundaries and runtime flow.
- [Runtime](concepts/runtime.md) — lifecycle, ports, health, and generated service defaults.
- [Governance](concepts/governance.md) — retry, breaker, rate limit, timeout, canary, and policy matching.
- [Control plane](concepts/control-plane.md) — admin diagnostics and `/admin/control-plane` metadata.
- [AI manifest](concepts/ai-manifest.md) — machine-readable CLI capabilities for agents and tooling.

## Guides

- [REST](guides/rest.md)
- [RPC](guides/rpc.md)
- [Gateway](guides/gateway.md)
- [Config](guides/config.md)
- [Discovery](guides/discovery.md)
- [MQ](guides/mq.md)
- [Cache](guides/cache.md)
- [OpenAPI](guides/openapi.md)
- [Model generation](guides/model.md)
- [Extensions](guides/extensions.md)
- [Deployment](guides/deployment.md)

## Comparisons and migrations

- [Gin migration](comparisons/gin.md)
- [go-zero migration](comparisons/go-zero.md)
- [Kratos migration](comparisons/kratos.md)
- [Kitex migration](comparisons/kitex.md)

## Case studies

- [Build a governed orders service](case-studies/build-orders-service.md)
- [Detect AI control-plane drift](case-studies/ai-control-plane-drift.md)
- [Move a Gin-style service into gofly](case-studies/migrate-from-gin.md)

## Operations

- [Observability](operations/observability.md)
- [Security](operations/security.md)
- [Production checklist](operations/production-checklist.md)
- [Troubleshooting](operations/troubleshooting.md)

## Runnable assets

- [Examples catalog](../examples/README.md)
- [Production orders example](../examples/production-orders/README.md)
- [Benchmark suite](../benchmarks/README.md)
