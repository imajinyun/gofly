#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path('.').resolve()

required_files = {
    'tutorial': pathlib.Path('docs/tutorials/zero-to-production.md'),
    'how-to': pathlib.Path('docs/how-to/standalone-examples.md'),
    'reference': pathlib.Path('docs/reference/api-surface.md'),
    'explanation': pathlib.Path('docs/explanation/adoption-model.md'),
}

required_index_terms = [
    '## Choose your path',
    '## Documentation model',
    '## Definition of done',
    '[From zero to production](tutorials/zero-to-production.md)',
    '[Use standalone examples](how-to/standalone-examples.md)',
    '[Stable API surface](reference/api-surface.md)',
    '[Adoption model](explanation/adoption-model.md)',
]

required_tutorial_terms = [
    'make examples-copyable-check',
    'make bench-evidence-check',
    'make docs-check',
    '../operations/production-checklist.md',
]

required_readme_terms = [
    '[docs/index.md](docs/index.md)',
    '[docs/tutorials/zero-to-production.md](docs/tutorials/zero-to-production.md)',
    '[docs/reference/benchmark-matrix.md](docs/reference/benchmark-matrix.md)',
    '[docs/explanation/adoption-model.md](docs/explanation/adoption-model.md)',
]

missing = []

for layer, path in required_files.items():
    full = root / path
    if not full.is_file():
        missing.append(f'missing {layer} doc: {path}')
        continue
    text = full.read_text(encoding='utf-8')
    title = text.splitlines()[0] if text.splitlines() else ''
    if layer == 'reference':
        if 'Reference' not in title:
            missing.append(f'{path} must start with a reference layer heading')
        continue
    expected_prefix = f'# {layer.title()}' if layer != 'how-to' else '# How-to'
    if expected_prefix not in title:
        missing.append(f'{path} must start with a {layer} layer heading')

index_text = (root / 'docs/index.md').read_text(encoding='utf-8')
for term in required_index_terms:
    if term not in index_text:
        missing.append(f'docs/index.md missing productized navigation term: {term}')

tutorial_text = (root / 'docs/tutorials/zero-to-production.md').read_text(encoding='utf-8')
for term in required_tutorial_terms:
    if term not in tutorial_text:
        missing.append(f'docs/tutorials/zero-to-production.md missing release-path term: {term}')

readme_text = (root / 'README.md').read_text(encoding='utf-8')
for term in required_readme_terms:
    if term not in readme_text:
        missing.append(f'README.md missing documentation navigation term: {term}')

if missing:
    print('documentation taxonomy check failed:', file=sys.stderr)
    for item in missing:
        print(f'  {item}', file=sys.stderr)
    sys.exit(1)

print('documentation taxonomy ok')
PY
