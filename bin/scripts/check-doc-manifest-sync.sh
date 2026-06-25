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
template_verification = feature_library.get('templateVerification') or {}
validated_templates = set(template_verification.get('validatedTemplates', []))
if not features:
    missing.append('manifest featureLibrary.features must not be empty')
if not templates:
    missing.append('manifest featureLibrary.templates must not be empty')
if validated_templates != templates:
    missing.append(
        'manifest featureLibrary.templateVerification.validatedTemplates must match templates: '
        f'missing={sorted(templates - validated_templates)} extra={sorted(validated_templates - templates)}'
    )

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

trust_path = root / 'docs/reference/template-profile-trust.json'
if not trust_path.is_file():
    missing.append('docs/reference/template-profile-trust.json is missing')
    trust = {}
else:
    trust = json.loads(trust_path.read_text(encoding='utf-8'))

if trust.get('schema') != 'gofly.template_profile_trust.v1':
    missing.append('template profile trust schema must be gofly.template_profile_trust.v1')
if trust.get('sourceOfTruth') != 'gofly ai manifest --format json':
    missing.append('template profile trust sourceOfTruth mismatch')
if trust.get('generatedProfileSource') != 'docs/reference/generated-upgrade-dry-run.json':
    missing.append('template profile trust generatedProfileSource mismatch')
if trust.get('acceptanceGate') != 'make doc-manifest-sync-check':
    missing.append('template profile trust acceptanceGate mismatch')

template_trust = trust.get('templateTrust') or []
template_trust_ids = {
    item.get('id')
    for item in template_trust
    if isinstance(item, dict) and item.get('id')
}
if template_trust_ids != templates:
    missing.append(
        'template trust ids must match manifest templates: '
        f'missing={sorted(templates - template_trust_ids)} extra={sorted(template_trust_ids - templates)}'
    )

required_template_fields = (
    'id',
    'purpose',
    'generatedOutputGuarantees',
    'dependencyBoundary',
    'verificationCommands',
    'aiManifestLinkage',
    'trustStatus',
)
for item in template_trust:
    if not isinstance(item, dict):
        missing.append(f'template trust entry must be an object: {item!r}')
        continue
    template_id = item.get('id', '<missing>')
    for field in required_template_fields:
        if not item.get(field):
            missing.append(f'template trust {template_id}: {field} is required')
    if item.get('trustStatus') != 'e2e-validated':
        missing.append(f'template trust {template_id}: trustStatus must be e2e-validated')
    linkage = item.get('aiManifestLinkage') or {}
    if linkage.get('templateField') != 'featureLibrary.templates':
        missing.append(f'template trust {template_id}: templateField mismatch')
    if linkage.get('verificationField') != 'featureLibrary.templateVerification.validatedTemplates':
        missing.append(f'template trust {template_id}: verificationField mismatch')
    if template_id != '<missing>' and template_id not in str(linkage.get('command', '')):
        missing.append(f'template trust {template_id}: command must reference template id')
    for command in ('make test-generated-matrix', 'make doc-manifest-sync-check'):
        if command not in (item.get('verificationCommands') or []):
            missing.append(f'template trust {template_id}: verificationCommands missing {command!r}')
    boundary = item.get('dependencyBoundary', '')
    if 'root module' not in boundary or 'generated' not in boundary:
        missing.append(f'template trust {template_id}: dependencyBoundary must mention generated/root module boundary')
    if len(item.get('generatedOutputGuarantees') or []) < 3:
        missing.append(f'template trust {template_id}: generatedOutputGuarantees must include at least 3 guarantees')

upgrade_path = root / 'docs/reference/generated-upgrade-dry-run.json'
if not upgrade_path.is_file():
    missing.append('docs/reference/generated-upgrade-dry-run.json is missing')
    upgrade = {}
else:
    upgrade = json.loads(upgrade_path.read_text(encoding='utf-8'))
upgrade_profiles = {
    item.get('profile')
    for item in upgrade.get('profiles') or []
    if isinstance(item, dict) and item.get('profile')
}
profile_trust = trust.get('generatedProfileTrust') or []
profile_trust_ids = {
    item.get('profile')
    for item in profile_trust
    if isinstance(item, dict) and item.get('profile')
}
if profile_trust_ids != upgrade_profiles:
    missing.append(
        'generated profile trust ids must match generated-upgrade-dry-run profiles: '
        f'missing={sorted(upgrade_profiles - profile_trust_ids)} extra={sorted(profile_trust_ids - upgrade_profiles)}'
    )
for item in profile_trust:
    if not isinstance(item, dict):
        missing.append(f'generated profile trust entry must be an object: {item!r}')
        continue
    profile = item.get('profile', '<missing>')
    for field in (
        'profile',
        'purpose',
        'generatedOutputGuarantees',
        'dependencyBoundary',
        'verificationCommands',
    ):
        if not item.get(field):
            missing.append(f'generated profile trust {profile}: {field} is required')
    if item.get('rollbackNoteRequired') is not True:
        missing.append(f'generated profile trust {profile}: rollbackNoteRequired must be true')
    for command in ('make generated-upgrade-dry-run-check', 'make generated-version-compat-check'):
        if command not in (item.get('verificationCommands') or []):
            missing.append(f'generated profile trust {profile}: verificationCommands missing {command!r}')
    boundary = item.get('dependencyBoundary', '')
    if 'root module' not in boundary or 'generated project' not in boundary:
        missing.append(f'generated profile trust {profile}: dependencyBoundary must mention generated project/root module boundary')
    if len(item.get('generatedOutputGuarantees') or []) < 3:
        missing.append(f'generated profile trust {profile}: generatedOutputGuarantees must include at least 3 guarantees')

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
