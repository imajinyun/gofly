#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import subprocess
import sys

root = pathlib.Path(".").resolve()
missing = []

tracked = subprocess.run(
    ["git", "ls-files", "docs/reference"],
    cwd=root,
    check=False,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)
if tracked.returncode != 0:
    print(tracked.stderr, file=sys.stderr)
    sys.exit(tracked.returncode)

reference_files = [line.strip() for line in tracked.stdout.splitlines() if line.strip()]
if not reference_files:
    missing.append("docs/reference must contain tracked governance contracts")

allowed_unreferenced = {
    "docs/reference/README.md",
}

runtime_markers = (
    "current.txt",
    "regression-report",
    "summary.md",
    "coverage.out",
    ".tmp-test",
    ".aiflow",
    ".harness",
    ".trae",
)
for rel in reference_files:
    name = pathlib.PurePosixPath(rel).name
    if any(marker in rel for marker in runtime_markers):
        missing.append(f"{rel}: runtime evidence must not be tracked under docs/reference")
    if not (name.endswith(".json") or name.endswith(".md") or name.endswith(".yaml") or name.endswith(".yml")):
        missing.append(f"{rel}: unexpected reference contract extension")
    path = root / rel
    if not path.is_file():
        missing.append(f"{rel}: tracked reference path is not a file")
        continue
    if name.endswith(".json"):
        text = path.read_text(encoding="utf-8")
        if '"schema"' not in text:
            missing.append(f"{rel}: JSON reference contracts must declare schema")

search_roots = [
    root / "Makefile",
    root / "bin" / "scripts",
    root / "docs",
    root / "cmd" / "gofly" / "internal",
]

haystack_parts = []
for search_root in search_roots:
    if search_root.is_file():
        haystack_parts.append(search_root.read_text(encoding="utf-8", errors="replace"))
        continue
    if not search_root.is_dir():
        continue
    for path in search_root.rglob("*"):
        if not path.is_file():
            continue
        if path.parts[-1] in {".git"}:
            continue
        if path.suffix not in {".go", ".sh", ".py", ".json", ".md", ".yaml", ".yml", ""}:
            continue
        haystack_parts.append(path.read_text(encoding="utf-8", errors="replace"))
haystack = "\n".join(haystack_parts)

for rel in reference_files:
    if rel in allowed_unreferenced:
        continue
    basename = pathlib.PurePosixPath(rel).name
    if rel not in haystack and basename not in haystack:
        missing.append(f"{rel}: reference contract is not linked from scripts, docs, or command contracts")

gitignore = (root / ".gitignore").read_text(encoding="utf-8")
for needle in ("docs/*", "!docs/reference/", "!docs/reference/*"):
    if needle not in gitignore:
        missing.append(f".gitignore must keep docs/reference tracked via {needle!r}")

if missing:
    print("reference contract check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("reference contracts ok")
PY
