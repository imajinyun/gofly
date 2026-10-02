# Integration Test Modules

Each child directory is an independent Go module for a cross-project or
external-runtime integration boundary. These tests are not discovered by the
root module and must be run through their owning governance script.

`zrpc-interop` verifies bidirectional gofly/go-zero gRPC compatibility and uses
the `integration` build tag. Run it through:

```sh
sh bin/scripts/check-zrpc-proto-compatibility.sh
```
