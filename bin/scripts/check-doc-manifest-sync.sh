#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import subprocess
import sys

root = pathlib.Path('.').resolve()

result = subprocess.run(
    ['go', 'run', './cmd/gofly', 'ai', 'manifest', '--format', 'json'],
    cwd=root,
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
if result.returncode != 0:
    print('doc manifest sync check failed: could not run gofly ai manifest', file=sys.stderr)
    print(result.stderr, file=sys.stderr)
    sys.exit(result.returncode)

try:
    envelope = json.loads(result.stdout)
except json.JSONDecodeError as exc:
    print(f'doc manifest sync check failed: manifest is not JSON: {exc}', file=sys.stderr)
    sys.exit(1)

manifest = envelope.get('data', {})
missing = []

if manifest.get('schemaVersion') != 'gofly.ai.tool-manifest.v1':
    missing.append('manifest schemaVersion must be gofly.ai.tool-manifest.v1')

for section in ('docs', 'examples'):
    links = manifest.get(section, [])
    if not links:
        missing.append(f'manifest {section} must not be empty')
        continue
    for link in links:
        title = link.get('title', '')
        path = link.get('path', '')
        if not title or not path:
            missing.append(f'manifest {section} entry must include title and path: {link!r}')
            continue
        if path.startswith('/') or '..' in pathlib.PurePosixPath(path).parts:
            missing.append(f'manifest {section} path must be repo-relative and local: {path}')
            continue
        if not (root / path).is_file():
            missing.append(f'manifest {section} path does not exist: {path}')

verify_commands = set(manifest.get('verifyCommands', []))
for command in ('make docs-check', 'make examples-smoke', 'make test-generated-matrix', 'make doc-manifest-sync-check'):
    if command not in verify_commands:
        missing.append(f'manifest verifyCommands missing {command!r}')

feature_library = manifest.get('featureLibrary', {})
features = set(feature_library.get('features', []))
templates = set(feature_library.get('templates', []))
if not features:
    missing.append('manifest featureLibrary.features must not be empty')
if not templates:
    missing.append('manifest featureLibrary.templates must not be empty')

feature_docs = {
    pathlib.Path('docs/concepts/ai-manifest.md'): ['auth-jwt', 'postgres-repository', 'redis-cache'],
}
template_docs = {
    pathlib.Path('docs/concepts/ai-manifest.md'): ['go-rest-minimal', 'go-rag-service', 'go-rpc-grpc'],
}

for rel, names in feature_docs.items():
    path = root / rel
    if not path.is_file():
        missing.append(f'documented feature file is missing: {rel}')
        continue
    text = path.read_text(encoding='utf-8')
    for name in names:
        if name not in text:
            missing.append(f'{rel} must document feature {name!r}')
        if name not in features:
            missing.append(f'{rel} documents feature {name!r} but manifest does not expose it')

for rel, names in template_docs.items():
    path = root / rel
    if not path.is_file():
        missing.append(f'documented template file is missing: {rel}')
        continue
    text = path.read_text(encoding='utf-8')
    for name in names:
        if name not in text:
            missing.append(f'{rel} must document template {name!r}')
        if name not in templates:
            missing.append(f'{rel} documents template {name!r} but manifest does not expose it')

makefile = (root / 'Makefile').read_text(encoding='utf-8')
if 'doc-manifest-sync-check' not in makefile:
    missing.append('Makefile must expose doc-manifest-sync-check')
docs_check_line = next((line for line in makefile.splitlines() if line.startswith('docs-check:')), '')
if 'doc-manifest-sync-check' not in docs_check_line:
    missing.append('docs-check must depend on doc-manifest-sync-check')

if missing:
    print('doc manifest sync check failed:', file=sys.stderr)
    for item in missing:
        print(f'  {item}', file=sys.stderr)
    sys.exit(1)

print('doc manifest sync ok')
PY
