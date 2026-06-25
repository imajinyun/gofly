#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "coverage-trend.json"
missing = []


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


def make_var(makefile, name):
    match = re.search(rf"^{re.escape(name)}\s*\?=\s*(.+)$", makefile, re.M)
    return match.group(1).strip() if match else ""


if not manifest_path.is_file():
    missing.append("docs/reference/coverage-trend.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

makefile = read_text(root / "Makefile")
coverage_script = read_text(root / "bin" / "scripts" / "coverage-check.sh")
target_names = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(manifest.get("schema") == "gofly.coverage_trend.v1", "coverage trend schema must be gofly.coverage_trend.v1")
require("coverage-trend-check" in target_names, "Makefile must expose coverage-trend-check")
require("coverage-trend-check" in docs_check_line, "docs-check must depend on coverage-trend-check")
require("check-coverage-trend.sh" in makefile, "Makefile must call check-coverage-trend.sh")
require("cover-check" in target_names, "Makefile must expose cover-check")
require("coverage-check.sh" in makefile, "Makefile must call coverage-check.sh")

expected = manifest.get("ratchetPolicy") or {}
threshold = str(expected.get("thresholdPercent", ""))
ratchet = str(expected.get("ratchetPercent", ""))
require(threshold == make_var(makefile, "COVERAGE_THRESHOLD"), "manifest thresholdPercent must match Makefile COVERAGE_THRESHOLD")
require(ratchet == make_var(makefile, "COVERAGE_RATCHET"), "manifest ratchetPercent must match Makefile COVERAGE_RATCHET")
require(expected.get("blockingGate") == "make cover-check", "ratchetPolicy.blockingGate must be make cover-check")
require(expected.get("trendGate") == "make coverage-trend-check", "ratchetPolicy.trendGate must be make coverage-trend-check")
require(expected.get("volatileArtifacts") == ["coverage.out"], "ratchetPolicy.volatileArtifacts must list coverage.out")

for needle in [
    "COVERAGE_THRESHOLD",
    "COVERAGE_RATCHET",
    "GOFLAGS",
    "-count=1",
    "GOCACHE",
    "GOTMPDIR",
    "cover -func",
    "malformed coverage profile line",
]:
    require(needle in coverage_script, f"coverage-check.sh must contain {needle!r}")

evidence = manifest.get("evidence") or []
required_ids = {
    "coverage-threshold",
    "coverage-ratchet",
    "cache-isolation",
    "profile-sanitization",
    "volatile-artifact-boundary",
}
actual_ids = {item.get("id") for item in evidence if isinstance(item, dict)}
require(actual_ids == required_ids, f"evidence ids = {sorted(actual_ids)!r}, want {sorted(required_ids)!r}")
for item in evidence:
    if not isinstance(item, dict):
        missing.append(f"evidence entry must be an object: {item!r}")
        continue
    for field in ("id", "command", "artifact", "owner"):
        require(bool(item.get(field)), f"{item.get('id', '<missing>')}: {field} is required")

docs = {
    root / "docs" / "reference" / "coverage-trend.md": [
        "gofly.coverage_trend.v1",
        "coverage-trend.json",
        "make coverage-trend-check",
        "make cover-check",
        "COVERAGE_RATCHET",
        "coverage.out",
        "docs-check",
    ],
    root / "docs" / "index.md": [
        "reference/coverage-trend.md",
    ],
    root / "README.md": [
        "docs/reference/coverage-trend.md",
    ],
}
for path, needles in docs.items():
    text = read_text(path)
    for needle in needles:
        require(needle in text, f"{path.relative_to(root)}: missing {needle!r}")

governance_report = read_text(root / "bin" / "scripts" / "governance-report.sh")
for needle in [
    "coverage-trend.json",
    "gofly.coverage_trend.v1",
    "coverageTrend",
    "make coverage-trend-check",
]:
    require(needle in governance_report, f"governance-report.sh must expose {needle!r}")

if missing:
    print("coverage trend check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("coverage trend governance ok")
PY
