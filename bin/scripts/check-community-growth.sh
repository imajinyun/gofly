#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path('.').resolve()

text_checks = {
    pathlib.Path('CONTRIBUTING.md'): [
        '## Local Setup',
        '## Development Checks',
        '## Generated Project Changes',
        '## Documentation and Examples',
        '## Security',
        'go test -shuffle=on ./...',
        'make governance-10-rounds',
        'make examples-smoke',
        'make docs-check',
        'make test-generated-matrix',
        'SECURITY.md',
    ],
    pathlib.Path('ROADMAP.md'): [
        '## v0.2 Production proof',
        '## v0.5 Ecosystem preview',
        '## v1.0 Compatibility',
        'production-orders',
        'Plugin registry',
        'Benchmark trend',
        'Stable CLI flags and JSON output',
        'Stable control-plane schema migration policy',
        'Generated project compatibility policy',
        '## Validation gates',
    ],
    pathlib.Path('.github/pull_request_template.md'): [
        'make ci-fast',
        'make ci',
        'make governance-10-rounds',
        'make examples-smoke',
        'make docs-link-check',
        'Public API compatibility checked',
        'Generated code compatibility considered',
        'Rollout or rollback considerations',
    ],
    pathlib.Path('README.md'): [
        'CONTRIBUTING.md',
        'ROADMAP.md',
        'SECURITY.md',
    ],
}

missing = []

for rel, terms in text_checks.items():
    path = root / rel
    if not path.is_file():
        missing.append(f'missing community file: {rel}')
        continue
    text = path.read_text(encoding='utf-8')
    for term in terms:
        if term not in text:
            missing.append(f'{rel} missing required term: {term}')

template_checks = {
    pathlib.Path('.github/ISSUE_TEMPLATE/bug_report.yml'): {
        'labels': ['bug', 'needs-triage'],
        'body_ids': ['version', 'area', 'reproduce', 'expected', 'actual', 'environment', 'validation'],
        'terms': ['make examples-smoke', 'make docs-check'],
    },
    pathlib.Path('.github/ISSUE_TEMPLATE/feature_request.yml'): {
        'labels': ['enhancement', 'needs-triage'],
        'body_ids': ['area', 'problem', 'proposal', 'alternatives', 'compatibility', 'validation'],
        'terms': ['Public API', 'generated code', 'migration', 'make p1-growth-check'],
    },
    pathlib.Path('.github/ISSUE_TEMPLATE/good_first_issue.yml'): {
        'labels': ['good first issue', 'needs-triage'],
        'body_ids': ['area', 'task', 'validation'],
        'terms': ['go test -shuffle=on ./...', 'make examples-smoke', 'make docs-check'],
    },
}

for rel, expected in template_checks.items():
    path = root / rel
    if not path.is_file():
        missing.append(f'missing issue template: {rel}')
        continue
    text = path.read_text(encoding='utf-8')
    for label in expected['labels']:
        if label not in text:
            missing.append(f'{rel} missing label: {label}')
    for body_id in expected['body_ids']:
        if f'id: {body_id}' not in text:
            missing.append(f'{rel} missing body id: {body_id}')
    for term in expected['terms']:
        if term not in text:
            missing.append(f'{rel} missing required term: {term}')

config = root / '.github/ISSUE_TEMPLATE/config.yml'
if not config.is_file():
    missing.append('missing issue template config')
else:
    text = config.read_text(encoding='utf-8')
    if 'blank_issues_enabled: false' not in text:
        missing.append('.github/ISSUE_TEMPLATE/config.yml must disable blank issues')
    if 'SECURITY.md' not in text and 'security/policy' not in text:
        missing.append('.github/ISSUE_TEMPLATE/config.yml must link security policy')

if missing:
    print('community growth check failed:', file=sys.stderr)
    for item in missing:
        print(f'  {item}', file=sys.stderr)
    sys.exit(1)

print('community growth assets ok')
PY
