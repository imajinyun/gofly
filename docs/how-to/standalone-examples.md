# How-to: use standalone examples

Each directory under `examples/` is a standalone Go module. Copy it out of the
repository, keep its `go.mod`, and point the `replace` directive at a released
gofly version when you are not hacking on this checkout.

The catalog is [examples/README.md](../../examples/README.md).

## Copy and build

```sh
make examples-copyable-check
make examples-smoke
```

`examples-copyable-check` copies each example to a temporary directory and
builds it there so in-repo relative paths cannot hide broken modules.

## Choose an example

| Goal | Example |
| --- | --- |
| First REST process | `examples/getting-started/restserver` |
| First RPC process | `examples/getting-started/rpcserver` |
| Production reference | `examples/production/production-orders` |
| Multi-service topology | `examples/production/microshop` |
| go-zero migration | `examples/migration/gozero-basic` |
| Control-plane drift | `examples/ai-first/ai-governed-service` |

Run the module from its directory:

```sh
go test ./...
go run .
```

## Rules

- Do not add example-only dependencies to the gofly root module.
- Do not treat example Docker Compose files as hosted production.
- Keep example README commands in sync with `make examples-smoke`.
