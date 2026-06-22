#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"

python3 - "$root" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
workflow_dir = root / ".github" / "workflows"
sha_pattern = re.compile(r"^[0-9a-fA-F]{40}$")
uses_pattern = re.compile(r"^\s*uses:\s*([^\s#]+)")

violations = []
workflow_files = sorted(workflow_dir.glob("*.yml")) + sorted(workflow_dir.glob("*.yaml"))
for path in workflow_files:
    rel = path.relative_to(root).as_posix()
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        match = uses_pattern.match(line)
        if not match:
            continue

        target = match.group(1).strip('"\'')
        if target.startswith(("./", "../")):
            continue

        if target.startswith("docker://"):
            if "@sha256:" not in target:
                violations.append((rel, number, target, "Docker actions must be digest pinned with @sha256"))
            continue

        if "@" not in target:
            violations.append((rel, number, target, "action reference is missing @<sha>"))
            continue

        ref = target.rsplit("@", 1)[1]
        if not sha_pattern.fullmatch(ref):
            violations.append((rel, number, target, "action reference must use a 40-character commit SHA"))

if violations:
    print("GitHub Actions pin check failed:", file=sys.stderr)
    for rel, number, target, reason in violations:
        print(f"- {rel}:{number}: {target}: {reason}", file=sys.stderr)
    raise SystemExit(1)

print(f"GitHub Actions pin check passed: {len(workflow_files)} workflow file(s)")
PY
