# go-zero / goctl alignment

gofly is a goctl-compatible migration path, not a drop-in replacement for
go-zero. SQL access is `SQLStore` / `NewCluster` rather than go-zero `sqlx`.
Caching uses the typed `cache` package and generated Redis cache-aside
repositories instead of go-zero `cache`.

## Rollback plan

If generated SQL or cache output diverges from a goctl project, keep the
existing go-zero datastore, pin `REFERENCE_APP_MODE=memory`, and rerun
`make goctl-model-parity-replay-check` before promoting the gofly model.

## Reference app

Copy the production orders example at `examples/production/production-orders`.
It proves REST, RPC, SQL outbox, and cache topology without claiming go-zero
sqlx parity.

## RPC alignment

go-zero zrpc provides one integrated native gRPC path: configuration, etcd
registration and resolution, default interceptors, balancing, and a runnable
goctl scaffold. gofly keeps two explicit RPC transports:

- the default production scaffold uses gofly HTTP-RPC with dynamic governance,
  descriptors, mux diagnostics, and control-plane visibility;
- `gofly new rpc <name> --profile gozero-compatible` generates a runnable
  native gRPC service with standard protobuf stubs, discovery lifecycle, health
  transitions, default observability, a typed client, generated P2C/EWMA
  balancing configuration, and a production check with rule restart/rollback
  evidence. The compatibility gate also runs real bidirectional calls between
  go-zero zRPC and gofly servers and clients, including server-streaming,
  client-streaming, and bidirectional streaming data-plane lifecycles.
  Generated production services also share one adaptive admission limiter across
  unary and streaming calls; direct library users opt in explicitly.

The streaming matrix proves standard gRPC transport compatibility. The native
gateway can expose server-streaming methods as SSE with ordered protobuf JSON
`message` events, filtered initial metadata headers, terminal trailer/error
events, request cancellation, and the existing route timeout. Client-streaming
and bidirectional HTTP transcoding remain HTTP 501 until a separate duplex
protocol contract is chosen. This does not reproduce every zRPC middleware
default or claim byte-for-byte zRPC/goctl parity.

`gofly rpc gen` defaults to native gRPC bindings. Use `--transport gofly` for
the HTTP-RPC descriptor/client/server output, or `--transport both` when an
explicit dual-transport migration is required. These surfaces are compatible
migration tools; they do not claim byte-for-byte zrpc implementation parity.
