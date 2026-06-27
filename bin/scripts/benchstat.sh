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
#   bash bin/scripts/benchstat.sh --regression-check # block HTTP hot-path budget regressions

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
RATCHET_FILE="${BENCH_DIR}/budget-ratchet.json"
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
	python3 - "$BASELINE_FILE" "$CURRENT_FILE" "$REGRESSION_REPORT_FILE" "$RATCHET_FILE" "$BENCH_ALLOC_REGRESSION_TOLERANCE" <<'PY'
import json
import re
import statistics
import sys
from pathlib import Path

baseline_path = Path(sys.argv[1])
current_path = Path(sys.argv[2])
report_path = Path(sys.argv[3])
ratchet_path = Path(sys.argv[4])
alloc_tolerance = float(sys.argv[5])

line_re = re.compile(
    r"^(Benchmark\S+)-\d+\s+\d+\s+([0-9.]+)\s+ns/op\s+([0-9.]+)\s+B/op\s+([0-9.]+)\s+allocs/op$"
)

ratchet = json.loads(ratchet_path.read_text(encoding="utf-8"))
tracked = set(ratchet.get("trackedBenchmarks") or [])
latency_policy = ratchet.get("latencyPolicy") or {}
promoted_latency = {
    item.get("benchmark", ""): item
    for item in latency_policy.get("promoted") or []
    if isinstance(item, dict) and item.get("benchmark")
}

policy_failures = []


def require_policy(condition: bool, message: str) -> None:
    if not condition:
        policy_failures.append(message)


def validate_ratchet_policy() -> None:
    allocation_policy = ratchet.get("allocationPolicy") or {}
    report_only = set(latency_policy.get("reportOnly") or [])
    promoted = latency_policy.get("promoted") or []
    rpc_policy = ratchet.get("rpcPolicy") or {}
    release_promotion = rpc_policy.get("releasePromotion") or {}
    rpc_candidates = rpc_policy.get("candidates") or []
    surface_policy = ratchet.get("surfacePolicy") or []

    require_policy(
        ratchet.get("schema") == "gofly.benchmark_budget_ratchet.v1",
        "budget ratchet schema mismatch",
    )
    require_policy(
        ratchet.get("acceptanceGate") == "make bench-regression-check",
        "budget ratchet acceptanceGate must be make bench-regression-check",
    )
    require_policy(bool(tracked), "budget ratchet trackedBenchmarks must not be empty")
    require_policy(
        allocation_policy.get("blocking") is True,
        "allocationPolicy.blocking must be true",
    )
    require_policy(
        allocation_policy.get("metric") == "allocs/op median",
        "allocationPolicy.metric must be allocs/op median",
    )
    require_policy(
        latency_policy.get("defaultMode") == "report-only",
        "latencyPolicy.defaultMode must remain report-only",
    )
    require_policy(
        isinstance(surface_policy, list) and bool(surface_policy),
        "surfacePolicy must list performance claim boundaries",
    )

    for item in promoted:
        if not isinstance(item, dict):
            require_policy(False, f"latencyPolicy.promoted item must be an object: {item!r}")
            continue
        benchmark = item.get("benchmark", "")
        require_policy(benchmark in tracked, f"promoted latency benchmark is not tracked: {benchmark}")
        require_policy(item.get("mode") == "blocking", f"{benchmark}: promoted latency mode must be blocking")
        require_policy(
            int(item.get("minimumBaselineSamples") or 0) >= 5,
            f"{benchmark}: promoted latency requires at least five baseline samples",
        )
        require_policy(
            float(item.get("maxRegressionRatio") or 0) >= 1,
            f"{benchmark}: promoted latency maxRegressionRatio must be >= 1",
        )
        require_policy(bool(item.get("reason")), f"{benchmark}: promoted latency reason is required")

    require_policy(
        not (set(promoted_latency) & report_only),
        "latencyPolicy promoted benchmarks must not also be report-only",
    )
    for benchmark in report_only:
        require_policy(benchmark in tracked, f"report-only latency benchmark is not tracked: {benchmark}")

    require_policy(
        release_promotion.get("status") == "blocked",
        "rpcPolicy.releasePromotion.status must remain blocked until Tier 1 criteria are met",
    )
    require_policy(
        release_promotion.get("requiredBlockingGate") == "make bench-regression-check",
        "rpcPolicy.releasePromotion.requiredBlockingGate must be make bench-regression-check",
    )
    require_policy(
        bool(release_promotion.get("rollbackGuidance")),
        "rpcPolicy.releasePromotion.rollbackGuidance is required",
    )
    require_policy(rpc_policy.get("status") == "report-only", "rpcPolicy.status must remain report-only")

    for item in rpc_candidates:
        if not isinstance(item, dict):
            require_policy(False, f"rpcPolicy candidate must be an object: {item!r}")
            continue
        benchmark = item.get("benchmark", "")
        require_policy(item.get("mode") == "candidate", f"{benchmark}: RPC benchmark mode must be candidate")
        require_policy(
            benchmark not in tracked,
            f"{benchmark}: RPC candidate must not enter trackedBenchmarks before promotion",
        )
        require_policy(bool(item.get("currentBlocker")), f"{benchmark}: currentBlocker is required")

    surface_ids = set()
    for item in surface_policy:
        if not isinstance(item, dict):
            require_policy(False, f"surfacePolicy item must be an object: {item!r}")
            continue
        surface_id = item.get("id", "")
        status = item.get("status", "")
        benchmark = item.get("benchmark", "")
        surface_ids.add(surface_id)
        require_policy(bool(surface_id), "surfacePolicy item id is required")
        require_policy(bool(item.get("surface")), f"{surface_id}: surface is required")
        require_policy(
            item.get("promotionGate") == "make bench-regression-check",
            f"{surface_id}: promotionGate must be make bench-regression-check",
        )
        require_policy(
            item.get("latencyMode") in {"blocking", "report-only"},
            f"{surface_id}: latencyMode must be blocking or report-only",
        )
        if status in {"allocation-blocking", "latency-and-allocation-blocking"}:
            require_policy(benchmark in tracked, f"{surface_id}: blocking surface benchmark must be tracked")
            require_policy(bool(item.get("evidence")), f"{surface_id}: blocking surface evidence is required")
        elif status == "candidate":
            require_policy(bool(benchmark), f"{surface_id}: candidate surface benchmark is required")
            require_policy(benchmark not in tracked, f"{surface_id}: candidate benchmark must stay out of trackedBenchmarks")
            require_policy(bool(item.get("currentBlocker")), f"{surface_id}: candidate currentBlocker is required")
        elif status == "unsupported-report-only":
            require_policy(not benchmark, f"{surface_id}: unsupported report-only surface must not name a benchmark")
            require_policy(
                item.get("latencyMode") == "report-only",
                f"{surface_id}: unsupported surface latencyMode must be report-only",
            )
            require_policy(bool(item.get("currentBlocker")), f"{surface_id}: unsupported currentBlocker is required")
            require_policy(bool(item.get("requiredEvidence")), f"{surface_id}: unsupported requiredEvidence is required")
        else:
            require_policy(False, f"{surface_id}: unknown surfacePolicy status {status!r}")

    for required_surface in (
        "rest-route-hot-path",
        "rpc-unary",
        "gateway-proxy",
        "governance-rule-match",
        "cache-hot-path",
    ):
        require_policy(
            required_surface in surface_ids,
            f"surfacePolicy missing required surface {required_surface}",
        )


validate_ratchet_policy()
if policy_failures:
    report = {
        "schema": "gofly.benchmark_regression_report.v1",
        "status": "failed",
        "policy": {
            "scope": "ratcheted gofly hot-path rows",
            "blockingMetric": "allocs/op median",
            "ratchet": str(ratchet_path),
            "ratchetSchema": ratchet.get("schema", ""),
            "latencyMode": latency_policy.get("defaultMode", ""),
            "latencyBlockingBenchmarks": sorted(promoted_latency),
            "rpcPolicy": ratchet.get("rpcPolicy", {}),
            "allocTolerance": alloc_tolerance,
        },
        "baselineFile": str(baseline_path),
        "currentFile": str(current_path),
        "checks": [],
        "failures": policy_failures,
    }
    report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"benchmark regression check failed; report written to {report_path}", file=sys.stderr)
    for failure in policy_failures:
        print(f"  {failure}", file=sys.stderr)
    sys.exit(1)


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
    base_ns = median([sample["nsPerOp"] for sample in base_samples])
    current_ns = median([sample["nsPerOp"] for sample in current_samples])
    alloc_budget = base_allocs + alloc_tolerance
    latency_rule = promoted_latency.get(name)
    latency_mode = latency_rule.get("mode", latency_policy.get("defaultMode", "report-only")) if latency_rule else latency_policy.get("defaultMode", "report-only")
    latency_budget = None
    latency_passed = True
    if latency_mode == "blocking":
        minimum_samples = int(latency_rule.get("minimumBaselineSamples") or 1)
        max_ratio = float(latency_rule.get("maxRegressionRatio") or 1)
        latency_budget = base_ns * max_ratio
        latency_passed = len(base_samples) >= minimum_samples and current_ns <= latency_budget
    row = {
        "benchmark": name,
        "baseline": {
            "samples": len(base_samples),
            "nsPerOpMedian": base_ns,
            "bytesPerOpMedian": median([sample["bytesPerOp"] for sample in base_samples]),
            "allocsPerOpMedian": base_allocs,
        },
        "current": {
            "samples": len(current_samples),
            "nsPerOpMedian": current_ns,
            "bytesPerOpMedian": median([sample["bytesPerOp"] for sample in current_samples]),
            "allocsPerOpMedian": current_allocs,
        },
        "budget": {
            "allocsPerOpMax": alloc_budget,
            "allocTolerance": alloc_tolerance,
            "latencyMode": latency_mode,
            "nsPerOpMax": latency_budget,
        },
        "status": "passed" if current_allocs <= alloc_budget and latency_passed else "failed",
    }
    if current_allocs > alloc_budget:
        failures.append(
            f"{name}: allocs/op median {current_allocs:g} exceeds budget {alloc_budget:g}"
        )
    if not latency_passed:
        if len(base_samples) < int(latency_rule.get("minimumBaselineSamples") or 1):
            failures.append(
                f"{name}: latency budget requires at least "
                f"{int(latency_rule.get('minimumBaselineSamples') or 1)} baseline samples"
            )
        else:
            failures.append(
                f"{name}: ns/op median {current_ns:g} exceeds budget {latency_budget:g}"
            )
    rows.append(row)

if missing:
    failures.extend(missing)

report = {
    "schema": "gofly.benchmark_regression_report.v1",
    "status": "passed" if not failures else "failed",
    "policy": {
        "scope": "ratcheted gofly hot-path rows",
        "blockingMetric": "allocs/op median",
        "ratchet": str(ratchet_path),
        "ratchetSchema": ratchet.get("schema", ""),
        "latencyMode": latency_policy.get("defaultMode", "report-only"),
        "latencyBlockingBenchmarks": sorted(promoted_latency),
        "rpcPolicy": ratchet.get("rpcPolicy", {}),
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
