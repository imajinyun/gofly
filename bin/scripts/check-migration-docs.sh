#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path('.').resolve()
path = root / 'docs' / 'reference' / 'from-go-zero-migration.md'
missing = []

if not path.is_file():
    missing.append('docs/reference/from-go-zero-migration.md is missing')
else:
    text = path.read_text(encoding='utf-8')
    for term in (
        '## 30-minute migration path',
        'examples/migration/gozero-basic',
        'goctl-compatible migration path, not a full goctl replacement',
        'make goctl-generator-compat-check',
        'make goctl-real-project-replay-check',
        'make goctl-model-parity-replay-check',
        'Rollback',
        '--profile gozero-compatible',
        '--style go_zero',
    ):
        if term not in text:
            missing.append(f'docs/reference/from-go-zero-migration.md missing {term!r}')

example = root / 'examples' / 'migration' / 'gozero-basic' / 'main.go'
if not example.is_file():
    missing.append('examples/migration/gozero-basic/main.go is missing')

makefile = (root / 'Makefile').read_text(encoding='utf-8')
if 'migration-docs-check' not in makefile:
    missing.append('Makefile must expose migration-docs-check')
if 'check-migration-docs.sh' not in makefile:
    missing.append('Makefile must call check-migration-docs.sh')

if missing:
    print('migration docs check failed:', file=sys.stderr)
    for item in missing:
        print(f'  {item}', file=sys.stderr)
    sys.exit(1)

print('migration docs ok')
PY
