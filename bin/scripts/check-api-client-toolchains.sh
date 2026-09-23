#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/gofly-api-client-toolchains-XXXXXX")"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
mkdir -p "$GOCACHE" "$GOTMPDIR"

report="${API_CLIENT_TOOLCHAIN_REPORT:-$tmp/api-client-toolchain-report.json}"
python3 "$root/bin/scripts/check-api-client-toolchains.py" \
  --root "$root" \
  --work "$tmp" \
  --report "$report" \
  --language "${API_CLIENT_TOOLCHAIN:-all}" \
  --required "${API_CLIENT_TOOLCHAIN_REQUIRED:-false}"

printf 'api client toolchain report: %s\n' "$report"
