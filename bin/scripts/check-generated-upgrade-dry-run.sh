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

makefile = read_text(root / "Makefile")
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
