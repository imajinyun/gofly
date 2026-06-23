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

workflow = workflow_path.read_text(encoding="utf-8")
checklist = checklist_path.read_text(encoding="utf-8")
makefile = makefile_path.read_text(encoding="utf-8")

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


branch_audit_expected = extract_branch_audit_expected(workflow)
release_needs = extract_release_needs(workflow)
job_ids = extract_job_ids(workflow)
integration_matrix = extract_integration_matrix(workflow)
integration_target = extract_make_target_body(makefile, "integration-tests")
dependency_target = extract_make_target_body(makefile, "dependency-upgrade-check")
dependency_job = extract_job_body(workflow, "dependency-upgrade-validation")

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
    f"{len(expected_integration_matrix)} integration matrix entries"
)
PY
