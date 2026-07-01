#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import subprocess
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
        "P9 publish hardening",
        "source allowlist",
        "contractVersion",
    ],
    pathlib.Path("examples/plugin-ecosystem/main.go"): [
        "Publishing",
        "publishingSummary",
        "ManifestFields",
        "RegistryFields",
        "RequiredGates",
        "ReleaseNotes",
        "TrustSources",
        "SourceAllowlist",
        "P13Publishing",
        "p13PublishingSummary",
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
        "sourcePolicy",
        "github-actions-oidc",
        "GOFLY-P13-10-PLUGIN-TEMPLATE-PUBLISH-HARDENING",
        "no-partial-writes",
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
        "Publishing.TrustSources",
        "Publishing.SourceAllowlist",
        "P13Publishing.RequiredRegistry",
        "P13Publishing.RequiredManifest",
        "P13Publishing.RequiredTemplate",
        "P13Publishing.FailureCases",
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
if "p9PublishHardening" not in set(contract.get("requiredFields") or []):
    missing.append("docs/reference/plugin-publishing-ux.json: schemaContract.requiredFields missing 'p9PublishHardening'")
if "p13PublishHardening" not in set(contract.get("requiredFields") or []):
    missing.append("docs/reference/plugin-publishing-ux.json: schemaContract.requiredFields missing 'p13PublishHardening'")
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

p9_hardening = manifest.get("p9PublishHardening") or {}
if p9_hardening.get("schema") != "gofly.plugin_publish_hardening.v1":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening schema mismatch")
if p9_hardening.get("aiflowTask") != "GOFLY-GOV-10P9-07":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening aiflowTask mismatch")
if p9_hardening.get("status") != "blocking-contract":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening status must be blocking-contract")
if p9_hardening.get("acceptanceGate") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening acceptanceGate mismatch")
for field in ("compatibilityPolicy", "publisherAction", "rollbackOrEscalation"):
    if len(str(p9_hardening.get(field) or "").split()) < 12:
        missing.append(f"docs/reference/plugin-publishing-ux.json: p9PublishHardening {field} must be actionable")
if set(p9_hardening.get("signatureTrustSources") or []) != {"github-actions-oidc"}:
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening signatureTrustSources mismatch")
digest_pinning = p9_hardening.get("digestPinning") or {}
if digest_pinning.get("algorithm") != "sha256":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening digestPinning.algorithm must be sha256")
if digest_pinning.get("registryField") != "checksum":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening digestPinning.registryField must be checksum")
if digest_pinning.get("failureCase") != "digest-mismatch":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening digestPinning.failureCase mismatch")
source_allowlist = p9_hardening.get("sourceAllowlist") or {}
if set(source_allowlist.get("allowedHosts") or []) != {"github.com"}:
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening sourceAllowlist.allowedHosts mismatch")
if source_allowlist.get("httpsOnly") is not True:
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening sourceAllowlist.httpsOnly must be true")
if source_allowlist.get("registryField") != "sourcePolicy":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening sourceAllowlist.registryField must be sourcePolicy")
template_versioning = p9_hardening.get("templateContractVersioning") or {}
if template_versioning.get("field") != "contractVersion" or template_versioning.get("current") != "1":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening templateContractVersioning must require contractVersion 1")
if not pathlib.Path(template_versioning.get("path") or "").is_file():
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening templateContractVersioning.path is missing")
partial_write = p9_hardening.get("noPartialWriteEvidence") or {}
if partial_write.get("failureCase") != "no-partial-writes":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening noPartialWriteEvidence.failureCase mismatch")
if partial_write.get("test") != "TestPluginResponseApplyRejectsPartialWritesWhenPatchFails":
    missing.append("docs/reference/plugin-publishing-ux.json: p9PublishHardening noPartialWriteEvidence.test mismatch")
required_registry_fields = set(p9_hardening.get("requiredRegistryFields") or [])
for field in ("checksum", "source", "sourcePolicy", "signature", "manifest"):
    if field not in required_registry_fields:
        missing.append(f"docs/reference/plugin-publishing-ux.json: p9PublishHardening requiredRegistryFields missing {field!r}")

p13_hardening = manifest.get("p13PublishHardening") or {}
if p13_hardening.get("schema") != "gofly.plugin_publish_hardening_p13.v1":
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening schema mismatch")
if p13_hardening.get("aiflowTask") != "GOFLY-P13-10-PLUGIN-TEMPLATE-PUBLISH-HARDENING":
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening aiflowTask mismatch")
if p13_hardening.get("status") != "blocking":
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening status must be blocking")
if set(p13_hardening.get("acceptanceGates") or []) != {
    "make plugin-conformance-check",
    "go test -C examples/plugin-ecosystem ./...",
    "go run -C examples/plugin-ecosystem .",
}:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening acceptanceGates mismatch")
required_p13_registry = {"checksum", "source", "sourcePolicy", "signature", "manifest"}
required_p13_manifest = {"compatibleVersions", "capabilities", "permissions", "requiresDryRun"}
required_p13_template = {"schema", "contractVersion", "entrypoints", "permissions", "checksum", "source"}
required_p13_failures = {"digest-mismatch", "malicious-path", "permission-escape", "no-partial-writes"}
required_p13_blockers = {"old-protocol", "future-only", "digest-mismatch", "malicious-path", "permission-escape", "no-partial-writes"}
if set(p13_hardening.get("requiredRegistryFields") or []) != required_p13_registry:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening requiredRegistryFields mismatch")
if set(p13_hardening.get("requiredManifestFields") or []) != required_p13_manifest:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening requiredManifestFields mismatch")
if set(p13_hardening.get("requiredTemplateFields") or []) != required_p13_template:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening requiredTemplateFields mismatch")
if set(p13_hardening.get("failureCases") or []) != required_p13_failures:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening failureCases mismatch")
if set(p13_hardening.get("publishBlockers") or []) != required_p13_blockers:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening publishBlockers mismatch")
p13_source_allowlist = p13_hardening.get("sourceAllowlist") or {}
if set(p13_source_allowlist.get("allowedHosts") or []) != {"github.com"}:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening sourceAllowlist.allowedHosts mismatch")
if p13_source_allowlist.get("httpsOnly") is not True:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening sourceAllowlist.httpsOnly must be true")
if p13_source_allowlist.get("registryField") != "sourcePolicy":
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening sourceAllowlist.registryField mismatch")
if set(p13_hardening.get("signatureTrustSources") or []) != {"github-actions-oidc"}:
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening signatureTrustSources mismatch")
if p13_hardening.get("exampleOutput") != "examples/plugin-ecosystem p13Publishing":
    missing.append("docs/reference/plugin-publishing-ux.json: p13PublishHardening exampleOutput mismatch")
for field in ("publisherAction", "rollbackOrEscalation"):
    if len(str(p13_hardening.get(field) or "").split()) < 14:
        missing.append(f"docs/reference/plugin-publishing-ux.json: p13PublishHardening {field} must be actionable")

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
    if template_data.get("contractVersion") != "1":
        missing.append(f"{template_path}: contractVersion must be 1")
    if template_data.get("protocol") != "1":
        missing.append(f"{template_path}: protocol must be 1")

registry_path = pathlib.Path("examples/plugin-ecosystem/registry/plugins.json")
if not registry_path.is_file():
    missing.append("examples/plugin-ecosystem/registry/plugins.json: file is missing")
    registry = {}
else:
    registry = json.loads(registry_path.read_text(encoding="utf-8"))
registry_plugins = registry.get("plugins") or []
for plugin in registry_plugins:
    if not isinstance(plugin, dict):
        missing.append(f"registry plugin entry must be an object: {plugin!r}")
        continue
    plugin_name = plugin.get("name", "<missing>")
    for field in ("checksum", "source", "sourcePolicy", "signature", "manifest"):
        if field not in plugin:
            missing.append(f"registry plugin {plugin_name}: missing {field!r}")
    checksum = str(plugin.get("checksum") or "")
    if not checksum.startswith("sha256:") or len(checksum) != len("sha256:") + 64:
        missing.append(f"registry plugin {plugin_name}: checksum must be sha256:<64 hex>")
    source = str(plugin.get("source") or "")
    if not source.startswith("https://github.com/"):
        missing.append(f"registry plugin {plugin_name}: source must use https://github.com allowlist")
    source_policy = plugin.get("sourcePolicy") or {}
    if set(source_policy.get("allowedHosts") or []) != {"github.com"}:
        missing.append(f"registry plugin {plugin_name}: sourcePolicy.allowedHosts must be github.com")
    if source_policy.get("httpsOnly") is not True:
        missing.append(f"registry plugin {plugin_name}: sourcePolicy.httpsOnly must be true")
    signature = plugin.get("signature") or {}
    if signature.get("trustSource") != "github-actions-oidc":
        missing.append(f"registry plugin {plugin_name}: signature.trustSource must be github-actions-oidc")
    for field in ("provenance", "bundle"):
        if not str(signature.get(field) or "").startswith("https://github.com/"):
            missing.append(f"registry plugin {plugin_name}: signature.{field} must use github release evidence")
    manifest_data = plugin.get("manifest") or {}
    if manifest_data.get("requiresDryRun") is not True:
        missing.append(f"registry plugin {plugin_name}: manifest.requiresDryRun must be true")
    if set(manifest_data.get("permissions") or []) != {"filesystem:write-relative"}:
        missing.append(f"registry plugin {plugin_name}: manifest permissions must be filesystem:write-relative only")

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

p9_report = report.get("p9PublishHardening") or {}
if p9_report.get("schema") != "gofly.plugin_publish_hardening_report.v1":
    missing.append("docs/reference/plugin-conformance-report.json: p9PublishHardening schema mismatch")
if p9_report.get("aiflowTask") != "GOFLY-GOV-10P9-07":
    missing.append("docs/reference/plugin-conformance-report.json: p9PublishHardening aiflowTask mismatch")
if p9_report.get("status") != "blocking-contract":
    missing.append("docs/reference/plugin-conformance-report.json: p9PublishHardening status must be blocking-contract")
if p9_report.get("acceptanceGate") != "make plugin-conformance-check":
    missing.append("docs/reference/plugin-conformance-report.json: p9PublishHardening acceptanceGate mismatch")
if p9_report.get("source") != "docs/reference/plugin-publishing-ux.json":
    missing.append("docs/reference/plugin-conformance-report.json: p9PublishHardening source mismatch")
p9_rows = {
    item.get("id"): item
    for item in p9_report.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p9_rows = {
    "compatibility-policy": {"old-protocol", "current-protocol", "future-plus-current-protocol", "future-only-protocol"},
    "signature-trust-source": {"signature.trustSource", "signature.provenance", "signature.bundle"},
    "digest-pinning": {"registry.checksum", "digest-mismatch"},
    "source-allowlist": {"sourcePolicy.allowedHosts", "sourcePolicy.httpsOnly"},
    "template-contract-versioning": {"template.contractVersion", "template.schema", "template.entrypoints"},
    "no-partial-write-rollback": {"no-partial-writes", "TestPluginResponseApplyRejectsPartialWritesWhenPatchFails"},
}
if set(p9_rows) != set(expected_p9_rows):
    missing.append(
        "docs/reference/plugin-conformance-report.json: p9PublishHardening rows drifted "
        f"missing={sorted(set(expected_p9_rows) - set(p9_rows))} "
        f"extra={sorted(set(p9_rows) - set(expected_p9_rows))}"
    )
for row_id, expected_evidence in expected_p9_rows.items():
    row = p9_rows.get(row_id) or {}
    for field in ("id", "surface", "requiredEvidence", "blockDecision", "rollbackOrEscalation"):
        if row.get(field) in ("", None, []):
            missing.append(f"docs/reference/plugin-conformance-report.json: p9PublishHardening {row_id}: {field} is required")
    evidence = set(row.get("requiredEvidence") or [])
    for required in expected_evidence:
        if required not in evidence:
            missing.append(f"docs/reference/plugin-conformance-report.json: p9PublishHardening {row_id}: missing evidence {required!r}")
    for field in ("blockDecision", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 8:
            missing.append(f"docs/reference/plugin-conformance-report.json: p9PublishHardening {row_id}: {field} must be actionable")

p13_report = report.get("p13PublishHardening") or {}
if p13_report.get("schema") != "gofly.plugin_publish_hardening_p13_report.v1":
    missing.append("docs/reference/plugin-conformance-report.json: p13PublishHardening schema mismatch")
if p13_report.get("aiflowTask") != "GOFLY-P13-10-PLUGIN-TEMPLATE-PUBLISH-HARDENING":
    missing.append("docs/reference/plugin-conformance-report.json: p13PublishHardening aiflowTask mismatch")
if p13_report.get("status") != "blocking":
    missing.append("docs/reference/plugin-conformance-report.json: p13PublishHardening status must be blocking")
if set(p13_report.get("acceptanceGates") or []) != set(p13_hardening.get("acceptanceGates") or []):
    missing.append("docs/reference/plugin-conformance-report.json: p13PublishHardening acceptanceGates must match plugin-publishing-ux.json")
if p13_report.get("source") != "docs/reference/plugin-publishing-ux.json":
    missing.append("docs/reference/plugin-conformance-report.json: p13PublishHardening source mismatch")
if p13_report.get("exampleOutput") != p13_hardening.get("exampleOutput"):
    missing.append("docs/reference/plugin-conformance-report.json: p13PublishHardening exampleOutput mismatch")
p13_rows = {
    item.get("id"): item
    for item in p13_report.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p13_rows = {
    "registry-manifest-contract": {
        "registry.checksum",
        "registry.source",
        "registry.sourcePolicy",
        "registry.signature",
        "registry.manifest",
        "manifest.compatibleVersions",
        "manifest.permissions",
        "manifest.requiresDryRun",
    },
    "source-signature-digest-contract": {
        "sourcePolicy.allowedHosts",
        "sourcePolicy.httpsOnly",
        "signature.trustSource",
        "signature.provenance",
        "registry.checksum",
        "digest-mismatch",
    },
    "template-versioning-contract": {
        "template.schema",
        "template.contractVersion",
        "template.entrypoints",
        "template.permissions",
        "template.checksum",
        "template.source",
    },
    "failure-isolation-contract": {
        "digest-mismatch",
        "malicious-path",
        "permission-escape",
        "no-partial-writes",
        "TestPluginResponseApplyRejectsPartialWritesWhenPatchFails",
    },
}
if set(p13_rows) != set(expected_p13_rows):
    missing.append(
        "docs/reference/plugin-conformance-report.json: p13PublishHardening rows drifted "
        f"missing={sorted(set(expected_p13_rows) - set(p13_rows))} "
        f"extra={sorted(set(p13_rows) - set(expected_p13_rows))}"
    )
for row_id, expected_evidence in expected_p13_rows.items():
    row = p13_rows.get(row_id) or {}
    for field in ("id", "surface", "requiredEvidence", "blockDecision", "rollbackOrEscalation"):
        if row.get(field) in ("", None, []):
            missing.append(f"docs/reference/plugin-conformance-report.json: p13PublishHardening {row_id}: {field} is required")
    evidence = set(row.get("requiredEvidence") or [])
    for required in expected_evidence:
        if required not in evidence:
            missing.append(f"docs/reference/plugin-conformance-report.json: p13PublishHardening {row_id}: missing evidence {required!r}")
    for field in ("blockDecision", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 10:
            missing.append(f"docs/reference/plugin-conformance-report.json: p13PublishHardening {row_id}: {field} must be actionable")
for field in ("publisherAction", "rollbackOrEscalation"):
    if len(str(p13_report.get(field) or "").split()) < 14:
        missing.append(f"docs/reference/plugin-conformance-report.json: p13PublishHardening {field} must be actionable")

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

example = subprocess.run(
    ["go", "run", "-C", "examples/plugin-ecosystem", "."],
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.STDOUT,
)
if example.returncode != 0:
    missing.append("examples/plugin-ecosystem runnable report failed:\n" + example.stdout)
else:
    try:
        example_report = json.loads(example.stdout)
    except json.JSONDecodeError as exc:
        missing.append(f"examples/plugin-ecosystem emitted invalid JSON: {exc}")
        example_report = {}
    if example_report.get("schema") != "gofly.plugin_ecosystem.v1":
        missing.append("examples/plugin-ecosystem schema mismatch")
    p13_example = example_report.get("p13Publishing") or {}
    if p13_example.get("schema") != p13_hardening.get("schema"):
        missing.append("examples/plugin-ecosystem p13Publishing schema mismatch")
    if p13_example.get("aiflowTask") != p13_hardening.get("aiflowTask"):
        missing.append("examples/plugin-ecosystem p13Publishing aiflowTask mismatch")
    if p13_example.get("status") != p13_hardening.get("status"):
        missing.append("examples/plugin-ecosystem p13Publishing status mismatch")
    if set(p13_example.get("requiredRegistry") or []) != set(p13_hardening.get("requiredRegistryFields") or []):
        missing.append("examples/plugin-ecosystem p13Publishing requiredRegistry mismatch")
    if set(p13_example.get("requiredManifest") or []) != set(p13_hardening.get("requiredManifestFields") or []):
        missing.append("examples/plugin-ecosystem p13Publishing requiredManifest mismatch")
    if set(p13_example.get("requiredTemplate") or []) != set(p13_hardening.get("requiredTemplateFields") or []):
        missing.append("examples/plugin-ecosystem p13Publishing requiredTemplate mismatch")
    if set(p13_example.get("failureCases") or []) != set(p13_hardening.get("failureCases") or []):
        missing.append("examples/plugin-ecosystem p13Publishing failureCases mismatch")
    if set(p13_example.get("sourceAllowlist") or []) != set((p13_hardening.get("sourceAllowlist") or {}).get("allowedHosts") or []):
        missing.append("examples/plugin-ecosystem p13Publishing sourceAllowlist mismatch")
    if set(p13_example.get("signatureTrust") or []) != set(p13_hardening.get("signatureTrustSources") or []):
        missing.append("examples/plugin-ecosystem p13Publishing signatureTrust mismatch")
    if set(p13_example.get("publishBlockers") or []) != set(p13_hardening.get("publishBlockers") or []):
        missing.append("examples/plugin-ecosystem p13Publishing publishBlockers mismatch")
    if len(str(p13_example.get("noPartialWritePolicy") or "").split()) < 10:
        missing.append("examples/plugin-ecosystem p13Publishing noPartialWritePolicy must be actionable")

if missing:
    print("plugin conformance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("plugin conformance governance ok")
PY
