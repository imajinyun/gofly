# Explanation: The gofly Adoption Model

gofly is designed for teams that outgrow a router-only service but do not want to glue together contracts, RPC, discovery, governance, observability, and release checks by hand.

## Documentation layers

gofly documentation follows four layers:

| Layer | Purpose | Start here |
| --- | --- | --- |
| Tutorial | Take a new user from zero to a working production-shaped service. | [From Zero to Production](../tutorials/zero-to-production.md) |
| How-to | Solve one operational or migration task. | [Use Standalone Examples](../how-to/standalone-examples.md) |
| Reference | Define exact contracts, commands, and compatibility surfaces. | [Stable API Surface Reference](../reference/api-surface.md) |
| Explanation | Explain why gofly makes a design tradeoff. | This page |

## Why control-plane state matters

Most Go frameworks expose runtime behavior through logs and metrics. gofly also exposes structured runtime state so humans and AI agents can answer:

- which routes and RPC methods are live;
- which governance rules are active;
- which discovery endpoints are selected;
- which contracts and descriptors changed between releases.

That is why the compatibility policy treats CLI JSON, OpenAPI, descriptors, and `/admin/control-plane` fields as stable product surfaces.

## Why examples are standalone modules

Standalone examples reduce copy cost. Users can copy `examples/production-orders`, drop the local replace, pin a gofly release, and run tests without carrying the entire repository. They also prevent optional example dependencies from contaminating the root module.

## Why benchmark data is a matrix

Router benchmarks alone do not describe a microservice framework. gofly publishes REST, RPC, OpenAPI, and governance scenarios so teams can decide whether the additional runtime guarantees are worth the overhead for their workload.
