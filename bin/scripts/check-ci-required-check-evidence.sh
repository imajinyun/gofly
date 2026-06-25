#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "ci-required-check-evidence.json"
missing = []

expected_checks = {
    "build & test (go 1.26)",
    "build & test (go stable)",
    "golangci-lint",
    "platform smoke (macos-latest)",
    "platform smoke (windows-latest)",
    "security (govulncheck + gosec)",
    "supply-chain lint + OSV",
    "CodeQL security analysis",
    "dependency review",
    "dependency upgrade validation",
    "branch protection required-check audit",
    "contract / api+rpc (check + breaking)",
    "governance gates",
    "bench + fuzz smoke",
    "integration tests (storage-mysql-postgres)",
    "integration tests (config-consul-nacos-etcd)",
    "integration tests (mq-brokers)",
    "integration tests (gateway-transcode)",
    "docker build + trivy",
    "OSSF Scorecard",
}

expected_release_prerequisites = {
    "build-test",
    "platform-smoke",
    "lint",
    "security",
    "supply-chain",
    "codeql",
    "dependency-upgrade-validation",
    "contract-check",
    "governance",
    "bench-fuzz",
    "integration",
    "docker",
    "scorecard",
}


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


def extract_branch_audit_expected(workflow):
    match = re.search(r"expected = \{(?P<body>.*?)\n\s*\}", workflow, re.S)
    require(match is not None, "ci.yml branch-protection expected check set is missing")
    if not match:
        return set()
    return set(re.findall(r'"([^"]+)"', match.group("body")))


def extract_release_needs(workflow):
    match = re.search(r"\n\s+release:\n(?P<body>.*?)(?:\n\s{2}[A-Za-z0-9_-]+:|\Z)", workflow, re.S)
    require(match is not None, "ci.yml release job is missing")
    if not match:
        return set()
    needs_match = re.search(r"needs:\s*\[(?P<items>[^\]]+)\]", match.group("body"))
    require(needs_match is not None, "ci.yml release job needs list is missing")
    if not needs_match:
        return set()
    return {item.strip() for item in needs_match.group("items").split(",") if item.strip()}


def extract_job_ids(workflow):
    match = re.search(r"\njobs:\n(?P<body>.*?)(?:\n\S|\Z)", workflow, re.S)
    require(match is not None, "ci.yml jobs block is missing")
    if not match:
        return set()
    return set(re.findall(r"^\s{2}([A-Za-z0-9_-]+):\n", match.group("body"), re.M))


def extract_job_body(workflow, job_id):
    match = re.search(rf"\n\s{{2}}{re.escape(job_id)}:\n(?P<body>.*?)(?:\n\s{{2}}[A-Za-z0-9_-]+:|\Z)", workflow, re.S)
    require(match is not None, f"ci.yml job {job_id!r} is missing")
    return match.group("body") if match else ""


def extract_upload_artifact_names(workflow):
    names = set(re.findall(r"\n\s+name:\s*([A-Za-z0-9_.${}() /_-]+)\n\s+path:", workflow))
    names.update(re.findall(r"gh run download[^\n]+--name\s+([A-Za-z0-9_.${}() /_-]+)", workflow))
    return {name.strip() for name in names}


def local_gate_exists(gate, targets):
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        return target in targets
    return (
        gate.startswith("go test ")
        or gate.startswith("GitHub ")
        or gate.startswith("OpenSSF ")
    )


if not manifest_path.is_file():
    missing.append("docs/reference/ci-required-check-evidence.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

makefile = read_text(root / "Makefile")
workflow = read_text(root / ".github" / "workflows" / "ci.yml")
checklist = read_text(root / "docs" / "operations" / "production-checklist.md")
docs = read_text(root / "docs" / "reference" / "ci-required-check-evidence.md")
docs_index = read_text(root / "docs" / "index.md")
readme = read_text(root / "README.md")
required_check_script = read_text(root / "bin" / "scripts" / "check-required-checks-drift.sh")
governance_report = read_text(root / "bin" / "scripts" / "governance-report.sh")

target_names = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.ci_required_check_evidence.v1", "schema must be gofly.ci_required_check_evidence.v1")
require("ci-required-check-evidence-check" in target_names, "Makefile must expose ci-required-check-evidence-check")
require("ci-required-check-evidence-check" in docs_check_line, "docs-check must depend on ci-required-check-evidence-check")
require("check-ci-required-check-evidence.sh" in makefile, "Makefile must call check-ci-required-check-evidence.sh")
require("required-checks-drift-check" in target_names, "Makefile must keep required-checks-drift-check")

branch_expected = extract_branch_audit_expected(workflow)
release_needs = extract_release_needs(workflow)
job_ids = extract_job_ids(workflow)
artifact_names = extract_upload_artifact_names(workflow)

checks = manifest.get("checks") or []
actual_checks = {item.get("check") for item in checks if isinstance(item, dict)}
require(actual_checks == expected_checks, f"required checks drifted: missing={sorted(expected_checks - actual_checks)} extra={sorted(actual_checks - expected_checks)}")
require(branch_expected == expected_checks, "manifest checks must match branch-protection expected check set")
manifest_prereqs = set(manifest.get("releasePrerequisites") or [])
require(manifest_prereqs == expected_release_prerequisites, "releasePrerequisites must match release job needs")
require(release_needs == expected_release_prerequisites, "ci.yml release job needs drifted from manifest")
require(manifest_prereqs <= job_ids, f"releasePrerequisites include unknown jobs: {sorted(manifest_prereqs - job_ids)}")

for item in checks:
    if not isinstance(item, dict):
        missing.append(f"check entry must be an object: {item!r}")
        continue
    check = item.get("check", "")
    job = item.get("job", "")
    local_gate = item.get("localGate", "")
    artifact = item.get("artifact", "")
    for field, value in {
        "check": check,
        "job": job,
        "localGate": local_gate,
        "artifact": artifact,
    }.items():
        require(bool(value), f"{check or '<missing>'}: {field} is required")
    if not check:
        continue
    require(check in checklist, f"production checklist must document required check {check!r}")
    require(check in docs, f"ci required-check evidence docs must list {check!r}")
    if job:
        require(job in job_ids, f"{check}: workflow job {job!r} is missing")
        job_body = extract_job_body(workflow, job)
        if job not in {"build-test", "platform-smoke", "integration"}:
            require(check in workflow or check in job_body, f"{check}: workflow must expose stable check name")
    if local_gate:
        require(local_gate_exists(local_gate, target_names), f"{check}: local gate is not runnable or documented: {local_gate!r}")
    if artifact:
        artifact_is_documented = (
            artifact in workflow
            or artifact in artifact_names
            or artifact in docs
            or artifact.endswith("job logs")
            or artifact.endswith("job summary")
            or artifact.startswith("workflow logs")
            or artifact.startswith("platform smoke")
            or artifact.startswith("integration matrix")
            or artifact.startswith("govulncheck")
            or artifact.startswith("CodeQL")
            or artifact.startswith("Dependency Review")
        )
        require(artifact_is_documented, f"{check}: artifact {artifact!r} is not documented in workflow or docs")

for needle in [
    "expected_default_checks",
    "expected_release_needs",
    "expected_integration_matrix",
    "expected_docker_ci_evidence_files",
]:
    require(needle in required_check_script, f"required-check drift script must keep {needle}")

for path_text, needles in {
    "docs/reference/ci-required-check-evidence.md": [
        "gofly.ci_required_check_evidence.v1",
        "ci-required-check-evidence.json",
        "make ci-required-check-evidence-check",
        "releasePrerequisites",
    ],
    "docs/index.md": ["reference/ci-required-check-evidence.md"],
    "README.md": ["docs/reference/ci-required-check-evidence.md"],
}.items():
    text = {"docs/index.md": docs_index, "README.md": readme}.get(path_text, docs)
    for needle in needles:
        require(needle in text, f"{path_text}: missing {needle!r}")

for needle in [
    "ci-required-check-evidence.json",
    "gofly.ci_required_check_evidence.v1",
    "ciRequiredChecks",
    "make ci-required-check-evidence-check",
]:
    require(needle in governance_report, f"governance-report.sh must expose {needle!r}")

if missing:
    print("CI required-check evidence check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print(f"CI required-check evidence governance ok: {len(checks)} checks, {len(manifest_prereqs)} release prerequisites")
PY
