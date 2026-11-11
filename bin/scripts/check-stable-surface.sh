#!/usr/bin/env sh
set -eu

scripts_dir="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

run_check() {
	name="$1"
	shift
	printf '\n== stable surface: %s ==\n' "$name"
	"$@"
}

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/api-surface.md"): [
        "# Reference: stable API surface",
        "## Stable adoption tiers",
        "Tier 0",
        "Tier 1",
        "Tier 2",
        "Tier 3",
        "compatibility.md",
        "api-type-system.md",
    ],
    pathlib.Path("docs/reference/compatibility.md"): [
        "## Adoption tier policy",
        "## Deprecation and migration window",
        "one minor release line",
        "Generated projects on the previous profile still compile",
    ],
    pathlib.Path("docs/releases/stable.md"): [
        "## Release Compatibility Checklist",
        "API compatibility report",
        "coexistence window",
        "compatibility.md",
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

run_check "public Go API compatibility" sh "$scripts_dir/check-public-api.sh"
printf '\nstable surface release-blocking contracts ok\n'
