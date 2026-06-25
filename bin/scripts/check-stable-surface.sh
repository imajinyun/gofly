#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/stable-surface.md"): [
        "gofly.stable_surface.v1",
        "v1 candidate",
        "rest",
        "core/governance",
        "core/controlplane",
        "CLI JSON",
        "generated production service",
        "Tier 2 to Tier 1",
        "rpc",
        "gateway",
        "app",
        "make stable-surface-check",
        "deprecation",
        "release note",
    ],
    pathlib.Path("docs/reference/api-surface.md"): [
        "v1 candidate",
        "stable-surface.md",
        "Tier 0",
        "Tier 1",
        "Tier 2",
    ],
    pathlib.Path("docs/reference/compatibility.md"): [
        "v1 candidate",
        "Tier 2 to Tier 1",
        "compatibility tests",
    ],
    pathlib.Path("docs/releases/stable.md"): [
        "v1 candidate",
        "stable-surface.md",
        "deprecation",
        "coexistence window",
    ],
    pathlib.Path("Makefile"): [
        "stable-surface-check",
        "check-stable-surface.sh",
    ],
}

missing = []
for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("stable surface check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("stable surface governance ok")
PY
