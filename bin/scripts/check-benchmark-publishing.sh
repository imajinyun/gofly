#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

manifest_path = pathlib.Path("bench/publishing.json")
missing = []

if not manifest_path.is_file():
    missing.append("bench/publishing.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.benchmark_publishing.v1":
    missing.append("bench/publishing.json schema must be gofly.benchmark_publishing.v1")

artifacts = manifest.get("artifacts") or {}
required_artifacts = {
    "currentRaw": ("make bench-stat", "bench/current.txt"),
    "trendSummary": ("make bench-trend", "bench/summary.md"),
    "regressionReport": ("make bench-regression-check", "bench/regression-report.json"),
    "publicBaseline": ("make bench-baseline", "bench/evidence.md"),
}
for name, (command, path) in required_artifacts.items():
    item = artifacts.get(name) or {}
    if item.get("command") != command:
        missing.append(f"{name} command = {item.get('command')!r}, want {command!r}")
    if item.get("path") != path:
        missing.append(f"{name} path = {item.get('path')!r}, want {path!r}")
if (artifacts.get("regressionReport") or {}).get("schema") != "gofly.benchmark_regression_report.v1":
    missing.append("regressionReport schema must reference gofly.benchmark_regression_report.v1")

release_rule = manifest.get("releaseRule") or {}
for term in [
    "raw output from make bench-stat",
    "bench/summary.md from make bench-trend",
    "bench/regression-report.json from make bench-regression-check",
    "bench/evidence.md when publishing a new public baseline",
]:
    if term not in release_rule.get("attach", []):
        missing.append(f"releaseRule.attach missing {term!r}")
for area in ["REST", "RPC", "gateway", "governance", "OpenAPI", "code generation hot paths"]:
    if area not in release_rule.get("changedAreas", []):
        missing.append(f"releaseRule.changedAreas missing {area!r}")
for note in ["include significant benchstat rows", "state allocation regression status", "state latency is report-only unless promoted by policy"]:
    if note not in release_rule.get("notes", []):
        missing.append(f"releaseRule.notes missing {note!r}")

docs = {
    pathlib.Path("docs/reference/benchmark-matrix.md"): [
        "gofly.benchmark_publishing.v1",
        "bench/publishing.json",
        "make bench-regression-check",
        "bench/regression-report.json",
        "latency is report-only",
    ],
    pathlib.Path("docs/releases/stable.md"): [
        "bench/publishing.json",
        "make bench-regression-check",
        "bench/regression-report.json",
        "gofly.benchmark_publishing.v1",
    ],
    pathlib.Path("Makefile"): [
        "bench-publish-check",
        "check-benchmark-publishing.sh",
    ],
}
for path, needles in docs.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("benchmark publishing check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("benchmark publishing governance ok")
PY
