#!/usr/bin/env bash
# shellcheck disable=SC2086
# benchstat.sh — run benchmarks, compare against a baseline, and emit trend artifacts.
# Usage:
#   bash bin/scripts/benchstat.sh              # run bench package benchmarks, save to bench/current.txt
#   bash bin/scripts/benchstat.sh --compare    # compare bench/current.txt against bench/baseline.txt
#   bash bin/scripts/benchstat.sh --smoke      # run one iteration for CI smoke
#   bash bin/scripts/benchstat.sh --trend      # write bench/summary.md from the current run
#   bash bin/scripts/benchstat.sh --matrix     # print the authoritative benchmark matrix
#   bash bin/scripts/benchstat.sh --baseline   # refresh bench/baseline.txt and bench/evidence.md
#   bash bin/scripts/benchstat.sh --evidence   # write bench/evidence.md from bench/baseline.txt
#   bash bin/scripts/benchstat.sh --check-evidence # validate tracked benchmark evidence
#   bash bin/scripts/benchstat.sh --regression-check # block HTTP hot-path allocs/op regressions

set -eu

# BENCH_PKGS intentionally supports a whitespace-separated package list.

GO="${GO:-go}"
BENCH_DIR="${BENCH_DIR:-bench}"
CURRENT_FILE="${BENCH_DIR}/current.txt"
BASELINE_FILE="${BENCH_DIR}/baseline.txt"
SUMMARY_FILE="${BENCH_DIR}/summary.md"
MATRIX_FILE="${BENCH_DIR}/matrix.md"
EVIDENCE_FILE="${BENCH_DIR}/evidence.md"
REGRESSION_REPORT_FILE="${BENCH_DIR}/regression-report.json"
BENCH_ALLOC_REGRESSION_TOLERANCE="${BENCH_ALLOC_REGRESSION_TOLERANCE:-0}"

# Package that contains the reproducible benchmark matrix and public artifacts.
# Set BENCH_PKGS explicitly to include legacy package-local benchmarks.
BENCH_PKGS="${BENCH_PKGS:-./bench/}"
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
		BenchmarkRPCUnary \
		BenchmarkRPCStreamGovernance; do
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

check_regression() {
	if [ ! -f "$BASELINE_FILE" ]; then
		echo "Baseline not found at $BASELINE_FILE; run --baseline first."
		exit 1
	fi
	if [ ! -f "$CURRENT_FILE" ]; then
		echo "Current results not found at $CURRENT_FILE; run --smoke or make bench-stat first."
		exit 1
	fi
	mkdir -p "$BENCH_DIR"
	python3 - "$BASELINE_FILE" "$CURRENT_FILE" "$REGRESSION_REPORT_FILE" "$BENCH_ALLOC_REGRESSION_TOLERANCE" <<'PY'
import json
import re
import statistics
import sys
from pathlib import Path

baseline_path = Path(sys.argv[1])
current_path = Path(sys.argv[2])
report_path = Path(sys.argv[3])
alloc_tolerance = float(sys.argv[4])

line_re = re.compile(
    r"^(Benchmark\S+)-\d+\s+\d+\s+([0-9.]+)\s+ns/op\s+([0-9.]+)\s+B/op\s+([0-9.]+)\s+allocs/op$"
)

tracked = {
    "BenchmarkHTTPHello/gofly",
    "BenchmarkHTTPPathParams/gofly",
    "BenchmarkHTTPJSONBinding/gofly",
    "BenchmarkHTTPMiddlewareChain/gofly",
    "BenchmarkHTTPOpenAPI/disabled",
    "BenchmarkHTTPOpenAPI/enabled",
    "BenchmarkHTTPGovernance/disabled",
    "BenchmarkHTTPGovernance/enabled",
}


def parse(path: Path) -> dict[str, list[dict[str, float]]]:
    parsed: dict[str, list[dict[str, float]]] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        match = line_re.match(raw.strip())
        if not match:
            continue
        name, ns_op, bytes_op, allocs_op = match.groups()
        if name not in tracked:
            continue
        parsed.setdefault(name, []).append(
            {
                "nsPerOp": float(ns_op),
                "bytesPerOp": float(bytes_op),
                "allocsPerOp": float(allocs_op),
            }
        )
    return parsed


def median(values: list[float]) -> float:
    return float(statistics.median(values))


baseline = parse(baseline_path)
current = parse(current_path)
rows = []
failures = []
missing = []

for name in sorted(tracked):
    if name not in baseline:
        missing.append(f"baseline missing {name}")
        continue
    if name not in current:
        missing.append(f"current missing {name}")
        continue
    base_samples = baseline[name]
    current_samples = current[name]
    base_allocs = median([sample["allocsPerOp"] for sample in base_samples])
    current_allocs = median([sample["allocsPerOp"] for sample in current_samples])
    alloc_budget = base_allocs + alloc_tolerance
    row = {
        "benchmark": name,
        "baseline": {
            "samples": len(base_samples),
            "nsPerOpMedian": median([sample["nsPerOp"] for sample in base_samples]),
            "bytesPerOpMedian": median([sample["bytesPerOp"] for sample in base_samples]),
            "allocsPerOpMedian": base_allocs,
        },
        "current": {
            "samples": len(current_samples),
            "nsPerOpMedian": median([sample["nsPerOp"] for sample in current_samples]),
            "bytesPerOpMedian": median([sample["bytesPerOp"] for sample in current_samples]),
            "allocsPerOpMedian": current_allocs,
        },
        "budget": {
            "allocsPerOpMax": alloc_budget,
            "allocTolerance": alloc_tolerance,
            "latencyMode": "report-only",
        },
        "status": "passed" if current_allocs <= alloc_budget else "failed",
    }
    if row["status"] == "failed":
        failures.append(
            f"{name}: allocs/op median {current_allocs:g} exceeds budget {alloc_budget:g}"
        )
    rows.append(row)

if missing:
    failures.extend(missing)

report = {
    "schema": "gofly.benchmark_regression_report.v1",
    "status": "passed" if not failures else "failed",
    "policy": {
        "scope": "HTTP hot-path gofly rows",
        "blockingMetric": "allocs/op median",
        "latencyMode": "report-only",
        "allocTolerance": alloc_tolerance,
    },
    "baselineFile": str(baseline_path),
    "currentFile": str(current_path),
    "checks": rows,
    "failures": failures,
}
report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")

if failures:
    print(f"benchmark regression check failed; report written to {report_path}", file=sys.stderr)
    for failure in failures:
        print(f"  {failure}", file=sys.stderr)
    sys.exit(1)

print(f"benchmark regression ok; report written to {report_path}")
PY
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
		echo "| RPC stream governance | BenchmarkRPCStreamGovernance | gofly RPC stream governance | BENCH_PATTERN=BenchmarkRPCStreamGovernance make bench-stat | Aggregate stream governance overhead |"
		echo "| RPC server stream governance | BenchmarkRPCServerStreamGovernance | gofly RPC server stream governance | BENCH_PATTERN=BenchmarkRPCServerStreamGovernance make bench-stat | Server-stream setup, send and policy overhead |"
		echo "| RPC client stream governance | BenchmarkRPCClientStreamGovernance | gofly RPC client stream governance | BENCH_PATTERN=BenchmarkRPCClientStreamGovernance make bench-stat | Client-stream send, response and policy overhead |"
		echo "| RPC bidi stream governance | BenchmarkRPCBidiStreamGovernance | gofly RPC bidi stream governance | BENCH_PATTERN=BenchmarkRPCBidiStreamGovernance make bench-stat | Bidirectional stream round-trip and policy overhead |"
		echo "| RPC resolver/balancer smoke | examples/rpc-idl-matrix | gofly resolver, weighted round-robin, P2C, consistent hash, health-aware | go run -C examples/rpc-idl-matrix . | resolver/balancer smoke and Kitex boundary evidence |"
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
	--regression-check)
		check_regression
		;;
	*)
		run_benchmarks "$CURRENT_FILE" "$BENCH_COUNT"
		;;
esac
