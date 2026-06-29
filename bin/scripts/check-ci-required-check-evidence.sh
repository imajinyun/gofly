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

expected_integration_matrix = {
    "storage-sql": "integration tests (storage-mysql-postgres)",
    "config-discovery": "integration tests (config-consul-nacos-etcd)",
    "mq-brokers": "integration tests (mq-brokers)",
    "gateway-transcode": "integration tests (gateway-transcode)",
    "observability-release": "governance gates",
}

expected_release_drift_jobs = expected_release_prerequisites
expected_hosted_release_evidence = {
    "artifact-upload": {
        "producerJob": "release",
        "requiredCheck": "release (tagged)",
        "workflowMarker": "Upload release verification evidence",
        "releasePrerequisite": True,
    },
    "checksums": {
        "producerJob": "release",
        "requiredCheck": "release (tagged)",
        "workflowMarker": "dist/checksums.txt",
        "releasePrerequisite": True,
    },
    "sbom": {
        "producerJob": "release",
        "requiredCheck": "release (tagged)",
        "workflowMarker": "release-evidence/docker/release-docker-sbom.spdx.json",
        "releasePrerequisite": True,
    },
    "provenance": {
        "producerJob": "release",
        "requiredCheck": "release (tagged)",
        "workflowMarker": "Verify release attestations",
        "releasePrerequisite": True,
    },
    "docker-digest": {
        "producerJob": "release",
        "requiredCheck": "release (tagged)",
        "workflowMarker": "release-evidence/docker/release-docker-digests.json",
        "releasePrerequisite": True,
    },
    "trivy": {
        "producerJob": "release",
        "requiredCheck": "release (tagged)",
        "workflowMarker": "Trivy release image scan",
        "releasePrerequisite": True,
    },
    "codeql": {
        "producerJob": "codeql",
        "requiredCheck": "CodeQL security analysis",
        "workflowMarker": "Perform CodeQL Analysis",
        "releasePrerequisite": True,
    },
    "scorecard": {
        "producerJob": "scorecard",
        "requiredCheck": "OSSF Scorecard",
        "workflowMarker": "Upload Scorecard SARIF",
        "releasePrerequisite": True,
    },
    "dependency-review": {
        "producerJob": "dependency-review",
        "requiredCheck": "dependency review",
        "workflowMarker": "Dependency Review is a pull-request-only gate",
        "releasePrerequisite": False,
    },
    "required-check-drift": {
        "producerJob": "branch-protection-audit",
        "requiredCheck": "branch protection required-check audit",
        "workflowMarker": "required-status-checks.json",
        "releasePrerequisite": False,
    },
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
    normalized = gate
    if " make " in gate:
        normalized = "make " + gate.split(" make ", 1)[1]
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        return target in targets
    if normalized.startswith("make "):
        target = normalized.removeprefix("make ").split()[0]
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

integration_matrix = manifest.get("integrationMatrix")
if not isinstance(integration_matrix, list):
    missing.append("integrationMatrix must be a list")
    integration_matrix = []
integration_ids = set()
for item in integration_matrix:
    if not isinstance(item, dict):
        missing.append(f"integrationMatrix entry must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    if not item_id:
        missing.append("integrationMatrix id is required")
    elif item_id in integration_ids:
        missing.append(f"duplicate integrationMatrix id: {item_id}")
    integration_ids.add(item_id)
    for field in (
        "id",
        "surface",
        "owner",
        "supportedProfiles",
        "localGate",
        "ciJob",
        "requiredCheck",
        "releasePrerequisite",
        "dependencyUpgradeTrigger",
        "rollbackNote",
    ):
        require(bool(item.get(field)), f"integrationMatrix {item_id or '<missing>'}: {field} is required")
    expected_check = expected_integration_matrix.get(item_id)
    if expected_check:
        require(item.get("requiredCheck") == expected_check, f"integrationMatrix {item_id}: requiredCheck mismatch")
    require(item.get("requiredCheck") in actual_checks, f"integrationMatrix {item_id}: requiredCheck is not in checks")
    require(item.get("ciJob") in job_ids, f"integrationMatrix {item_id}: ciJob is missing")
    require(item.get("releasePrerequisite") in manifest_prereqs, f"integrationMatrix {item_id}: releasePrerequisite is not release-blocking")
    require(local_gate_exists(item.get("localGate", ""), target_names), f"integrationMatrix {item_id}: localGate is not runnable or documented")
    require(len(item.get("supportedProfiles") or []) >= 2, f"integrationMatrix {item_id}: supportedProfiles must name at least two profiles")
    for field in ("dependencyUpgradeTrigger", "rollbackNote"):
        require(len(str(item.get(field) or "").split()) >= 8, f"integrationMatrix {item_id}: {field} must be actionable")
require(set(expected_integration_matrix) == integration_ids, f"integrationMatrix ids drifted: missing={sorted(set(expected_integration_matrix) - integration_ids)} extra={sorted(integration_ids - set(expected_integration_matrix))}")

release_drift = manifest.get("releasePrerequisiteDrift")
if not isinstance(release_drift, list):
    missing.append("releasePrerequisiteDrift must be a list")
    release_drift = []
release_drift_jobs = set()
for item in release_drift:
    if not isinstance(item, dict):
        missing.append(f"releasePrerequisiteDrift entry must be an object: {item!r}")
        continue
    job = item.get("job", "")
    if not job:
        missing.append("releasePrerequisiteDrift job is required")
    elif job in release_drift_jobs:
        missing.append(f"duplicate releasePrerequisiteDrift job: {job}")
    release_drift_jobs.add(job)
    for field in (
        "job",
        "owner",
        "localGate",
        "requiredChecks",
        "artifact",
        "driftPolicy",
        "fallbackPolicy",
    ):
        require(bool(item.get(field)), f"releasePrerequisiteDrift {job or '<missing>'}: {field} is required")
    require(job in manifest_prereqs, f"releasePrerequisiteDrift {job}: job is not a release prerequisite")
    require(job in job_ids, f"releasePrerequisiteDrift {job}: job is missing from workflow")
    for check in item.get("requiredChecks") or []:
        require(check in actual_checks, f"releasePrerequisiteDrift {job}: required check {check!r} is not in checks")
        matching = [entry for entry in checks if isinstance(entry, dict) and entry.get("check") == check]
        if matching:
            require(matching[0].get("job") == job, f"releasePrerequisiteDrift {job}: required check {check!r} belongs to {matching[0].get('job')!r}")
            require(matching[0].get("localGate") == item.get("localGate"), f"releasePrerequisiteDrift {job}: localGate mismatch for {check!r}")
    for field in ("driftPolicy", "fallbackPolicy"):
        require(len(str(item.get(field) or "").split()) >= 10, f"releasePrerequisiteDrift {job}: {field} must be actionable")
require(release_drift_jobs == expected_release_drift_jobs, f"releasePrerequisiteDrift jobs drifted: missing={sorted(expected_release_drift_jobs - release_drift_jobs)} extra={sorted(release_drift_jobs - expected_release_drift_jobs)}")

hosted = manifest.get("hostedReleaseEvidence")
if not isinstance(hosted, dict):
    missing.append("hostedReleaseEvidence must be an object")
    hosted = {}
require(hosted.get("schema") == "gofly.hosted_release_evidence.v1", "hostedReleaseEvidence schema mismatch")
require(hosted.get("aiflowTask") == "GOFLY-GOV-10P9-05", "hostedReleaseEvidence must identify GOFLY-GOV-10P9-05")
require(hosted.get("acceptanceGate") == "make ci-required-check-evidence-check", "hostedReleaseEvidence acceptanceGate mismatch")
require(hosted.get("releaseJob") == "release", "hostedReleaseEvidence releaseJob must be release")
require(hosted.get("uploadArtifact") == "release-dist-evidence", "hostedReleaseEvidence uploadArtifact must be release-dist-evidence")
require(len(str(hosted.get("policy") or "").split()) >= 18, "hostedReleaseEvidence policy must be actionable")
hosted_rows = hosted.get("rows")
if not isinstance(hosted_rows, list):
    missing.append("hostedReleaseEvidence rows must be a list")
    hosted_rows = []
hosted_by_id = {}
for row in hosted_rows:
    if not isinstance(row, dict):
        missing.append(f"hostedReleaseEvidence row must be an object: {row!r}")
        continue
    row_id = row.get("id", "")
    if not row_id:
        missing.append("hostedReleaseEvidence row id is required")
        continue
    if row_id in hosted_by_id:
        missing.append(f"duplicate hostedReleaseEvidence id: {row_id}")
    hosted_by_id[row_id] = row
require(set(hosted_by_id) == set(expected_hosted_release_evidence), f"hostedReleaseEvidence ids drifted: missing={sorted(set(expected_hosted_release_evidence) - set(hosted_by_id))} extra={sorted(set(hosted_by_id) - set(expected_hosted_release_evidence))}")

release_job_body = extract_job_body(workflow, "release")
for row_id, expected in sorted(expected_hosted_release_evidence.items()):
    row = hosted_by_id.get(row_id) or {}
    for field in (
        "id",
        "producerJob",
        "requiredCheck",
        "hostedEvidence",
        "localGate",
        "workflowMarker",
        "releasePrerequisite",
        "fallbackPolicy",
    ):
        require(field in row and row.get(field) not in ("", None), f"hostedReleaseEvidence {row_id}: {field} is required")
    for field, value in expected.items():
        require(row.get(field) == value, f"hostedReleaseEvidence {row_id}: {field} mismatch: got {row.get(field)!r}, want {value!r}")
    producer = row.get("producerJob", "")
    required_check = row.get("requiredCheck", "")
    local_gate = row.get("localGate", "")
    require(producer in job_ids, f"hostedReleaseEvidence {row_id}: producer job {producer!r} is missing from workflow")
    if row.get("releasePrerequisite") is True:
        if producer == "release":
            require("if: startsWith(github.ref, 'refs/tags/v')" in release_job_body, f"hostedReleaseEvidence {row_id}: release row must stay tag-scoped")
        else:
            require(producer in manifest_prereqs, f"hostedReleaseEvidence {row_id}: producer {producer!r} must be a release prerequisite")
    if required_check != "release (tagged)":
        require(required_check in actual_checks, f"hostedReleaseEvidence {row_id}: required check {required_check!r} is not in checks")
        matching = [entry for entry in checks if isinstance(entry, dict) and entry.get("check") == required_check]
        if matching:
            require(matching[0].get("job") == producer, f"hostedReleaseEvidence {row_id}: required check job mismatch")
            require(matching[0].get("localGate") == local_gate, f"hostedReleaseEvidence {row_id}: local gate mismatch")
    else:
        require(producer == "release", f"hostedReleaseEvidence {row_id}: release tagged rows must be produced by release job")
    marker = str(row.get("workflowMarker") or "")
    require(marker in workflow, f"hostedReleaseEvidence {row_id}: workflow marker {marker!r} is missing")
    require(local_gate_exists(local_gate, target_names), f"hostedReleaseEvidence {row_id}: localGate is not runnable or documented: {local_gate!r}")
    require(len(str(row.get("fallbackPolicy") or "").split()) >= 8, f"hostedReleaseEvidence {row_id}: fallbackPolicy must be actionable")
require("release-dist-evidence" in workflow, "ci.yml: hosted release upload artifact release-dist-evidence is missing")
require("if-no-files-found: error" in release_job_body, "ci.yml: hosted release evidence upload must fail on missing files")

for needle in [
    "expected_default_checks",
    "expected_release_needs",
    "expected_integration_matrix",
    "expected_docker_ci_evidence_files",
    "expected_hosted_release_evidence",
]:
    require(needle in required_check_script, f"required-check drift script must keep {needle}")

for path_text, needles in {
    "docs/reference/ci-required-check-evidence.md": [
        "gofly.ci_required_check_evidence.v1",
        "ci-required-check-evidence.json",
        "make ci-required-check-evidence-check",
        "releasePrerequisites",
        "integrationMatrix",
        "releasePrerequisiteDrift",
        "hostedReleaseEvidence",
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
