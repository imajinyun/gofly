#!/usr/bin/env sh
set -eu

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

export GOCACHE="$tmpdir/gocache"
export GOTMPDIR="$tmpdir/gotmp"
mkdir -p "$GOCACHE" "$GOTMPDIR"

doctor_json="$tmpdir/doctor.json"
bug_json="$tmpdir/bug.json"
release_json="$tmpdir/release.json"

go run ./cmd/gofly doctor --json > "$doctor_json"
go run ./cmd/gofly bug --json > "$bug_json"
API_BASE_REF=definitely-missing-release-base-ref go run ./cmd/gofly release check --json --strict > "$release_json"

python3 - "$doctor_json" "$bug_json" "$release_json" <<'PY'
import json
import pathlib
import sys

doctor = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
bug = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
release = json.loads(pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"))

missing = []

if not doctor.get("nextActions"):
    missing.append("doctor --json: missing non-empty nextActions")
for check in doctor.get("checks", []):
    if check.get("status") in {"warn", "fail"} and not (check.get("nextActions") or check.get("fix_hint")):
        missing.append(f"doctor --json: {check.get('name')} lacks nextActions/fix_hint")

support = bug.get("supportBundle") or {}
if support.get("schema") != "gofly.support_bundle.v1":
    missing.append("bug --json: supportBundle.schema is not gofly.support_bundle.v1")
if not support.get("commands"):
    missing.append("bug --json: supportBundle.commands is empty")
if not support.get("redaction"):
    missing.append("bug --json: supportBundle.redaction is empty")
if not bug.get("nextActions"):
    missing.append("bug --json: missing nextActions")

if release.get("command") != "release.check":
    missing.append("release check --json: command is not release.check")
data = release.get("data") or {}
if "summary" not in data or "checks" not in data:
    missing.append("release check --json: missing data.summary/checks")
error = release.get("error")
if error is not None and not error.get("remediation"):
    missing.append("release check --json: error lacks remediation")

docs = {
    pathlib.Path("docs/reference/cli-json-contracts.md"): [
        "nextActions",
        "supportBundle",
        "gofly.support_bundle.v1",
    ],
    pathlib.Path("docs/operations/troubleshooting.md"): [
        "gofly doctor --json",
        "gofly bug --json",
        "gofly release check --json --strict",
        "support bundle",
    ],
}
for path, needles in docs.items():
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("dx troubleshooting check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("dx troubleshooting governance ok")
PY
