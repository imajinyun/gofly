#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/gofly-api-client-compatibility-XXXXXX")"
report="${API_CLIENT_COMPATIBILITY_REPORT:-$root/.tmp-test/api-client-compatibility-report.json}"
required="${API_CLIENT_COMPATIBILITY_REQUIRED:-${API_CLIENT_TOOLCHAIN_REQUIRED:-false}}"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$(dirname -- "$report")" "$tmp/bin"

(
	cd "$root"
	"${GO:-go}" build -trimpath -o "$tmp/bin/gofly" ./cmd/gofly
)

python3 - "$root" "$tmp" "$report" "$required" <<'PY'
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(sys.argv[1]).resolve()
work = pathlib.Path(sys.argv[2]).resolve()
report_path = pathlib.Path(sys.argv[3]).resolve()
required_raw = sys.argv[4].strip().lower()
required = required_raw in {"1", "true", "yes", "on"}
fixture_root = root / "testdata" / "api" / "client" / "compatibility"
manifest_path = fixture_root / "manifest.json"
contract_path = root / "docs" / "reference" / "api-client-compatibility.json"
toolchain_runner = root / "bin" / "scripts" / "check-api-client-toolchains.py"
gofly = work / "bin" / "gofly"
languages = ("javascript", "typescript", "java", "kotlin", "dart")
expected_cases = {
    "optional-field-addition": ("compatible", "represented"),
    "required-field-addition": ("breaking", "represented"),
    "field-rename-removal": ("breaking", "represented"),
    "enum-value-addition": ("compatible", "not-represented"),
    "enum-value-removal": ("breaking", "not-represented"),
    "status-code-change": ("breaking", "not-represented"),
    "path-change": ("breaking", "represented"),
    "auth-requirement-change": ("breaking", "not-represented"),
}
errors = []


def write_report(payload):
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def require(condition, message):
    if not condition:
        errors.append(message)


def load_json(path, label):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"cannot read {label}: {exc}")
        return {}


def relative_fixture(value, case_id, key):
    if not isinstance(value, str) or not value:
        errors.append(f"{case_id}: {key} must be a non-empty relative path")
        return None
    candidate = (fixture_root / value).resolve()
    try:
        candidate.relative_to(fixture_root.resolve())
    except ValueError:
        errors.append(f"{case_id}: {key} escapes testdata/api/client/compatibility")
        return None
    if not candidate.is_file():
        errors.append(f"{case_id}: {key} fixture is missing: {value}")
        return None
    return candidate


def output_file(directory):
    files = [path for path in directory.rglob("*") if path.is_file()]
    if len(files) != 1:
        raise RuntimeError(f"generated file count is {len(files)}, want 1")
    file = files[0].resolve()
    try:
        file.relative_to(directory.resolve())
    except ValueError as exc:
        raise RuntimeError(f"generated output escapes its language directory: {file}") from exc
    return file


def generate(api_file, language, destination):
    destination.mkdir(parents=True, exist_ok=True)
    command = [
        str(gofly), "api", "client", "--file", str(api_file), "--dir", str(destination),
        "--language", language, "--base-url", "https://api.compatibility.example",
    ]
    completed = subprocess.run(command, cwd=root, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
    if completed.returncode != 0:
        raise RuntimeError(f"generation failed ({language}): {completed.stdout[-4096:]}")
    file = output_file(destination)
    data = file.read_bytes()
    return {
        "file": str(file.relative_to(work)),
        "bytes": len(data),
        "sha256": hashlib.sha256(data).hexdigest(),
        "text": data.decode("utf-8"),
    }


def model_surface(language, text):
    surface = set()
    if language == "javascript":
        for name in re.findall(r"^\s*async\s+(\w+)\(", text, re.M):
            surface.add("method:" + name)
        return surface
    if language == "typescript":
        for name, body in re.findall(r"export interface (\w+) \{(.*?)^\}", text, re.M | re.S):
            for field in re.findall(r"^\s*(\w+)\??:\s*", body, re.M):
                surface.add(f"field:{name}.{field}")
        for name in re.findall(r"^\s*async\s+(\w+)\(", text, re.M):
            surface.add("method:" + name)
        return surface
    if language == "java":
        for name, body in re.findall(r"public static class (\w+) \{(.*?)^  \}", text, re.M | re.S):
            for field in re.findall(r"^    public [^;]+\s+(\w+);", body, re.M):
                surface.add(f"field:{name}.{field}")
        for name in re.findall(r"^  public [^ (]+(?:<[^>]+>)?\s+(\w+)\(", text, re.M):
            if name != "APIClient":
                surface.add("method:" + name)
        return surface
    if language == "kotlin":
        for name, body in re.findall(r"data class (\w+)\((.*?)^\)", text, re.M | re.S):
            for field in re.findall(r"\bval\s+(\w+):", body):
                surface.add(f"field:{name}.{field}")
        for name in re.findall(r"^  fun\s+(\w+)\(", text, re.M):
            surface.add("method:" + name)
        return surface
    if language == "dart":
        for name, body in re.findall(r"class (?!APIClient\b)(\w+) \{(.*?)^\}", text, re.M | re.S):
            for field in re.findall(r"^  final [^;]+\s+(\w+);", body, re.M):
                surface.add(f"field:{name}.{field}")
        for name in re.findall(r"^  Future<[^>]+>\s+(\w+)\(", text, re.M):
            surface.add("method:" + name)
        return surface
    raise RuntimeError(f"unsupported language {language}")


def acknowledged(case):
    acknowledgement = case.get("acknowledgement")
    return isinstance(acknowledgement, dict) and bool(acknowledgement.get("id")) and bool(acknowledgement.get("reason"))


def compatible_toolchains(case, api_file, revision):
    tool_report = work / "toolchains" / case["id"] / (revision + ".json")
    command = [
        sys.executable, str(toolchain_runner), "--root", str(root), "--work", str(work / "toolchains" / case["id"] / revision),
        "--report", str(tool_report), "--api-file", str(api_file), "--gofly-bin", str(gofly),
        "--language", "all", "--required", "true" if required else "false",
        "--base-url", "https://api.compatibility.example", "--verification-profile", "syntax",
    ]
    completed = subprocess.run(command, cwd=root, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
    report = load_json(tool_report, f"{case['id']} {revision} toolchain report") if tool_report.is_file() else {}
    status = report.get("status")
    if completed.returncode != 0 or status not in {"pass", "partial"}:
        errors.append(f"{case['id']}: compatible {revision} toolchain validation failed: {completed.stdout[-4096:]}")
    for entry in report.get("results") or []:
        if entry.get("status") == "fail":
            errors.append(f"{case['id']}: compatible {revision} {entry.get('language')} toolchain failed")
    return {
        "status": status or "missing",
        "results": [{"language": row.get("language"), "status": row.get("status")} for row in report.get("results") or []],
    }


manifest = load_json(manifest_path, "fixture manifest")
contract = load_json(contract_path, "compatibility contract")
require(manifest.get("schema") == "gofly.api_client_compatibility_fixtures.v1", "fixture manifest schema mismatch")
require(contract.get("schema") == "gofly.api_client_compatibility.v1", "compatibility contract schema mismatch")
require(contract.get("status") == "blocking-contract", "compatibility contract must be blocking")
require(contract.get("acceptanceGate") == "make api-client-compatibility-check", "compatibility contract acceptanceGate mismatch")
require(tuple(manifest.get("languages") or []) == languages, "fixture manifest languages drifted")
require(tuple(contract.get("languages") or []) == languages, "compatibility contract languages drifted")
require(toolchain_runner.is_file(), "api client toolchain runner is missing")
require(gofly.is_file(), "temporary gofly binary is missing")

# This invariant is deliberately evaluated in-process so a future refactor cannot
# accidentally turn an absent acknowledgement into a passing breaking row.
require(not acknowledged({"apiCompatibility": "breaking"}), "unacknowledged breaking self-check unexpectedly passed")

results = []
cases = manifest.get("cases") or []
require(isinstance(cases, list) and len(cases) == 8, "fixture manifest must define eight versioning cases")
seen = set()
for case in cases if isinstance(cases, list) else []:
    if not isinstance(case, dict):
        errors.append(f"fixture case must be an object: {case!r}")
        continue
    case_id = case.get("id")
    if not isinstance(case_id, str) or not case_id or case_id in seen:
        errors.append(f"invalid or duplicate fixture case id: {case_id!r}")
        continue
    seen.add(case_id)
    base = relative_fixture(case.get("base"), case_id, "base")
    target = relative_fixture(case.get("target"), case_id, "target")
    api_compatibility = case.get("apiCompatibility")
    require(api_compatibility in {"compatible", "breaking"}, f"{case_id}: apiCompatibility is invalid")
    expected_source = case.get("sourceCompatibility")
    require(isinstance(expected_source, dict) and set(expected_source) == set(languages), f"{case_id}: sourceCompatibility must cover every language")
    require(case.get("semanticCoverage") in {"represented", "not-represented"}, f"{case_id}: semanticCoverage is invalid")
    expected_api, expected_coverage = expected_cases.get(case_id, (None, None))
    require(expected_api is not None, f"{case_id}: unsupported compatibility case")
    require(api_compatibility == expected_api, f"{case_id}: apiCompatibility = {api_compatibility}, want {expected_api}")
    require(case.get("semanticCoverage") == expected_coverage, f"{case_id}: semanticCoverage = {case.get('semanticCoverage')}, want {expected_coverage}")
    if api_compatibility == "breaking" and not acknowledged(case):
        errors.append(f"{case_id}: breaking API change has no explicit acknowledgement")
    row = {
        "id": case_id,
        "change": case.get("change"),
        "apiCompatibility": api_compatibility,
        "semanticCoverage": case.get("semanticCoverage"),
        "acknowledgement": case.get("acknowledgement"),
        "languages": [],
    }
    if base is None or target is None:
        results.append(row)
        continue
    base_text = base.read_text(encoding="utf-8")
    target_text = target.read_text(encoding="utf-8")
    for assertion in case.get("assertions") or []:
        require(assertion in base_text or assertion in target_text, f"{case_id}: assertion is absent from both fixtures: {assertion!r}")
    for language in languages:
        try:
            first_base = generate(base, language, work / "generated" / case_id / language / "base-first")
            second_base = generate(base, language, work / "generated" / case_id / language / "base-second")
            first_target = generate(target, language, work / "generated" / case_id / language / "target-first")
            second_target = generate(target, language, work / "generated" / case_id / language / "target-second")
            if first_base["sha256"] != second_base["sha256"]:
                errors.append(f"{case_id}/{language}: base generation is not byte deterministic")
            if first_target["sha256"] != second_target["sha256"]:
                errors.append(f"{case_id}/{language}: target generation is not byte deterministic")
            base_surface = model_surface(language, first_base["text"])
            target_surface = model_surface(language, first_target["text"])
            computed = "compatible" if base_surface <= target_surface else "breaking"
            expected = expected_source.get(language) if isinstance(expected_source, dict) else None
            if computed != expected:
                errors.append(f"{case_id}/{language}: source compatibility = {computed}, want {expected}; missing={sorted(base_surface - target_surface)}")
            row["languages"].append({
                "language": language,
                "sourceCompatibility": computed,
                "base": {key: first_base[key] for key in ("file", "bytes", "sha256")},
                "target": {key: first_target[key] for key in ("file", "bytes", "sha256")},
                "publicSurface": {"base": sorted(base_surface), "target": sorted(target_surface)},
            })
        except (OSError, RuntimeError, UnicodeDecodeError) as exc:
            errors.append(f"{case_id}/{language}: {exc}")
    if api_compatibility == "compatible":
        row["toolchainValidation"] = {
            "base": compatible_toolchains(case, base, "base"),
            "target": compatible_toolchains(case, target, "target"),
        }
    results.append(row)

require(seen == set(expected_cases), f"fixture case ids drifted: got {sorted(seen)}, want {sorted(expected_cases)}")

status = "fail" if errors else "pass"
payload = {
    "schema": "gofly.api_client_compatibility_report.v1",
    "contract": "docs/reference/api-client-compatibility.json",
    "fixtureManifest": "testdata/api/client/compatibility/manifest.json",
    "requiredToolchains": required,
    "status": status,
    "results": results,
    "errors": errors,
}
write_report(payload)
if errors:
    print("api client compatibility check failed:", file=sys.stderr)
    for error in errors:
        print("- " + error, file=sys.stderr)
    raise SystemExit(1)
print("api client compatibility OK")
PY

printf 'api client compatibility report: %s\n' "$report"
