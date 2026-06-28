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
        "R8 publish protocol matrix",
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
for field in ("schema", "acceptanceGate", "permissionReview", "publishProtocolMatrix", "checklist", "publishingCommands", "releaseNoteFields"):
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

publish_protocol = manifest.get("publishProtocolMatrix") or {}
if publish_protocol.get("schema") != "gofly.plugin_publish_protocol_matrix.v1":
    missing.append("docs/reference/plugin-publishing-ux.json: publishProtocolMatrix schema mismatch")
if publish_protocol.get("aiflowTask") != "GOFLY-GOV-10R8-07":
    missing.append("docs/reference/plugin-publishing-ux.json: publishProtocolMatrix aiflowTask mismatch")
if publish_protocol.get("status") != "blocking-contract":
    missing.append("docs/reference/plugin-publishing-ux.json: publishProtocolMatrix status must be blocking-contract")
if publish_protocol.get("acceptanceGate") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-publishing-ux.json: publishProtocolMatrix acceptanceGate mismatch")
required_publish_surfaces = {
    "registry schema",
    "manifest schema",
    "digest sha256",
    "signature provenance",
    "permission minimization",
    "source policy",
    "template contract",
    "malicious path rejection",
    "no partial writes",
}
actual_publish_surfaces = set(publish_protocol.get("requiredSurfaces") or [])
for surface in sorted(required_publish_surfaces - actual_publish_surfaces):
    missing.append(f"docs/reference/plugin-publishing-ux.json: publishProtocolMatrix requiredSurfaces missing {surface!r}")
for field in ("publisherAction", "rollbackOrEscalation"):
    if len(str(publish_protocol.get(field) or "").split()) < 10:
        missing.append(f"docs/reference/plugin-publishing-ux.json: publishProtocolMatrix {field} must be actionable")

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

r8_protocol = report.get("r8PublishProtocolMatrix") or {}
if r8_protocol.get("schema") != "gofly.plugin_template_publish_protocol.v1":
    missing.append("docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix schema mismatch")
if r8_protocol.get("aiflowTask") != "GOFLY-GOV-10R8-07":
    missing.append("docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix aiflowTask mismatch")
if r8_protocol.get("status") != "blocking-contract":
    missing.append("docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix status must be blocking-contract")
if r8_protocol.get("acceptanceGate") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix acceptanceGate mismatch")
r8_rows = {
    item.get("id"): item
    for item in r8_protocol.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_r8_rows = {
    "registry-schema-protocol": {
        "evidenceIds": {"registry-schema"},
        "markers": {"registry", "checksum", "source", "manifest"},
    },
    "manifest-schema-protocol": {
        "evidenceIds": {"manifest-schema", "permission-escape"},
        "markers": {"manifest", "permissions", "requiresDryRun"},
    },
    "digest-signature-source-protocol": {
        "evidenceIds": {"digest-mismatch", "signature-provenance"},
        "markers": {"sha256", "signature", "source"},
    },
    "compatibility-protocol": {
        "evidenceIds": {"old-protocol", "current-protocol", "future-plus-current-protocol", "future-only-protocol"},
        "markers": {"old", "current", "future"},
    },
    "template-contract-protocol": {
        "evidenceIds": {"template-directory-supply-chain"},
        "markers": {"template", "entrypoints", "checksum", "source"},
    },
    "failure-isolation-protocol": {
        "evidenceIds": {"malicious-path", "permission-escape", "no-partial-writes", "digest-mismatch"},
        "markers": {"malicious", "partial", "permission", "digest"},
    },
}
if set(r8_rows) != set(expected_r8_rows):
    missing.append(
        "docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix rows drifted "
        f"missing={sorted(set(expected_r8_rows) - set(r8_rows))} "
        f"extra={sorted(set(r8_rows) - set(expected_r8_rows))}"
    )
for row_id, expected_row in expected_r8_rows.items():
    row = r8_rows.get(row_id) or {}
    for field in ("id", "surface", "publishRequirement", "evidenceIds", "gate", "blockDecision", "rollbackOrEscalation"):
        if row.get(field) in ("", None, []):
            missing.append(f"docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix {row_id}: {field} is required")
    if row.get("gate") != "make plugin-conformance-check":
        missing.append(f"docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix {row_id}: gate mismatch")
    actual_evidence = set(row.get("evidenceIds") or [])
    for evidence_id in sorted(expected_row["evidenceIds"] - actual_evidence):
        missing.append(f"docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix {row_id}: missing evidenceId {evidence_id!r}")
    row_text = json.dumps(row, sort_keys=True).lower()
    for marker in expected_row["markers"]:
        if marker.lower() not in row_text:
            missing.append(f"docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix {row_id}: missing marker {marker!r}")
    for field in ("publishRequirement", "blockDecision", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 10:
            missing.append(f"docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix {row_id}: {field} must be actionable")
    rollback_text = str(row.get("rollbackOrEscalation") or "").lower()
    if not any(marker in rollback_text for marker in ("pin", "disable", "keep", "rollback", "republish")):
        missing.append(f"docs/reference/plugin-conformance-report.json: r8PublishProtocolMatrix {row_id}: rollbackOrEscalation must name pin, disable, keep, rollback, or republish")

adopter_contract = report.get("adopterPublishingContract") or {}
if adopter_contract.get("schema") != "gofly.plugin_adopter_publishing_contract.v1":
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract schema mismatch")
if adopter_contract.get("source") != "docs/reference/plugin-conformance-report.json":
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract source mismatch")
if adopter_contract.get("publishingUX") != "docs/reference/plugin-publishing-ux.json":
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract publishingUX mismatch")
if adopter_contract.get("dashboardReportField") != "pluginAdoption.publishingConformance":
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract dashboardReportField mismatch")
if set(adopter_contract.get("consumers") or []) != {"adopter", "plugin-publisher", "release-manager", "ci-agent"}:
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract consumers mismatch")
if set(adopter_contract.get("acceptanceGates") or []) != {
    "make plugin-conformance-check",
    "go test -C examples/plugin-ecosystem ./...",
    "go run -C examples/plugin-ecosystem .",
}:
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract acceptanceGates mismatch")
if len(str(adopter_contract.get("policy") or "").split()) < 18:
    missing.append("docs/reference/plugin-conformance-report.json: adopterPublishingContract policy must be actionable")

expected_publish_blockers = {
    "registry-or-manifest-invalid": {"registry-schema", "manifest-schema"},
    "protocol-incompatible": {"old-protocol", "future-only-protocol"},
    "integrity-or-provenance-missing": {"digest-mismatch", "signature-provenance"},
    "permission-or-filesystem-escape": {"permission-escape", "malicious-path", "no-partial-writes"},
}
publish_blockers = {
    item.get("id"): item
    for item in adopter_contract.get("publishBlockers") or []
    if isinstance(item, dict) and item.get("id")
}
if set(publish_blockers) != set(expected_publish_blockers):
    missing.append(
        "docs/reference/plugin-conformance-report.json: adopterPublishingContract publishBlockers drifted "
        f"missing={sorted(set(expected_publish_blockers) - set(publish_blockers))} "
        f"extra={sorted(set(publish_blockers) - set(expected_publish_blockers))}"
    )
for blocker_id, expected_cases_for_blocker in expected_publish_blockers.items():
    blocker = publish_blockers.get(blocker_id) or {}
    actual_cases = set(blocker.get("cases") or [])
    for case_id in expected_cases_for_blocker:
        if case_id not in actual_cases:
            missing.append(
                "docs/reference/plugin-conformance-report.json: "
                f"adopterPublishingContract {blocker_id} missing case {case_id!r}"
            )
    for field in ("adopterAction", "rollbackAction"):
        if len(str(blocker.get(field) or "").split()) < 10:
            missing.append(
                "docs/reference/plugin-conformance-report.json: "
                f"adopterPublishingContract {blocker_id} {field} must be actionable"
            )

template_requirements = set(adopter_contract.get("templatePublishingRequirements") or [])
for field in ("template schema", "entrypoints", "permissions", "checksum", "source", "dry-run evidence"):
    if field not in template_requirements:
        missing.append(
            "docs/reference/plugin-conformance-report.json: "
            f"adopterPublishingContract templatePublishingRequirements missing {field!r}"
        )

supply_chain_rows = report.get("supplyChainRows")
if not isinstance(supply_chain_rows, list):
    missing.append("docs/reference/plugin-conformance-report.json: supplyChainRows must be a list")
    supply_chain_rows = []
expected_supply_chain_rows = {
    "registry-schema-supply-chain": {
        "surface": "plugin-registry",
        "requiredEvidence": {"registry.checksum", "registry.source", "registry.protocol", "registry.manifest"},
    },
    "manifest-permission-supply-chain": {
        "surface": "plugin-manifest",
        "requiredEvidence": {"manifest.compatibleVersions", "manifest.capabilities", "manifest.permissions", "manifest.requiresDryRun"},
    },
    "template-directory-supply-chain": {
        "surface": "third-party-template-directory",
        "requiredEvidence": {"template.schema", "template.entrypoints", "template.permissions", "template.checksum", "template.source"},
    },
    "failure-isolation-supply-chain": {
        "surface": "plugin-and-template-effects",
        "requiredEvidence": {"malicious path", "digest mismatch", "permission escape", "no partial writes"},
    },
}
supply_chain_map = {
    item.get("id"): item
    for item in supply_chain_rows
    if isinstance(item, dict) and item.get("id")
}
if set(supply_chain_map) != set(expected_supply_chain_rows):
    missing.append(
        "docs/reference/plugin-conformance-report.json: supplyChainRows drifted "
        f"missing={sorted(set(expected_supply_chain_rows) - set(supply_chain_map))} "
        f"extra={sorted(set(supply_chain_map) - set(expected_supply_chain_rows))}"
    )
for row_id, expected_row in expected_supply_chain_rows.items():
    row = supply_chain_map.get(row_id) or {}
    if row.get("surface") != expected_row["surface"]:
        missing.append(f"docs/reference/plugin-conformance-report.json: supplyChainRows {row_id}: surface mismatch")
    if row.get("gate") != "make plugin-conformance-check":
        missing.append(f"docs/reference/plugin-conformance-report.json: supplyChainRows {row_id}: gate must be make plugin-conformance-check")
    evidence = set(row.get("requiredEvidence") or [])
    for required in sorted(expected_row["requiredEvidence"]):
        if required not in evidence:
            missing.append(f"docs/reference/plugin-conformance-report.json: supplyChainRows {row_id}: missing requiredEvidence {required!r}")
    for field in (
        "compatibilityWindow",
        "permissionBoundary",
        "digestOrChecksum",
        "sourceRequirement",
        "isolationRequirement",
        "rollbackOrEscalation",
    ):
        if len(str(row.get(field) or "").split()) < 10:
            missing.append(f"docs/reference/plugin-conformance-report.json: supplyChainRows {row_id}: {field} must be actionable")
    row_text = json.dumps(row, sort_keys=True).lower()
    for marker in ("compatibility", "permission", "checksum", "source", "isolation"):
        if marker not in row_text:
            missing.append(f"docs/reference/plugin-conformance-report.json: supplyChainRows {row_id}: missing marker {marker!r}")
    rollback_text = str(row.get("rollbackOrEscalation") or "").lower()
    if not any(marker in rollback_text for marker in ("pin", "disable", "keep", "rollback")):
        missing.append(
            f"docs/reference/plugin-conformance-report.json: supplyChainRows {row_id}: "
            "rollbackOrEscalation must name pin, disable, keep, or rollback"
        )

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
