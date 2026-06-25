#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "deprecation-lifecycle.json"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def make_target_body(makefile, target):
    match = re.search(
        rf"^\.PHONY:\s*{re.escape(target)}\n"
        rf"{re.escape(target)}:.*?\n(?P<body>.*?)(?=^\.PHONY:|\Z)",
        makefile,
        re.S | re.M,
    )
    require(match is not None, f"Makefile target {target!r} is missing")
    return match.group("body") if match else ""


def make_target_deps(makefile, target):
    match = re.search(rf"^{re.escape(target)}:(?P<deps>[^#\n]*)", makefile, re.M)
    require(match is not None, f"Makefile target {target!r} is missing")
    return match.group("deps") if match else ""


if not manifest_path.is_file():
    missing.append("docs/reference/deprecation-lifecycle.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.deprecation_lifecycle.v1":
    missing.append("deprecation lifecycle schema must be gofly.deprecation_lifecycle.v1")

makefile = read_text(root / "Makefile")
check_body = make_target_body(makefile, "deprecation-lifecycle-check")
contract_deps = make_target_deps(makefile, "contract-docs-check")
require("check-deprecation-lifecycle.sh" in check_body, "deprecation-lifecycle-check must call check-deprecation-lifecycle.sh")
require("deprecation-lifecycle-check" in contract_deps, "contract-docs-check must depend on deprecation-lifecycle-check")

policy = manifest.get("policy") or {}
for field in [
    "requiredClassification",
    "minimumCoexistenceWindow",
    "securityExceptionRequiredFields",
]:
    require(field in policy, f"policy.{field} is required")
if policy.get("minimumCoexistenceWindow") != "one minor release line":
    missing.append("policy.minimumCoexistenceWindow must be one minor release line")
for classification in ["compatible-addition", "behavioral-fix", "deprecation", "breaking-candidate"]:
    if classification not in policy.get("requiredClassification", []):
        missing.append(f"policy.requiredClassification missing {classification!r}")

support_lifecycle = manifest.get("supportLifecycle")
if not isinstance(support_lifecycle, list):
    missing.append("supportLifecycle must be a list")
    support_lifecycle = []

required_support_ids = {
    "rest-v1-candidate",
    "governance-controlplane-v1-candidate",
    "cli-json-v1-candidate",
    "generated-production-service-v1-candidate",
    "rpc-gateway-app-tier1-candidates",
}
required_support_fields = {
    "id",
    "surface",
    "tier",
    "owner",
    "supportWindow",
    "compatibilityClass",
    "sunsetTrigger",
    "releaseNoteEvidence",
    "validationGate",
    "rollbackGuidance",
}
support_ids = set()
support_classes = set()
for item in support_lifecycle:
    if not isinstance(item, dict):
        missing.append(f"supportLifecycle entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    if not item_id:
        missing.append("supportLifecycle id is required")
    elif item_id in support_ids:
        missing.append(f"duplicate supportLifecycle id: {item_id}")
    support_ids.add(item_id)
    missing_fields = sorted(required_support_fields - set(item))
    if missing_fields:
        missing.append(f"supportLifecycle {item_id or '<missing>'} missing fields: {missing_fields}")
    support_classes.add(item.get("compatibilityClass", ""))
    gate = item.get("validationGate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"supportLifecycle {item_id}: validationGate target {target!r} missing")
    else:
        missing.append(f"supportLifecycle {item_id}: validationGate must be a make target")
    evidence = item.get("releaseNoteEvidence", "")
    if evidence.startswith("docs/"):
        evidence_path = evidence.split("#", 1)[0]
        require((root / evidence_path).exists(), f"supportLifecycle {item_id}: releaseNoteEvidence path missing: {evidence_path}")
    else:
        missing.append(f"supportLifecycle {item_id}: releaseNoteEvidence must reference docs/")
    for field in ("sunsetTrigger", "rollbackGuidance"):
        require(len(str(item.get(field) or "").split()) >= 10, f"supportLifecycle {item_id}: {field} must be actionable")
require(required_support_ids <= support_ids, f"supportLifecycle missing ids: {sorted(required_support_ids - support_ids)!r}")
require({"stable", "evolving"} <= support_classes, "supportLifecycle must include stable and evolving compatibility classes")

active = manifest.get("activeDeprecations")
if not isinstance(active, list):
    missing.append("activeDeprecations must be a list")
    active = []

required_deprecation_fields = {
    "id",
    "surface",
    "oldSurface",
    "replacement",
    "firstDeprecatedVersion",
    "minimumRemovalVersion",
    "coexistenceWindow",
    "rollbackGuidance",
    "validationGate",
    "releaseNoteClassification",
}
ids = set()
for item in active:
    if not isinstance(item, dict):
        missing.append(f"activeDeprecations entry must be an object: {item!r}")
        continue
    missing_fields = sorted(required_deprecation_fields - set(item))
    if missing_fields:
        missing.append(f"active deprecation {item.get('id', '<missing>')} missing fields: {missing_fields}")
    item_id = item.get("id", "")
    if not item_id:
        missing.append("active deprecation id is required")
    elif item_id in ids:
        missing.append(f"duplicate active deprecation id: {item_id}")
    ids.add(item_id)
    if item.get("releaseNoteClassification") != "deprecation":
        missing.append(f"{item_id}: releaseNoteClassification must be deprecation")
    if item.get("coexistenceWindow") != "one minor release line":
        missing.append(f"{item_id}: coexistenceWindow must be one minor release line")

declared_markers = set(manifest.get("deprecatedMarkers") or [])
actual_markers = set()
for path in root.rglob("*.go"):
    rel = path.relative_to(root)
    parts = set(rel.parts)
    if ".git" in parts or ".harness" in parts or ".tmp-test" in parts or "vendor" in parts:
        continue
    if rel.name.endswith("_test.go"):
        continue
    text = path.read_text(encoding="utf-8")
    for line in text.splitlines():
        if "Deprecated:" in line:
            actual_markers.add(str(rel))

if declared_markers != actual_markers:
    missing.append(
        "deprecatedMarkers must match production Go files with Deprecated: markers; "
        f"declared={sorted(declared_markers)!r} actual={sorted(actual_markers)!r}"
    )

if active and not actual_markers:
    missing.append("activeDeprecations is non-empty but no production Go Deprecated: marker was found")
if actual_markers and not active:
    missing.append("production Go Deprecated: markers require activeDeprecations entries")

docs = {
    root / "docs" / "reference" / "deprecation-lifecycle.md": [
        "gofly.deprecation_lifecycle.v1",
        "make deprecation-lifecycle-check",
        "activeDeprecations",
        "supportLifecycle",
        "supportWindow",
        "sunsetTrigger",
        "one minor release line",
        "rollbackGuidance",
    ],
    root / "docs" / "reference" / "stable-surface.md": [
        "deprecation-lifecycle.json",
        "make deprecation-lifecycle-check",
        "activeDeprecations",
        "supportLifecycle",
    ],
    root / "docs" / "reference" / "compatibility.md": [
        "deprecation-lifecycle.json",
        "make deprecation-lifecycle-check",
    ],
    root / "docs" / "releases" / "stable.md": [
        "make deprecation-lifecycle-check",
        "gofly.deprecation_lifecycle.v1",
    ],
}
for path, needles in docs.items():
    text = read_text(path)
    for needle in needles:
        if needle not in text:
            missing.append(f"{path.relative_to(root)}: missing {needle!r}")

if missing:
    print("deprecation lifecycle check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("deprecation lifecycle governance ok")
PY
