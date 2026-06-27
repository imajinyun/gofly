#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/plugin-conformance.md"): [
        "gofly.plugin_conformance.v1",
        "plugin-conformance-report.json",
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
        "requiresDryRun must be true",
        "permissions are required",
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

compatibility = manifest.get("protocolCompatibility") or []
compatibility_by_case = {item.get("case"): item for item in compatibility if isinstance(item, dict)}
expected_compatibility = {
    "old protocol": (False, {"0"}),
    "current protocol": (True, {"1"}),
    "future-plus-current protocol": (True, {"1", "2"}),
    "future protocol": (False, {"2"}),
}
for case, (accepted, versions) in expected_compatibility.items():
    item = compatibility_by_case.get(case)
    if not item:
        missing.append(f"docs/reference/plugin-publishing-ux.json: protocolCompatibility missing {case!r}")
        continue
    if item.get("accepted") is not accepted:
        missing.append(f"docs/reference/plugin-publishing-ux.json: protocolCompatibility {case!r} accepted must be {accepted}")
    if set(item.get("compatibleVersions") or []) != versions:
        missing.append(f"docs/reference/plugin-publishing-ux.json: protocolCompatibility {case!r} compatibleVersions mismatch")
    for field in ("publisherAction", "rollbackOrEscalation"):
        if len(str(item.get(field) or "").split()) < 8:
            missing.append(f"docs/reference/plugin-publishing-ux.json: protocolCompatibility {case!r} {field} must be actionable")

failure_policy = manifest.get("failureIsolationPolicy") or {}
for field in ("maliciousPathRejected", "digestMismatchRejected", "permissionEscapeRejected", "partialWritesRejected"):
    if failure_policy.get(field) is not True:
        missing.append(f"docs/reference/plugin-publishing-ux.json: failureIsolationPolicy.{field} must be true")
if failure_policy.get("reportField") != "failure isolation":
    missing.append("docs/reference/plugin-publishing-ux.json: failureIsolationPolicy.reportField must be failure isolation")
for field in ("publisherAction", "rollbackOrEscalation"):
    if len(str(failure_policy.get(field) or "").split()) < 8:
        missing.append(f"docs/reference/plugin-publishing-ux.json: failureIsolationPolicy.{field} must be actionable")

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

report_path = pathlib.Path("docs/reference/plugin-conformance-report.json")
if not report_path.is_file():
    missing.append("docs/reference/plugin-conformance-report.json: file is missing")
    report = {}
else:
    report = json.loads(report_path.read_text(encoding="utf-8"))

if report.get("schema") != "gofly.plugin_conformance_report.v1":
    missing.append("docs/reference/plugin-conformance-report.json: schema must be gofly.plugin_conformance_report.v1")
if report.get("sourceOfTruth") != "docs/reference/plugin-conformance.md":
    missing.append("docs/reference/plugin-conformance-report.json: sourceOfTruth must be docs/reference/plugin-conformance.md")
if report.get("aiflowTask") != "GOFLY-P1-4-PLUGIN-CONFORMANCE-RUNNER":
    missing.append("docs/reference/plugin-conformance-report.json: aiflowTask must be GOFLY-P1-4-PLUGIN-CONFORMANCE-RUNNER")
if report.get("acceptanceGate") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-conformance-report.json: acceptanceGate must be make plugin-conformance-check")
runner = report.get("runner") or {}
if runner.get("mode") != "make-target-json-report":
    missing.append("docs/reference/plugin-conformance-report.json: runner.mode must be make-target-json-report")
if runner.get("command") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-conformance-report.json: runner.command must be make plugin-conformance-check")
protocol = report.get("protocol") or {}
if protocol.get("current") != "1":
    missing.append("docs/reference/plugin-conformance-report.json: protocol.current must be 1")
if protocol.get("contractTest") != "TestPluginProtocolSchemaContract":
    missing.append("docs/reference/plugin-conformance-report.json: protocol.contractTest must be TestPluginProtocolSchemaContract")

expected_cases = {
    "registry-schema": ("registry", "pass", {"TestPluginRegistryIndexValidationAndFiltering"}),
    "manifest-schema": ("manifest", "pass", {"TestPluginManifestContractValidation", "TestPluginProtocolSchemaContract"}),
    "old-protocol": ("compatibility", "reject", {"TestPluginProtocolCompatibilityMatrix"}),
    "current-protocol": ("compatibility", "accept", {"TestPluginProtocolCompatibilityMatrix"}),
    "future-plus-current-protocol": ("compatibility", "accept", {"TestPluginProtocolCompatibilityMatrix"}),
    "future-only-protocol": ("compatibility", "reject", {"TestPluginProtocolCompatibilityMatrix"}),
    "digest-mismatch": ("digest", "reject", {"TestResolveRemotePluginRejectsDigestMismatch"}),
    "signature-provenance": ("signature", "record", {"TestPluginEcosystemReport"}),
    "permission-escape": ("permission", "reject", {"TestPluginManifestContractValidation"}),
    "malicious-path": ("filesystem", "reject", {"TestPluginResponseWriteFilesRejectsEscapingPaths"}),
    "no-partial-writes": ("failure-isolation", "reject", {"TestPluginResponseApplyRejectsPartialWritesWhenPatchFails"}),
}
cases = report.get("cases") or []
case_map = {item.get("id"): item for item in cases if isinstance(item, dict)}
if set(case_map) != set(expected_cases):
    missing.append(
        "docs/reference/plugin-conformance-report.json: cases drifted "
        f"missing={sorted(set(expected_cases) - set(case_map))} "
        f"extra={sorted(set(case_map) - set(expected_cases))}"
    )
for case_id, (category, expected, required_tests) in expected_cases.items():
    item = case_map.get(case_id) or {}
    if item.get("category") != category:
        missing.append(f"docs/reference/plugin-conformance-report.json: {case_id} category must be {category}")
    if item.get("expected") != expected:
        missing.append(f"docs/reference/plugin-conformance-report.json: {case_id} expected must be {expected}")
    tests = set(item.get("tests") or [])
    for test_name in required_tests:
        if test_name not in tests:
            missing.append(f"docs/reference/plugin-conformance-report.json: {case_id} missing test {test_name!r}")
    for evidence in item.get("evidence") or []:
        if not pathlib.Path(evidence).exists():
            missing.append(f"docs/reference/plugin-conformance-report.json: {case_id} evidence path is missing: {evidence}")
    if len(str(item.get("failurePolicy") or "").split()) < 8:
        missing.append(f"docs/reference/plugin-conformance-report.json: {case_id} failurePolicy must be actionable")

release_gates = set(report.get("releaseGates") or [])
for command in (
    "make plugin-conformance-check",
    "go test -C examples/plugin-ecosystem ./...",
    "go run -C examples/plugin-ecosystem .",
):
    if command not in release_gates:
        missing.append(f"docs/reference/plugin-conformance-report.json: releaseGates missing {command!r}")

if missing:
    print("plugin conformance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("plugin conformance governance ok")
PY
