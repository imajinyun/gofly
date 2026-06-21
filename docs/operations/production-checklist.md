# Production Checklist

Use this checklist before merging or releasing a gofly service.

## Build and tests

```sh
go test ./...
gofly release check --strict
```

For framework changes:

```sh
make ci-fast
make test-generated-matrix
make bench-smoke
```

## Runtime

- [ ] REST, RPC, and admin ports are explicit.
- [ ] `/healthz` and `/readyz` return expected status.
- [ ] `/admin/control-plane` is reachable only from trusted networks.
- [ ] generated smoke tests pass.

## Governance

- [ ] timeouts exist for slow paths.
- [ ] retry attempts are bounded.
- [ ] breakers protect unstable downstreams.
- [ ] rate and concurrency limits protect public or expensive endpoints.

## Config and discovery

- [ ] config files are versioned and reviewed.
- [ ] environment overrides are documented.
- [ ] discovery provider and endpoints are correct for the target environment.

## Observability and security

- [ ] logs include request id and trace id.
- [ ] metrics and traces are exported to trusted backends.
- [ ] admin token or private networking is configured.
- [ ] secrets are not present in source, logs, or snapshots.
