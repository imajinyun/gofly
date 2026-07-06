#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
workflow_path = root / ".github" / "workflows" / "ci.yml"
makefile_path = root / "Makefile"
missing = []

workflow = workflow_path.read_text(encoding="utf-8") if workflow_path.is_file() else ""
makefile = makefile_path.read_text(encoding="utf-8") if makefile_path.is_file() else ""


def require(condition, message):
    if not condition:
        missing.append(message)


def job_block(job_name):
    pattern = rf"(?ms)^  {re.escape(job_name)}:\n(?P<body>.*?)(?=^  [a-zA-Z0-9_-]+:\n|\Z)"
    match = re.search(pattern, workflow)
    return match.group("body") if match else ""


gateway_job = job_block("gateway-profile-contract")
release_job = job_block("release")
branch_audit_job = job_block("branch-protection-audit")

require(workflow, ".github/workflows/ci.yml must exist")
require("required-checks-drift-check:" in makefile, "Makefile must expose required-checks-drift-check")
require("sh $(SCRIPTS_DIR)/check-required-checks-drift.sh" in makefile, "required-checks-drift-check must execute check-required-checks-drift.sh")

require(gateway_job, "ci.yml must define gateway-profile-contract job")
require("name: gateway profile contract" in gateway_job, "gateway-profile-contract job must publish required check name")
require("Gateway profile contract gate" in gateway_job, "gateway-profile-contract job must have an explicit gate step")
require("Gateway aggregation diff summary" in gateway_job, "gateway-profile-contract job must publish aggregation markdown summary")
require("Upload gateway aggregation SARIF artifact" in gateway_job, "gateway-profile-contract job must upload aggregation SARIF artifact")
require("Upload gateway aggregation SARIF to Code Scanning" in gateway_job, "gateway-profile-contract job must keep optional Code Scanning upload step")
require("vars.GOFLY_UPLOAD_AGGREGATION_SARIF == 'true'" in gateway_job, "Code Scanning upload must stay opt-in")
for token in (
    "GatewayProfileValidateCommandJSON",
    "GatewayProfileValidateCommandBreakingAndUsage",
    "GatewayAggregationValidateCommandJSON",
    "ReleaseGatewayProfileContractCheck",
    "ReleaseGatewayAggregationContractCheck",
    "ExecuteAIManifestJSONEnvelope",
    "ExecuteAIManifestAliasAndText",
    "TestAINewGeneratedProjectVerificationMatrix",
    "--format markdown",
    "--format sarif",
    "gateway-aggregation.sarif",
    "gateway-aggregation-sarif",
    "github/codeql-action/upload-sarif@8aad20d150bbac5944a9f9d289da16a4b0d87c1e",
    "security-events: write",
    "sarif_file: gateway-aggregation.sarif",
    "if-no-files-found: error",
    "edge-openapi-breaking.json",
    "Intentionally breaking fixture",
    "$GITHUB_STEP_SUMMARY",
    "make aiflow-profile-gate-check",
    "make required-checks-drift-check",
):
    require(token in gateway_job, f"gateway-profile-contract job missing {token!r}")

require(release_job, "ci.yml must define release job")
release_needs = re.search(r"needs:\s*\[(?P<needs>[^\]]+)\]", release_job)
require(release_needs is not None, "release job must declare explicit needs")
if release_needs is not None:
    needs = {item.strip() for item in release_needs.group("needs").split(",")}
    require("gateway-profile-contract" in needs, "release job must need gateway-profile-contract")

require(branch_audit_job, "ci.yml must define branch-protection-audit job")
require('"gateway profile contract"' in branch_audit_job, "branch protection audit must expect gateway profile contract required check")

if missing:
    print("required-check drift check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("required-check drift ok: gateway-profile-contract is hosted, release-blocking, and branch-protection-audited")
PY
