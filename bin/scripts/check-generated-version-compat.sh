#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".")
matrix_path = root / "testdata/generated-compat/matrix.json"
required_files = [
    root / "testdata/generated-compat/v0.1/orders.api",
    root / "testdata/generated-compat/v0.1/greeter.proto",
    root / "testdata/generated-compat/v0.1/service-config.json",
    root / "testdata/generated-compat/current/orders.api",
    root / "testdata/generated-compat/current/greeter.proto",
    root / "testdata/generated-compat/future/orders.api",
    root / "testdata/generated-compat/future/greeter.proto",
    root / "docs/reference/generated-version-compat.md",
]

missing = []
for path in required_files:
    if not path.is_file():
        missing.append(f"{path}: file is missing")

if matrix_path.is_file():
    data = json.loads(matrix_path.read_text(encoding="utf-8"))
    if data.get("schema") != "gofly.generated_version_compat.v1":
        missing.append("testdata/generated-compat/matrix.json: unexpected schema")
    profiles = {item.get("profile") for item in data.get("profiles", [])}
    for profile in ("old", "current", "future"):
        if profile not in profiles:
            missing.append(f"testdata/generated-compat/matrix.json: missing profile {profile!r}")
    for item in data.get("profiles", []):
        for field in ("api", "proto", "serviceConfig", "expectedDiff", "verification"):
            if field not in item:
                missing.append(f"testdata/generated-compat/matrix.json: profile {item.get('profile')} missing {field}")
else:
    missing.append("testdata/generated-compat/matrix.json: file is missing")

doc = root / "docs/reference/generated-version-compat.md"
if doc.is_file():
    text = doc.read_text(encoding="utf-8")
    for needle in (
        "make generated-version-compat-check",
        "old",
        "current",
        "future",
        "generated project snapshots",
        "explainable diffs",
    ):
        if needle not in text:
            missing.append(f"{doc}: missing {needle!r}")

if missing:
    print("generated version compatibility check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("generated version compatibility governance ok")
PY
