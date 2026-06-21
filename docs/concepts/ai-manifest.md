# AI manifest

The AI manifest describes gofly capabilities in a machine-readable form so agents and automation can discover safe commands, inputs, outputs, risks, and generated files.

## Inspect the manifest

```sh
gofly ai manifest --json
```

Useful checks:

```sh
gofly ai manifest --json | python3 -m json.tool
gofly ai manifest --json | grep 'new service'
```

## How agents use it

Agents should prefer manifest-declared commands instead of guessing CLI flags. For example, the manifest exposes `new service` as the canonical production scaffold command, including dry-run/plan support and output modes.

## Production use

- Use `--dry-run` before writing generated files in automation.
- Use JSON output where available for CI and agents.
- Treat high-risk commands as requiring review when they overwrite files or change release state.

## Related docs

- [Quickstart](../getting-started/quickstart.md)
- [Control plane](control-plane.md)
- [Production checklist](../operations/production-checklist.md)
