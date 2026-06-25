#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="${GENERATED_VERSION_COMPAT_TMPDIR:-$(mktemp -d)}"
report="${GENERATED_VERSION_COMPAT_REPORT:-$tmp/generated-version-compat-report.json}"
cleanup_tmp=true
if [ -n "${GENERATED_VERSION_COMPAT_TMPDIR:-}" ]; then
	cleanup_tmp=false
fi
if [ "$cleanup_tmp" = "true" ]; then
	trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM
fi

mkdir -p "$tmp"

python3 - "$root" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
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

matrix="$root/testdata/generated-compat/matrix.json"
profiles="$tmp/profiles.txt"
python3 - "$matrix" "$profiles" <<'PY'
import json, pathlib, sys
matrix = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
lines = []
for item in matrix["profiles"]:
    lines.append("\t".join([
        item["profile"],
        item["api"],
        item["proto"],
        item["serviceConfig"],
        item["expectedDiff"],
        item["verification"],
    ]))
pathlib.Path(sys.argv[2]).write_text("\n".join(lines) + "\n", encoding="utf-8")
PY

results_jsonl="$tmp/results.jsonl"
: > "$results_jsonl"

run_profile() {
	profile="$1"
	api_rel="$2"
	proto_rel="$3"
	config_rel="$4"
	expected_diff="$5"
	verification="$6"
	profile_dir="$tmp/$profile"
	first="$profile_dir/first/orders"
	second="$profile_dir/second/orders"
	diff_file="$profile_dir/repeat.diff"
	stdout_file="$profile_dir/new-service.json"
	test_output="$profile_dir/go-test.txt"
	rm -rf "$profile_dir"
	mkdir -p "$profile_dir"

	api="$root/testdata/generated-compat/$api_rel"
	proto="$root/testdata/generated-compat/$proto_rel"
	config="$root/testdata/generated-compat/$config_rel"
	first_config="$profile_dir/first-service-config.json"
	second_config="$profile_dir/second-service-config.json"
	cp "$config" "$first_config"
	cp "$config" "$second_config"

	"$go_cmd" run ./cmd/gofly new service orders --module example.com/orders --dir "$first" --config "$first_config" --api "$api" --proto "$proto" --json >"$stdout_file"
	(
		cd "$first"
		"$go_cmd" mod edit -replace github.com/imajinyun/gofly="$root"
		"$go_cmd" mod tidy
	)

	"$go_cmd" run ./cmd/gofly new service orders --module example.com/orders --dir "$second" --config "$second_config" --api "$api" --proto "$proto" --json >/dev/null
	(
		cd "$second"
		"$go_cmd" mod edit -replace github.com/imajinyun/gofly="$root"
		"$go_cmd" mod tidy
	)

	if diff -ru "$first" "$second" >"$diff_file"; then
		repeat_status="clean"
	else
		repeat_status="changed"
	fi
	if [ "$repeat_status" != "clean" ]; then
		printf 'generated profile %s is not deterministic; see %s\n' "$profile" "$diff_file" >&2
		exit 1
	fi

	(
		cd "$first"
		"$go_cmd" test ./...
	) >"$test_output" 2>&1

	python3 - "$results_jsonl" "$profile" "$api_rel" "$proto_rel" "$config_rel" "$expected_diff" "$verification" "$stdout_file" "$test_output" "$diff_file" <<'PY'
import json, pathlib, sys
out = pathlib.Path(sys.argv[1])
stdout = json.loads(pathlib.Path(sys.argv[8]).read_text(encoding="utf-8"))
test_output = pathlib.Path(sys.argv[9]).read_text(encoding="utf-8", errors="replace")
diff_text = pathlib.Path(sys.argv[10]).read_text(encoding="utf-8", errors="replace")
generated = stdout.get("data", {}).get("generatedFiles", 0)
payload = {
    "profile": sys.argv[2],
    "api": sys.argv[3],
    "proto": sys.argv[4],
    "serviceConfig": sys.argv[5],
    "expectedDiff": sys.argv[6],
    "verification": sys.argv[7],
    "generatedFiles": generated,
    "goTest": "passed" if "FAIL" not in test_output else "failed",
    "repeatGenerationDiff": "clean" if not diff_text.strip() else "changed",
    "snapshot": {
        "hasGoMod": generated > 0,
        "hasGeneratedSmoke": "internal/smoke" in "\n".join(stdout.get("data", {}).get("nextActions", [])) or generated > 0,
    },
}
with out.open("a", encoding="utf-8") as f:
    f.write(json.dumps(payload, sort_keys=True) + "\n")
PY
}

while IFS="$(printf '\t')" read -r profile api_rel proto_rel config_rel expected_diff verification; do
	[ -n "$profile" ] || continue
	printf '\n== generated version profile: %s ==\n' "$profile"
	run_profile "$profile" "$api_rel" "$proto_rel" "$config_rel" "$expected_diff" "$verification"
done < "$profiles"

python3 - "$results_jsonl" "$report" <<'PY'
import json, pathlib, sys
rows = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").splitlines() if line.strip()]
failures = []
for row in rows:
    if row["generatedFiles"] <= 0:
        failures.append(f"{row['profile']}: generatedFiles must be positive")
    if row["goTest"] != "passed":
        failures.append(f"{row['profile']}: go test did not pass")
    if row["repeatGenerationDiff"] != "clean":
        failures.append(f"{row['profile']}: repeat generation diff is not clean")
report = {
    "schema": "gofly.generated_version_compat_report.v1",
    "status": "failed" if failures else "passed",
    "profiles": rows,
    "failures": failures,
}
pathlib.Path(sys.argv[2]).write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
if failures:
    for failure in failures:
        print(failure, file=sys.stderr)
    raise SystemExit(1)
print(f"generated version compatibility matrix ok: {len(rows)} profiles")
print(f"generated version compatibility report: {sys.argv[2]}")
PY
