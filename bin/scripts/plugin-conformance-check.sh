#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/plugin-conformance.md"): [
        "gofly.plugin_conformance.v1",
        "gofly.plugin_publishing_ux.v1",
        "plugin-publishing-ux.json",
        "Publishing contract",
        "registry JSON schema",
        "plugin manifest schema",
        "`name`, `remote`, `version`",
        "`compatibleVersions`, `capabilities`, `permissions`, and",
        "make plugin-conformance-check",
        "go test -C examples/plugin-ecosystem ./...",
        "go run -C examples/plugin-ecosystem .",
        "protocol compatibility",
        "digest provenance",
        "permission rationale",
        "template contract",
        "rollback and failure isolation behavior",
        "digest",
        "least permission",
        "compatibility runner",
        "failure isolation",
        "old protocol",
        "current protocol",
        "future protocol",
        "malicious path",
        "digest mismatch",
        "permission escape",
    ],
    pathlib.Path("examples/plugin-ecosystem/main.go"): [
        "Publishing",
        "publishingSummary",
        "ManifestFields",
        "RegistryFields",
        "RequiredGates",
        "ReleaseNotes",
        "requiresDryRun",
        "digest provenance",
        "signature provenance",
        "old-protocol",
        "current-protocol",
        "future-plus-current",
        "future-only",
        "digest-mismatch",
        "malicious-path",
        "permission-escape",
        "failure-isolation",
    ],
    pathlib.Path("cmd/gofly/internal/generator/plugin.go"): [
        "PluginProtocolSchema",
        "PluginRegistryEntry",
        "validatePluginRegistryChecksum",
        "PluginPermissionWriteRelative",
    ],
    pathlib.Path("cmd/gofly/internal/generator/plugin_test.go"): [
        "TestPluginProtocolCompatibilityMatrix",
        "TestPluginProtocolSchemaContract",
        "TestPluginRegistryIndexValidationAndFiltering",
    ],
    pathlib.Path("examples/plugin-ecosystem/main_test.go"): [
        "Publishing.ManifestFields",
        "Publishing.RequiredGates",
        "Publishing.ReleaseNotes",
    ],
}

missing = []
for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

manifest_path = pathlib.Path("docs/reference/plugin-publishing-ux.json")
if not manifest_path.is_file():
    missing.append("docs/reference/plugin-publishing-ux.json: file is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.plugin_publishing_ux.v1":
    missing.append("docs/reference/plugin-publishing-ux.json: schema must be gofly.plugin_publishing_ux.v1")
contract = manifest.get("schemaContract") or {}
if contract.get("version") != "1":
    missing.append("docs/reference/plugin-publishing-ux.json: schemaContract.version must be 1")
for field in ("schema", "acceptanceGate", "permissionReview", "checklist", "publishingCommands", "releaseNoteFields"):
    if field not in set(contract.get("requiredFields") or []):
        missing.append(f"docs/reference/plugin-publishing-ux.json: schemaContract.requiredFields missing {field!r}")
if manifest.get("acceptanceGate") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-publishing-ux.json: acceptanceGate must be make plugin-conformance-check")

permission_review = manifest.get("permissionReview") or {}
if permission_review.get("allowedPermissions") != ["filesystem:write-relative"]:
    missing.append("docs/reference/plugin-publishing-ux.json: permissionReview.allowedPermissions must be filesystem:write-relative only")
if permission_review.get("leastPrivilege") is not True:
    missing.append("docs/reference/plugin-publishing-ux.json: permissionReview.leastPrivilege must be true")
if permission_review.get("requiresDryRun") is not True:
    missing.append("docs/reference/plugin-publishing-ux.json: permissionReview.requiresDryRun must be true")
if permission_review.get("requiresRationale") is not True:
    missing.append("docs/reference/plugin-publishing-ux.json: permissionReview.requiresRationale must be true")

expected_checklist = {
    "permission-review": {"manifest.permissions", "permissionReview.rationale", "permissionReview.leastPrivilege"},
    "registry-publishing": {"registry.checksum", "registry.source", "registry.protocol", "registry.manifest"},
    "third-party-template-publishing": {"template.schema", "template.entrypoints", "template.permissions", "template.checksum", "template.source"},
    "digest-and-signature": {"digest.sha256", "signature.provenance", "source.repository"},
    "compatibility-matrix": {"old protocol", "current protocol", "future protocol", "future-plus-current protocol"},
    "failure-isolation": {"malicious path", "digest mismatch", "permission escape", "no partial writes"},
}
seen_checklist = {}
for item in manifest.get("checklist") or []:
    if not isinstance(item, dict):
        missing.append(f"docs/reference/plugin-publishing-ux.json: checklist item must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    seen_checklist[item_id] = set(item.get("evidence") or [])
    if item.get("required") is not True:
        missing.append(f"docs/reference/plugin-publishing-ux.json: checklist {item_id or '<missing>'} must be required")
for item_id, expected_evidence in expected_checklist.items():
    actual = seen_checklist.get(item_id)
    if actual is None:
        missing.append(f"docs/reference/plugin-publishing-ux.json: checklist missing {item_id!r}")
        continue
    missing_evidence = expected_evidence - actual
    for evidence in sorted(missing_evidence):
        missing.append(f"docs/reference/plugin-publishing-ux.json: checklist {item_id!r} missing evidence {evidence!r}")

commands = set(manifest.get("publishingCommands") or [])
for command in ("make plugin-conformance-check", "go test -C examples/plugin-ecosystem ./...", "go run -C examples/plugin-ecosystem ."):
    if command not in commands:
        missing.append(f"docs/reference/plugin-publishing-ux.json: publishingCommands missing {command!r}")
release_note_fields = set(manifest.get("releaseNoteFields") or [])
for field in ("protocol compatibility", "digest provenance", "signature provenance", "permission rationale", "template contract", "rollback and failure isolation behavior"):
    if field not in release_note_fields:
        missing.append(f"docs/reference/plugin-publishing-ux.json: releaseNoteFields missing {field!r}")

template_contract = manifest.get("templateContract") or {}
template_path = pathlib.Path(template_contract.get("path") or "")
if not template_path.is_file():
    missing.append(f"docs/reference/plugin-publishing-ux.json: templateContract.path does not exist: {template_path}")
else:
    template_data = json.loads(template_path.read_text(encoding="utf-8"))
    for field in template_contract.get("requiredFields") or []:
        if field not in template_data:
            missing.append(f"{template_path}: missing template field {field!r}")

if missing:
    print("plugin conformance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("plugin conformance governance ok")
PY
