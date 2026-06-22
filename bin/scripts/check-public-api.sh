#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
module="${MODULE_PATH:-$($go_cmd list -m)}"
report="${API_COMPAT_REPORT:-api-compat-report.json}"

write_report() {
	status="$1"
	base="$2"
	reason="$3"
	python3 - "$report" "$status" "$base" "$reason" "$module" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
data = {
    "schema": "gofly.api_compat_report.v1",
    "status": sys.argv[2],
    "base_ref": sys.argv[3],
    "reason": sys.argv[4],
    "module": sys.argv[5],
}
path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

fail_release_skip() {
	case "${GITHUB_REF:-}" in
	refs/tags/v*) return 0 ;;
	esac
	[ "${API_COMPAT_REQUIRED:-false}" = "true" ] || [ "${GOVERNANCE_RELEASE:-false}" = "true" ]
}

run_apidiff() {
	if [ -n "${APIDIFF_TOOL:-}" ]; then
		"$APIDIFF_TOOL" "$@"
		return
	fi
	"$go_cmd" tool apidiff "$@"
}

base_ref="${API_BASE_REF:-}"
if [ -z "$base_ref" ]; then
	if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ] && [ -n "${GITHUB_BASE_REF:-}" ]; then
		base_ref="origin/${GITHUB_BASE_REF}"
	elif git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
		base_ref="HEAD~1"
	else
		base_ref="origin/main"
	fi
fi

if ! git rev-parse --verify "${base_ref}^{commit}" >/dev/null 2>&1; then
	reason="base ref '${base_ref}' is not available"
	echo "$reason; skipping public API compatibility check"
	write_report skipped "$base_ref" "$reason"
	if fail_release_skip; then
		echo "public API compatibility skip is forbidden for release/tag governance"
		exit 1
	fi
	exit 0
fi

tmp="$(mktemp -d)"
trap 'git worktree remove -f "$tmp/base" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT INT TERM

git worktree add --detach "$tmp/base" "$base_ref" >/dev/null

echo "public API base: ${base_ref}"
echo "module: ${module}"

(cd "$tmp/base" && run_apidiff -m -w "$tmp/base.exp" "$module")
run_apidiff -m -w "$tmp/current.exp" "$module"

changes="$(run_apidiff -m -incompatible "$tmp/base.exp" "$tmp/current.exp")"
if [ -n "$changes" ]; then
	echo "incompatible public Go API changes detected:"
	echo "$changes"
	write_report failed "$base_ref" "incompatible public Go API changes detected"
	exit 1
fi

echo "no incompatible public Go API changes detected"
write_report passed "$base_ref" "no incompatible public Go API changes detected"
