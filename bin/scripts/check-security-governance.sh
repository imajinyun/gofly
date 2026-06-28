#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "security-defensive-governance.json"
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


makefile = read_text(root / "Makefile")
dashboard = json.loads(read_text(root / "docs" / "reference" / "governance-dashboard-contract.json") or "{}")
ci_evidence = json.loads(read_text(root / "docs" / "reference" / "ci-required-check-evidence.json") or "{}")
baseline = json.loads(read_text(root / "bin" / "scripts" / "gosec-exception-baseline.json") or "{}")
governance_script = read_text(root / "bin" / "scripts" / "governance-10-rounds.sh")
inventory_script = read_text(root / "bin" / "scripts" / "gosec-exception-inventory.sh")

try:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
except FileNotFoundError:
    manifest = {}
    missing.append("docs/reference/security-defensive-governance.json is missing")
except json.JSONDecodeError as exc:
    manifest = {}
    missing.append(f"docs/reference/security-defensive-governance.json is invalid JSON: {exc}")


def target_deps(name):
    match = re.search(rf"^{re.escape(name)}:(?P<deps>[^#\n]*)", makefile, re.M)
    require(match is not None, f"Makefile target {name!r} is missing")
    return match.group("deps") if match else ""


def target_body(name):
    match = re.search(
        rf"^\.PHONY:\s*{re.escape(name)}\n"
        rf"{re.escape(name)}:.*?\n(?P<body>.*?)(?=^\.PHONY:|\Z)",
        makefile,
        re.S | re.M,
    )
    require(match is not None, f"Makefile target {name!r} is missing")
    return match.group("body") if match else ""


security_deps = target_deps("security")
gosec_body = target_body("gosec")
govulncheck_body = target_body("govulncheck")
inventory_body = target_body("gosec-inventory-check")

security_order = security_deps.split()
require(security_order[:3] == ["security-governance-check", "govulncheck", "gosec"], "security target must run governance, govulncheck, and gosec in order")
require("$(GOVULNCHECK) -scan=$(GOVULNCHECK_SCAN) -show=traces $(PKGS)" in govulncheck_body, "govulncheck must include scan mode, traces, and package scope")
require("GOSEC_INVENTORY_BASELINE=$(GOSEC_INVENTORY_BASELINE)" in gosec_body, "gosec must check #nosec baseline before scanning")
require("$(GOSEC) $(GOSEC_FLAGS) ./..." in gosec_body, "gosec must scan the repository with configured flags")
require("gosec-exception-inventory.sh" in inventory_body, "gosec-inventory-check must use gosec exception inventory")

security = dashboard.get("security") or {}
require(security.get("gosecGate") == "make gosec", "dashboard security.gosecGate must be make gosec")
require(security.get("govulncheckGate") == "make govulncheck", "dashboard security.govulncheckGate must be make govulncheck")
require(security.get("baseline") == "bin/scripts/gosec-exception-baseline.json", "dashboard security baseline path mismatch")

checks = {
    item.get("job"): item
    for item in ci_evidence.get("checks") or []
    if isinstance(item, dict) and item.get("job")
}
security_check = checks.get("security") or {}
require(security_check.get("localGate") == "make security", "CI security check localGate must be make security")
require(security_check.get("artifact") == "govulncheck and gosec output", "CI security check artifact must name govulncheck and gosec output")

require(baseline.get("schema") == "gofly.gosec_exception_baseline.v1", "gosec baseline schema mismatch")
allowed = baseline.get("allowed_exceptions") or []
require(bool(allowed), "gosec baseline must contain reviewed exceptions")
for item in allowed:
    parts = item.split("|")
    require(len(parts) == 3, f"gosec baseline entry must be file|rules|rationale: {item!r}")
    if len(parts) == 3:
        require(parts[1].startswith("G"), f"gosec baseline entry rules must be explicit: {item!r}")
        require(len(parts[2].split()) >= 6, f"gosec baseline rationale must be actionable: {item!r}")

for needle in (
    "GOVERNANCE_SKIP_SECURITY",
    "assert_not_release_skip GOVERNANCE_SKIP_SECURITY",
    "run make security before merge/release",
    "round_security_audit",
):
    require(needle in governance_script, f"governance-10-rounds.sh missing security release-skip guard {needle!r}")

for needle in ("trust_boundary", "current_protection", "coverage_tests", "replaceable_helper"):
    require(needle in inventory_script, f"gosec exception inventory must emit {needle!r}")

require(manifest.get("schema") == "gofly.security_defensive_governance.v1", "security defensive governance schema mismatch")
require(manifest.get("aiflowTask") == "GOFLY-GOV-10R3-08", "security defensive governance aiflowTask mismatch")
require(manifest.get("acceptanceGate") == "make security", "security defensive governance acceptanceGate mismatch")
aggregate_gates = set(manifest.get("aggregateGates") or [])
for gate in ("make security-governance-check", "make govulncheck", "make gosec"):
    require(gate in aggregate_gates, f"security defensive governance aggregateGates missing {gate}")

for rel in manifest.get("sourceCode") or []:
    require((root / rel).exists(), f"security defensive governance source is missing: {rel}")

policy = manifest.get("policy") or {}
for key in (
    "securityGateRunsGovernanceBeforeScanners",
    "gosecBaselineMustBeReviewedAndMachineChecked",
    "releaseGovernanceCannotSkipSecurity",
    "pathWritesUseRootAndSymlinkGuards",
    "externalProcessesUseArgvAndExplicitOperatorInput",
    "remoteDownloadsRejectInsecureSchemesAndLimitBytes",
    "bodyLimitsAreCoveredForRestRpcGateway",
    "secretValuesAreRedactedFromRuntimeAndAdminSurfaces",
):
    require(policy.get(key) is True, f"policy.{key} must be true")

surface_ids = {item.get("id") for item in manifest.get("surfaces") or [] if isinstance(item, dict)}
required_surfaces = {
    "scanner-and-baseline",
    "release-skip-protection",
    "path-and-symlink-safety",
    "external-process-and-remote-downloads",
    "body-limit-and-url-scheme",
    "secret-redaction-and-template-safety",
}
require(surface_ids == required_surfaces, f"security defensive surfaces mismatch: {sorted(surface_ids)!r}")

for item in manifest.get("surfaces") or []:
    if not isinstance(item, dict):
        missing.append(f"surface entry must be an object: {item!r}")
        continue
    surface = item.get("id", "<missing>")
    for field in ("risk", "gate", "evidenceRefs"):
        require(item.get(field), f"surface {surface}: {field} is required")
    gate = str(item.get("gate") or "")
    require(gate.startswith("make ") or gate.startswith("go test "), f"surface {surface}: gate must be runnable")
    for ref in item.get("evidenceRefs") or []:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        require(ref_path, f"surface {surface}: ref path is required")
        require(needles, f"surface {surface}: ref contains list is required for {ref_path}")
        if not ref_path:
            continue
        path = root / ref_path
        if not path.exists():
            missing.append(f"surface {surface}: evidence path is missing: {ref_path}")
            continue
        text = path.read_text(encoding="utf-8") if path.is_file() else ""
        for needle in needles:
            require(needle in text, f"surface {surface}: {ref_path} missing {needle!r}")

execution = manifest.get("aiflowExecution") or {}
require(execution.get("status") == "aiflow-driven", "aiflowExecution.status must be aiflow-driven")
require("GOFLY-GOV-10R3-08" in str(execution.get("driver") or ""), "aiflowExecution.driver must reference GOFLY-GOV-10R3-08")
completion_policy = str(execution.get("completionPolicy") or "")
for needle in ("make security", "govulncheck", "gosec", "commit"):
    require(needle in completion_policy, f"aiflowExecution.completionPolicy missing {needle!r}")

if missing:
    print("security governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("security governance ok")
PY
