#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
catalog_path = root / "testdata" / "catalog.json"
errors = []

try:
    catalog = json.loads(catalog_path.read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError) as exc:
    print(f"testdata layout check failed: read {catalog_path}: {exc}", file=sys.stderr)
    sys.exit(1)

if catalog.get("schema") != "gofly.testdata_catalog.v1":
    errors.append("catalog schema must be gofly.testdata_catalog.v1")

makefile = (root / "Makefile").read_text(encoding="utf-8")
make_targets = set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.MULTILINE))
readme = (root / "testdata" / "README.md").read_text(encoding="utf-8")
kind_roots = {
    "fixture": root / "testdata",
    "benchmark": root / "bench" / "testdata",
    "integration": root / "tests" / "integration",
}
ids = set()
paths = set()
family_roots = {kind: [] for kind in kind_roots}

for item in catalog.get("families") or []:
    if not isinstance(item, dict):
        errors.append(f"catalog family must be an object: {item!r}")
        continue
    family_id = item.get("id")
    path = item.get("path")
    kind = item.get("kind")
    if not isinstance(family_id, str) or not family_id:
        errors.append(f"catalog family has invalid id: {item!r}")
        continue
    if family_id in ids:
        errors.append(f"duplicate family id: {family_id}")
    ids.add(family_id)
    if kind not in kind_roots:
        errors.append(f"{family_id}: invalid kind {kind!r}")
        continue
    if not isinstance(item.get("owner"), str) or not item["owner"].strip():
        errors.append(f"{family_id}: owner is required")
    if not isinstance(path, str) or not path:
        errors.append(f"{family_id}: path is required")
        continue
    pure_path = pathlib.PurePosixPath(path)
    expected_prefix = pathlib.PurePosixPath(kind_roots[kind].relative_to(root).as_posix())
    if pure_path.is_absolute() or ".." in pure_path.parts or pure_path.parts[: len(expected_prefix.parts)] != expected_prefix.parts:
        errors.append(f"{family_id}: unsafe {kind} path: {path}")
        continue
    if path in paths:
        errors.append(f"duplicate family path: {path}")
    paths.add(path)
    resolved = (root / path).resolve()
    try:
        resolved.relative_to(kind_roots[kind])
    except ValueError:
        errors.append(f"{family_id}: resolved path escapes {kind_roots[kind].relative_to(root)}: {path}")
        continue
    if not resolved.is_dir():
        errors.append(f"{family_id}: family directory is missing: {path}")
        continue
    family_roots[kind].append(resolved)
    for child in resolved.rglob("*"):
        if child.is_symlink():
            errors.append(f"{family_id}: fixture trees must not contain symlinks: {child.relative_to(root)}")
    if kind == "fixture" and any(resolved.rglob("go.mod")):
        errors.append(f"{family_id}: data-only fixture family must not contain go.mod")
    if kind == "benchmark" and not (resolved / "manifest.json").is_file():
        errors.append(f"{family_id}: benchmark family must contain manifest.json")
    if kind == "integration":
        if not (resolved / "go.mod").is_file():
            errors.append(f"{family_id}: integration family must contain go.mod")
        tests = sorted(resolved.rglob("*_test.go"))
        if not tests:
            errors.append(f"{family_id}: integration family must contain tests")
        for test in tests:
            first_line = test.read_text(encoding="utf-8").splitlines()[:1]
            if first_line != ["//go:build integration"]:
                errors.append(f"{family_id}: integration test must use the integration build tag: {test.relative_to(root)}")
    gate = item.get("gate")
    if not isinstance(gate, str) or not gate.startswith("make "):
        errors.append(f"{family_id}: gate must be a make command")
    else:
        target = gate.removeprefix("make ").split()[0]
        if target not in make_targets:
            errors.append(f"{family_id}: unknown make target {target!r}")
    consumers = item.get("consumers")
    if not isinstance(consumers, list) or not consumers:
        errors.append(f"{family_id}: consumers are required")
    else:
        for consumer in consumers:
            if not isinstance(consumer, str) or not (root / consumer).exists():
                errors.append(f"{family_id}: consumer is missing: {consumer!r}")
    display_path = path.removeprefix("testdata/") if kind == "fixture" else path
    if f"`{display_path}`" not in readme:
        errors.append(f"testdata/README.md does not document {path}")

expected_top_level = {"api", "compatibility", "migration", "model", "rpc"}
actual_top_level = {path.name for path in (root / "testdata").iterdir() if path.is_dir()}
if actual_top_level != expected_top_level:
    errors.append(
        "testdata top-level domains drifted: "
        f"missing={sorted(expected_top_level - actual_top_level)!r} "
        f"extra={sorted(actual_top_level - expected_top_level)!r}"
    )

owned_files = {
    "fixture": [path for path in (root / "testdata").rglob("*") if path.is_file() and path.name not in {"README.md", "catalog.json"}],
    "benchmark": [path for path in (root / "bench" / "testdata").rglob("*") if path.is_file()],
    "integration": [path for path in (root / "tests" / "integration").rglob("*") if path.is_file() and path.name != "README.md"],
}
for kind, files in owned_files.items():
    for path in files:
        if not any(path.is_relative_to(family_root) for family_root in family_roots[kind]):
            errors.append(f"unowned {kind} file: {path.relative_to(root)}")

if errors:
    print("testdata layout check failed:", file=sys.stderr)
    for error in errors:
        print(f"  {error}", file=sys.stderr)
    sys.exit(1)

print(f"testdata layout ok: {len(paths)} fixture families")
PY
