#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
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

if missing:
    print("security governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("security governance ok")
PY
