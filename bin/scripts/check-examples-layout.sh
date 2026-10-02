#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
examples_root = root / "examples"
catalog_path = examples_root / "catalog.json"
errors = []

try:
    catalog = json.loads(catalog_path.read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError) as exc:
    print(f"examples layout check failed: read {catalog_path}: {exc}", file=sys.stderr)
    sys.exit(1)

if catalog.get("schema") != "gofly.examples_catalog.v1":
    errors.append("catalog schema must be gofly.examples_catalog.v1")

allowed_kinds = {"starter", "recipe", "reference", "evidence"}
names = set()
paths = set()
for item in catalog.get("examples") or []:
    if not isinstance(item, dict):
        errors.append(f"catalog item must be an object: {item!r}")
        continue
    name = item.get("name")
    path = item.get("path")
    kind = item.get("kind")
    if not isinstance(name, str) or not name:
        errors.append(f"catalog item has invalid name: {item!r}")
        continue
    if name in names:
        errors.append(f"duplicate catalog name: {name}")
    names.add(name)
    if not isinstance(path, str) or not path:
        errors.append(f"{name}: path is required")
        continue
    pure_path = pathlib.PurePosixPath(path)
    if pure_path.is_absolute() or ".." in pure_path.parts or pure_path.parts[:1] != ("examples",):
        errors.append(f"{name}: unsafe catalog path: {path}")
        continue
    if path in paths:
        errors.append(f"duplicate catalog path: {path}")
    paths.add(path)
    resolved = (root / path).resolve()
    try:
        resolved.relative_to(examples_root)
    except ValueError:
        errors.append(f"{name}: resolved path escapes examples: {path}")
        continue
    if not resolved.is_dir() or not (resolved / "go.mod").is_file():
        errors.append(f"{name}: catalog path is not a standalone module: {path}")
    if kind not in allowed_kinds:
        errors.append(f"{name}: invalid kind {kind!r}")
    if not isinstance(item.get("cli"), bool):
        errors.append(f"{name}: cli must be boolean")
    if not isinstance(item.get("description"), str) or not item["description"].strip():
        errors.append(f"{name}: description is required")

discovered = {
    path.parent.relative_to(root).as_posix()
    for path in examples_root.rglob("go.mod")
}
if paths != discovered:
    errors.append(
        "catalog module set drifted: "
        f"missing={sorted(discovered - paths)!r} extra={sorted(paths - discovered)!r}"
    )

readme = (examples_root / "README.md").read_text(encoding="utf-8")
for path in sorted(paths):
    relative = path.removeprefix("examples/")
    if f"({relative})" not in readme:
        errors.append(f"examples/README.md does not link {path}")

test_pattern = re.compile(r"^func (Test[A-Za-z0-9_]+)\(", re.MULTILINE)
for module in sorted(discovered):
    test_names = []
    for test_file in (root / module).rglob("*_test.go"):
        test_names.extend(test_pattern.findall(test_file.read_text(encoding="utf-8")))
    if test_names == ["TestMainDemo"]:
        errors.append(f"{module}: TestMainDemo is the only test and has no behavior assertion")

for relative in (
    "bin/scripts/examples-lib.sh",
    "bin/scripts/examples-smoke.sh",
    "bin/scripts/check-examples-copyable.sh",
    "Makefile",
):
    for line_number, line in enumerate((root / relative).read_text(encoding="utf-8").splitlines(), 1):
        if "build -o" in line and "./..." in line and '"$output/"' not in line:
            errors.append(f"{relative}:{line_number}: single-file output cannot build a multi-package module")

governance = (root / "bin/scripts/governance-10-rounds.sh").read_text(encoding="utf-8")
if "./examples/..." in governance:
    errors.append("governance-10-rounds.sh uses a root-module examples glob")

if errors:
    print("examples layout check failed:", file=sys.stderr)
    for error in errors:
        print(f"  {error}", file=sys.stderr)
    sys.exit(1)

print(f"examples layout ok: {len(paths)} standalone modules")
PY
