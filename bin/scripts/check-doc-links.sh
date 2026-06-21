#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import re
import sys

root = pathlib.Path('.').resolve()
missing = []
pattern = re.compile(r'\[[^\]]+\]\(([^)#][^)]*)\)')

for path in list(root.glob('README*.md')) + list((root / 'docs').rglob('*.md')) + list((root / 'examples').rglob('*.md')):
    rel = path.relative_to(root)
    if rel.parts[:2] == ('docs', 'superpowers'):
        continue
    raw = path.read_text(encoding='utf-8')
    text = re.sub(r'```.*?```', '', raw, flags=re.S)
    for target in pattern.findall(text):
        if '://' in target or target.startswith('mailto:') or target.startswith('#'):
            continue
        clean = target.split('#', 1)[0]
        if not clean:
            continue
        candidate = (path.parent / clean).resolve()
        try:
            candidate.relative_to(root)
        except ValueError:
            missing.append(f'{path.relative_to(root)} -> {target} escapes repository')
            continue
        if not candidate.exists():
            missing.append(f'{path.relative_to(root)} -> {target}')

if missing:
    print('missing markdown links:', file=sys.stderr)
    for item in missing:
        print('  ' + item, file=sys.stderr)
    sys.exit(1)
print('markdown links ok')
PY
