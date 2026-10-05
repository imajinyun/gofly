#!/usr/bin/env sh
# shellcheck disable=SC2086
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$root"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
export GOPROXY="${GOPROXY:-direct}"
export GOFLAGS="${GOFLAGS:--count=1}"
export GOFLY_MIDDLEWARE_REPORT_DIR="${GOFLY_MIDDLEWARE_REPORT_DIR:-$root/.tmp-test/middleware-presets}"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$GOFLY_MIDDLEWARE_REPORT_DIR"

go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--count=1 -shuffle=on}"
pattern='^Test(MiddlewarePreset|GenerateMiddlewarePreset|GenerateAllMiddlewarePreset|APIMiddlewarePreset|RunMainMiddleware|CommandHelpTopic|CommandCatalogCoversPrimaryTopics|GeneralCatalogTopics)'
packages='./cmd/gofly ./cmd/gofly/internal/generator ./cmd/gofly/internal/command ./cmd/gofly/internal/command/help'

for package in $packages; do
    "$go_cmd" test "$package" -list "$pattern" > "$tmp/tests.txt"
    count="$(awk '/^Test/ { n++ } END { print n+0 }' "$tmp/tests.txt")"
    if [ "$count" -eq 0 ]; then
        printf 'middleware gate selected no tests in %s\n' "$package" >&2
        exit 1
    fi
done

"$go_cmd" test $testflags $packages -run "$pattern"
printf 'middleware runtime report: %s/runtime.jsonl\n' "$GOFLY_MIDDLEWARE_REPORT_DIR"
