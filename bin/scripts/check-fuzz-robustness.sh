#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "fuzz-robustness.json"
missing = []


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


def make_target_body(makefile, target):
    match = re.search(
        rf"^\.PHONY:\s*{re.escape(target)}\n"
        rf"{re.escape(target)}:.*?\n(?P<body>.*?)(?=^\.PHONY:|\Z)",
        makefile,
        re.S | re.M,
    )
    require(match is not None, f"Makefile target {target!r} is missing")
    return match.group("body") if match else ""


def make_target_deps(makefile, target):
    match = re.search(rf"^{re.escape(target)}:(?P<deps>[^#\n]*)", makefile, re.M)
    require(match is not None, f"Makefile target {target!r} is missing")
    return match.group("deps") if match else ""


if not manifest_path.is_file():
    missing.append("docs/reference/fuzz-robustness.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.fuzz_robustness.v1":
    missing.append("fuzz robustness manifest schema must be gofly.fuzz_robustness.v1")

targets = manifest.get("targets") or []
if len(targets) != 4:
    missing.append(f"fuzz robustness manifest must list 4 targets, got {len(targets)}")

makefile = read_text(root / "Makefile")
workflow = read_text(root / ".github" / "workflows" / "ci.yml")
docs = {
    root / "docs" / "reference" / "fuzz-robustness.md": [
        "gofly.fuzz_robustness.v1",
        "make fuzz-robustness-check",
        "make fuzz-smoke",
        "parser",
        "REST binding",
    ],
    root / "docs" / "operations" / "production-checklist.md": [
        "make fuzz-robustness-check",
        "make fuzz-smoke",
        "bench + fuzz smoke",
    ],
    root / "docs" / "releases" / "stable.md": [
        "make fuzz-robustness-check",
        "make fuzz-smoke",
        "gofly.fuzz_robustness.v1",
    ],
}

fuzz_check_body = make_target_body(makefile, "fuzz-robustness-check")
fuzz_smoke_body = make_target_body(makefile, "fuzz-smoke")
docs_check_deps = make_target_deps(makefile, "docs-check")

require("check-fuzz-robustness.sh" in fuzz_check_body, "fuzz-robustness-check must call check-fuzz-robustness.sh")
require("fuzz-robustness-check" in docs_check_deps, "docs-check must depend on fuzz-robustness-check")

expected_surface = {
    "FuzzParseAPI": "parser",
    "FuzzParseProto": "parser",
    "FuzzBindJSON": "REST binding",
    "FuzzBindQuery": "REST binding",
}
expected_names = set(expected_surface)
actual_names = {item.get("name") for item in targets if isinstance(item, dict)}
if actual_names != expected_names:
    missing.append(f"fuzz target set = {sorted(actual_names)!r}, want {sorted(expected_names)!r}")

for item in targets:
    if not isinstance(item, dict):
        missing.append(f"fuzz target entry must be an object: {item!r}")
        continue
    name = item.get("name", "")
    package = item.get("package", "")
    file_name = item.get("file", "")
    surface = item.get("surface", "")
    command = item.get("smokeCommand", "")
    if not name or not package or not file_name or not command:
        missing.append(f"target {name or '<missing>'} must include name, package, file, and smokeCommand")
        continue
    if surface != expected_surface.get(name):
        missing.append(f"{name}: surface = {surface!r}, want {expected_surface.get(name)!r}")
    source = read_text(root / file_name)
    if not re.search(rf"func\s+{re.escape(name)}\s*\(\s*f\s+\*testing\.F\s*\)", source):
        missing.append(f"{file_name}: missing fuzz function {name}")
    expected_command = f"go test -run=Fuzz -fuzz={name} -fuzztime=20s {package}"
    if command != expected_command:
        missing.append(f"{name}: smokeCommand = {command!r}, want {expected_command!r}")
    make_command = command.replace("go test", "$(GO) test")
    if make_command not in fuzz_smoke_body:
        missing.append(f"Makefile fuzz-smoke missing {make_command!r}")
    if command not in workflow:
        missing.append(f"ci.yml bench-fuzz job missing {command!r}")

release_rule = manifest.get("releaseRule") or {}
if release_rule.get("blockingCheck") != "make fuzz-robustness-check":
    missing.append("releaseRule.blockingCheck must be make fuzz-robustness-check")
if release_rule.get("smokeCheck") != "make fuzz-smoke":
    missing.append("releaseRule.smokeCheck must be make fuzz-smoke")
if "parser" not in release_rule.get("surfaces", []):
    missing.append("releaseRule.surfaces must include parser")
if "REST binding" not in release_rule.get("surfaces", []):
    missing.append("releaseRule.surfaces must include REST binding")

for path, needles in docs.items():
    text = read_text(path)
    for needle in needles:
        if needle not in text:
            missing.append(f"{path.relative_to(root)}: missing {needle!r}")

if missing:
    print("fuzz robustness check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("fuzz robustness governance ok")
PY
