# gofly Documentation

gofly is a Go microservice framework and code-generation toolchain for teams that need REST, RPC, governance, service discovery, observability, and machine-readable runtime state in one adoption path.

Start with the golden path, then use the module guides when you wire production capabilities into an existing service.

## Choose your path

| Goal | Path | Done when |
| --- | --- | --- |
| Create a production-shaped service from scratch | [From zero to production](tutorials/zero-to-production.md) | generated tests pass, `/admin/control-plane` is captured, examples and docs checks pass |
| Run a narrow capability before committing to a service shape | [Use standalone examples](how-to/standalone-examples.md) | the copied example builds outside this repository with a released gofly dependency |
| Understand what is safe to automate or depend on | [Stable API surface](reference/api-surface.md) | consumers use documented stable CLI JSON, OpenAPI/RPC, and control-plane fields |
| Decide whether gofly fits an existing platform | [Adopter decision guide](explanation/adopter-decision-guide.md) and [Adoption model](explanation/adoption-model.md) | the team can explain why governance, examples, benchmarks, and control-plane state matter |

## Getting started

- [From zero to production](tutorials/zero-to-production.md) — build a production-shaped service with contracts, governance, control-plane state, examples, and release gates.
- [Quickstart](getting-started/quickstart.md) — run the golden-path production service.
- [Installation](getting-started/installation.md) — install `gofly`, verify toolchain requirements, and choose local or CI usage.
- [First service](getting-started/first-service.md) — understand the generated project layout and commands.
- [Stable releases](releases/stable.md) — install and verify binary, Docker, checksum, SBOM, provenance, and release evidence index artifacts.

## Documentation model

- Tutorial: [From zero to production](tutorials/zero-to-production.md)
- How-to: [Use standalone examples](how-to/standalone-examples.md)
- Reference: [Stable API surface](reference/api-surface.md), [Framework gap matrix](reference/framework-gap-matrix.md), [Compatibility policy](reference/compatibility.md), [CLI JSON contracts](reference/cli-json-contracts.md), [Control-plane contracts](reference/control-plane-contracts.md), [Benchmark matrix](reference/benchmark-matrix.md), [Coverage trend evidence](reference/coverage-trend.md), [CI required-check evidence](reference/ci-required-check-evidence.md), [Runtime SLO evidence](reference/runtime-slo.md), [Generated upgrade dry-run](reference/generated-upgrade-dry-run.md), [API example consistency](reference/api-example-consistency.md)
- Growth: [P1 growth roadmap](reference/p1-growth-roadmap.md)
- Explanation: [Adopter decision guide](explanation/adopter-decision-guide.md), [Adoption model](explanation/adoption-model.md)

## Definition of done

A documentation change is complete when it keeps the four-layer model navigable, links to the stable API and benchmark evidence where relevant, and passes:

```sh
make docs-check
make examples-copyable-check
make bench-evidence-check
```

This makes the documentation path executable instead of a loose set of pages.

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
- [P1 growth roadmap](reference/p1-growth-roadmap.md)

## Comparisons and migrations

- [Gin migration](comparisons/gin.md)
- [go-zero migration](comparisons/go-zero.md)
- [Kratos migration](comparisons/kratos.md)
- [Kitex migration](comparisons/kitex.md)

Each migration guide includes mapping, validation gates, and rollback criteria so teams can move one route or service boundary at a time.

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
- [Benchmark suite](../bench/README.md)
