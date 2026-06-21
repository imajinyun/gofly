#!/usr/bin/env bash
# shellcheck disable=SC2086
# benchstat.sh — run benchmarks, compare against a baseline, and emit trend artifacts.
# Usage:
#   bash bin/scripts/benchstat.sh              # run benchmarks, save to bench/current.txt
#   bash bin/scripts/benchstat.sh --compare    # compare bench/current.txt against bench/baseline.txt
#   bash bin/scripts/benchstat.sh --smoke      # run one iteration for CI smoke
#   bash bin/scripts/benchstat.sh --trend      # write bench/summary.md from the current run
#   bash bin/scripts/benchstat.sh --matrix     # print the authoritative benchmark matrix
#   bash bin/scripts/benchstat.sh --baseline   # refresh bench/baseline.txt and bench/evidence.md
#   bash bin/scripts/benchstat.sh --evidence   # write bench/evidence.md from bench/baseline.txt
#   bash bin/scripts/benchstat.sh --check-evidence # validate tracked benchmark evidence

set -eu

# BENCH_PKGS intentionally supports a whitespace-separated package list.

GO="${GO:-go}"
BENCH_DIR="${BENCH_DIR:-bench}"
CURRENT_FILE="${BENCH_DIR}/current.txt"
BASELINE_FILE="${BENCH_DIR}/baseline.txt"
SUMMARY_FILE="${BENCH_DIR}/summary.md"
MATRIX_FILE="${BENCH_DIR}/matrix.md"
EVIDENCE_FILE="${BENCH_DIR}/evidence.md"

# Packages that contain the reproducible Phase 2 benchmark matrix. Set
# BENCH_PKGS explicitly to include legacy package-local benchmarks.
BENCH_PKGS="${BENCH_PKGS:-./benchmarks/}"
BENCH_PATTERN="${BENCH_PATTERN:-Benchmark}"
BENCH_COUNT="${BENCH_COUNT:-5}"

write_environment() {
	goos="$($GO env GOOS 2>/dev/null || echo unknown)"
	goarch="$($GO env GOARCH 2>/dev/null || echo unknown)"
	goversion="$($GO version 2>/dev/null || echo unknown)"
	cpu="unknown"
	case "$(uname -s 2>/dev/null || echo unknown)" in
		Darwin)
			cpu="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)"
			;;
		Linux)
			cpu="$(awk -F: '/model name/ {gsub(/^[ \t]+/, "", $2); print $2; exit}' /proc/cpuinfo 2>/dev/null || echo unknown)"
			;;
	esac
	printf 'goos: %s\n' "$goos"
	printf 'goarch: %s\n' "$goarch"
	printf 'go: %s\n' "$goversion"
	printf 'cpu: %s\n' "$cpu"
}

run_benchmarks() {
	out_file="${1:-$CURRENT_FILE}"
	count="${2:-$BENCH_COUNT}"
	mkdir -p "$BENCH_DIR"
	echo "Running benchmarks (count=${count}) ..."
	$GO test -run='^$' \
		-bench="$BENCH_PATTERN" \
		-count="$count" -benchmem \
		$BENCH_PKGS > "$out_file"
	echo "Results written to $out_file"
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

write_trend() {
	if [ ! -f "$CURRENT_FILE" ]; then
		echo "Current results not found at $CURRENT_FILE; run benchmarks first."
		exit 1
	fi
	{
		echo "# Benchmark trend"
		echo
		echo "Generated: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
		echo
		echo "## Reproduce"
		echo
		echo '```sh'
		echo "BENCH_PKGS=\"$BENCH_PKGS\" BENCH_PATTERN=\"$BENCH_PATTERN\" bash bin/scripts/benchstat.sh"
		echo '```'
		echo
		if command -v benchstat >/dev/null 2>&1 && [ -f "$BASELINE_FILE" ]; then
			echo "## benchstat vs baseline"
			echo
			echo '```text'
			benchstat "$BASELINE_FILE" "$CURRENT_FILE"
			echo '```'
			echo
		elif [ -f "$BASELINE_FILE" ]; then
			echo "benchstat was not found; install with: go install golang.org/x/perf/cmd/benchstat@latest"
			echo
		else
			echo "No baseline found at $BASELINE_FILE; trend summary includes the current raw run only."
			echo
		fi
		echo "## Current raw output"
		echo
		echo '```text'
		cat "$CURRENT_FILE"
		echo '```'
	} > "$SUMMARY_FILE"
	echo "Trend summary written to $SUMMARY_FILE"
}

write_evidence() {
	if [ ! -f "$BASELINE_FILE" ]; then
		echo "Baseline not found at $BASELINE_FILE; run --baseline first."
		exit 1
	fi
	if grep -Eq '(^|[[:space:]])(FAIL|--- FAIL|panic:|exit status)' "$BASELINE_FILE"; then
		echo "Baseline contains failed benchmark output; refresh it before writing evidence."
		exit 1
	fi
	{
		echo "# Benchmark evidence"
		echo
		echo "This file is the committed public baseline for the benchmark matrix. It is intended for release notes, regression triage, and external reproduction."
		echo
		echo "## Environment"
		echo
		echo '```text'
		write_environment
		echo '```'
		echo
		echo "## Reproduce"
		echo
		echo '```sh'
		echo "BENCH_COUNT=$BENCH_COUNT BENCH_PKGS=\"$BENCH_PKGS\" BENCH_PATTERN=\"$BENCH_PATTERN\" make bench-baseline"
		echo '```'
		echo
		echo "## Matrix"
		echo
		echo "See [Benchmark matrix](matrix.md) for the scenario list, comparison candidates, and trust signals."
		echo
		echo "## Raw baseline"
		echo
		echo '```text'
		cat "$BASELINE_FILE"
		echo '```'
	} > "$EVIDENCE_FILE"
	echo "Benchmark evidence written to $EVIDENCE_FILE"
}

check_evidence() {
	for file in "$BASELINE_FILE" "$MATRIX_FILE" "$EVIDENCE_FILE"; do
		if [ ! -f "$file" ]; then
			echo "missing benchmark artifact: $file"
			exit 1
		fi
	done
	if grep -Eq '(^|[[:space:]])(FAIL|--- FAIL|panic:|exit status)' "$BASELINE_FILE" "$EVIDENCE_FILE"; then
		echo "benchmark evidence contains failed run output"
		exit 1
	fi
	for benchmark in \
		BenchmarkHTTPHello \
		BenchmarkHTTPPathParams \
		BenchmarkHTTPJSONBinding \
		BenchmarkHTTPMiddlewareChain \
		BenchmarkHTTPOpenAPI \
		BenchmarkHTTPGovernance \
		BenchmarkRPCUnary; do
		if ! grep -q "$benchmark" "$BASELINE_FILE"; then
			echo "baseline is missing $benchmark"
			exit 1
		fi
		if ! grep -q "$benchmark" "$MATRIX_FILE"; then
			echo "matrix is missing $benchmark"
			exit 1
		fi
	done
	if ! grep -q 'BENCH_COUNT=' "$EVIDENCE_FILE"; then
		echo "evidence is missing reproduction command"
		exit 1
	fi
	echo "benchmark evidence ok"
}

write_matrix() {
	mkdir -p "$BENCH_DIR"
	{
		echo "# Benchmark matrix"
		echo
		echo "This is the authoritative public benchmark matrix for gofly release notes and CI smoke checks. It explains what is measured, which competitors are comparable, and which command reproduces the data."
		echo
		echo "| Area | Benchmark | Candidates | Reproduce | Trust signal |"
		echo "| --- | --- | --- | --- | --- |"
		echo "| REST routing | BenchmarkHTTPHello | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPHello make bench-stat | Dispatch latency and allocations |"
		echo "| REST path params | BenchmarkHTTPPathParams | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPPathParams make bench-stat | Param extraction overhead |"
		echo "| REST JSON binding | BenchmarkHTTPJSONBinding | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPJSONBinding make bench-stat | Binding and response overhead |"
		echo "| REST middleware | BenchmarkHTTPMiddlewareChain | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPMiddlewareChain make bench-stat | Five-layer middleware overhead |"
		echo "| OpenAPI overhead | BenchmarkHTTPOpenAPI | gofly disabled/enabled | BENCH_PATTERN=BenchmarkHTTPOpenAPI make bench-stat | Contract metadata cost |"
		echo "| Governance overhead | BenchmarkHTTPGovernance | gofly disabled/enabled | BENCH_PATTERN=BenchmarkHTTPGovernance make bench-stat | Runtime policy decision cost |"
		echo "| RPC unary | BenchmarkRPCUnary | gofly RPC, gRPC-Go | BENCH_PATTERN=BenchmarkRPCUnary make bench-stat | Service-to-service call cost |"
		echo
		echo "## Release rule"
		echo
		echo "Every stable release should attach raw benchmark output from \`make bench-stat\` plus \`bench/summary.md\` from \`make bench-trend\`. If a release changes REST, RPC, gateway, or governance hot paths, paste the significant benchstat rows into the release notes."
	} > "$MATRIX_FILE"
	echo "Benchmark matrix written to $MATRIX_FILE"
}

case "${1:-}" in
	--compare)
		compare
		;;
	--smoke)
		run_benchmarks "$CURRENT_FILE" 1
		;;
	--trend)
		write_trend
		;;
	--matrix)
		write_matrix
		;;
	--baseline)
		run_benchmarks "$BASELINE_FILE" "$BENCH_COUNT"
		write_evidence
		;;
	--evidence)
		write_evidence
		;;
	--check-evidence)
		check_evidence
		;;
	*)
		run_benchmarks "$CURRENT_FILE" "$BENCH_COUNT"
		;;
esac
