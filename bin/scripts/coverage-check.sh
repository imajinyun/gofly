#!/usr/bin/env sh
# shellcheck disable=SC2086
set -eu

# TESTFLAGS and PKGS intentionally support whitespace-separated flag/package lists.

threshold="${COVERAGE_THRESHOLD:-60}"
ratchet="${COVERAGE_RATCHET:-}"
profile="${COVERAGE_PROFILE:-coverage.out}"
pkgs="${PKGS:-./...}"
go_cmd="${GO:-go}"
testflags="${TESTFLAGS:--shuffle=on}"
tmp="${COVERAGE_TMPDIR:-}"

if [ -z "$tmp" ]; then
	tmp="$(mktemp -d)"
	trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM
else
	mkdir -p "$tmp"
fi

case " ${GOFLAGS:-} " in
*" -count=1 "*) ;;
*) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" ;;
esac

if [ -z "${GOCACHE:-}" ]; then
	export GOCACHE="$tmp/gocache"
	mkdir -p "$GOCACHE"
fi
if [ -z "${GOTMPDIR:-}" ]; then
	export GOTMPDIR="$tmp/gotmp"
	mkdir -p "$GOTMPDIR"
fi

"$go_cmd" test -p=1 $testflags -covermode=atomic -coverprofile="$profile" $pkgs
module_path="$("$go_cmd" list -m)"
sanitized_profile="$tmp/coverage.sanitized.out"
malformed_file="$tmp/coverage.malformed-count"
awk -v malformed_file="$malformed_file" -v module_path="$module_path" '
	NR == 1 { print; next }
	/^[^[:space:]]+:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ [0-9]+ [0-9]+$/ {
		split($1, loc, ":")
		if (index(loc[1], module_path "/") == 1) {
			print
			next
		}
	}
	NF == 0 { next }
	{ malformed++ }
	END { print malformed + 0 > malformed_file }
' "$profile" >"$sanitized_profile"
malformed_count="$(cat "$malformed_file")"
if [ "$malformed_count" != "0" ]; then
	printf 'ignored %s malformed coverage profile line(s) in %s\n' "$malformed_count" "$profile"
fi
cover_report="$tmp/coverage-func.txt"
if ! "$go_cmd" tool cover -func="$sanitized_profile" >"$cover_report"; then
	echo "coverage profile is invalid after sanitizing: $profile"
	exit 1
fi
coverage="$(tail -n 1 "$cover_report" | awk '{print $NF}' | tr -d '%')"

echo "coverage threshold: ${threshold}%"
if [ -n "$ratchet" ]; then
	echo "coverage ratchet: ${ratchet}%"
fi
echo "measured: ${coverage}%"

awk -v threshold="$threshold" -v ratchet="$ratchet" -v coverage="$coverage" 'BEGIN {
	floor = threshold + 0
	label = "threshold"
	if (ratchet != "" && ratchet + 0 > floor) {
		floor = ratchet + 0
		label = "ratchet"
	}
	if (coverage + 0 < floor) {
		printf "coverage %.2f%% < %s %.2f%% - FAIL\n", coverage, label, floor
		exit 1
	}
	printf "coverage %.2f%% >= %s %.2f%% - OK\n", coverage, label, floor
}'
