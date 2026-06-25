# Tutorial: From Zero to Production

This tutorial is the productized path for a new gofly adopter. It takes a service from install to a production-shaped runtime with contracts, governance, examples, and release checks.

## 1. Install and verify

```sh
go install github.com/imajinyun/gofly/cmd/gofly@latest
gofly doctor --json
```

Use the [installation guide](../getting-started/installation.md) when a CI worker needs pinned Go or protoc tooling.

## 2. Generate a service

```sh
gofly new service orders --style production
cd orders
go test ./...
```

The generated service includes REST routes, health endpoints, OpenAPI, governance config, admin control-plane endpoints, Docker assets, and local CI targets.

## 3. Add contracts before logic grows

```sh
gofly api check --file api/orders.api
gofly api doc --file api/orders.api --output openapi.json
gofly api breaking --base api/orders.api --target api/orders.api
```

For RPC services, use:

```sh
gofly rpc check --file proto/orders.proto
gofly rpc doc --file proto/orders.proto --output rpc-openapi.json
gofly rpc breaking --base proto/orders.proto --target proto/orders.proto
```

## 4. Turn on runtime governance

Keep policy in `etc/governance.json` so operations can review it without reading handlers:

```json
{
  "name": "orders-create",
  "transport": "rest",
  "service": "orders",
  "method": "POST",
  "path": "/orders",
  "policy": {
    "timeout": 2000000000,
    "retry": {"attempts": 2, "backoff": 100000000},
    "breaker": {"enabled": true, "failureRatio": 0.5, "minRequests": 20},
    "rateLimit": {"rate": 100, "burst": 100}
  }
}
```

See [Governance](../concepts/governance.md) for policy semantics.

## 5. Expose control-plane state

Run the service and inspect runtime state:

```sh
curl -s localhost:9090/admin/control-plane
```

Production adopters should snapshot this endpoint during release verification and compare descriptors, governance rules, discovery endpoints, and OpenAPI checksums.

## 6. Validate examples and docs

```sh
make examples-copyable-check
make examples-smoke
make docs-check
```

Examples are standalone modules; copy one as the starting point for a production service when a generated service is too broad for a spike.

## 7. Run release gates

```sh
make ci-fast
make bench-evidence-check
make bench-smoke
make bench-matrix
```

Before tagging, also run `make bench-stat` and `make bench-trend` so release notes include reproducible performance data.

Use the [production checklist](../operations/production-checklist.md) as the final merge review checklist for service changes.

## Next paths

- Need an operations checklist? Read [Production checklist](../operations/production-checklist.md).
- Need stable surface guarantees? Read [Compatibility Policy](../reference/compatibility.md).
- Migrating from another framework? Start with [Gin](../comparisons/gin.md), [go-zero](../comparisons/go-zero.md), [Kratos](../comparisons/kratos.md), or [Kitex](../comparisons/kitex.md).
