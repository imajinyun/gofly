#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import re
import sys
import json

root = pathlib.Path(".").resolve()
workflow_path = root / ".github" / "workflows" / "ci.yml"
checklist_path = root / "docs" / "operations" / "production-checklist.md"
makefile_path = root / "Makefile"
governance_script_path = root / "bin" / "scripts" / "governance-10-rounds.sh"
agents_path = root / "AGENTS.md"
required_check_manifest_path = root / "docs" / "reference" / "ci-required-check-evidence.json"
release_evidence_index_path = root / "docs" / "releases" / "evidence-index.json"
release_evidence_consumption_path = root / "docs" / "releases" / "evidence-consumption.json"
integration_ownership_path = root / "docs" / "reference" / "integration-ownership-matrix.json"

expected_default_checks = {
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

expected_release_needs = {
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
    "storage-mysql-postgres": "./core/storage/",
    "config-consul-nacos-etcd": "./core/config/... ./core/discovery/...",
    "mq-brokers": "./core/mq/...",
    "gateway-transcode": "./gateway/",
}

expected_integration_packages = " ".join(expected_integration_matrix.values())

expected_benchmark_artifacts = {
    "bench/baseline.txt",
    "bench/matrix.md",
    "bench/evidence.md",
}

expected_docker_ci_evidence_files = {
    "docker-build-evidence.json",
    "docker-build-metadata.json",
    "docker-image-inspect.json",
    "trivy-results.sarif",
}

expected_release_docker_evidence_files = {
    "release-evidence/checksums-attestation-verification.json",
    "release-evidence/docker/release-docker-attestation-verification.json",
    "release-evidence/docker/release-docker-digests.json",
    "release-evidence/docker/release-docker-inspect.txt",
    "release-evidence/docker/release-docker-manifest.json",
    "release-evidence/docker/release-docker-sbom.spdx.json",
    "release-evidence/docker/release-trivy-results.json",
    "release-evidence/docker/ci/docker-build-evidence.json",
    "release-evidence/docker/ci/trivy-results.sarif",
}

expected_release_docker_digest_fields = {
    "digest_source",
    "image",
    "manifest_count",
    "manifest_digest",
    "platforms",
    "schema",
}

expected_ci_docker_build_fields = {
    "build_metadata",
    "image_id",
    "image_ref",
    "repo_digests",
    "repo_tags",
    "schema",
}

expected_governance_rounds = {
    1: "baseline and module graph",
    2: "format check",
    3: "unit tests without test cache",
    4: "vet static analysis",
    5: "golangci-lint",
    6: "race detector",
    7: "module tidy verification",
    8: "runtime cache bypass tests",
    9: "plugin no-local-cache tests",
    10: "AI governance pipeline manifest check",
    11: "generated output determinism and path safety",
    12: "generated project verification matrix",
    13: "generated project runtime control-plane smoke",
    14: "docs, coverage, security, and final package listing",
}

expected_governance_skip_envs = {
    "GOVERNANCE_SKIP_RACE": {
        "round": "6",
        "compensating_gate": "run go test -race ./... before merge/release",
        "release_blocking": True,
    },
    "GOVERNANCE_SKIP_GENERATED_MATRIX": {
        "round": "12",
        "compensating_gate": "make test-generated-matrix in build-test job",
        "release_blocking": False,
    },
    "GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE": {
        "round": "13",
        "compensating_gate": "make generated-control-plane-smoke in build-test job",
        "release_blocking": True,
    },
    "GOVERNANCE_SKIP_SECURITY": {
        "round": "14",
        "compensating_gate": "run make security before merge/release",
        "release_blocking": True,
    },
}

workflow = workflow_path.read_text(encoding="utf-8")
checklist = checklist_path.read_text(encoding="utf-8")
makefile = makefile_path.read_text(encoding="utf-8")
governance_script = governance_script_path.read_text(encoding="utf-8")
agents = agents_path.read_text(encoding="utf-8")
release_artifacts_script = (root / "bin" / "scripts" / "check-release-artifacts.sh").read_text(encoding="utf-8")
release_artifacts_test = (root / "bin" / "scripts" / "check-release-artifacts-test.sh").read_text(encoding="utf-8")
public_api_script = (root / "bin" / "scripts" / "check-public-api.sh").read_text(encoding="utf-8")
public_api_test = (root / "bin" / "scripts" / "check-public-api-test.sh").read_text(encoding="utf-8")
required_check_manifest = json.loads(required_check_manifest_path.read_text(encoding="utf-8"))
release_evidence_index = json.loads(release_evidence_index_path.read_text(encoding="utf-8"))
release_evidence_consumption = json.loads(release_evidence_consumption_path.read_text(encoding="utf-8"))
integration_ownership = json.loads(integration_ownership_path.read_text(encoding="utf-8"))

missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def extract_branch_audit_expected(text):
    match = re.search(r"expected = \{(?P<body>.*?)\n\s*\}", text, re.S)
    require(match is not None, "ci.yml: branch-protection-audit expected set is missing")
    if not match:
        return set()
    return set(re.findall(r'"([^"]+)"', match.group("body")))


def extract_release_needs(text):
    match = re.search(r"\n\s+release:\n(?P<body>.*?)(?:\n\s{2}[A-Za-z0-9_-]+:|\Z)", text, re.S)
    require(match is not None, "ci.yml: release job is missing")
    if not match:
        return set()
    needs_match = re.search(r"needs:\s*\[(?P<items>[^\]]+)\]", match.group("body"))
    require(needs_match is not None, "ci.yml: release job needs list is missing")
    if not needs_match:
        return set()
    return {item.strip() for item in needs_match.group("items").split(",") if item.strip()}


def extract_job_ids(text):
    match = re.search(r"\njobs:\n(?P<body>.*?)(?:\n\S|\Z)", text, re.S)
    require(match is not None, "ci.yml: jobs block is missing")
    if not match:
        return set()
    return set(re.findall(r"^\s{2}([A-Za-z0-9_-]+):\n", match.group("body"), re.M))


def extract_job_body(text, job_id):
    match = re.search(rf"\n\s{{2}}{re.escape(job_id)}:\n(?P<body>.*?)(?:\n\s{{2}}[A-Za-z0-9_-]+:|\Z)", text, re.S)
    require(match is not None, f"ci.yml: {job_id} job is missing")
    return match.group("body") if match else ""


def extract_integration_matrix(text):
    body = extract_job_body(text, "integration")
    entries = {}
    for match in re.finditer(r"- area:\s*([^\n]+)\n\s+packages:\s*([^\n]+)", body):
        entries[match.group(1).strip()] = match.group(2).strip()
    require(entries != {}, "ci.yml: integration matrix area/package entries are missing")
    return entries


def extract_step_body(job_body, step_name):
    match = re.search(
        rf"\n\s+- name:\s*{re.escape(step_name)}\n(?P<body>.*?)(?:\n\s+- name:|\Z)",
        job_body,
        re.S,
    )
    require(match is not None, f"ci.yml: step {step_name!r} is missing")
    return match.group("body") if match else ""


def extract_upload_artifact_paths(step_body):
    match = re.search(r"\n\s+path:\s*\|\n(?P<body>.*?)(?:\n\s+if-no-files-found:|\n\s+[A-Za-z0-9_-]+:|\Z)", step_body, re.S)
    require(match is not None, "ci.yml: upload-artifact path block is missing")
    if not match:
        return set()
    return {line.strip() for line in match.group("body").splitlines() if line.strip()}


def extract_python_evidence_keys(step_body, schema):
    schema_index = step_body.find(f'"schema": "{schema}"')
    require(schema_index >= 0, f"ci.yml: evidence schema {schema!r} is missing")
    if schema_index < 0:
        return set()
    window = step_body[max(0, schema_index - 500): schema_index + 1000]
    return set(re.findall(r'"([A-Za-z_][A-Za-z0-9_]*)"\s*:', window))


def extract_make_target_body(text, target):
    match = re.search(rf"^\.PHONY:\s*{re.escape(target)}\n{re.escape(target)}:.*?\n(?P<body>.*?)(?=^\.PHONY:|\Z)", text, re.S | re.M)
    require(match is not None, f"Makefile: {target} target is missing")
    return match.group("body") if match else ""


def extract_make_target_deps(text, target):
    match = re.search(rf"^{re.escape(target)}:(?P<deps>[^#\n]*)", text, re.M)
    require(match is not None, f"Makefile: {target} target is missing")
    return match.group("deps") if match else ""


branch_audit_expected = extract_branch_audit_expected(workflow)
release_needs = extract_release_needs(workflow)
job_ids = extract_job_ids(workflow)
integration_matrix = extract_integration_matrix(workflow)
integration_target = extract_make_target_body(makefile, "integration-tests")
dependency_target = extract_make_target_body(makefile, "dependency-upgrade-check")
dependency_job = extract_job_body(workflow, "dependency-upgrade-validation")
governance_job = extract_job_body(workflow, "governance")
bench_job = extract_job_body(workflow, "bench-fuzz")
docker_job = extract_job_body(workflow, "docker")
release_job = extract_job_body(workflow, "release")
governance_target_deps = extract_make_target_deps(makefile, "governance")
governance_10_target = extract_make_target_body(makefile, "governance-10-rounds")
ci_target_deps = extract_make_target_deps(makefile, "ci")
supply_chain_target_deps = extract_make_target_deps(makefile, "supply-chain")
bench_evidence_target = extract_make_target_body(makefile, "bench-evidence-check")
bench_regression_target = extract_make_target_body(makefile, "bench-regression-check")
docker_upload_step = extract_step_body(docker_job, "Upload Docker and Trivy evidence")
docker_build_step = extract_step_body(docker_job, "Build Docker image")
release_digest_step = extract_step_body(release_job, "Collect release Docker digest evidence")
release_upload_step = extract_step_body(release_job, "Upload release verification evidence")

require(
    branch_audit_expected == expected_default_checks,
    "ci.yml: branch-protection-audit expected checks drifted: "
    f"missing={sorted(expected_default_checks - branch_audit_expected)} extra={sorted(branch_audit_expected - expected_default_checks)}",
)

for check in sorted(expected_default_checks):
    require(check in checklist, f"production-checklist.md: missing required check {check!r}")

require("release (tagged)" in checklist, "production-checklist.md: missing release (tagged) job reference")
require("release provenance" in makefile or "release-artifacts-test" in makefile, "Makefile: release provenance fixture target is missing")
require("release-artifacts-test" in makefile, "Makefile: release-artifacts-test target is missing")
require("api-compat-test" in makefile, "Makefile: api-compat-test target is missing")
require(
    re.search(r"^supply-chain:.*release-artifacts-test", makefile, re.M) is not None,
    "Makefile: supply-chain target must depend on release-artifacts-test",
)
require(
    "api-compat-test" in supply_chain_target_deps,
    "Makefile: supply-chain target must depend on api-compat-test so skip semantics stay covered in CI",
)
require(
    "run: make supply-chain" in workflow,
    "ci.yml: supply-chain job must run make supply-chain so release-artifacts-test is included",
)
require(
    "Public API compatibility report" in workflow and "api-compat-report" in workflow,
    "ci.yml: supply-chain job must emit and upload the public API compatibility report",
)
require(
    "API_COMPAT_REQUIRED: ${{ startsWith(github.ref, 'refs/tags/v') && 'true' || 'false' }}" in workflow,
    "ci.yml: public API compatibility must be release-blocking on version tags",
)
for marker in (
    "gofly.api_compat_report.v1",
    "status",
    "base_ref",
    "reason",
    "module",
    "base ref '",
    "skipping public API compatibility check",
    "public API compatibility skip is forbidden for release/tag governance",
    "API_COMPAT_REQUIRED",
    "GOVERNANCE_RELEASE",
    "refs/tags/v",
):
    require(marker in public_api_script, f"check-public-api.sh: missing API compatibility skip governance marker {marker!r}")
for marker in (
    "gofly.api_compat_report.v1",
    "run_skip_allowed",
    "run_skip_forbidden",
    "API_COMPAT_REQUIRED=true",
    "GITHUB_REF=refs/tags/v0.0.0-fixture",
    "GOVERNANCE_RELEASE=true",
    "skipping public API compatibility check",
    "public API compatibility skip is forbidden for release/tag governance",
):
    require(marker in public_api_test, f"check-public-api-test.sh: missing API compatibility skip fixture marker {marker!r}")

require(
    release_needs == expected_release_needs,
    "ci.yml: release job needs drifted: "
    f"missing={sorted(expected_release_needs - release_needs)} extra={sorted(release_needs - expected_release_needs)}",
)
require(
    release_needs <= job_ids,
    f"ci.yml: release needs unknown job id(s): {sorted(release_needs - job_ids)}",
)

manifest_checks = {
    item.get("check"): item
    for item in required_check_manifest.get("checks", [])
    if isinstance(item, dict)
}
release_drift = required_check_manifest.get("releasePrerequisiteDrift")
if not isinstance(release_drift, list):
    missing.append("ci-required-check-evidence.json: releasePrerequisiteDrift must be a list")
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
        if not item.get(field):
            missing.append(f"releasePrerequisiteDrift {job or '<missing>'}: {field} is required")
    require(job in release_needs, f"releasePrerequisiteDrift {job}: job is not a release prerequisite")
    required_checks = item.get("requiredChecks") or []
    require(len(required_checks) >= 1, f"releasePrerequisiteDrift {job}: requiredChecks must not be empty")
    for check in required_checks:
        require(check in expected_default_checks, f"releasePrerequisiteDrift {job}: unknown required check {check!r}")
        require(check in branch_audit_expected, f"releasePrerequisiteDrift {job}: check {check!r} is not branch-protected")
        manifest_check = manifest_checks.get(check) or {}
        require(manifest_check.get("job") == job, f"releasePrerequisiteDrift {job}: check {check!r} belongs to {manifest_check.get('job')!r}")
        require(manifest_check.get("localGate") == item.get("localGate"), f"releasePrerequisiteDrift {job}: localGate mismatch for {check!r}")
    require(any((manifest_checks.get(check) or {}).get("artifact") == item.get("artifact") for check in required_checks), f"releasePrerequisiteDrift {job}: artifact does not match required check evidence")
    for field in ("driftPolicy", "fallbackPolicy"):
        require(len(str(item.get(field) or "").split()) >= 10, f"releasePrerequisiteDrift {job}: {field} must be actionable")
require(
    release_drift_jobs == expected_release_needs,
    "ci-required-check-evidence.json: releasePrerequisiteDrift jobs drifted: "
    f"missing={sorted(expected_release_needs - release_drift_jobs)} extra={sorted(release_drift_jobs - expected_release_needs)}",
)

release_evidence_items = [
    item
    for item in release_evidence_index.get("evidence") or []
    if isinstance(item, dict)
]
release_evidence_ids = {
    item.get("id", "")
    for item in release_evidence_items
    if item.get("id")
}
consumption_items = [
    item
    for item in release_evidence_consumption.get("items") or []
    if isinstance(item, dict)
]
consumption_ids = {
    item.get("id", "")
    for item in consumption_items
    if item.get("id")
}
require(
    release_evidence_consumption.get("schema") == "gofly.release_evidence_consumption.v1",
    "evidence-consumption.json: schema mismatch",
)
drift_closure = release_evidence_consumption.get("driftClosure") or {}
require(
    drift_closure.get("schema") == "gofly.release_evidence_drift_closure.v1",
    "evidence-consumption.json: driftClosure schema mismatch",
)
require(
    drift_closure.get("requiredCheckSource") == "docs/reference/ci-required-check-evidence.json",
    "evidence-consumption.json: driftClosure requiredCheckSource must point to ci-required-check-evidence.json",
)
require(
    drift_closure.get("driftGate") == "make required-checks-drift-check",
    "evidence-consumption.json: driftClosure driftGate must be make required-checks-drift-check",
)
require(
    drift_closure.get("dashboardReportField") == "releaseEvidenceConsumption.driftClosure",
    "evidence-consumption.json: driftClosure dashboardReportField mismatch",
)
require(
    set(drift_closure.get("requiredEvidenceIds") or []) == release_evidence_ids,
    "evidence-consumption.json: driftClosure requiredEvidenceIds drifted from evidence-index: "
    f"missing={sorted(release_evidence_ids - set(drift_closure.get('requiredEvidenceIds') or []))} "
    f"extra={sorted(set(drift_closure.get('requiredEvidenceIds') or []) - release_evidence_ids)}",
)
require(
    consumption_ids == release_evidence_ids,
    "evidence-consumption.json: consumed evidence ids drifted from evidence-index: "
    f"missing={sorted(release_evidence_ids - consumption_ids)} extra={sorted(consumption_ids - release_evidence_ids)}",
)
for item in release_evidence_items:
    evidence_id = item.get("id", "")
    producer = item.get("producerJob", "")
    require(
        producer == "release" or producer in release_drift_jobs,
        f"evidence-index.json: release evidence {evidence_id!r} producer {producer!r} is not a release prerequisite or explicit release artifact producer",
    )
    if producer != "release":
        require(
            any(entry.get("job") == producer for entry in release_drift),
            f"evidence-index.json: release evidence {evidence_id!r} producer {producer!r} lacks releasePrerequisiteDrift coverage",
        )
for item in consumption_items:
    evidence_id = item.get("id", "")
    for field in ("questionAnswered", "consumerAction", "rollbackOrEscalation"):
        require(
            len(str(item.get(field) or "").split()) >= 5,
            f"evidence-consumption.json: {evidence_id} {field} must be actionable",
        )

default_required_job_ids = expected_release_needs | {"dependency-review", "branch-protection-audit"}
require(
    default_required_job_ids <= job_ids,
    f"ci.yml: missing required job id(s): {sorted(default_required_job_ids - job_ids)}",
)

require(
    integration_matrix == expected_integration_matrix,
    "ci.yml: integration matrix drifted: "
    f"missing={sorted(set(expected_integration_matrix) - set(integration_matrix))} "
    f"extra={sorted(set(integration_matrix) - set(expected_integration_matrix))} "
    f"packages={integration_matrix}",
)
require(
    f"$(GO) test -tags=integration -count=1 {expected_integration_packages}" in integration_target,
    "Makefile: integration-tests packages must match the CI integration matrix package union",
)
require(
    "$(MAKE) integration-tests" in dependency_target,
    "Makefile: dependency-upgrade-check must run integration-tests when DEPENDENCY_UPGRADE_RUN_INTEGRATION=true",
)
require(
    "Skipping integration-tests here; required CI integration matrix provides Docker-backed coverage." in dependency_target,
    "Makefile: dependency-upgrade-check skip message must name the required CI integration matrix",
)
require(
    "make dependency-upgrade-check DEPENDENCY_UPGRADE_RUN_INTEGRATION=false" in dependency_job,
    "ci.yml: dependency upgrade validation must delegate Docker-backed coverage with DEPENDENCY_UPGRADE_RUN_INTEGRATION=false",
)
require(
    "Docker-backed integration coverage is delegated to the required integration matrix" in dependency_job,
    "ci.yml: dependency upgrade summary must document integration matrix delegation",
)
for area in sorted(expected_integration_matrix):
    require(f"integration tests ({area})" in checklist, f"production-checklist.md: missing integration matrix check for {area!r}")
require(
    "make dependency-upgrade-check" in checklist,
    "production-checklist.md: missing dependency-upgrade-check operator command",
)
require(
    "DEPENDENCY_UPGRADE_RUN_INTEGRATION" in makefile,
    "Makefile: DEPENDENCY_UPGRADE_RUN_INTEGRATION toggle is missing",
)
require(
    "bash $(SCRIPTS_DIR)/benchstat.sh --check-evidence" in bench_evidence_target,
    "Makefile: bench-evidence-check must validate tracked benchmark evidence through benchstat.sh --check-evidence",
)
require(
    "bash $(SCRIPTS_DIR)/benchstat.sh --regression-check" in bench_regression_target,
    "Makefile: bench-regression-check must validate HTTP allocation budgets through benchstat.sh --regression-check",
)
require(
    "bench-evidence-check" in ci_target_deps,
    "Makefile: ci target must include bench-evidence-check so local full CI validates benchmark evidence",
)
require(
    "run: make bench-evidence-check" in bench_job,
    "ci.yml: bench-fuzz job must run make bench-evidence-check before benchmark smoke/trend artifacts",
)
require(
    "run: make bench-regression-check" in bench_job,
    "ci.yml: bench-fuzz job must run make bench-regression-check after benchmark smoke",
)
require(
    bench_job.index("run: make bench-evidence-check") < bench_job.index("bash bin/scripts/benchstat.sh --smoke"),
    "ci.yml: benchmark evidence gate must run before benchmark smoke rewrites current benchmark artifacts",
)
require(
    bench_job.index("bash bin/scripts/benchstat.sh --smoke") < bench_job.index("run: make bench-regression-check"),
    "ci.yml: benchmark regression gate must run after benchmark smoke writes bench/current.txt",
)
require(
    bench_job.index("run: make bench-regression-check") < bench_job.index("bash bin/scripts/benchstat.sh --trend"),
    "ci.yml: benchmark regression gate must run before benchmark trend artifacts are uploaded",
)
for artifact in sorted(expected_benchmark_artifacts):
    require((root / artifact).exists(), f"benchmark artifact is missing: {artifact}")
require(
    "bench-evidence-check" in agents or "bench-evidence-check" in makefile,
    "benchmark evidence governance must mention bench-evidence-check in AGENTS.md or Makefile",
)

docker_ci_upload_paths = extract_upload_artifact_paths(docker_upload_step)
require(
    docker_ci_upload_paths == expected_docker_ci_evidence_files,
    "ci.yml: docker-trivy-evidence artifact file set drifted: "
    f"missing={sorted(expected_docker_ci_evidence_files - docker_ci_upload_paths)} "
    f"extra={sorted(docker_ci_upload_paths - expected_docker_ci_evidence_files)}",
)
require(
    "name: docker-trivy-evidence" in docker_upload_step,
    "ci.yml: Docker CI evidence artifact name must remain docker-trivy-evidence for release download",
)
require(
    "--name docker-trivy-evidence" in release_job,
    "ci.yml: release job must download the docker-trivy-evidence artifact from the Docker required check",
)
require(
    "--dir release-evidence/docker/ci" in release_job,
    "ci.yml: release job must normalize downloaded Docker CI evidence under release-evidence/docker/ci",
)
for evidence_file in sorted(expected_docker_ci_evidence_files):
    if evidence_file in {"docker-build-evidence.json", "trivy-results.sarif"}:
        release_path = f"release-evidence/docker/ci/{evidence_file}"
        require(release_path in release_job, f"ci.yml: release job must assert downloaded Docker CI evidence {release_path}")
require(
    extract_python_evidence_keys(docker_build_step, "gofly.docker_build_evidence.v1") == expected_ci_docker_build_fields,
    "ci.yml: docker-build-evidence.json schema fields drifted from release validator expectations",
)
require(
    "gofly.docker_build_evidence.v1" in release_artifacts_script,
    "check-release-artifacts.sh: release validator must validate the CI Docker build evidence schema",
)
for field in sorted(expected_ci_docker_build_fields):
    require(
        f'"{field}"' in release_artifacts_script or f"{field!r}" in release_artifacts_script,
        f"check-release-artifacts.sh: missing validation for CI Docker build evidence field {field!r}",
    )

release_digest_fields = extract_python_evidence_keys(release_digest_step, "gofly.release_docker_digest_evidence.v1")
require(
    release_digest_fields == expected_release_docker_digest_fields,
    "ci.yml: release-docker-digests.json schema fields drifted: "
    f"missing={sorted(expected_release_docker_digest_fields - release_digest_fields)} "
    f"extra={sorted(release_digest_fields - expected_release_docker_digest_fields)}",
)
for evidence_file in sorted(expected_release_docker_evidence_files):
    require(evidence_file in release_job, f"ci.yml: release Docker evidence file is not produced or asserted: {evidence_file}")
for marker in (
    "RELEASE_REQUIRE_DOCKER_EVIDENCE: \"true\"",
    "RELEASE_EVIDENCE_DIR: release-evidence/docker",
    "run: make release-artifacts-check",
):
    require(marker in release_job, f"ci.yml: Docker release evidence verification marker is missing: {marker}")
for marker in (
    "gofly.release_docker_digest_evidence.v1",
    "release-docker-digests.json",
    "release-trivy-results.json",
    "release-docker-attestation-verification.json",
    "release-docker-sbom",
    "checksums-attestation-verification.json",
    "trivy-results.sarif",
    "docker-build-evidence.json",
):
    require(marker in release_artifacts_script, f"check-release-artifacts.sh: missing Docker release evidence marker {marker!r}")
for marker in (
    "gofly.release_docker_digest_evidence.v1",
    "gofly.docker_build_evidence.v1",
    "release-trivy-results.json",
    "release-docker-attestation-verification.json",
    "checksums-attestation-verification.json",
    "docker-build-evidence.json",
):
    require(marker in release_artifacts_test, f"check-release-artifacts-test.sh: missing Docker evidence fixture marker {marker!r}")

rounds = {}
for match in re.finditer(r"run_round\s+(\d+)\s+\"([^\"]+)\"", governance_script):
    rounds[int(match.group(1))] = match.group(2)
for round_no, round_name in expected_governance_rounds.items():
    require(
        rounds.get(round_no) == round_name,
        f"governance-10-rounds.sh: round {round_no} drifted: expected {round_name!r}, got {rounds.get(round_no)!r}",
    )
require(
    set(rounds) == set(expected_governance_rounds),
    "governance-10-rounds.sh: executable round set drifted: "
    f"missing={sorted(set(expected_governance_rounds) - set(rounds))} extra={sorted(set(rounds) - set(expected_governance_rounds))}",
)
for env_name, metadata in sorted(expected_governance_skip_envs.items()):
    require(env_name in governance_script, f"governance-10-rounds.sh: missing skip env {env_name}")
    require(
        f"record_skip {metadata['round']}" in governance_script and metadata["compensating_gate"] in governance_script,
        f"governance-10-rounds.sh: skip env {env_name} must record its compensating gate",
    )
    if metadata["release_blocking"]:
        require(
            f"assert_not_release_skip {env_name}" in governance_script,
            f"governance-10-rounds.sh: release-blocking skip env {env_name} must be rejected for releases",
        )

require(
    "sh $(SCRIPTS_DIR)/governance-10-rounds.sh" in governance_10_target,
    "Makefile: governance-10-rounds target must delegate to bin/scripts/governance-10-rounds.sh",
)
for env_name in ("COVERAGE_THRESHOLD", "COVERAGE_RATCHET"):
    require(
        f"{env_name}=$({env_name})" in governance_10_target,
        f"Makefile: governance-10-rounds target must pass {env_name} to the script",
    )
require(
    "governance-10-rounds" in governance_target_deps,
    "Makefile: governance target must delegate to governance-10-rounds instead of running a partial gate subset",
)
require(
    "api-compat" in governance_target_deps,
    "Makefile: governance target must keep the public API compatibility gate",
)
require(
    "governance" in ci_target_deps or "governance-10-rounds" in ci_target_deps,
    "Makefile: ci target must include governance or governance-10-rounds so local full CI matches required CI gates",
)
require(
    "run: make governance-10-rounds" in governance_job,
    "ci.yml: governance job must run make governance-10-rounds",
)
require(
    "GOVERNANCE_SKIP_REPORT" in governance_job and "Upload governance skip report" in workflow,
    "ci.yml: governance job must emit and upload the governance skip report",
)
require(
    'GOVERNANCE_SKIP_GENERATED_MATRIX: "true"' in governance_job,
    "ci.yml: governance job must explicitly skip the generated matrix only when the build-test job compensates",
)
require(
    "startsWith(github.ref, 'refs/tags/v') && 'false' || 'true'" in governance_job,
    "ci.yml: governance job must force generated control-plane smoke on release tags",
)
require(
    "GOVERNANCE_SKIP_RACE" not in governance_job and "GOVERNANCE_SKIP_SECURITY" not in governance_job,
    "ci.yml: governance job must not skip release-blocking race or security gates by default",
)
require(
    "cleanup_ai_manifest_http" in governance_script
    and "trap 'cleanup_ai_manifest_http;" in governance_script
    and "cleanup_ai_manifest_http\n\ttrap 'chmod -R u+w" in governance_script,
    "governance-10-rounds.sh: AI manifest HTTP fixture must be killed on both failure and success paths",
)
for marker in (
    "make governance-10-rounds",
    "Round 14 of `bin/scripts/governance-10-rounds.sh`",
    "GOVERNANCE_SKIP_SECURITY",
    "COVERAGE_RATCHET",
):
    require(marker in agents, f"AGENTS.md: missing governance documentation marker {marker!r}")
for marker in ("go test", "go vet", "golangci-lint", "-race", "gosec", "govulncheck"):
    require(marker in agents, f"AGENTS.md: missing mandatory governance gate marker {marker!r}")

for check in ("branch protection required-check audit", "release (tagged)", "release-artifacts-test"):
    require(check in checklist or check in workflow or check in makefile, f"required-check marker {check!r} is not referenced")

require(
    integration_ownership.get("schema") == "gofly.integration_ownership_matrix.v1",
    "integration-ownership-matrix.json: schema mismatch",
)
require(
    integration_ownership.get("status") == "blocking",
    "integration-ownership-matrix.json: status must be blocking",
)
require(
    integration_ownership.get("blockingGate") == "make required-checks-drift-check",
    "integration-ownership-matrix.json: blockingGate mismatch",
)
for included in (
    "SQL integration ownership",
    "Redis integration ownership",
    "MQ integration ownership",
    "discovery integration ownership",
    "gateway integration ownership",
    "RPC integration ownership",
    "observability integration ownership",
    "generated-project dependency boundary",
    "release prerequisite drift",
    "rollback notes",
):
    require(
        included in set((integration_ownership.get("scope") or {}).get("included") or []),
        f"integration-ownership-matrix.json: scope.included missing {included!r}",
    )
for excluded in (
    "community size",
    "cluster provisioning",
    "production credential management",
    "framework runtime replacement claims",
):
    require(
        excluded in set((integration_ownership.get("scope") or {}).get("excluded") or []),
        f"integration-ownership-matrix.json: scope.excluded missing {excluded!r}",
    )
for evidence_path in integration_ownership.get("sourceEvidence") or []:
    require((root / evidence_path).exists(), f"integration-ownership-matrix.json: sourceEvidence path missing: {evidence_path}")
release_policy = integration_ownership.get("releasePolicy") or {}
require(
    release_policy.get("requiredCheckSource") == "docs/reference/ci-required-check-evidence.json",
    "integration-ownership-matrix.json: releasePolicy.requiredCheckSource mismatch",
)
require(
    release_policy.get("dependencyEvidence") == "docs/reference/dependency-upgrade-evidence.json",
    "integration-ownership-matrix.json: releasePolicy.dependencyEvidence mismatch",
)
for field in ("fallbackPolicy", "generatedProjectPolicy"):
    require(
        len(str(release_policy.get(field) or "").split()) >= 12,
        f"integration-ownership-matrix.json: releasePolicy.{field} must be actionable",
    )

integration_rows = integration_ownership.get("integrations")
if not isinstance(integration_rows, list):
    missing.append("integration-ownership-matrix.json: integrations must be a list")
    integration_rows = []
expected_family_checks = {
    "sql": {
        "requiredCheck": "integration tests (storage-mysql-postgres)",
        "releasePrerequisite": "integration",
        "ciJob": "integration",
        "localGate": "make db-cache-productization-check",
    },
    "redis": {
        "requiredCheck": "integration tests (mq-brokers)",
        "releasePrerequisite": "integration",
        "ciJob": "integration",
        "localGate": "make db-cache-productization-check",
    },
    "mq": {
        "requiredCheck": "integration tests (mq-brokers)",
        "releasePrerequisite": "integration",
        "ciJob": "integration",
        "localGate": "make integration-tests",
    },
    "discovery": {
        "requiredCheck": "integration tests (config-consul-nacos-etcd)",
        "releasePrerequisite": "integration",
        "ciJob": "integration",
        "localGate": "make discovery-adapter-matrix-check",
    },
    "gateway": {
        "requiredCheck": "integration tests (gateway-transcode)",
        "releasePrerequisite": "integration",
        "ciJob": "integration",
        "localGate": "make api-contract-check",
    },
    "rpc": {
        "requiredCheck": "contract / api+rpc (check + breaking)",
        "releasePrerequisite": "contract-check",
        "ciJob": "contract-check",
        "localGate": "make rpc-boundary-check",
    },
    "observability": {
        "requiredCheck": "governance gates",
        "releasePrerequisite": "governance",
        "ciJob": "governance",
        "localGate": "make governance-report-check",
    },
}
actual_family_ids = {
    item.get("id")
    for item in integration_rows
    if isinstance(item, dict) and item.get("id")
}
require(
    actual_family_ids == set(expected_family_checks),
    "integration-ownership-matrix.json: integration ids drifted: "
    f"missing={sorted(set(expected_family_checks) - actual_family_ids)} extra={sorted(actual_family_ids - set(expected_family_checks))}",
)
target_names = set(re.findall(r"^([A-Za-z0-9_.-]+):", makefile, re.M))
for item in integration_rows:
    if not isinstance(item, dict):
        missing.append(f"integration-ownership-matrix.json: integration row must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in (
        "id",
        "family",
        "owner",
        "surface",
        "supportedProfiles",
        "localGate",
        "ciJob",
        "requiredCheck",
        "releasePrerequisite",
        "dependencyUpgradeTrigger",
        "generatedProjectBoundary",
        "evidence",
        "rollbackNote",
    ):
        require(item.get(field) not in ("", None, []), f"integration-ownership-matrix.json: {item_id}: {field} is required")
    expected = expected_family_checks.get(item_id) or {}
    for field, expected_value in expected.items():
        require(item.get(field) == expected_value, f"integration-ownership-matrix.json: {item_id}: {field} mismatch")
    require(item.get("requiredCheck") in expected_default_checks, f"integration-ownership-matrix.json: {item_id}: requiredCheck is not branch-protected")
    require(item.get("requiredCheck") in branch_audit_expected, f"integration-ownership-matrix.json: {item_id}: requiredCheck is not in branch audit expected set")
    require(item.get("releasePrerequisite") in release_needs, f"integration-ownership-matrix.json: {item_id}: releasePrerequisite is not release-blocking")
    require(item.get("ciJob") in job_ids, f"integration-ownership-matrix.json: {item_id}: ciJob is missing")
    local_gate = str(item.get("localGate") or "")
    if local_gate.startswith("make "):
        gate_target = local_gate.split()[1]
        require(gate_target in target_names, f"integration-ownership-matrix.json: {item_id}: localGate target is missing: {gate_target}")
    require(len(item.get("supportedProfiles") or []) >= 2, f"integration-ownership-matrix.json: {item_id}: supportedProfiles must name at least two profiles")
    for evidence_path in item.get("evidence") or []:
        require((root / evidence_path).exists(), f"integration-ownership-matrix.json: {item_id}: evidence path missing: {evidence_path}")
    for field in ("dependencyUpgradeTrigger", "generatedProjectBoundary", "rollbackNote"):
        require(
            len(str(item.get(field) or "").split()) >= 10,
            f"integration-ownership-matrix.json: {item_id}: {field} must be actionable",
        )
    require(
        "generated" in str(item.get("generatedProjectBoundary") or "").lower(),
        f"integration-ownership-matrix.json: {item_id}: generatedProjectBoundary must name generated projects",
    )

adopter_proof_rows = integration_ownership.get("adopterProofRows")
if not isinstance(adopter_proof_rows, list):
    missing.append("integration-ownership-matrix.json: adopterProofRows must be a list")
    adopter_proof_rows = []
expected_adopter_proofs = {
    "sql-generated-boundary-proof": {
        "family": "sql",
        "adapter": "SQLStore / Cluster",
        "gate": "make db-cache-productization-check",
        "requiredAdapters": {"mysql", "postgres", "sqlite-memory"},
    },
    "redis-cache-adoption-proof": {
        "family": "redis",
        "adapter": "Redis model cache / tiered cache",
        "gate": "make db-cache-productization-check",
        "requiredAdapters": {"redis-cache", "redis-stream", "typed-tiered-cache"},
    },
    "discovery-adapter-adoption-proof": {
        "family": "discovery",
        "adapter": "memory / Consul / etcdv3 discovery",
        "gate": "make discovery-adapter-matrix-check",
        "requiredAdapters": {"memory", "consul", "etcdv3"},
    },
}
actual_adopter_proof_ids = {
    item.get("id")
    for item in adopter_proof_rows
    if isinstance(item, dict) and item.get("id")
}
require(
    actual_adopter_proof_ids == set(expected_adopter_proofs),
    "integration-ownership-matrix.json: adopterProofRows drifted: "
    f"missing={sorted(set(expected_adopter_proofs) - actual_adopter_proof_ids)} "
    f"extra={sorted(actual_adopter_proof_ids - set(expected_adopter_proofs))}",
)
for item in adopter_proof_rows:
    if not isinstance(item, dict):
        missing.append(f"integration-ownership-matrix.json: adopterProofRows row must be an object: {item!r}")
        continue
    proof_id = item.get("id", "<missing>")
    expected = expected_adopter_proofs.get(proof_id) or {}
    for field in (
        "id",
        "family",
        "adapter",
        "supportedAdapters",
        "generatedProjectBoundary",
        "dependencyUpgradeTrigger",
        "observabilityEvidence",
        "fallbackBehavior",
        "gate",
        "rollbackOrEscalation",
    ):
        require(item.get(field) not in ("", None, []), f"integration-ownership-matrix.json: adopter proof {proof_id}: {field} is required")
    for field in ("family", "adapter", "gate"):
        require(item.get(field) == expected.get(field), f"integration-ownership-matrix.json: adopter proof {proof_id}: {field} mismatch")
    gate = str(item.get("gate") or "")
    if gate.startswith("make "):
        gate_target = gate.split()[1]
        require(gate_target in target_names, f"integration-ownership-matrix.json: adopter proof {proof_id}: gate target is missing: {gate_target}")
    supported = set(item.get("supportedAdapters") or [])
    require(
        expected.get("requiredAdapters", set()) <= supported,
        f"integration-ownership-matrix.json: adopter proof {proof_id}: supportedAdapters missing {sorted(expected.get('requiredAdapters', set()) - supported)}",
    )
    require(item.get("family") in actual_family_ids, f"integration-ownership-matrix.json: adopter proof {proof_id}: family has no integration row")
    for field in (
        "generatedProjectBoundary",
        "dependencyUpgradeTrigger",
        "observabilityEvidence",
        "fallbackBehavior",
        "rollbackOrEscalation",
    ):
        require(
            len(str(item.get(field) or "").split()) >= 10,
            f"integration-ownership-matrix.json: adopter proof {proof_id}: {field} must be actionable",
        )
    lower_boundary = str(item.get("generatedProjectBoundary") or "").lower()
    require("generated" in lower_boundary and "dependencies" in lower_boundary, f"integration-ownership-matrix.json: adopter proof {proof_id}: generatedProjectBoundary must name generated dependencies")
    require("fallback" in str(item.get("fallbackBehavior") or "").lower(), f"integration-ownership-matrix.json: adopter proof {proof_id}: fallbackBehavior must name fallback")
    rollback_text = str(item.get("rollbackOrEscalation") or "").lower()
    require(
        "rollback" in rollback_text or "pin" in rollback_text or "disable" in rollback_text or "keep" in rollback_text,
        f"integration-ownership-matrix.json: adopter proof {proof_id}: rollbackOrEscalation must name rollback, pin, disable, or keep",
    )

if missing:
    print("required-check drift check failed:", file=sys.stderr)
    for item in missing:
        print(f"  - {item}", file=sys.stderr)
    sys.exit(1)

print(
    "required-check drift ok: "
    f"{len(expected_default_checks)} default checks, {len(expected_release_needs)} release prerequisites, "
    f"{len(expected_integration_matrix)} integration matrix entries, "
    f"{len(expected_family_checks)} integration ownership families, "
    f"{len(expected_governance_rounds)} governance rounds"
)
PY
