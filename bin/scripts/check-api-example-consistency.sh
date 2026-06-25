#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "api-example-consistency.json"
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


if not manifest_path.is_file():
    missing.append("docs/reference/api-example-consistency.json is missing")
    manifest = {}
else:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

if manifest.get("schema") != "gofly.api_example_consistency.v1":
    missing.append("api example consistency schema must be gofly.api_example_consistency.v1")
health_path = root / "docs" / "reference" / "examples-health-index.json"
if health_path.is_file():
    health = json.loads(health_path.read_text(encoding="utf-8"))
else:
    health = {}
    missing.append("docs/reference/examples-health-index.json is missing")
if health.get("schema") != "gofly.examples_health_index.v1":
    missing.append("examples health index schema must be gofly.examples_health_index.v1")
if health.get("sourceOfTruth") != "examples/README.md":
    missing.append("examples health index sourceOfTruth must be examples/README.md")
if health.get("acceptanceGate") != "make api-example-consistency-check":
    missing.append("examples health index acceptanceGate must be make api-example-consistency-check")
if health.get("copyableGate") != "make examples-copyable-check":
    missing.append("examples health index copyableGate must be make examples-copyable-check")
if health.get("smokeGate") != "make examples-smoke":
    missing.append("examples health index smokeGate must be make examples-smoke")

makefile = read_text(root / "Makefile")
target_names = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")
require("api-example-consistency-check" in target_names, "Makefile must expose api-example-consistency-check")
require("api-example-consistency-check" in docs_check_line, "docs-check must depend on api-example-consistency-check")
require("check-api-example-consistency.sh" in makefile, "Makefile must call check-api-example-consistency.sh")

surfaces = manifest.get("surfaces") or []
required_surfaces = {
    "rest",
    "rpc",
    "rpc-idl-matrix",
    "resilience",
    "plugin-ecosystem",
    "migration-proof",
    "dependency-upgrade-evidence",
}
actual_surfaces = {item.get("surface") for item in surfaces if isinstance(item, dict)}
if actual_surfaces != required_surfaces:
    missing.append(f"surfaces = {sorted(actual_surfaces)!r}, want {sorted(required_surfaces)!r}")

examples_readme = read_text(root / "examples" / "README.md")
readme = read_text(root / "README.md")

health_examples = health.get("examples") or []
health_names = {item.get("name") for item in health_examples if isinstance(item, dict)}
required_health_examples = {
    "restserver",
    "http-middleware",
    "migration-proof",
    "rpc-idl-matrix",
    "plugin-ecosystem",
    "cache-local",
    "microshop",
    "resilience",
}
if health_names != required_health_examples:
    missing.append(f"examples health names = {sorted(health_names)!r}, want {sorted(required_health_examples)!r}")
for item in health_examples:
    if not isinstance(item, dict):
        missing.append(f"examples health entry must be an object: {item!r}")
        continue
    name = item.get("name", "<missing>")
    for field in ("name", "path", "goMod", "runtimeMode", "ports", "smokeCommands", "outputSchema", "riskNotes"):
        if field not in item or item[field] in ("", None, []):
            if field != "ports":
                missing.append(f"examples health {name}: {field} is required")
    path = item.get("path", "")
    go_mod = item.get("goMod", "")
    if path:
        if not (root / path).is_dir():
            missing.append(f"examples health {name}: path is missing: {path}")
        if path not in examples_readme:
            missing.append(f"examples health {name}: examples/README.md must reference {path}")
    if go_mod and not (root / go_mod).is_file():
        missing.append(f"examples health {name}: goMod is missing: {go_mod}")
    smoke_commands = item.get("smokeCommands") or []
    if not any("examples/" in command or "-C examples/" in command for command in smoke_commands):
        missing.append(f"examples health {name}: at least one smoke command must reference examples/")
    if not item.get("outputSchema"):
        missing.append(f"examples health {name}: outputSchema is required")
    if not item.get("riskNotes"):
        missing.append(f"examples health {name}: riskNotes is required")

for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"surface entry must be an object: {item!r}")
        continue
    surface = item.get("surface", "")
    docs = item.get("docs", "")
    example = item.get("example", "")
    package_example = item.get("packageExample", "")
    gate = item.get("gate", "")
    for field, value in {
        "surface": surface,
        "docs": docs,
        "example": example,
        "packageExample": package_example,
        "gate": gate,
    }.items():
        if not value:
            missing.append(f"{surface or '<missing>'}: {field} is required")
    if not surface:
        continue

    docs_text = read_text(root / docs) if docs else ""
    example_path = root / example if example else root
    package_example_path = root / package_example if package_example else root
    if example and not (example_path.is_dir() or example_path.is_file()):
        missing.append(f"{surface}: example path is missing: {example}")
    if package_example and not package_example_path.is_file():
        missing.append(f"{surface}: packageExample path is missing: {package_example}")

    if example.startswith("examples/") and example not in examples_readme:
        missing.append(f"{surface}: examples/README.md must reference {example}")
    if docs and pathlib.Path(docs).name not in readme and surface in {"rest", "rpc"}:
        missing.append(f"{surface}: README.md must link the guide or examples catalog for {docs}")

    gate_target = gate.removeprefix("make ").split()[0]
    if gate_target not in target_names:
        missing.append(f"{surface}: gate target {gate_target!r} is missing from Makefile")
    if gate not in docs_text and gate not in examples_readme and gate not in makefile:
        missing.append(f"{surface}: gate {gate!r} must be documented near docs/examples/Makefile")

    if package_example.endswith("_test.go"):
        test_text = read_text(package_example_path)
        if "func Example" not in test_text and "Test" not in test_text:
            missing.append(f"{surface}: {package_example} must contain Example or Test coverage")

docs = {
    root / "docs" / "reference" / "api-example-consistency.md": [
        "gofly.api_example_consistency.v1",
        "make api-example-consistency-check",
        "api-example-consistency.json",
        "examples-health-index.json",
        "docs-check",
    ],
    root / "docs" / "index.md": [
        "reference/api-example-consistency.md",
    ],
    root / "README.md": [
        "docs/reference/api-example-consistency.md",
    ],
}
for path, needles in docs.items():
    text = read_text(path)
    for needle in needles:
        if needle not in text:
            missing.append(f"{path.relative_to(root)}: missing {needle!r}")

if missing:
    print("api example consistency check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("api example consistency governance ok")
PY
