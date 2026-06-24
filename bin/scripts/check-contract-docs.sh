#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path('.').resolve()

checks = {
    'docs/reference/cli-json-contracts.md': [
        '## Standard envelope',
        '## Stable command contracts',
        'gofly ai control-plane --json',
        'gofly api diff --format json',
        'gofly rpc descriptor --format json',
        'error.code',
    ],
    'docs/reference/control-plane-contracts.md': [
        'GET /admin/control-plane',
        '## Snapshot object',
        '## Diff object',
        '## Consumer action object',
        'gofly-control-plane.v1',
        'secretBoundary',
    ],
    'docs/reference/api-surface.md': [
        'cli-json-contracts.md',
        'control-plane-contracts.md',
        '## Stable adoption tiers',
        'Tier 0',
        'Tier 1',
        'Tier 2',
        'Tier 3',
        'gofly new service --style production',
    ],
    'docs/guides/rest.md': [
        '## Binding and validation',
        'ctx.BindRequest',
        'rest.BindRequest',
        'rest.Config.Validator',
        'rest.DefaultErrorResponses()',
        'rest.JSONErrorResponse',
        'ctx.Error(err)',
        '"code": "invalid_argument"',
        '"fields"',
    ],
    'docs/guides/openapi.md': [
        'rest.StructSchema',
        'rest.DefaultErrorResponses()',
        'rest.JSONErrorResponse',
        'required',
        'oneof',
    ],
    'docs/reference/compatibility.md': [
        'cli-json-contracts.md',
        'control-plane-contracts.md',
        '## Adoption tier policy',
        '## Deprecation and migration window',
        'Deprecated:',
        'one minor release line',
    ],
    'docs/releases/stable.md': [
        '## Release Compatibility Checklist',
        'Tier 0',
        'Tier 1',
        'API compatibility report',
        'coexistence window',
    ],
    'docs/getting-started/first-service.md': [
        'etc/orders.json',
        'internal/routes/',
        'internal/api/v1/ping/',
        'internal/admin/',
        'internal/discovery/',
        'deploy/k8s/',
        'deploy/helm/',
        'deploy/observability/',
        'bin/production-check.sh',
        'make production-check',
    ],
    'docs/index.md': [
        'reference/cli-json-contracts.md',
        'reference/control-plane-contracts.md',
    ],
    'README.md': [
        'docs/reference/cli-json-contracts.md',
        'docs/reference/control-plane-contracts.md',
    ],
}

missing = []
for rel, needles in checks.items():
    path = root / rel
    if not path.exists():
        missing.append(f'{rel}: file is missing')
        continue
    text = path.read_text(encoding='utf-8')
    for needle in needles:
        if needle not in text:
            missing.append(f'{rel}: missing {needle!r}')

if missing:
    print('contract documentation check failed:', file=sys.stderr)
    for item in missing:
        print('  ' + item, file=sys.stderr)
    sys.exit(1)

print('contract documentation ok')
PY
