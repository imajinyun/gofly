# gofly Documentation

gofly is an AI-native Go microservice toolkit: contract-first codegen, runtime
governance, and a queryable control-plane. This index is the human entry point.
Machine-readable evidence lives under [reference/](reference/README.md).

## Choose your path

- New to gofly: [From zero to production](tutorials/zero-to-production.md)
- Copy a runnable module: [Use standalone examples](how-to/standalone-examples.md)
- Generate one production service: [First production service](getting-started/first-service.md)
- Migrating from go-zero: [go-zero migration](reference/from-go-zero-migration.md)
- Integrate REST: [REST binding and errors](guides/rest.md)
- Publish OpenAPI: [OpenAPI schemas](guides/openapi.md)
- Read stable contracts: [Stable API surface](reference/api-surface.md)
- Understand the bet: [Adoption model](explanation/adoption-model.md)

## Documentation model

Docs follow a four-layer split:

| Layer | Question | Start here |
| --- | --- | --- |
| Tutorial | How do I get a service running? | [zero-to-production](tutorials/zero-to-production.md) |
| How-to | How do I copy an example? | [standalone examples](how-to/standalone-examples.md) |
| Reference | What is stable? | [API surface](reference/api-surface.md), [CLI JSON](reference/cli-json-contracts.md), [control-plane](reference/control-plane-contracts.md) |
| Explanation | Why is gofly shaped this way? | [adoption model](explanation/adoption-model.md) |

Operations and release:

- [Troubleshooting](operations/troubleshooting.md)
- [Production checklist](operations/production-checklist.md)
- [Stable release](releases/stable.md)
- [Compatibility policy](reference/compatibility.md)
- [Benchmark matrix](reference/benchmark-matrix.md)
- [Public roadmap](../ROADMAP.md)

## Definition of done

A user-facing capability is done when:

1. A tutorial or how-to can reproduce it without reading generator internals.
2. The matching `make` gate in [ROADMAP.md](../ROADMAP.md) is green.
3. Generated or example output stays inside the target module.
4. Claims match [reference contracts](reference/README.md). RPC tier-1
   throughput claims stay withheld until `make bench-evidence-check` promotes
   them.

## Engineering evidence

Tracked contracts consumed by Make gates:

- [CLI JSON contracts](reference/cli-json-contracts.md)
- [Control-plane contracts](reference/control-plane-contracts.md)
- [Generated service layout](reference/generated-service-layout.md)
- [Generated upgrade dry run](reference/generated-upgrade-dry-run.md)
- [Model schema IR contract](reference/model-schema-ir-contract.md)
- [Goctl generator compatibility](reference/goctl-generator-compatibility.json)
- [API contract governance](reference/api-contract-governance.json)
- [DX support bundle](reference/dx-support-bundle.json)
- [REST middleware profiles](reference/rest-middleware-profiles.md)
- [API client generation](reference/api-client-generation.md)
- [Reference app topology](reference/reference-app-topology.json)
- [DB/cache productization](reference/db-cache-productization.json)

