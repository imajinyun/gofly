#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "dependency-upgrade-evidence.json"
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


def workflow_job_body(workflow, job_id):
    match = re.search(
        rf"\n\s{{2}}{re.escape(job_id)}:\n(?P<body>.*?)(?:\n\s{{2}}[A-Za-z0-9_-]+:|\Z)",
        workflow,
        re.S,
    )
    require(match is not None, f"ci.yml job {job_id!r} is missing")
    return match.group("body") if match else ""


if not manifest_path.is_file():
    missing.append("docs/reference/dependency-upgrade-evidence.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.dependency_upgrade_evidence.v1":
    missing.append("dependency upgrade evidence schema must be gofly.dependency_upgrade_evidence.v1")

makefile = read_text(root / "Makefile")
workflow = read_text(root / ".github" / "workflows" / "ci.yml")
check_body = make_target_body(makefile, "dependency-upgrade-evidence-check")
upgrade_body = make_target_body(makefile, "dependency-upgrade-check")
mod_verify_body = make_target_body(makefile, "mod-verify")
root_policy_body = make_target_body(makefile, "root-dependency-policy-check")
docs_deps = make_target_deps(makefile, "docs-check")
upgrade_deps = make_target_deps(makefile, "dependency-upgrade-check")
ci_job = workflow_job_body(workflow, "dependency-upgrade-validation")

require(
    "check-dependency-upgrade-evidence.sh" in check_body,
    "dependency-upgrade-evidence-check must call check-dependency-upgrade-evidence.sh",
)
require(
    "dependency-upgrade-evidence-check" in docs_deps,
    "docs-check must depend on dependency-upgrade-evidence-check",
)
require(
    "dependency-upgrade-evidence-check" in upgrade_deps,
    "dependency-upgrade-check must depend on dependency-upgrade-evidence-check",
)
for dep in ("root-dependency-policy-check", "mod-verify", "govulncheck"):
    require(dep in upgrade_deps, f"dependency-upgrade-check must depend on {dep}")
require("$(GO) mod verify" in mod_verify_body, "mod-verify must run go mod verify")
require(
    "check-root-dependency-policy.sh" in root_policy_body,
    "root-dependency-policy-check must call check-root-dependency-policy.sh",
)

expected_commands = {
    "localGate": "make dependency-upgrade-check",
    "evidenceGate": "make dependency-upgrade-evidence-check",
    "ciGate": "make dependency-upgrade-check DEPENDENCY_UPGRADE_RUN_INTEGRATION=false",
    "moduleVerification": "go mod verify",
    "vulnerabilityScan": "make govulncheck",
    "integrationGate": "make integration-tests",
}
commands = manifest.get("commands") or {}
for key, value in expected_commands.items():
    if commands.get(key) != value:
        missing.append(f"commands.{key} = {commands.get(key)!r}, want {value!r}")

evidence = manifest.get("evidence") or []
required_evidence = {
    "module-verification": "go mod verify",
    "vulnerability-scan": "make govulncheck",
    "docker-backed-integration": "make integration-tests",
    "root-dependency-policy": "make root-dependency-policy-check",
}
seen = set()
for item in evidence:
    if not isinstance(item, dict):
        missing.append(f"evidence entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    seen.add(item_id)
    if item_id in required_evidence and item.get("command") != required_evidence[item_id]:
        missing.append(f"evidence {item_id}: command = {item.get('command')!r}, want {required_evidence[item_id]!r}")
    for field in ("owner", "requiredWhen", "artifact"):
        if not item.get(field):
            missing.append(f"evidence {item_id or '<missing>'}: {field} is required")
for item_id in required_evidence:
    if item_id not in seen:
        missing.append(f"evidence missing {item_id!r}")

ownership = manifest.get("ownership")
if not isinstance(ownership, list):
    missing.append("ownership must be a list")
    ownership = []
required_ownership = {
    "root-runtime-dependencies",
    "generated-project-dependencies",
    "toolchain-and-go-tools",
    "docker-backed-integration-dependencies",
}
ownership_ids = set()
for item in ownership:
    if not isinstance(item, dict):
        missing.append(f"ownership entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    if not item_id:
        missing.append("ownership id is required")
    elif item_id in ownership_ids:
        missing.append(f"duplicate ownership id: {item_id}")
    ownership_ids.add(item_id)
    for field in (
        "id",
        "owner",
        "scope",
        "allowedLocation",
        "upgradeTrigger",
        "requiredEvidence",
        "integrationDelegation",
        "rollbackGuidance",
    ):
        if not item.get(field):
            missing.append(f"ownership {item_id or '<missing>'}: {field} is required")
    evidence_refs = set(item.get("requiredEvidence") or [])
    unknown_refs = evidence_refs - set(required_evidence)
    if unknown_refs:
        missing.append(f"ownership {item_id}: unknown requiredEvidence {sorted(unknown_refs)!r}")
    for field in ("upgradeTrigger", "integrationDelegation", "rollbackGuidance"):
        require(len(str(item.get(field) or "").split()) >= 8, f"ownership {item_id}: {field} must be actionable")
require(ownership_ids == required_ownership, f"ownership ids drifted: missing={sorted(required_ownership - ownership_ids)} extra={sorted(ownership_ids - required_ownership)}")

delegation = manifest.get("ciDelegation") or {}
if delegation.get("job") != "dependency-upgrade-validation":
    missing.append("ciDelegation.job must be dependency-upgrade-validation")
if delegation.get("integrationMatrixJob") != "integration tests (${ matrix.area })":
    missing.append("ciDelegation.integrationMatrixJob must be integration tests (${ matrix.area })")
if delegation.get("skipToggle") != "DEPENDENCY_UPGRADE_RUN_INTEGRATION=false":
    missing.append("ciDelegation.skipToggle must be DEPENDENCY_UPGRADE_RUN_INTEGRATION=false")
if delegation.get("reason") != "avoid duplicate Docker-backed dependency startup cost":
    missing.append("ciDelegation.reason must explain duplicate Docker-backed dependency startup cost")

for needle in [
    "$(MAKE) integration-tests",
    "Skipping integration-tests here; required CI integration matrix provides Docker-backed coverage.",
]:
    if needle not in upgrade_body:
        missing.append(f"dependency-upgrade-check missing {needle!r}")
for needle in [
    "make dependency-upgrade-check DEPENDENCY_UPGRADE_RUN_INTEGRATION=false",
    "Docker-backed integration coverage is delegated to the required integration matrix",
    "No go.mod/go.sum changes detected",
]:
    if needle not in ci_job:
        missing.append(f"dependency-upgrade-validation job missing {needle!r}")

docs = {
    root / "docs" / "reference" / "dependency-upgrade-evidence.md": [
        "gofly.dependency_upgrade_evidence.v1",
        "make dependency-upgrade-check",
        "make dependency-upgrade-evidence-check",
        "DEPENDENCY_UPGRADE_RUN_INTEGRATION=false",
        "root-dependency-policy",
        "ownership",
        "generated-project-dependencies",
    ],
    root / "docs" / "operations" / "production-checklist.md": [
        "make dependency-upgrade-evidence-check",
        "dependency-upgrade-evidence.json",
    ],
    root / "docs" / "operations" / "security.md": [
        "make dependency-upgrade-check",
        "dependency-upgrade-evidence.json",
    ],
}
for path, needles in docs.items():
    text = read_text(path)
    for needle in needles:
        if needle not in text:
            missing.append(f"{path.relative_to(root)}: missing {needle!r}")

if missing:
    print("dependency upgrade evidence check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("dependency upgrade evidence governance ok")
PY
