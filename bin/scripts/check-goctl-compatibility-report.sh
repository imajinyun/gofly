#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "goctl-compatibility-report.json"
missing = []

expected_stages = {
    "api-flag-parity": ("blocking", "make goctl-api-flag-parity-check", "docs/reference/goctl-api-flag-parity.json"),
    "rpc-protoc-parity": ("blocking", "make goctl-rpc-protoc-parity-check", "docs/reference/goctl-rpc-protoc-parity.json"),
    "model-parity-replay": ("blocking", "make goctl-model-parity-replay-check", "docs/reference/goctl-model-parity-replay.json"),
    "oracle-replay": ("report-only", "make goctl-oracle-replay-check", "docs/reference/goctl-oracle-replay.json"),
    "real-project-replay": ("blocking", "make goctl-real-project-replay-check", "docs/reference/goctl-real-project-replay.json"),
}
expected_blocking_gates = {
    "make goctl-api-flag-parity-check",
    "make goctl-rpc-protoc-parity-check",
    "make goctl-model-parity-replay-check",
    "make goctl-real-project-replay-check",
    "make goctl-generator-compat-check",
}
expected_report_only_gates = {
    "make goctl-surface-drift-check",
    "make goctl-oracle-replay-check",
}


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def read_json(path):
    text = read_text(path)
    if not text:
        return {}
    return json.loads(text)


def require(condition, message):
    if not condition:
        missing.append(message)


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


def gate_is_known(gate, targets):
    if gate.startswith("make "):
        parts = gate.removeprefix("make ").split()
        return bool(parts) and parts[0] in targets
    return gate.startswith("go test ")


manifest = read_json(manifest_path)
makefile = read_text(root / "Makefile")
generator_matrix = read_json(root / "docs" / "reference" / "goctl-generator-compatibility.json")
oracle_contract = read_json(root / "docs" / "reference" / "goctl-oracle-replay.json")
targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.goctl_compatibility_report.v1", "schema must be gofly.goctl_compatibility_report.v1")
require(manifest.get("acceptanceGate") == "make goctl-compatibility-report-check", "acceptanceGate mismatch")
require("goctl-compatibility-report-check" in targets, "Makefile must expose goctl-compatibility-report-check")
require("check-goctl-compatibility-report.sh" in makefile, "Makefile must call check-goctl-compatibility-report.sh")
require("goctl-compatibility-report-check" in docs_check_line, "docs-check must depend on goctl-compatibility-report-check")
require("not a full goctl replacement" in str(manifest.get("positioning") or ""), "positioning must preserve migration path stance")

for source in manifest.get("sourceOfTruth") or []:
    require((root / source).exists(), f"sourceOfTruth path missing: {source}")

stages = {item.get("id"): item for item in manifest.get("stages") or []}
require(set(stages) == set(expected_stages), f"stages drifted: missing={sorted(set(expected_stages) - set(stages))} extra={sorted(set(stages) - set(expected_stages))}")
for stage_id, (status, gate, contract) in expected_stages.items():
    item = stages.get(stage_id) or {}
    require(item.get("status") == status, f"{stage_id}: status must be {status}")
    require(item.get("gate") == gate, f"{stage_id}: gate mismatch")
    require(item.get("contract") == contract, f"{stage_id}: contract mismatch")
    require(gate_is_known(gate, targets), f"{stage_id}: gate is not known: {gate}")
    require((root / contract).is_file(), f"{stage_id}: contract file missing: {contract}")
    require(len(str(item.get("summary") or "").split()) >= 8, f"{stage_id}: summary must be actionable")
    if status == "report-only":
        require(item.get("reportOnlyClasses"), f"{stage_id}: reportOnlyClasses are required")

blocking = set(manifest.get("blockingGates") or [])
report_only = set(manifest.get("reportOnlyGates") or [])
require(blocking == expected_blocking_gates, f"blockingGates drifted: missing={sorted(expected_blocking_gates - blocking)} extra={sorted(blocking - expected_blocking_gates)}")
require(report_only == expected_report_only_gates, f"reportOnlyGates drifted: missing={sorted(expected_report_only_gates - report_only)} extra={sorted(report_only - expected_report_only_gates)}")
require(not blocking & report_only, f"gates cannot be both blocking and report-only: {sorted(blocking & report_only)}")
for gate in blocking | report_only:
    require(gate_is_known(gate, targets), f"unknown gate in report: {gate}")

release_gates = set(generator_matrix.get("releaseGates") or [])
for gate in expected_blocking_gates - {"make goctl-generator-compat-check"}:
    require(gate in release_gates, f"goctl generator compatibility releaseGates missing {gate}")

failure_policy = oracle_contract.get("failureClasses") or {}
oracle_report_only = set((stages.get("oracle-replay") or {}).get("reportOnlyClasses") or [])
require(oracle_report_only <= set(failure_policy.get("reportOnly") or []), "oracle reportOnlyClasses must be backed by goctl-oracle-replay failureClasses.reportOnly")

checklist = manifest.get("releaseChecklist") or []
for needle in (
    "make goctl-compatibility-report-check",
    "make goctl-generator-compat-check",
    "make goctl-real-project-replay-check",
    "report-only",
):
    require(any(needle in str(item) for item in checklist), f"releaseChecklist missing {needle!r}")

claim_policy = manifest.get("claimPolicy") or {}
require("goctl-compatible migration coverage" in str(claim_policy.get("allowed") or ""), "claimPolicy.allowed must describe migration coverage")
for forbidden in ("full goctl replacement", "byte-for-byte output parity", "default external plugin execution"):
    require(forbidden in str(claim_policy.get("forbidden") or ""), f"claimPolicy.forbidden missing {forbidden!r}")

if missing:
    print("goctl compatibility report check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("goctl compatibility report OK")
PY
