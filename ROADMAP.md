# Roadmap

## v0.2 Production proof

- `production-orders` example with REST, RPC, discovery, MQ, outbox, saga, resilience, and observability.
- Microshop multi-service example with per-service control-plane snapshots.
- AI-governed service example for runtime drift checks.
- Release artifact signals: checksums, SBOM, Docker images, and Homebrew tap configuration.
- Community onboarding through contribution, issue, and PR templates.

## v0.5 Ecosystem preview

- Plugin registry prototype with protocol, checksum, source, capabilities, and permission metadata.
- Public benchmark dashboard and Benchmark trend evidence for REST, RPC, gateway, and governance hot paths.
- Migration guides from Gin, go-zero, Kratos, and Kitex.
- External-service Compose profiles for Consul/etcd, Kafka/RabbitMQ, and SQL outbox storage.

## v1.0 Compatibility

- Stable CLI flags and JSON output for automation and AI agents.
- Stable control-plane schema migration policy.
- Generated project compatibility policy backed by temporary-module smoke tests.
- Public deprecation and rollback guidance for stable and Tier 1 surfaces.

## Validation gates

- `make docs-check` validates documentation links, migration docs, community signals, P1 growth assets, contract docs, and snippet compilation.
- `make examples-smoke` validates runnable examples and machine-readable example contracts.
- `make governance-10-rounds` runs the no-cache repository governance workflow before release-impacting changes.

## Next

- Expand generated service examples into standalone modules when the repository moves examples out of root-module builds.
- Publish benchmark trend reports with stable evidence files after the benchmark matrix converges.
