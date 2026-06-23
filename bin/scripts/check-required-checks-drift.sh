#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
workflow_path = root / ".github" / "workflows" / "ci.yml"
checklist_path = root / "docs" / "operations" / "production-checklist.md"
makefile_path = root / "Makefile"
governance_script_path = root / "bin" / "scripts" / "governance-10-rounds.sh"
agents_path = root / "AGENTS.md"

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
    11: "generated project verification matrix",
    12: "generated project runtime control-plane smoke",
    13: "docs, coverage, security, and final package listing",
}

expected_governance_skip_envs = {
    "GOVERNANCE_SKIP_RACE": {
        "round": "6",
        "compensating_gate": "run go test -race ./... before merge/release",
        "release_blocking": True,
    },
    "GOVERNANCE_SKIP_GENERATED_MATRIX": {
        "round": "11",
        "compensating_gate": "make test-generated-matrix in build-test job",
        "release_blocking": False,
    },
    "GOVERNANCE_SKIP_GENERATED_CONTROL_PLANE_SMOKE": {
        "round": "12",
        "compensating_gate": "make generated-control-plane-smoke in build-test job",
        "release_blocking": True,
    },
    "GOVERNANCE_SKIP_SECURITY": {
        "round": "13",
        "compensating_gate": "run make security before merge/release",
        "release_blocking": True,
    },
}

workflow = workflow_path.read_text(encoding="utf-8")
checklist = checklist_path.read_text(encoding="utf-8")
makefile = makefile_path.read_text(encoding="utf-8")
governance_script = governance_script_path.read_text(encoding="utf-8")
agents = agents_path.read_text(encoding="utf-8")

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
governance_target_deps = extract_make_target_deps(makefile, "governance")
governance_10_target = extract_make_target_body(makefile, "governance-10-rounds")
ci_target_deps = extract_make_target_deps(makefile, "ci")

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
require(
    re.search(r"^supply-chain:.*release-artifacts-test", makefile, re.M) is not None,
    "Makefile: supply-chain target must depend on release-artifacts-test",
)
require(
    "run: make supply-chain" in workflow,
    "ci.yml: supply-chain job must run make supply-chain so release-artifacts-test is included",
)

require(
    release_needs == expected_release_needs,
    "ci.yml: release job needs drifted: "
    f"missing={sorted(expected_release_needs - release_needs)} extra={sorted(release_needs - expected_release_needs)}",
)
require(
    release_needs <= job_ids,
    f"ci.yml: release needs unknown job id(s): {sorted(release_needs - job_ids)}",
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
    "Round 13 of `bin/scripts/governance-10-rounds.sh`",
    "GOVERNANCE_SKIP_SECURITY",
    "COVERAGE_RATCHET",
):
    require(marker in agents, f"AGENTS.md: missing governance documentation marker {marker!r}")
for marker in ("go test", "go vet", "golangci-lint", "-race", "gosec", "govulncheck"):
    require(marker in agents, f"AGENTS.md: missing mandatory governance gate marker {marker!r}")

for check in ("branch protection required-check audit", "release (tagged)", "release-artifacts-test"):
    require(check in checklist or check in workflow or check in makefile, f"required-check marker {check!r} is not referenced")

if missing:
    print("required-check drift check failed:", file=sys.stderr)
    for item in missing:
        print(f"  - {item}", file=sys.stderr)
    sys.exit(1)

print(
    "required-check drift ok: "
    f"{len(expected_default_checks)} default checks, {len(expected_release_needs)} release prerequisites, "
    f"{len(expected_integration_matrix)} integration matrix entries, "
    f"{len(expected_governance_rounds)} governance rounds"
)
PY
