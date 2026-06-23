#!/usr/bin/env sh
set -eu

script_dir=$(unset CDPATH && cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(unset CDPATH && cd -- "$script_dir/../.." && pwd)
check_script="$script_dir/check-public-api.sh"

tmp_root=$(mktemp -d)
trap 'rm -rf "$tmp_root"' EXIT INT HUP TERM

assert_report() {
	report=$1
	want_status=$2
	want_base=$3
	want_reason=$4
	python3 - "$report" "$want_status" "$want_base" "$want_reason" <<'PY'
import json
import pathlib
import sys

report = pathlib.Path(sys.argv[1])
want_status = sys.argv[2]
want_base = sys.argv[3]
want_reason = sys.argv[4]

if not report.is_file() or report.stat().st_size == 0:
    raise SystemExit(f"missing non-empty API compatibility report: {report}")

data = json.loads(report.read_text(encoding="utf-8"))
checks = {
    "schema": "gofly.api_compat_report.v1",
    "status": want_status,
    "base_ref": want_base,
    "reason": want_reason,
    "module": "github.com/gofly/gofly",
}
for key, want in checks.items():
    got = data.get(key)
    if got != want:
        raise SystemExit(f"{report}: {key}={got!r}, want {want!r}")
PY
}

run_skip_allowed() {
	name=$1
	base_ref=$2
	reason="base ref '$base_ref' is not available"
	report="$tmp_root/$name.json"
	out="$tmp_root/$name.out"
	(
		cd "$repo_root"
		API_BASE_REF="$base_ref" API_COMPAT_REPORT="$report" sh "$check_script"
	) >"$out" 2>&1
	assert_report "$report" skipped "$base_ref" "$reason"
	if ! grep -F "skipping public API compatibility check" "$out" >/dev/null; then
		echo "expected skip output for $name" >&2
		cat "$out" >&2
		return 1
	fi
}

run_skip_forbidden() {
	name=$1
	base_ref=$2
	reason="base ref '$base_ref' is not available"
	report="$tmp_root/$name.json"
	out="$tmp_root/$name.out"
	shift 2
	if (
		cd "$repo_root"
		env API_BASE_REF="$base_ref" API_COMPAT_REPORT="$report" "$@" sh "$check_script"
	) >"$out" 2>&1; then
		echo "expected forbidden API compatibility skip to fail for $name" >&2
		cat "$out" >&2
		return 1
	fi
	assert_report "$report" skipped "$base_ref" "$reason"
	if ! grep -F "public API compatibility skip is forbidden for release/tag governance" "$out" >/dev/null; then
		echo "expected forbidden skip output for $name" >&2
		cat "$out" >&2
		return 1
	fi
}

run_skip_allowed "missing-base-non-release" "refs/heads/gofly-api-fixture-missing"
run_skip_forbidden "missing-base-api-required" "refs/heads/gofly-api-fixture-missing" API_COMPAT_REQUIRED=true
run_skip_forbidden "missing-base-release-tag" "refs/heads/gofly-api-fixture-missing" GITHUB_REF=refs/tags/v0.0.0-fixture
run_skip_forbidden "missing-base-governance-release" "refs/heads/gofly-api-fixture-missing" GOVERNANCE_RELEASE=true

printf 'public API compatibility skip fixture tests passed for %s\n' "$repo_root"
