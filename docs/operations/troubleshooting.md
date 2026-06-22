# Troubleshooting

## Service does not start

Run:

```sh
go test ./...
go run ./cmd/<service>
```

Check config paths, port conflicts, and invalid governance JSON.

## Health check fails

```sh
curl -v http://127.0.0.1:8080/healthz
curl -v http://127.0.0.1:8080/readyz
```

If readiness fails but liveness passes, inspect dependencies such as discovery, database, MQ, or downstream RPC.

## Control plane is missing metadata

```sh
curl http://127.0.0.1:9090/admin/control-plane
```

Verify the generated service uses the production scaffold and that admin configuration has not been disabled.

## Discovery cannot resolve an upstream

- confirm provider name and endpoints;
- confirm the instance registered with the same service name;
- use memory discovery locally before switching to Consul or etcd.

## Benchmark smoke is noisy

Run a focused benchmark first:

```sh
go test ./bench -run='^$' -bench='BenchmarkHTTPHello/gofly' -benchtime=1x -count=1 -benchmem
```

Use `make bench-stat` and `benchstat` for real comparisons; do not draw conclusions from one smoke run.
