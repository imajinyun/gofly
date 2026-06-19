#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
module="${MODULE_PATH:-$($go_cmd list -m)}"

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
	echo "base ref '${base_ref}' is not available; skipping public API compatibility check"
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
	exit 1
fi

echo "no incompatible public Go API changes detected"
