#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path('.').resolve()

case_files = [
    pathlib.Path('docs/case-studies/build-orders-service.md'),
    pathlib.Path('docs/case-studies/ai-control-plane-drift.md'),
    pathlib.Path('docs/case-studies/migrate-from-gin.md'),
]
migration_files = [
    pathlib.Path('docs/comparisons/gin.md'),
    pathlib.Path('docs/comparisons/go-zero.md'),
    pathlib.Path('docs/comparisons/kratos.md'),
    pathlib.Path('docs/comparisons/kitex.md'),
]

case_terms = ['## Baseline', '## Adoption plan', '## Verification', '## Rollback']
migration_terms = ['## Migration steps', '## Validation gates', '## Rollback plan']

missing = []

for path in case_files:
    full = root / path
    if not full.is_file():
        missing.append(f'missing case study: {path}')
        continue
    text = full.read_text(encoding='utf-8')
    for term in case_terms:
        if term not in text:
            missing.append(f'{path} missing required case-study section: {term}')
    if 'examples/' not in text and '/admin/control-plane' not in text:
        missing.append(f'{path} must tie the case to a runnable example or control-plane contract')

for path in migration_files:
    full = root / path
    if not full.is_file():
        missing.append(f'missing migration guide: {path}')
        continue
    text = full.read_text(encoding='utf-8')
    for term in migration_terms:
        if term not in text:
            missing.append(f'{path} missing required migration section: {term}')
    if 'make docs-check' not in text or 'make examples-copyable-check' not in text:
        missing.append(f'{path} must include shared validation gates')

if missing:
    print('migration documentation check failed:', file=sys.stderr)
    for item in missing:
        print(f'  {item}', file=sys.stderr)
    sys.exit(1)

print('migration documentation ok')
PY
