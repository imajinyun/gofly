# Control plane

The control plane is the machine-readable admin surface for runtime state. Generated production services expose it at:

```sh
curl http://127.0.0.1:9090/admin/control-plane
```

## What it is for

- Confirm which generated capabilities are enabled.
- Inspect service metadata for operators and AI agents.
- Verify governance, discovery, and runtime wiring after startup.
- Provide a stable endpoint for smoke tests.

## Expected generated metadata

Generated production services include metadata similar to:

```json
{
  "metadata": {
    "generated.project": "orders",
    "generated.project.runtime": "service,rest,rpc,governance,discovery"
  }
}
```

## Security

The admin listener is intended for internal networks. In production:

- bind admin ports to private interfaces;
- require a token when exposed beyond localhost;
- do not put `/admin/*` behind a public internet route;
- log access to admin endpoints.

See [Security](../operations/security.md).
