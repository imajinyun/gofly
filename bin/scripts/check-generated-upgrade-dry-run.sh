#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "generated-upgrade-dry-run.json"
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


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/generated-upgrade-dry-run.json is missing")

require(
    manifest.get("schema") == "gofly.generated_upgrade_dry_run.v1",
    "generated upgrade dry-run schema must be gofly.generated_upgrade_dry_run.v1",
)

policy = manifest.get("artifactPolicy") or {}
require(
    policy.get("commitGeneratedRuntimeArtifacts") is False,
    "artifactPolicy.commitGeneratedRuntimeArtifacts must be false",
)
volatile_dirs = set(policy.get("volatileDirectories") or [])
for directory in (".tmp-test/generated-upgrade-dry-run", "$TMPDIR/gofly-generated-upgrade-*"):
    require(directory in volatile_dirs, f"artifactPolicy.volatileDirectories missing {directory!r}")

contract = manifest.get("diffReportContract") or {}
categories = contract.get("categories") or []
category_names = {item.get("category") for item in categories if isinstance(item, dict)}
required_categories = {
    "deterministic-repeat-generation",
    "compatible-addition",
    "formatting-only",
    "breaking-candidate",
}
require(category_names == required_categories, f"diffReportContract categories mismatch: {sorted(category_names)!r}")
for item in categories:
    if not isinstance(item, dict):
        missing.append(f"diffReportContract category must be an object: {item!r}")
        continue
    category = item.get("category", "<missing>")
    for field in ("description", "requiresRollbackNote", "severity"):
        require(field in item and item[field] not in ("", None), f"category {category}: {field} is required")
    require(item.get("severity") in {"pass-or-fail", "review", "block"}, f"category {category}: unexpected severity")

required_fields = set(contract.get("requiredFields") or [])
for field in ("profile", "repeatGeneration", "categories", "summary", "rollbackNote"):
    require(field in required_fields, f"diffReportContract.requiredFields missing {field!r}")

profiles = manifest.get("profiles") or []
profile_names = {item.get("profile") for item in profiles if isinstance(item, dict)}
require(profile_names == {"old", "current", "future"}, f"profiles mismatch: {sorted(profile_names)!r}")

for profile in profiles:
    if not isinstance(profile, dict):
        missing.append(f"profile entry must be an object: {profile!r}")
        continue
    name = profile.get("profile", "<missing>")
    for field in ("api", "proto", "serviceConfig"):
        rel = profile.get(field)
        require(bool(rel), f"profile {name}: {field} is required")
        if rel:
            require((root / rel).is_file(), f"profile {name}: {field} path is missing: {rel}")
    plugin = profile.get("pluginProfile") or {}
    registry = plugin.get("registry")
    require(plugin.get("protocol") == "1", f"profile {name}: plugin protocol must be 1")
    require(bool(plugin.get("compatibility")), f"profile {name}: plugin compatibility is required")
    require(bool(registry), f"profile {name}: plugin registry is required")
    if registry:
        require((root / registry).is_file(), f"profile {name}: plugin registry path is missing: {registry}")

    snapshot = profile.get("generatedSnapshot") or {}
    require(bool(snapshot.get("expectedDiff")), f"profile {name}: generatedSnapshot.expectedDiff is required")
    metadata = snapshot.get("metadata") or {}
    for key in ("generated.project.contract", "generated.project.runtime"):
        require(bool(metadata.get(key)), f"profile {name}: generatedSnapshot.metadata.{key} is required")

    diff_report = profile.get("diffReport") or {}
    require(diff_report.get("repeatGeneration") == "must-pass", f"profile {name}: diffReport.repeatGeneration must be must-pass")
    require(bool(diff_report.get("summary")), f"profile {name}: diffReport.summary is required")
    require(bool(diff_report.get("rollbackNote")), f"profile {name}: diffReport.rollbackNote is required")
    diff_categories = set(diff_report.get("categories") or [])
    require(
        "deterministic-repeat-generation" in diff_categories,
        f"profile {name}: diffReport.categories must include deterministic-repeat-generation",
    )
    unknown = diff_categories - required_categories
    require(not unknown, f"profile {name}: unknown diff categories: {sorted(unknown)!r}")

matrix_rel = manifest.get("migrationFidelityMatrix")
require(matrix_rel == "docs/reference/migration-fidelity-matrix.json", "migrationFidelityMatrix must point to docs/reference/migration-fidelity-matrix.json")
matrix_path = root / matrix_rel if matrix_rel else root / "docs" / "reference" / "migration-fidelity-matrix.json"
if matrix_path.is_file():
    matrix = json.loads(matrix_path.read_text(encoding="utf-8"))
else:
    matrix = {}
    missing.append("docs/reference/migration-fidelity-matrix.json is missing")

require(
    matrix.get("schema") == "gofly.generated_migration_fidelity.v1",
    "migration fidelity matrix schema must be gofly.generated_migration_fidelity.v1",
)
require(
    matrix.get("sourceOfTruth") == "docs/reference/generated-upgrade-dry-run.json",
    "migration fidelity matrix sourceOfTruth must be docs/reference/generated-upgrade-dry-run.json",
)
require(
    matrix.get("acceptanceGate") == "make generated-upgrade-dry-run-check",
    "migration fidelity matrix acceptanceGate must be make generated-upgrade-dry-run-check",
)
required_path_fields = set(matrix.get("requiredPathFields") or [])
for field in (
    "framework",
    "example",
    "docs",
    "dryRunProfile",
    "deterministicRegeneration",
    "diffCategories",
    "smokeGates",
    "rollbackNote",
    "compatibilityCaveat",
):
    require(field in required_path_fields, f"migration fidelity requiredPathFields missing {field!r}")

paths = matrix.get("paths") or []
frameworks = {item.get("framework") for item in paths if isinstance(item, dict)}
require(frameworks == {"gin", "go-zero", "kratos", "kitex"}, f"migration fidelity frameworks mismatch: {sorted(frameworks)!r}")
for item in paths:
    if not isinstance(item, dict):
        missing.append(f"migration fidelity path must be an object: {item!r}")
        continue
    framework = item.get("framework", "<missing>")
    for field in required_path_fields:
        require(field in item and item[field] not in ("", None, [], {}), f"migration fidelity {framework}: {field} is required")
    example = item.get("example")
    if example:
        require((root / example).is_dir(), f"migration fidelity {framework}: example path is missing: {example}")
    for doc_path in item.get("docs") or []:
        require((root / doc_path).is_file(), f"migration fidelity {framework}: doc path is missing: {doc_path}")
    require(item.get("dryRunProfile") in profile_names, f"migration fidelity {framework}: dryRunProfile must reference generated upgrade profile")
    require(item.get("deterministicRegeneration") == "required", f"migration fidelity {framework}: deterministicRegeneration must be required")
    diff_categories = set(item.get("diffCategories") or [])
    require("deterministic-repeat-generation" in diff_categories, f"migration fidelity {framework}: deterministic diff category is required")
    unknown = diff_categories - required_categories
    require(not unknown, f"migration fidelity {framework}: unknown diff categories: {sorted(unknown)!r}")
    require(any("make " in gate or gate.startswith("go test ") or gate.startswith("go run ") for gate in item.get("smokeGates") or []), f"migration fidelity {framework}: smokeGates must include runnable commands")
    require(len(item.get("rollbackNote", "")) >= 24, f"migration fidelity {framework}: rollbackNote is too short")
    require(len(item.get("compatibilityCaveat", "")) >= 24, f"migration fidelity {framework}: compatibilityCaveat is too short")

makefile = read_text(root / "Makefile")
rehearsal = manifest.get("upgradeRehearsal") or {}
require(rehearsal.get("schema") == "gofly.upgrade_rehearsal.v1", "upgrade rehearsal schema must be gofly.upgrade_rehearsal.v1")
require(rehearsal.get("source") == "docs/reference/generated-upgrade-dry-run.json", "upgrade rehearsal source mismatch")
require(rehearsal.get("acceptanceGate") == "make generated-upgrade-dry-run-check", "upgrade rehearsal acceptanceGate mismatch")
steps = rehearsal.get("steps") or []
required_step_ids = {
    "inventory-current-project",
    "regenerate-dry-run",
    "dependency-boundary-review",
    "release-evidence-review",
    "adopter-smoke-and-rollback",
}
actual_step_ids = {item.get("id") for item in steps if isinstance(item, dict)}
require(actual_step_ids == required_step_ids, f"upgrade rehearsal steps mismatch: {sorted(actual_step_ids)!r}")
seen_phases = set()
for item in steps:
    if not isinstance(item, dict):
        missing.append(f"upgrade rehearsal step must be an object: {item!r}")
        continue
    step = item.get("id", "<missing>")
    for field in ("id", "phase", "evidence", "gate", "expectedOutput", "failureClass", "rollbackOrEscalation"):
        require(bool(item.get(field)), f"upgrade rehearsal {step}: {field} is required")
    seen_phases.add(item.get("phase", ""))
    gate = item.get("gate", "")
    require(gate.startswith("make "), f"upgrade rehearsal {step}: gate must be a make target")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        require(re.search(rf"^{re.escape(target)}:", makefile, re.M), f"upgrade rehearsal {step}: gate target {target!r} missing")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"upgrade rehearsal {step}: evidence path is missing: {evidence}")
    require(item.get("failureClass") in {"candidate", "rollback-required"}, f"upgrade rehearsal {step}: failureClass must be candidate or rollback-required")
    for field in ("expectedOutput", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 8, f"upgrade rehearsal {step}: {field} must be actionable")
require({"baseline", "generation", "dependency", "release", "verification"} <= seen_phases, "upgrade rehearsal must cover baseline, generation, dependency, release, and verification phases")

target_body = make_target_body(makefile, "generated-upgrade-dry-run-check")
contract_deps = make_target_deps(makefile, "contract-docs-check")
require(
    "check-generated-upgrade-dry-run.sh" in target_body,
    "generated-upgrade-dry-run-check must call check-generated-upgrade-dry-run.sh",
)
require(
    "generated-upgrade-dry-run-check" in contract_deps,
    "contract-docs-check must depend on generated-upgrade-dry-run-check",
)

doc = read_text(root / "docs" / "reference" / "generated-upgrade-dry-run.md")
for needle in (
    "gofly.generated_upgrade_dry_run.v1",
    "make generated-upgrade-dry-run-check",
    "deterministic-repeat-generation",
    "compatible-addition",
    "formatting-only",
    "breaking-candidate",
    "rollbackNote",
):
    require(needle in doc, f"docs/reference/generated-upgrade-dry-run.md missing {needle!r}")

if missing:
    print("generated upgrade dry-run check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("generated upgrade dry-run governance ok")
PY
