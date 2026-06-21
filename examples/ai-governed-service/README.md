# AI-Governed Service Example

This example exposes a gofly REST service with a governed route and admin control-plane snapshot.

```sh
go run ./examples/ai-governed-service
```

In another terminal:

```sh
curl -s localhost:8200/v1/state
curl -s -H 'Authorization: Bearer ai-token' localhost:8200/admin/control-plane
go run ./examples/ai-governed-service expected
```

The expected contract output is intentionally small so an AI agent can compare it with `/admin/control-plane` and flag drift in service name, governance rules, admin settings, or checksum.
