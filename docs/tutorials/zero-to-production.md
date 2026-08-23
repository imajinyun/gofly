# Tutorial: from zero to production

This tutorial takes a new machine from install to a production-style gofly
service. It uses the Tier 0 golden path, not a preview template.

## 1. Install

```sh
go install github.com/imajinyun/gofly/cmd/gofly@latest
gofly version
```

## 2. Scaffold

```sh
gofly new service orders --style production --module example.com/orders
cd orders
go test ./...
go run ./cmd/orders
```

In another terminal:

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

The generated layout contract is
[generated-service-layout.md](../reference/generated-service-layout.md).
Walk through the files in [first-service.md](../getting-started/first-service.md).

## 3. Copy a known-good example first

If you want a compact in-repo reference instead of a fresh scaffold, copy
[production-orders](../../examples/production/production-orders) using
[standalone examples](../how-to/standalone-examples.md).

```sh
make examples-copyable-check
```

## 4. Production defaults

Before exposing the service, run the generated production check and the
repository [production checklist](../operations/production-checklist.md):

```sh
make production-check
```

If the command is not on `PATH`, use the generated `bin/production-check.sh`
inside the service module.

## 5. Release evidence

Do not claim performance or goctl replacement from this tutorial. Use:

```sh
make docs-check
make bench-evidence-check
gofly release check --strict
```

RPC latency remains report-only until the benchmark budget promotes it.
Goctl compatibility is a migration path, not a byte-for-byte clone.
