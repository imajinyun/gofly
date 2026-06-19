#!/usr/bin/env bash
# shellcheck disable=SC2086
# benchstat.sh — run benchmarks and optionally compare against a baseline.
# Usage:
#   bash bin/scripts/benchstat.sh              # run benchmarks, save to bench/current.txt
#   bash bin/scripts/benchstat.sh --compare    # compare bench/current.txt against bench/baseline.txt
#   bash bin/scripts/benchstat.sh --smoke      # run one iteration for CI smoke

set -eu

# BENCH_PKGS intentionally supports a whitespace-separated package list.

GO="${GO:-go}"
BENCH_DIR="${BENCH_DIR:-bench}"
CURRENT_FILE="${BENCH_DIR}/current.txt"
BASELINE_FILE="${BENCH_DIR}/baseline.txt"

# Packages that contain production-relevant benchmarks.
BENCH_PKGS="${BENCH_PKGS:-./rest/ ./rpc/ ./gateway/ ./core/breaker/ ./core/limit/ ./core/governance/ ./cmd/gofly/internal/generator/}"
BENCH_PATTERN="${BENCH_PATTERN:-Benchmark}"

run_benchmarks() {
	mkdir -p "$BENCH_DIR"
	count="${1:-5}"
	echo "Running benchmarks (count=${count}) ..."
	$GO test -run='^$' \
		-bench="$BENCH_PATTERN" \
		-count="$count" -benchmem \
		$BENCH_PKGS > "$CURRENT_FILE"
	echo "Results written to $CURRENT_FILE"
}

compare() {
	if ! command -v benchstat >/dev/null 2>&1; then
		echo "benchstat not found; install with: go install golang.org/x/perf/cmd/benchstat@latest"
		exit 1
	fi
	if [ ! -f "$BASELINE_FILE" ]; then
		echo "Baseline not found at $BASELINE_FILE; run benchmarks first to establish it."
		exit 1
	fi
	if [ ! -f "$CURRENT_FILE" ]; then
		echo "Current results not found at $CURRENT_FILE; run without --compare first."
		exit 1
	fi
	echo "Comparing $CURRENT_FILE against $BASELINE_FILE ..."
	benchstat "$BASELINE_FILE" "$CURRENT_FILE"
}

case "${1:-}" in
	--compare)
		compare
		;;
	--smoke)
		run_benchmarks 1
		;;
	*)
		run_benchmarks 5
		;;
esac
