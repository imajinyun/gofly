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

manifest_path = pathlib.Path("docs/reference/dx-support-bundle.json")
if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/dx-support-bundle.json: file is missing")

if manifest.get("schema") != "gofly.dx_support_bundle.v1":
    missing.append("dx support bundle schema must be gofly.dx_support_bundle.v1")
if manifest.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("dx support bundle acceptanceGate must be make dx-troubleshooting-check")

surfaces = manifest.get("surfaces") or []
surface_by_command = {
    item.get("command"): item for item in surfaces if isinstance(item, dict)
}
required_surfaces = {
    "gofly doctor --json": {"version", "go", "os", "arch", "checks", "summary", "nextActions"},
    "gofly bug --json": {"tool", "version", "environment", "checks", "supportBundle", "nextActions"},
    "gofly release check --json --strict": {"ok", "command", "data.summary", "data.checks", "error.remediation"},
    "gofly ai new --json --apply --verify": {"data.verification", "data.verifyRan", "data.verifyPassed", "data.nextActions"},
}
for command, fields in required_surfaces.items():
    surface = surface_by_command.get(command)
    if not surface:
        missing.append(f"dx support bundle surfaces missing {command!r}")
        continue
    stable = set(surface.get("stableFields") or [])
    absent = fields - stable
    if absent:
        missing.append(f"dx support bundle {command}: stableFields missing {sorted(absent)!r}")
    if surface.get("nextActionRequired") is True and "nextActions" not in " ".join(stable):
        missing.append(f"dx support bundle {command}: nextActionRequired surface must declare nextActions")
    if surface.get("failureGuidance") in ("", None):
        missing.append(f"dx support bundle {command}: failureGuidance is required")

bug_surface = surface_by_command.get("gofly bug --json") or {}
if bug_surface.get("supportBundleSchema") != "gofly.support_bundle.v1":
    missing.append("dx support bundle bug surface must reference gofly.support_bundle.v1")
redaction = set(bug_surface.get("redactionPolicy") or [])
for term in ("Authorization", "Cookie", "Set-Cookie", "*TOKEN*", "*SECRET*", "*PASSWORD*"):
    if term not in redaction:
        missing.append(f"dx support bundle redactionPolicy missing {term!r}")

failure_report = manifest.get("generatedFailureReport") or {}
if failure_report.get("schema") != "gofly.generated_project_failure_report.v1":
    missing.append("generatedFailureReport.schema must be gofly.generated_project_failure_report.v1")
for field in ("command", "status", "output", "error", "nextActions"):
    if field not in set(failure_report.get("fields") or []):
        missing.append(f"generatedFailureReport.fields missing {field!r}")
if failure_report.get("boundedOutput") is not True:
    missing.append("generatedFailureReport.boundedOutput must be true")
if failure_report.get("outputLimitBytes") != 4096:
    missing.append("generatedFailureReport.outputLimitBytes must be 4096")
if set(failure_report.get("statusValues") or []) != {"passed", "failed", "skipped"}:
    missing.append("generatedFailureReport.statusValues must be passed/failed/skipped")
if failure_report.get("rerunGuidanceField") != "nextActions":
    missing.append("generatedFailureReport.rerunGuidanceField must be nextActions")
if failure_report.get("redactionRequired") is not True:
    missing.append("generatedFailureReport.redactionRequired must be true")
failure_redaction = set(failure_report.get("redactionTerms") or [])
for term in ("Authorization", "Cookie", "Set-Cookie", "GOFLY_LLM_*", "*TOKEN*", "*SECRET*", "*PASSWORD*"):
    if term not in failure_redaction:
        missing.append(f"generatedFailureReport.redactionTerms missing {term!r}")
evidence_producers = failure_report.get("evidenceProducers") or []
producer_by_command = {
    item.get("command"): item for item in evidence_producers if isinstance(item, dict)
}
producer = producer_by_command.get("gofly ai new --json --apply --verify")
if not producer:
    missing.append("generatedFailureReport.evidenceProducers missing gofly ai new --json --apply --verify")
else:
    fields = set(producer.get("fields") or [])
    for field in ("data.verification", "data.nextActions"):
        if field not in fields:
            missing.append(f"generatedFailureReport producer missing {field!r}")
if not failure_report.get("ciArtifactUsage"):
    missing.append("generatedFailureReport.ciArtifactUsage is required")

for step in ("run gofly doctor --json", "run gofly release check --json --strict", "run gofly bug --json"):
    if step not in set(manifest.get("supportWorkflow") or []):
        missing.append(f"dx support bundle supportWorkflow missing {step!r}")
for doc_path in manifest.get("docs") or []:
    if not pathlib.Path(doc_path).is_file():
        missing.append(f"dx support bundle docs path is missing: {doc_path}")

docs = {
    pathlib.Path("docs/reference/cli-json-contracts.md"): [
        "nextActions",
        "supportBundle",
        "gofly.support_bundle.v1",
        "gofly.dx_support_bundle.v1",
        "gofly.generated_project_failure_report.v1",
        "outputLimitBytes",
        "rerunGuidanceField",
    ],
    pathlib.Path("docs/operations/troubleshooting.md"): [
        "gofly doctor --json",
        "gofly bug --json",
        "gofly release check --json --strict",
        "support bundle",
        "generated project verification failure",
        "4096 bytes",
        "nextActions",
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
