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
	for benchmark in \
		BenchmarkGatewayProxy \
		BenchmarkCacheHotPath \
		BenchmarkCacheHotPathGetOrLoadHit; do
		if ! grep -q "$benchmark" "$MATRIX_FILE"; then
			echo "matrix is missing candidate benchmark $benchmark"
			exit 1
		fi
		if ! grep -q "func $benchmark(" "$BENCH_DIR/gateway_cache_bench_test.go"; then
			echo "candidate benchmark source is missing $benchmark"
			exit 1
		fi
		if ! grep -q "$benchmark" "$RATCHET_FILE"; then
			echo "ratchet is missing candidate benchmark $benchmark"
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
    adopter_contract = ratchet.get("adopterPerformanceContract") or {}
    promotion_evidence = ratchet.get("performancePromotionEvidence") or {}
    p9_ownership = ratchet.get("p9GatewayCacheOwnership") or {}
    p10_ratchet = ratchet.get("p10PerformanceBudgetRatchet") or {}
    p13_gateway_cache_closeout = ratchet.get("p13GatewayCacheBenchmarkCloseout") or {}
    p15_gateway_cache_attachment = ratchet.get("p15GatewayCacheBudgetAttachment") or {}
    p16_gateway_cache_samples = ratchet.get("p16GatewayCacheTrendSampleAttachment") or {}
    p16_gateway_cache_promotion_review = ratchet.get("p16GatewayCacheAllocationPromotionReview") or {}
    report_only = set(latency_policy.get("reportOnly") or [])
    promoted = latency_policy.get("promoted") or []
    rpc_policy = ratchet.get("rpcPolicy") or {}
    release_promotion = rpc_policy.get("releasePromotion") or {}
    rpc_candidates = rpc_policy.get("candidates") or []
    surface_policy = ratchet.get("surfacePolicy") or []
    r8_depth = ratchet.get("r8PerformanceDepthMatrix") or {}
    r8_surfaces = [
        item
        for item in r8_depth.get("surfaces") or []
        if isinstance(item, dict)
    ]

    require_policy(
        ratchet.get("schema") == "gofly.benchmark_budget_ratchet.v1",
        "budget ratchet schema mismatch",
    )
    require_policy(
        ratchet.get("acceptanceGate") == "make bench-regression-check",
        "budget ratchet acceptanceGate must be make bench-regression-check",
    )
    require_policy(
        r8_depth.get("schema") == "gofly.benchmark_r8_performance_depth_matrix.v1",
        "r8PerformanceDepthMatrix schema mismatch",
    )
    require_policy(
        r8_depth.get("aiflowTask") == "GOFLY-GOV-10R8-09",
        "r8PerformanceDepthMatrix aiflowTask mismatch",
    )
    require_policy(
        r8_depth.get("acceptanceGate") == "make bench-regression-check",
        "r8PerformanceDepthMatrix acceptanceGate must be make bench-regression-check",
    )
    require_policy(
        len(str(r8_depth.get("policy") or "").split()) >= 25,
        "r8PerformanceDepthMatrix policy must be actionable",
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
        adopter_contract.get("schema") == "gofly.benchmark_adopter_performance_contract.v1",
        "adopterPerformanceContract schema mismatch",
    )
    require_policy(
        adopter_contract.get("source") == "bench/budget-ratchet.json",
        "adopterPerformanceContract source mismatch",
    )
    require_policy(
        adopter_contract.get("dashboardReportField") == "benchmark.adopterPerformanceContract",
        "adopterPerformanceContract dashboardReportField mismatch",
    )
    require_policy(
        set(adopter_contract.get("acceptanceGates") or []) == {
            "make bench-regression-check",
            "make bench-evidence-check",
            "make bench-trend",
        },
        "adopterPerformanceContract acceptanceGates mismatch",
    )
    require_policy(
        len(str(adopter_contract.get("policy") or "").split()) >= 20,
        "adopterPerformanceContract policy must be actionable",
    )
    require_policy(
        latency_policy.get("defaultMode") == "report-only",
        "latencyPolicy.defaultMode must remain report-only",
    )
    require_policy(
        isinstance(surface_policy, list) and bool(surface_policy),
        "surfacePolicy must list performance claim boundaries",
    )
    require_policy(
        promotion_evidence.get("schema") == "gofly.benchmark_performance_promotion_evidence.v1",
        "performancePromotionEvidence schema mismatch",
    )
    require_policy(
        promotion_evidence.get("acceptanceGate") == "make bench-regression-check",
        "performancePromotionEvidence acceptanceGate must be make bench-regression-check",
    )
    require_policy(
        p9_ownership.get("schema") == "gofly.benchmark_gateway_cache_ownership.v1",
        "p9GatewayCacheOwnership schema mismatch",
    )
    require_policy(
        p9_ownership.get("aiflowTask") == "GOFLY-GOV-10P9-04",
        "p9GatewayCacheOwnership aiflowTask mismatch",
    )
    require_policy(
        p9_ownership.get("acceptanceGate") == "make bench-regression-check",
        "p9GatewayCacheOwnership acceptanceGate must be make bench-regression-check",
    )
    require_policy(
        p9_ownership.get("status") == "candidate-report-only",
        "p9GatewayCacheOwnership status must remain candidate-report-only",
    )
    require_policy(
        len(str(p9_ownership.get("policy") or "").split()) >= 20,
        "p9GatewayCacheOwnership policy must be actionable",
    )
    require_policy(
        p10_ratchet.get("schema") == "gofly.benchmark_p10_performance_budget_ratchet.v1",
        "p10PerformanceBudgetRatchet schema mismatch",
    )
    require_policy(
        p10_ratchet.get("aiflowTask") == "GOFLY-P10-7-PERFORMANCE-BUDGET-RATCHET",
        "p10PerformanceBudgetRatchet aiflowTask mismatch",
    )
    require_policy(
        p10_ratchet.get("acceptanceGate") == "make bench-regression-check",
        "p10PerformanceBudgetRatchet acceptanceGate must be make bench-regression-check",
    )
    require_policy(
        p10_ratchet.get("status") == "closed-with-report-only-boundaries",
        "p10PerformanceBudgetRatchet status must record report-only boundary closeout",
    )
    require_policy(
        len(str(p10_ratchet.get("policy") or "").split()) >= 20,
        "p10PerformanceBudgetRatchet policy must be actionable",
    )

    confidence = promotion_evidence.get("multiRunConfidence") or {}
    baseline_samples_required = int(confidence.get("baselineSamplesRequired") or 0)
    current_samples_required = int(confidence.get("currentTrendSamplesRequired") or 0)
    require_policy(
        baseline_samples_required >= 5,
        "performancePromotionEvidence multiRunConfidence requires at least five baseline samples",
    )
    require_policy(
        current_samples_required >= 3,
        "performancePromotionEvidence multiRunConfidence requires at least three current trend samples",
    )
    require_policy(
        confidence.get("baselineArtifact") == "bench/baseline.txt",
        "performancePromotionEvidence baselineArtifact must be bench/baseline.txt",
    )
    require_policy(
        confidence.get("currentArtifact") == "bench/current.txt",
        "performancePromotionEvidence currentArtifact must be bench/current.txt",
    )
    require_policy(
        confidence.get("trendArtifact") == "bench/summary.md",
        "performancePromotionEvidence trendArtifact must be bench/summary.md",
    )
    require_policy(
        len(str(confidence.get("policy") or "").split()) >= 20,
        "performancePromotionEvidence multiRunConfidence policy must be actionable",
    )

    allocation_budget_rows = {
        item.get("benchmark"): item
        for item in promotion_evidence.get("promotedAllocationBudgets") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(allocation_budget_rows) == tracked,
        "performancePromotionEvidence promotedAllocationBudgets must cover every tracked benchmark",
    )
    for benchmark, item in allocation_budget_rows.items():
        require_policy(item.get("metric") == "allocs/op median", f"{benchmark}: allocation metric must be allocs/op median")
        require_policy(item.get("mode") == "blocking", f"{benchmark}: allocation budget mode must be blocking")
        require_policy(item.get("source") == "bench/baseline.txt", f"{benchmark}: allocation budget source must be bench/baseline.txt")
        require_policy(
            int(item.get("minimumBaselineSamples") or 0) >= baseline_samples_required,
            f"{benchmark}: allocation budget must require the multi-run baseline sample count",
        )
        for field in ("adopterAction", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{benchmark}: allocation budget {field} must be actionable",
            )

    latency_report_rows = {
        item.get("benchmark"): item
        for item in promotion_evidence.get("reportOnlyLatencyRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(latency_report_rows) == report_only,
        "performancePromotionEvidence reportOnlyLatencyRows must match latencyPolicy.reportOnly",
    )
    for benchmark, item in latency_report_rows.items():
        require_policy(item.get("mode") == "report-only", f"{benchmark}: latency row mode must be report-only")
        for field in ("promotionRequirement", "adopterAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{benchmark}: report-only latency {field} must be actionable",
            )

    evidence_candidates = {
        item.get("id"): item
        for item in promotion_evidence.get("candidateSurfaces") or []
        if isinstance(item, dict) and item.get("id")
    }
    require_policy(
        set(evidence_candidates) == {"gateway-proxy", "cache-hot-path"},
        "performancePromotionEvidence candidateSurfaces mismatch",
    )
    for surface_id, item in evidence_candidates.items():
        require_policy(
            item.get("status") == "candidate-report-only",
            f"{surface_id}: performance promotion candidate status must be candidate-report-only",
        )
        benchmarks = set(item.get("benchmarks") or [])
        require_policy(bool(benchmarks), f"{surface_id}: performance promotion candidate benchmarks are required")
        for benchmark in benchmarks:
            require_policy(
                benchmark not in tracked,
                f"{surface_id}: candidate benchmark {benchmark} must stay out of trackedBenchmarks before promotion",
            )
        for field in ("requiredEvidence", "adopterAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{surface_id}: performance promotion candidate {field} must be actionable",
            )

    adopter_actions = {
        item.get("id"): item
        for item in promotion_evidence.get("adopterActions") or []
        if isinstance(item, dict) and item.get("id")
    }
    for action_id in ("allocation-regression", "latency-trend-regression", "candidate-surface-promotion"):
        item = adopter_actions.get(action_id) or {}
        require_policy(bool(item), f"performancePromotionEvidence adopterActions missing {action_id}")
        for field in ("trigger", "action"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{action_id}: adopter action {field} must be actionable",
            )

    p9_candidates = {
        item.get("id"): item
        for item in p9_ownership.get("candidateSurfaces") or []
        if isinstance(item, dict) and item.get("id")
    }
    expected_p9_candidates = {
        "gateway-proxy": "BenchmarkGatewayProxy",
        "cache-hot-path": "BenchmarkCacheHotPath",
        "cache-hot-path-loader-hit": "BenchmarkCacheHotPathGetOrLoadHit",
    }
    require_policy(
        set(p9_candidates) == set(expected_p9_candidates),
        "p9GatewayCacheOwnership candidateSurfaces mismatch",
    )
    benchmark_source = Path("bench/gateway_cache_bench_test.go")
    source_text = benchmark_source.read_text(encoding="utf-8") if benchmark_source.is_file() else ""
    require_policy(source_text, "bench/gateway_cache_bench_test.go is missing")
    for surface_id, benchmark in expected_p9_candidates.items():
        item = p9_candidates.get(surface_id) or {}
        require_policy(item.get("benchmark") == benchmark, f"p9GatewayCacheOwnership {surface_id}: benchmark mismatch")
        require_policy(item.get("mode") == "candidate-report-only", f"p9GatewayCacheOwnership {surface_id}: mode must be candidate-report-only")
        require_policy(benchmark not in tracked, f"p9GatewayCacheOwnership {surface_id}: benchmark must stay out of trackedBenchmarks before promotion")
        require_policy(f"func {benchmark}(" in source_text, f"bench/gateway_cache_bench_test.go missing {benchmark}")
        for field in ("baselineRequirement", "currentTrendRequirement", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 8,
                f"p9GatewayCacheOwnership {surface_id}: {field} must be actionable",
            )
    promotion_criteria = set(p9_ownership.get("promotionCriteria") or [])
    for criterion in (
        "dedicated benchmark exists in bench/",
        "minimum 5 baseline samples",
        "minimum 3 current trend samples",
        "no allocation regression under bench-regression-check after promotion",
    ):
        require_policy(criterion in promotion_criteria, f"p9GatewayCacheOwnership promotionCriteria missing {criterion!r}")

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

    p10_blocking = {
        item.get("benchmark"): item
        for item in p10_ratchet.get("blockingBudgets") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    expected_p10_blocking = {
        "BenchmarkHTTPOpenAPI/disabled",
        "BenchmarkHTTPOpenAPI/enabled",
        "BenchmarkHTTPGovernance/disabled",
        "BenchmarkHTTPGovernance/enabled",
    }
    require_policy(
        set(p10_blocking) == expected_p10_blocking,
        "p10PerformanceBudgetRatchet blockingBudgets mismatch",
    )
    require_policy(
        set(p10_blocking) == set(promoted_latency),
        "p10PerformanceBudgetRatchet blockingBudgets must match promoted latency rows",
    )
    for benchmark, item in p10_blocking.items():
        require_policy(benchmark in tracked, f"{benchmark}: P10 blocking budget must stay tracked")
        require_policy(
            item.get("budgetScope") == "latency-and-allocation-blocking",
            f"{benchmark}: P10 blocking budget scope must be latency-and-allocation-blocking",
        )
        for field in ("confidence", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 8,
                f"{benchmark}: P10 blocking budget {field} must be actionable",
            )

    p10_allocation_only = set(p10_ratchet.get("allocationOnlyBudgets") or [])
    require_policy(
        p10_allocation_only == report_only,
        "p10PerformanceBudgetRatchet allocationOnlyBudgets must match latencyPolicy.reportOnly",
    )
    require_policy(
        p10_allocation_only <= tracked,
        "p10PerformanceBudgetRatchet allocationOnlyBudgets must stay tracked",
    )
    rpc_candidate_names_for_p10 = {
        item.get("benchmark", "")
        for item in rpc_candidates
        if isinstance(item, dict) and item.get("benchmark")
    }
    p10_report_only = set(p10_ratchet.get("reportOnlyLatencyRows") or [])
    p10_expected_report_only = (
        report_only
        | rpc_candidate_names_for_p10
        | {
            "BenchmarkGatewayProxy",
            "BenchmarkCacheHotPath",
            "BenchmarkCacheHotPathGetOrLoadHit",
        }
    )
    require_policy(
        p10_report_only == p10_expected_report_only,
        "p10PerformanceBudgetRatchet reportOnlyLatencyRows mismatch",
    )
    require_policy(
        not (p10_report_only & set(promoted_latency)),
        "p10PerformanceBudgetRatchet report-only latency rows must not include promoted latency rows",
    )
    for benchmark in (
        "BenchmarkRPCUnary/gofly_rpc",
        "BenchmarkRPCStreamGovernance",
        "BenchmarkRPCServerStreamGovernance",
        "BenchmarkRPCClientStreamGovernance",
        "BenchmarkRPCBidiStreamGovernance",
        "BenchmarkGatewayProxy",
        "BenchmarkCacheHotPath",
        "BenchmarkCacheHotPathGetOrLoadHit",
    ):
        require_policy(
            benchmark not in tracked,
            f"{benchmark}: P10 report-only candidate must stay out of trackedBenchmarks before promotion",
        )

    p10_hold_reasons = {
        item.get("surface"): item
        for item in p10_ratchet.get("promotionHoldReasons") or []
        if isinstance(item, dict) and item.get("surface")
    }
    require_policy(
        set(p10_hold_reasons) == {
            "REST latency except OpenAPI and governance",
            "RPC unary and stream",
            "gateway and cache",
        },
        "p10PerformanceBudgetRatchet promotionHoldReasons mismatch",
    )
    for surface, item in p10_hold_reasons.items():
        for field in ("reason", "nextAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{surface}: P10 promotion hold {field} must be actionable",
            )

    p10_promotion_rules = set(p10_ratchet.get("promotionRules") or [])
    for rule in (
        "minimum 5 baseline samples",
        "minimum 3 current trend samples",
        "no allocation regression under bench-regression-check",
        "latency rows require maxRegressionRatio and rollback action before blocking promotion",
    ):
        require_policy(rule in p10_promotion_rules, f"p10PerformanceBudgetRatchet promotionRules missing {rule!r}")

    require_policy(
        not (set(promoted_latency) & report_only),
        "latencyPolicy promoted benchmarks must not also be report-only",
    )
    for benchmark in report_only:
        require_policy(benchmark in tracked, f"report-only latency benchmark is not tracked: {benchmark}")

    adopter_blocking = {
        item.get("id"): item
        for item in adopter_contract.get("blockingSurfaces") or []
        if isinstance(item, dict) and item.get("id")
    }
    expected_blocking = {
        "rest-route-hot-path": "allocation-blocking",
        "governance-rule-match": "latency-and-allocation-blocking",
    }
    require_policy(
        set(adopter_blocking) == set(expected_blocking),
        f"adopterPerformanceContract blockingSurfaces drifted: {sorted(adopter_blocking)}",
    )
    for surface_id, budget_scope in expected_blocking.items():
        item = adopter_blocking.get(surface_id) or {}
        require_policy(item.get("budgetScope") == budget_scope, f"{surface_id}: budgetScope must be {budget_scope}")
        require_policy(item.get("benchmark") in tracked, f"{surface_id}: benchmark must be tracked")
        for field in ("adopterAction", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{surface_id}: adopterPerformanceContract {field} must be actionable",
            )

    adopter_report_only = {
        item.get("id"): item
        for item in adopter_contract.get("reportOnlySurfaces") or []
        if isinstance(item, dict) and item.get("id")
    }
    require_policy(
        set(adopter_report_only) == {
            "http-latency-report-only",
            "rpc-candidate-report-only",
            "gateway-cache-candidate-report-only",
        },
        "adopterPerformanceContract reportOnlySurfaces mismatch",
    )
    http_report_only = set((adopter_report_only.get("http-latency-report-only") or {}).get("benchmarks") or [])
    require_policy(
        report_only <= http_report_only,
        "adopterPerformanceContract http-latency-report-only must include all latencyPolicy.reportOnly rows",
    )
    rpc_report_only = set((adopter_report_only.get("rpc-candidate-report-only") or {}).get("benchmarks") or [])
    rpc_candidate_names = {
        item.get("benchmark", "")
        for item in rpc_candidates
        if isinstance(item, dict) and item.get("benchmark")
    }
    gateway_cache_candidate_names = {
        "BenchmarkGatewayProxy",
        "BenchmarkCacheHotPath",
        "BenchmarkCacheHotPathGetOrLoadHit",
    }
    candidate_benchmark_names = rpc_candidate_names | gateway_cache_candidate_names
    require_policy(
        rpc_candidate_names <= rpc_report_only,
        "adopterPerformanceContract rpc-candidate-report-only must include all RPC candidates",
    )
    gateway_cache_report_only = set((adopter_report_only.get("gateway-cache-candidate-report-only") or {}).get("benchmarks") or [])
    require_policy(
        gateway_cache_report_only == gateway_cache_candidate_names,
        "adopterPerformanceContract gateway-cache-candidate-report-only mismatch",
    )
    for benchmark in gateway_cache_report_only:
        require_policy(
            benchmark not in tracked,
            f"{benchmark}: adopterPerformanceContract Gateway/Cache candidate must stay out of trackedBenchmarks",
        )
    for surface_id, item in adopter_report_only.items():
        for field in ("adopterAction", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{surface_id}: adopterPerformanceContract {field} must be actionable",
            )

    require_policy(
        not adopter_contract.get("unsupportedSurfaces"),
        "adopterPerformanceContract unsupportedSurfaces must be empty after Gateway/Cache candidate promotion",
    )

    promotion_rules = set(adopter_contract.get("promotionRules") or [])
    for rule in (
        "minimum 5 baseline samples",
        "minimum 3 current trend samples",
        "no allocation regression under bench-regression-check",
    ):
        require_policy(rule in promotion_rules, f"adopterPerformanceContract promotionRules missing {rule!r}")

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

    p12_rpc_decision = ratchet.get("p12RpcBudgetPromotionDecision") or {}
    require_policy(
        p12_rpc_decision.get("schema") == "gofly.benchmark_p12_rpc_budget_promotion_decision.v1",
        "p12RpcBudgetPromotionDecision schema mismatch",
    )
    require_policy(
        p12_rpc_decision.get("aiflowTask") == "GOFLY-P12-1-RPC-BENCHMARK-BUDGET-PROMOTION",
        "p12RpcBudgetPromotionDecision aiflowTask mismatch",
    )
    require_policy(
        p12_rpc_decision.get("status") == "promotion-held-report-only",
        "p12RpcBudgetPromotionDecision status must be promotion-held-report-only",
    )
    require_policy(
        p12_rpc_decision.get("acceptanceGate") == "make bench-regression-check",
        "p12RpcBudgetPromotionDecision acceptanceGate mismatch",
    )
    p12_decision = p12_rpc_decision.get("decision") or {}
    require_policy(p12_decision.get("result") == "hold", "p12RpcBudgetPromotionDecision decision.result must be hold")
    require_policy(p12_decision.get("selectedSurface") == "none", "p12RpcBudgetPromotionDecision selectedSurface must be none")
    require_policy(
        p12_decision.get("nextReviewGate") == "make bench-regression-check && make rpc-boundary-check",
        "p12RpcBudgetPromotionDecision nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p12_decision.get(field) or "").split()) >= 16,
            f"p12RpcBudgetPromotionDecision decision.{field} must be actionable",
        )
    p12_candidates = {
        item.get("benchmark"): item
        for item in p12_rpc_decision.get("candidateRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p12_candidates) == rpc_candidate_names,
        "p12RpcBudgetPromotionDecision candidateRows must match rpcPolicy candidates",
    )
    for benchmark, item in p12_candidates.items():
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P12 currentMode must be report-only")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P12 proposedPromotionMode mismatch")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P12 promotionStatus must be blocked")
        require_policy(benchmark not in tracked, f"{benchmark}: P12 candidate must stay out of trackedBenchmarks")
        require_policy(len(item.get("blockers") or []) >= 3, f"{benchmark}: P12 blockers must include at least three reasons")
        require_policy(
            any(runtime in str(item.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack")),
            f"{benchmark}: P12 rollbackAction must name fallback runtime or previous RPC stack",
        )

    p13_rpc_closeout = ratchet.get("p13RpcTier1ReleaseTrainCloseout") or {}
    require_policy(
        p13_rpc_closeout.get("schema") == "gofly.benchmark_p13_rpc_tier1_release_train_closeout.v1",
        "p13RpcTier1ReleaseTrainCloseout schema mismatch",
    )
    require_policy(
        p13_rpc_closeout.get("aiflowTask") == "GOFLY-P13-01-RPC-TIER1-PROMOTION-CLOSEOUT",
        "p13RpcTier1ReleaseTrainCloseout aiflowTask mismatch",
    )
    require_policy(
        p13_rpc_closeout.get("status") == "no-surface-promoted",
        "p13RpcTier1ReleaseTrainCloseout status must be no-surface-promoted",
    )
    require_policy(
        p13_rpc_closeout.get("acceptanceGate") == "make bench-regression-check",
        "p13RpcTier1ReleaseTrainCloseout acceptanceGate mismatch",
    )
    p13_decision = p13_rpc_closeout.get("decision") or {}
    require_policy(p13_decision.get("result") == "hold", "p13RpcTier1ReleaseTrainCloseout decision.result must be hold")
    require_policy(p13_decision.get("selectedSurface") == "none", "p13RpcTier1ReleaseTrainCloseout selectedSurface must be none")
    require_policy(
        p13_decision.get("allocationBlockingSurface") == "none",
        "p13RpcTier1ReleaseTrainCloseout allocationBlockingSurface must be none",
    )
    require_policy(p13_decision.get("latencyMode") == "report-only", "p13RpcTier1ReleaseTrainCloseout latencyMode must be report-only")
    require_policy(
        p13_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
        "p13RpcTier1ReleaseTrainCloseout nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p13_decision.get(field) or "").split()) >= 16,
            f"p13RpcTier1ReleaseTrainCloseout decision.{field} must be actionable",
        )
    for forbidden_claim in ("Kitex", "gRPC-Go", "blocking RPC latency", "drop-in RPC replacement"):
        require_policy(
            forbidden_claim in str(p13_decision.get("releaseNotePolicy") or ""),
            f"p13RpcTier1ReleaseTrainCloseout releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p13_candidates = {
        item.get("benchmark"): item
        for item in p13_rpc_closeout.get("candidateRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    expected_p13_candidate_names = {
        "BenchmarkRPCUnary/gofly_rpc",
        "BenchmarkRPCServerStreamGovernance",
        "BenchmarkRPCClientStreamGovernance",
        "BenchmarkRPCBidiStreamGovernance",
    }
    require_policy(
        set(p13_candidates) == expected_p13_candidate_names,
        "p13RpcTier1ReleaseTrainCloseout candidateRows mismatch",
    )
    for benchmark, item in p13_candidates.items():
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P13 currentMode must be report-only")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P13 proposedPromotionMode mismatch")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P13 promotionStatus must be blocked")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P13 minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P13 minimumCurrentTrendSamples mismatch")
        require_policy(benchmark not in tracked, f"{benchmark}: P13 candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P13 latency must stay report-only")
        require_policy(len(item.get("blockers") or []) >= 3, f"{benchmark}: P13 blockers must include at least three reasons")
        require_policy(
            any(runtime in str(item.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack")),
            f"{benchmark}: P13 rollbackAction must name fallback runtime or previous RPC stack",
        )
    p13_rules = set(p13_rpc_closeout.get("blockingRules") or [])
    for rule in (
        "exactly one RPC surface may be promoted at a time",
        "promoted RPC rows require minimum 5 baseline samples",
        "promoted RPC rows require minimum 3 current trend samples",
        "promoted RPC rows require no allocation regression under bench-regression-check",
        "RPC latency remains report-only until maxRegressionRatio and two-release trend evidence are documented",
    ):
        require_policy(rule in p13_rules, f"p13RpcTier1ReleaseTrainCloseout blockingRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks RPC entry",
        "blocking RPC latency claim",
        "Kitex transport parity claim",
        "gRPC-Go ecosystem parity claim",
        "drop-in RPC replacement claim",
        "Tier 1 promoted RPC surface",
    ):
        require_policy(
            forbidden in set(p13_rpc_closeout.get("forbiddenUntilCleared") or []),
            f"p13RpcTier1ReleaseTrainCloseout forbiddenUntilCleared missing {forbidden!r}",
        )

    p14_rpc_review = ratchet.get("p14RpcReleaseTrainEvidenceReview") or {}
    require_policy(
        p14_rpc_review.get("schema") == "gofly.benchmark_p14_rpc_release_train_review.v1",
        "p14RpcReleaseTrainEvidenceReview schema mismatch",
    )
    require_policy(
        p14_rpc_review.get("aiflowTask") == "GOFLY-P14-02-RPC-RELEASE-TRAIN-EVIDENCE",
        "p14RpcReleaseTrainEvidenceReview aiflowTask mismatch",
    )
    require_policy(
        p14_rpc_review.get("status") == "hold-no-tracked-rpc-benchmark",
        "p14RpcReleaseTrainEvidenceReview status mismatch",
    )
    require_policy(
        p14_rpc_review.get("acceptanceGate") == "make bench-regression-check",
        "p14RpcReleaseTrainEvidenceReview acceptanceGate mismatch",
    )
    for source in (
        "docs/reference/rpc-tier1-evidence.json",
        "bench/budget-ratchet.json",
        "bench/rpc_bench_test.go",
        "docs/releases/evidence-index.json",
        "docs/reference/ci-required-check-evidence.json",
    ):
        require_policy(
            source in set(p14_rpc_review.get("sourceEvidence") or []),
            f"p14RpcReleaseTrainEvidenceReview sourceEvidence missing {source!r}",
        )
    p14_decision = p14_rpc_review.get("decision") or {}
    require_policy(p14_decision.get("result") == "hold", "p14RpcReleaseTrainEvidenceReview decision.result must be hold")
    require_policy(p14_decision.get("selectedSurface") == "none", "p14RpcReleaseTrainEvidenceReview selectedSurface must be none")
    require_policy(
        p14_decision.get("allocationBlockingSurface") == "none",
        "p14RpcReleaseTrainEvidenceReview allocationBlockingSurface must be none",
    )
    require_policy(p14_decision.get("latencyMode") == "report-only", "p14RpcReleaseTrainEvidenceReview latencyMode must be report-only")
    require_policy(
        p14_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
        "p14RpcReleaseTrainEvidenceReview nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p14_decision.get(field) or "").split()) >= 18,
            f"p14RpcReleaseTrainEvidenceReview decision.{field} must be actionable",
        )
    for forbidden_claim in ("Kitex", "gRPC-Go", "blocking RPC latency", "drop-in RPC replacement", "Tier 1 promoted RPC"):
        require_policy(
            forbidden_claim in str(p14_decision.get("releaseNotePolicy") or ""),
            f"p14RpcReleaseTrainEvidenceReview releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p14_candidates = {
        item.get("benchmark"): item
        for item in p14_rpc_review.get("candidateRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p14_candidates) == expected_p13_candidate_names,
        "p14RpcReleaseTrainEvidenceReview candidateRows mismatch",
    )
    for benchmark, item in p14_candidates.items():
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P14 currentMode must be report-only")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P14 proposedPromotionMode mismatch")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P14 promotionStatus must be blocked")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P14 minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P14 minimumCurrentTrendSamples mismatch")
        require_policy(item.get("releaseTrainStatus") == "not-attached", f"{benchmark}: P14 releaseTrainStatus must be not-attached")
        require_policy(benchmark not in tracked, f"{benchmark}: P14 candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P14 latency must stay report-only")
        require_policy(len(item.get("blockers") or []) >= 3, f"{benchmark}: P14 blockers must include at least three reasons")
        require_policy(
            any(runtime in str(item.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack")),
            f"{benchmark}: P14 rollbackAction must name fallback runtime or previous RPC stack",
        )
    p14_rules = set(p14_rpc_review.get("blockingRules") or [])
    for rule in (
        "P14 must not add RPC rows to trackedBenchmarks until release-train evidence is attached",
        "P14 may promote at most one RPC surface at a time",
        "promoted RPC rows require minimum 5 baseline samples",
        "promoted RPC rows require minimum 3 current trend samples",
        "promoted RPC rows require no allocation regression under bench-regression-check",
        "RPC latency remains report-only until two-release trend evidence is documented",
    ):
        require_policy(rule in p14_rules, f"p14RpcReleaseTrainEvidenceReview blockingRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks RPC entry",
        "blocking RPC latency claim",
        "Kitex transport parity claim",
        "gRPC-Go ecosystem parity claim",
        "drop-in RPC replacement claim",
        "Tier 1 promoted RPC surface",
    ):
        require_policy(
            forbidden in set(p14_rpc_review.get("forbiddenUntilCleared") or []),
            f"p14RpcReleaseTrainEvidenceReview forbiddenUntilCleared missing {forbidden!r}",
        )

    p15_rpc_attachment = ratchet.get("p15RpcReleaseTrainAttachment") or {}
    require_policy(
        p15_rpc_attachment.get("schema") == "gofly.benchmark_p15_rpc_release_train_attachment.v1",
        "p15RpcReleaseTrainAttachment schema mismatch",
    )
    require_policy(
        p15_rpc_attachment.get("aiflowTask") == "GOFLY-P15-02-RPC-RELEASE-TRAIN-ATTACHMENT",
        "p15RpcReleaseTrainAttachment aiflowTask mismatch",
    )
    require_policy(
        p15_rpc_attachment.get("status") == "hold-no-rpc-budget-attachment",
        "p15RpcReleaseTrainAttachment status mismatch",
    )
    require_policy(
        p15_rpc_attachment.get("acceptanceGate") == "make bench-regression-check",
        "p15RpcReleaseTrainAttachment acceptanceGate mismatch",
    )
    for source in (
        "docs/reference/rpc-tier1-evidence.json",
        "bench/budget-ratchet.json",
        "bench/rpc_bench_test.go",
        "docs/releases/evidence-index.json",
        "docs/reference/ci-required-check-evidence.json",
    ):
        require_policy(
            source in set(p15_rpc_attachment.get("sourceEvidence") or []),
            f"p15RpcReleaseTrainAttachment sourceEvidence missing {source!r}",
        )
    p15_budget_decision = p15_rpc_attachment.get("decision") or {}
    require_policy(p15_budget_decision.get("result") == "hold", "p15RpcReleaseTrainAttachment decision.result must be hold")
    require_policy(p15_budget_decision.get("selectedSurface") == "none", "p15RpcReleaseTrainAttachment selectedSurface must be none")
    require_policy(
        p15_budget_decision.get("allocationBlockingSurface") == "none",
        "p15RpcReleaseTrainAttachment allocationBlockingSurface must be none",
    )
    require_policy(p15_budget_decision.get("latencyMode") == "report-only", "p15RpcReleaseTrainAttachment latencyMode must be report-only")
    require_policy(
        p15_budget_decision.get("releaseTrainAttachmentStatus") == "not-attached",
        "p15RpcReleaseTrainAttachment releaseTrainAttachmentStatus must be not-attached",
    )
    require_policy(
        p15_budget_decision.get("nextReviewGate") == "make rpc-boundary-check && make bench-regression-check",
        "p15RpcReleaseTrainAttachment nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p15_budget_decision.get(field) or "").split()) >= 20,
            f"p15RpcReleaseTrainAttachment decision.{field} must be actionable",
        )
    for forbidden_claim in ("Kitex", "gRPC-Go", "blocking RPC latency", "drop-in replacement", "Tier 1 promoted"):
        require_policy(
            forbidden_claim in str(p15_budget_decision.get("releaseNotePolicy") or ""),
            f"p15RpcReleaseTrainAttachment releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p15_candidates = {
        item.get("benchmark"): item
        for item in p15_rpc_attachment.get("candidateRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p15_candidates) == expected_p13_candidate_names,
        "p15RpcReleaseTrainAttachment candidateRows mismatch",
    )
    for benchmark, item in p15_candidates.items():
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P15 currentMode must be report-only")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P15 proposedPromotionMode mismatch")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P15 promotionStatus must be blocked")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P15 minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P15 minimumCurrentTrendSamples mismatch")
        require_policy(item.get("releaseTrainStatus") == "not-attached", f"{benchmark}: P15 releaseTrainStatus must be not-attached")
        require_policy(benchmark not in tracked, f"{benchmark}: P15 candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P15 latency must stay report-only")
        require_policy(len(item.get("blockers") or []) >= 3, f"{benchmark}: P15 blockers must include at least three reasons")
        require_policy(
            any(runtime in str(item.get("rollbackAction") or "") for runtime in ("Kitex", "gRPC-Go", "RPC stack")),
            f"{benchmark}: P15 rollbackAction must name fallback runtime or previous RPC stack",
        )
    p15_rules = set(p15_rpc_attachment.get("blockingRules") or [])
    for rule in (
        "P15 must not add RPC rows to trackedBenchmarks until release-train evidence is attached",
        "P15 may attach at most one allocation-blocking RPC surface per release train",
        "attached RPC rows require minimum 5 baseline samples",
        "attached RPC rows require minimum 3 current trend samples",
        "attached RPC rows require no allocation regression under bench-regression-check",
        "RPC latency remains report-only until two-release trend evidence is documented",
    ):
        require_policy(rule in p15_rules, f"p15RpcReleaseTrainAttachment blockingRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks RPC entry",
        "blocking RPC latency claim",
        "Kitex transport parity claim",
        "gRPC-Go ecosystem parity claim",
        "drop-in RPC replacement claim",
        "Tier 1 promoted RPC surface",
    ):
        require_policy(
            forbidden in set(p15_rpc_attachment.get("forbiddenUntilCleared") or []),
            f"p15RpcReleaseTrainAttachment forbiddenUntilCleared missing {forbidden!r}",
        )

    require_policy(
        p13_gateway_cache_closeout.get("schema") == "gofly.benchmark_p13_gateway_cache_closeout.v1",
        "p13GatewayCacheBenchmarkCloseout schema mismatch",
    )
    require_policy(
        p13_gateway_cache_closeout.get("aiflowTask") == "GOFLY-P13-06-GATEWAY-CACHE-BENCH-EVIDENCE",
        "p13GatewayCacheBenchmarkCloseout aiflowTask mismatch",
    )
    require_policy(
        p13_gateway_cache_closeout.get("status") == "candidate-report-only",
        "p13GatewayCacheBenchmarkCloseout status must be candidate-report-only",
    )
    require_policy(
        p13_gateway_cache_closeout.get("acceptanceGate") == "make bench-regression-check",
        "p13GatewayCacheBenchmarkCloseout acceptanceGate mismatch",
    )
    for source in (
        "bench/gateway_cache_bench_test.go",
        "bench/matrix.md",
        "bench/budget-ratchet.json",
        "bench/README.md",
    ):
        require_policy(
            source in set(p13_gateway_cache_closeout.get("sourceEvidence") or []),
            f"p13GatewayCacheBenchmarkCloseout sourceEvidence missing {source!r}",
        )
    p13_gateway_decision = p13_gateway_cache_closeout.get("decision") or {}
    require_policy(p13_gateway_decision.get("result") == "hold", "p13GatewayCacheBenchmarkCloseout decision.result must be hold")
    require_policy(p13_gateway_decision.get("selectedSurface") == "none", "p13GatewayCacheBenchmarkCloseout selectedSurface must be none")
    require_policy(
        p13_gateway_decision.get("allocationBlockingSurface") == "none",
        "p13GatewayCacheBenchmarkCloseout allocationBlockingSurface must be none",
    )
    require_policy(
        p13_gateway_decision.get("latencyMode") == "report-only",
        "p13GatewayCacheBenchmarkCloseout latencyMode must be report-only",
    )
    require_policy(
        "make bench-regression-check" in str(p13_gateway_decision.get("nextReviewGate") or ""),
        "p13GatewayCacheBenchmarkCloseout nextReviewGate must include make bench-regression-check",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p13_gateway_decision.get(field) or "").split()) >= 16,
            f"p13GatewayCacheBenchmarkCloseout decision.{field} must be actionable",
        )
    for forbidden_claim in ("ratcheted allocation", "blocking latency", "production performance parity"):
        require_policy(
            forbidden_claim in str(p13_gateway_decision.get("releaseNotePolicy") or ""),
            f"p13GatewayCacheBenchmarkCloseout releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p13_gateway_candidates = {
        item.get("benchmark"): item
        for item in p13_gateway_cache_closeout.get("candidateRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p13_gateway_candidates) == gateway_cache_candidate_names,
        "p13GatewayCacheBenchmarkCloseout candidateRows mismatch",
    )
    for benchmark, item in p13_gateway_candidates.items():
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P13 Gateway/Cache currentMode must be report-only")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P13 Gateway/Cache proposedPromotionMode mismatch")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P13 Gateway/Cache promotionStatus must be blocked")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P13 Gateway/Cache minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P13 Gateway/Cache minimumCurrentTrendSamples mismatch")
        require_policy(benchmark not in tracked, f"{benchmark}: P13 Gateway/Cache candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P13 Gateway/Cache latency must stay report-only")
        require_policy(len(item.get("blockers") or []) >= 3, f"{benchmark}: P13 Gateway/Cache blockers must include at least three reasons")
        require_policy(
            len(str(item.get("rollbackAction") or "").split()) >= 12,
            f"{benchmark}: P13 Gateway/Cache rollbackAction must be actionable",
        )
    p13_gateway_rules = set(p13_gateway_cache_closeout.get("blockingRules") or [])
    for rule in (
        "Gateway and Cache candidate rows must stay out of trackedBenchmarks before promotion",
        "promoted Gateway and Cache rows require minimum 5 baseline samples",
        "promoted Gateway and Cache rows require minimum 3 current trend samples",
        "promoted Gateway and Cache rows require no allocation regression under bench-regression-check",
        "Gateway and Cache latency remains report-only until maxRegressionRatio and rollback evidence are documented",
    ):
        require_policy(rule in p13_gateway_rules, f"p13GatewayCacheBenchmarkCloseout blockingRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks Gateway entry",
        "trackedBenchmarks Cache entry",
        "blocking Gateway latency claim",
        "blocking Cache latency claim",
        "ratcheted Gateway performance claim",
        "ratcheted Cache performance claim",
    ):
        require_policy(
            forbidden in set(p13_gateway_cache_closeout.get("forbiddenUntilCleared") or []),
            f"p13GatewayCacheBenchmarkCloseout forbiddenUntilCleared missing {forbidden!r}",
        )

    require_policy(
        p15_gateway_cache_attachment.get("schema") == "gofly.benchmark_p15_gateway_cache_budget_attachment.v1",
        "p15GatewayCacheBudgetAttachment schema mismatch",
    )
    require_policy(
        p15_gateway_cache_attachment.get("aiflowTask") == "GOFLY-P15-03-GATEWAY-CACHE-BUDGET-ATTACHMENT",
        "p15GatewayCacheBudgetAttachment aiflowTask mismatch",
    )
    require_policy(
        p15_gateway_cache_attachment.get("status") == "hold-no-gateway-cache-budget-attachment",
        "p15GatewayCacheBudgetAttachment status mismatch",
    )
    require_policy(
        p15_gateway_cache_attachment.get("acceptanceGate") == "make bench-regression-check",
        "p15GatewayCacheBudgetAttachment acceptanceGate mismatch",
    )
    for source in (
        "bench/gateway_cache_bench_test.go",
        "bench/matrix.md",
        "bench/budget-ratchet.json",
        "bench/README.md",
    ):
        require_policy(
            source in set(p15_gateway_cache_attachment.get("sourceEvidence") or []),
            f"p15GatewayCacheBudgetAttachment sourceEvidence missing {source!r}",
        )
    p15_gateway_decision = p15_gateway_cache_attachment.get("decision") or {}
    require_policy(p15_gateway_decision.get("result") == "hold", "p15GatewayCacheBudgetAttachment decision.result must be hold")
    require_policy(p15_gateway_decision.get("selectedSurface") == "none", "p15GatewayCacheBudgetAttachment selectedSurface must be none")
    require_policy(
        p15_gateway_decision.get("allocationBlockingSurface") == "none",
        "p15GatewayCacheBudgetAttachment allocationBlockingSurface must be none",
    )
    require_policy(p15_gateway_decision.get("latencyMode") == "report-only", "p15GatewayCacheBudgetAttachment latencyMode must be report-only")
    require_policy(
        p15_gateway_decision.get("budgetAttachmentStatus") == "not-attached",
        "p15GatewayCacheBudgetAttachment budgetAttachmentStatus must be not-attached",
    )
    require_policy(
        p15_gateway_decision.get("nextReviewGate")
        == "BENCH_PATTERN='BenchmarkGatewayProxy|BenchmarkCacheHotPath' BENCH_COUNT=5 make bench-stat && make bench-regression-check",
        "p15GatewayCacheBudgetAttachment nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p15_gateway_decision.get(field) or "").split()) >= 20,
            f"p15GatewayCacheBudgetAttachment decision.{field} must be actionable",
        )
    for forbidden_claim in ("ratcheted allocation blocking", "blocking latency", "production performance parity"):
        require_policy(
            forbidden_claim in str(p15_gateway_decision.get("releaseNotePolicy") or ""),
            f"p15GatewayCacheBudgetAttachment releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p15_gateway_candidates = {
        item.get("benchmark"): item
        for item in p15_gateway_cache_attachment.get("candidateRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p15_gateway_candidates) == gateway_cache_candidate_names,
        "p15GatewayCacheBudgetAttachment candidateRows mismatch",
    )
    for benchmark, item in p15_gateway_candidates.items():
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P15 Gateway/Cache currentMode must be report-only")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P15 Gateway/Cache proposedPromotionMode mismatch")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P15 Gateway/Cache promotionStatus must be blocked")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P15 Gateway/Cache minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P15 Gateway/Cache minimumCurrentTrendSamples mismatch")
        require_policy(item.get("budgetAttachment") == "blocked", f"{benchmark}: P15 Gateway/Cache budgetAttachment must be blocked")
        require_policy(benchmark not in tracked, f"{benchmark}: P15 Gateway/Cache candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P15 Gateway/Cache latency must stay report-only")
        require_policy(len(item.get("blockers") or []) >= 3, f"{benchmark}: P15 Gateway/Cache blockers must include at least three reasons")
        require_policy(
            len(str(item.get("rollbackAction") or "").split()) >= 12,
            f"{benchmark}: P15 Gateway/Cache rollbackAction must be actionable",
        )
    p15_gateway_rules = set(p15_gateway_cache_attachment.get("attachmentRules") or [])
    for rule in (
        "Gateway and Cache candidate rows must stay out of trackedBenchmarks before budget attachment",
        "P15 may attach at most one Gateway or Cache allocation-blocking surface per release train",
        "attached Gateway and Cache rows require minimum 5 baseline samples",
        "attached Gateway and Cache rows require minimum 3 current trend samples",
        "attached Gateway and Cache rows require no allocation regression under bench-regression-check",
        "Gateway and Cache latency remains report-only until maxRegressionRatio and rollback evidence are documented",
    ):
        require_policy(rule in p15_gateway_rules, f"p15GatewayCacheBudgetAttachment attachmentRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks Gateway entry",
        "trackedBenchmarks Cache entry",
        "blocking Gateway latency claim",
        "blocking Cache latency claim",
        "ratcheted Gateway performance claim",
        "ratcheted Cache performance claim",
    ):
        require_policy(
            forbidden in set(p15_gateway_cache_attachment.get("forbiddenUntilCleared") or []),
            f"p15GatewayCacheBudgetAttachment forbiddenUntilCleared missing {forbidden!r}",
        )

    require_policy(
        p16_gateway_cache_samples.get("schema") == "gofly.benchmark_p16_gateway_cache_trend_sample_attachment.v1",
        "p16GatewayCacheTrendSampleAttachment schema mismatch",
    )
    require_policy(
        p16_gateway_cache_samples.get("aiflowTask") == "GOFLY-P16-02-GATEWAY-CACHE-TREND-SAMPLE-ATTACHMENT",
        "p16GatewayCacheTrendSampleAttachment aiflowTask mismatch",
    )
    require_policy(
        p16_gateway_cache_samples.get("status") == "current-trend-attached-baseline-held",
        "p16GatewayCacheTrendSampleAttachment status mismatch",
    )
    require_policy(
        p16_gateway_cache_samples.get("acceptanceGate") == "make bench-regression-check",
        "p16GatewayCacheTrendSampleAttachment acceptanceGate mismatch",
    )
    for source in (
        "bench/current.txt",
        "bench/gateway_cache_bench_test.go",
        "bench/budget-ratchet.json",
        "bench/matrix.md",
    ):
        require_policy(
            source in set(p16_gateway_cache_samples.get("sourceEvidence") or []),
            f"p16GatewayCacheTrendSampleAttachment sourceEvidence missing {source!r}",
        )
    p16_capture = p16_gateway_cache_samples.get("capture") or {}
    require_policy(
        p16_capture.get("command")
        == "BENCH_PATTERN='BenchmarkGatewayProxy|BenchmarkCacheHotPath' BENCH_COUNT=5 BENCH_PKGS='./bench/' bash bin/scripts/benchstat.sh",
        "p16GatewayCacheTrendSampleAttachment capture.command mismatch",
    )
    require_policy(p16_capture.get("sampleType") == "current-trend", "p16GatewayCacheTrendSampleAttachment sampleType mismatch")
    require_policy(p16_capture.get("sampleCount") == 5, "p16GatewayCacheTrendSampleAttachment sampleCount must be five")
    require_policy(
        p16_capture.get("baselineStatus") == "missing-gateway-cache-rows",
        "p16GatewayCacheTrendSampleAttachment baselineStatus must hold promotion",
    )
    require_policy(
        "ignored local runtime evidence" in str(p16_capture.get("currentSourcePolicy") or ""),
        "p16GatewayCacheTrendSampleAttachment currentSourcePolicy must keep bench/current.txt uncommitted",
    )
    p16_gateway_decision = p16_gateway_cache_samples.get("decision") or {}
    require_policy(p16_gateway_decision.get("result") == "hold", "p16GatewayCacheTrendSampleAttachment decision.result must be hold")
    require_policy(p16_gateway_decision.get("selectedSurface") == "none", "p16GatewayCacheTrendSampleAttachment selectedSurface must be none")
    require_policy(
        p16_gateway_decision.get("allocationBlockingSurface") == "none",
        "p16GatewayCacheTrendSampleAttachment allocationBlockingSurface must be none",
    )
    require_policy(p16_gateway_decision.get("latencyMode") == "report-only", "p16GatewayCacheTrendSampleAttachment latencyMode must be report-only")
    require_policy(
        p16_gateway_decision.get("currentTrendAttachmentStatus") == "attached",
        "p16GatewayCacheTrendSampleAttachment currentTrendAttachmentStatus must be attached",
    )
    require_policy(
        p16_gateway_decision.get("baselineAttachmentStatus") == "not-attached",
        "p16GatewayCacheTrendSampleAttachment baselineAttachmentStatus must be not-attached",
    )
    require_policy(
        p16_gateway_decision.get("promotionStatus") == "blocked",
        "p16GatewayCacheTrendSampleAttachment promotionStatus must be blocked",
    )
    require_policy(
        p16_gateway_decision.get("nextReviewGate")
        == "BENCH_PATTERN='BenchmarkGatewayProxy|BenchmarkCacheHotPath' BENCH_COUNT=5 make bench-baseline && make bench-regression-check",
        "p16GatewayCacheTrendSampleAttachment nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p16_gateway_decision.get(field) or "").split()) >= 20,
            f"p16GatewayCacheTrendSampleAttachment decision.{field} must be actionable",
        )
    for forbidden_claim in ("allocation-blocking coverage", "blocking latency", "production performance parity"):
        require_policy(
            forbidden_claim in str(p16_gateway_decision.get("releaseNotePolicy") or ""),
            f"p16GatewayCacheTrendSampleAttachment releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p16_gateway_rows = {
        item.get("benchmark"): item
        for item in p16_gateway_cache_samples.get("currentTrendRows") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p16_gateway_rows) == gateway_cache_candidate_names,
        "p16GatewayCacheTrendSampleAttachment currentTrendRows mismatch",
    )
    for benchmark, item in p16_gateway_rows.items():
        require_policy(item.get("sampleCount") == 5, f"{benchmark}: P16 Gateway/Cache sampleCount must be five")
        require_policy(float(item.get("nsPerOpMedian") or 0) > 0, f"{benchmark}: P16 Gateway/Cache nsPerOpMedian must be positive")
        require_policy(float(item.get("nsPerOpMin") or 0) > 0, f"{benchmark}: P16 Gateway/Cache nsPerOpMin must be positive")
        require_policy(float(item.get("nsPerOpMax") or 0) >= float(item.get("nsPerOpMedian") or 0), f"{benchmark}: P16 Gateway/Cache nsPerOpMax must cover median")
        require_policy(float(item.get("bytesPerOpMedian") or 0) >= 0, f"{benchmark}: P16 Gateway/Cache bytesPerOpMedian must be non-negative")
        require_policy(float(item.get("allocsPerOpMedian") or 0) >= 0, f"{benchmark}: P16 Gateway/Cache allocsPerOpMedian must be non-negative")
        require_policy(item.get("currentMode") == "report-only", f"{benchmark}: P16 Gateway/Cache currentMode must be report-only")
        require_policy(item.get("promotionStatus") == "blocked", f"{benchmark}: P16 Gateway/Cache promotionStatus must be blocked")
        require_policy(item.get("baselineSamplesAttached") == 0, f"{benchmark}: P16 Gateway/Cache baselineSamplesAttached must remain zero")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P16 Gateway/Cache minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P16 Gateway/Cache minimumCurrentTrendSamples mismatch")
        require_policy(benchmark not in tracked, f"{benchmark}: P16 Gateway/Cache candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P16 Gateway/Cache latency must stay report-only")
        for field in ("cacheModeNote", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{benchmark}: P16 Gateway/Cache {field} must be actionable",
            )
    p16_rules = set(p16_gateway_cache_samples.get("attachmentRules") or [])
    for rule in (
        "P16 current trend attachment must include exactly five samples for each Gateway and Cache candidate row",
        "P16 current trend attachment must not add Gateway or Cache rows to trackedBenchmarks",
        "P16 current trend attachment must keep Gateway and Cache latency report-only",
        "P16 promotion remains blocked until committed baseline rows contain at least five samples per candidate row",
        "P16 promotion remains blocked until cache-mode notes and rollback evidence are attached with the allocation budget",
    ):
        require_policy(rule in p16_rules, f"p16GatewayCacheTrendSampleAttachment attachmentRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks Gateway entry",
        "trackedBenchmarks Cache entry",
        "blocking Gateway latency claim",
        "blocking Cache latency claim",
        "allocation-blocking Gateway claim",
        "allocation-blocking Cache claim",
        "production Gateway performance parity claim",
        "production Cache performance parity claim",
    ):
        require_policy(
            forbidden in set(p16_gateway_cache_samples.get("forbiddenUntilCleared") or []),
            f"p16GatewayCacheTrendSampleAttachment forbiddenUntilCleared missing {forbidden!r}",
        )

    require_policy(
        p16_gateway_cache_promotion_review.get("schema")
        == "gofly.benchmark_p16_gateway_cache_allocation_promotion_review.v1",
        "p16GatewayCacheAllocationPromotionReview schema mismatch",
    )
    require_policy(
        p16_gateway_cache_promotion_review.get("aiflowTask")
        == "GOFLY-P16-03-GATEWAY-CACHE-ALLOCATION-PROMOTION-REVIEW",
        "p16GatewayCacheAllocationPromotionReview aiflowTask mismatch",
    )
    require_policy(
        p16_gateway_cache_promotion_review.get("status") == "promotion-held-baseline-missing",
        "p16GatewayCacheAllocationPromotionReview status mismatch",
    )
    require_policy(
        p16_gateway_cache_promotion_review.get("acceptanceGate") == "make bench-regression-check",
        "p16GatewayCacheAllocationPromotionReview acceptanceGate mismatch",
    )
    for source in (
        "bench/budget-ratchet.json",
        "bench/gateway_cache_bench_test.go",
        "bench/matrix.md",
        "bench/current.txt",
        "docs/reference/governance-p16-roadmap.json",
    ):
        require_policy(
            source in set(p16_gateway_cache_promotion_review.get("sourceEvidence") or []),
            f"p16GatewayCacheAllocationPromotionReview sourceEvidence missing {source!r}",
        )
    p16_snapshot = p16_gateway_cache_promotion_review.get("prerequisiteSnapshot") or {}
    require_policy(
        p16_snapshot.get("currentTrendAttachment") == "attached",
        "p16GatewayCacheAllocationPromotionReview currentTrendAttachment mismatch",
    )
    require_policy(
        p16_snapshot.get("baselineStatus") == "missing-gateway-cache-rows",
        "p16GatewayCacheAllocationPromotionReview baselineStatus mismatch",
    )
    require_policy(
        p16_snapshot.get("baselineSamplesAttached") == 0,
        "p16GatewayCacheAllocationPromotionReview baselineSamplesAttached must remain zero",
    )
    require_policy(
        p16_snapshot.get("minimumBaselineSamples") == 5,
        "p16GatewayCacheAllocationPromotionReview minimumBaselineSamples mismatch",
    )
    require_policy(
        p16_snapshot.get("minimumCurrentTrendSamples") == 3,
        "p16GatewayCacheAllocationPromotionReview minimumCurrentTrendSamples mismatch",
    )
    require_policy(
        p16_snapshot.get("trackedBenchmarkPromotion") == "not-promoted",
        "p16GatewayCacheAllocationPromotionReview trackedBenchmarkPromotion mismatch",
    )
    require_policy(
        p16_snapshot.get("latencyMode") == "report-only",
        "p16GatewayCacheAllocationPromotionReview latencyMode mismatch",
    )
    p16_review_decision = p16_gateway_cache_promotion_review.get("decision") or {}
    require_policy(
        p16_review_decision.get("result") == "hold",
        "p16GatewayCacheAllocationPromotionReview decision.result must be hold",
    )
    require_policy(
        p16_review_decision.get("selectedSurface") == "none",
        "p16GatewayCacheAllocationPromotionReview selectedSurface must be none",
    )
    require_policy(
        p16_review_decision.get("allocationBlockingSurface") == "none",
        "p16GatewayCacheAllocationPromotionReview allocationBlockingSurface must be none",
    )
    require_policy(
        p16_review_decision.get("promotionClass") == "none",
        "p16GatewayCacheAllocationPromotionReview promotionClass must be none",
    )
    require_policy(
        p16_review_decision.get("promotionStatus") == "blocked",
        "p16GatewayCacheAllocationPromotionReview promotionStatus must be blocked",
    )
    require_policy(
        p16_review_decision.get("latencyMode") == "report-only",
        "p16GatewayCacheAllocationPromotionReview decision.latencyMode must be report-only",
    )
    require_policy(
        p16_review_decision.get("nextReviewGate")
        == "BENCH_PATTERN='BenchmarkGatewayProxy|BenchmarkCacheHotPath' BENCH_COUNT=5 make bench-baseline && make bench-regression-check",
        "p16GatewayCacheAllocationPromotionReview nextReviewGate mismatch",
    )
    for field in ("reason", "releaseNotePolicy"):
        require_policy(
            len(str(p16_review_decision.get(field) or "").split()) >= 20,
            f"p16GatewayCacheAllocationPromotionReview decision.{field} must be actionable",
        )
    for forbidden_claim in ("allocation-blocking coverage", "blocking latency", "production performance parity"):
        require_policy(
            forbidden_claim in str(p16_review_decision.get("releaseNotePolicy") or ""),
            f"p16GatewayCacheAllocationPromotionReview releaseNotePolicy must mention {forbidden_claim!r}",
        )
    p16_review_rows = {
        item.get("benchmark"): item
        for item in p16_gateway_cache_promotion_review.get("reviewedSurfaces") or []
        if isinstance(item, dict) and item.get("benchmark")
    }
    require_policy(
        set(p16_review_rows) == gateway_cache_candidate_names,
        "p16GatewayCacheAllocationPromotionReview reviewedSurfaces mismatch",
    )
    for benchmark, item in p16_review_rows.items():
        require_policy(item.get("currentTrendSampleCount") == 5, f"{benchmark}: P16 promotion review currentTrendSampleCount must be five")
        require_policy(item.get("baselineSamplesAttached") == 0, f"{benchmark}: P16 promotion review baselineSamplesAttached must remain zero")
        require_policy(item.get("minimumBaselineSamples") == 5, f"{benchmark}: P16 promotion review minimumBaselineSamples mismatch")
        require_policy(item.get("minimumCurrentTrendSamples") == 3, f"{benchmark}: P16 promotion review minimumCurrentTrendSamples mismatch")
        require_policy(item.get("proposedPromotionMode") == "allocation-blocking", f"{benchmark}: P16 promotion review proposedPromotionMode mismatch")
        require_policy(item.get("promotionDecision") == "hold", f"{benchmark}: P16 promotion review promotionDecision must be hold")
        require_policy(item.get("latencyMode") == "report-only", f"{benchmark}: P16 promotion review latencyMode must be report-only")
        require_policy(benchmark not in tracked, f"{benchmark}: P16 promotion review candidate must stay out of trackedBenchmarks")
        require_policy(benchmark not in promoted_latency, f"{benchmark}: P16 promotion review latency must stay report-only")
        blockers = item.get("blockers") or []
        require_policy(len(blockers) >= 3, f"{benchmark}: P16 promotion review blockers must explain the hold")
        require_policy(
            any("baseline" in str(blocker).lower() for blocker in blockers),
            f"{benchmark}: P16 promotion review blockers must mention baseline",
        )
        for field in ("cacheModeNote", "rollbackGuardrail"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"{benchmark}: P16 promotion review {field} must be actionable",
            )
    p16_review_rules = set(p16_gateway_cache_promotion_review.get("blockingRules") or [])
    for rule in (
        "P16 allocation promotion review must not add Gateway or Cache rows to trackedBenchmarks",
        "P16 allocation promotion review must keep Gateway and Cache latency report-only",
        "P16 allocation promotion review may promote at most one Gateway or Cache surface per release train",
        "P16 allocation promotion review requires committed five-sample baseline rows before allocation blocking",
        "P16 allocation promotion review requires rollback guardrails and cache-mode notes before promotion",
        "P16 allocation promotion review requires bench-regression-check before any trackedBenchmarks update",
    ):
        require_policy(rule in p16_review_rules, f"p16GatewayCacheAllocationPromotionReview blockingRules missing {rule!r}")
    for forbidden in (
        "trackedBenchmarks Gateway entry",
        "trackedBenchmarks Cache entry",
        "blocking Gateway latency claim",
        "blocking Cache latency claim",
        "allocation-blocking Gateway claim",
        "allocation-blocking Cache claim",
        "production Gateway performance parity claim",
        "production Cache performance parity claim",
    ):
        require_policy(
            forbidden in set(p16_gateway_cache_promotion_review.get("forbiddenUntilCleared") or []),
            f"p16GatewayCacheAllocationPromotionReview forbiddenUntilCleared missing {forbidden!r}",
        )
    p12_rules = set(p12_rpc_decision.get("blockingRules") or [])
    for rule in (
        "exactly one RPC surface may be promoted at a time",
        "promoted RPC rows must enter trackedBenchmarks only through bench-regression-check",
        "promoted RPC rows require minimum 5 baseline samples",
        "promoted RPC rows require minimum 3 current trend samples",
        "promoted RPC rows require no allocation regression under bench-regression-check",
    ):
        require_policy(rule in p12_rules, f"p12RpcBudgetPromotionDecision blockingRules missing {rule!r}")
    p12_forbidden = set(p12_rpc_decision.get("forbiddenUntilCleared") or [])
    for forbidden in (
        "trackedBenchmarks RPC entry",
        "blocking RPC latency claim",
        "Kitex transport parity claim",
        "gRPC-Go ecosystem parity claim",
        "release note Tier 1 replacement claim",
    ):
        require_policy(forbidden in p12_forbidden, f"p12RpcBudgetPromotionDecision forbiddenUntilCleared missing {forbidden!r}")

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
            require_policy(
                benchmark in candidate_benchmark_names,
                f"{surface_id}: candidate benchmark must be a known RPC, Gateway, or Cache candidate",
            )
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

    expected_r8_surfaces = {
        "rest-router": {
            "benchmark": "BenchmarkHTTPHello/gofly",
            "status": "allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "report-only",
        },
        "path-params": {
            "benchmark": "BenchmarkHTTPPathParams/gofly",
            "status": "allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "report-only",
        },
        "json-binding": {
            "benchmark": "BenchmarkHTTPJSONBinding/gofly",
            "status": "allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "report-only",
        },
        "middleware-chain": {
            "benchmark": "BenchmarkHTTPMiddlewareChain/gofly",
            "status": "allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "report-only",
        },
        "openapi-disabled": {
            "benchmark": "BenchmarkHTTPOpenAPI/disabled",
            "status": "latency-and-allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "blocking",
        },
        "openapi-enabled": {
            "benchmark": "BenchmarkHTTPOpenAPI/enabled",
            "status": "latency-and-allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "blocking",
        },
        "governance-disabled": {
            "benchmark": "BenchmarkHTTPGovernance/disabled",
            "status": "latency-and-allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "blocking",
        },
        "governance-enabled": {
            "benchmark": "BenchmarkHTTPGovernance/enabled",
            "status": "latency-and-allocation-blocking",
            "allocationMode": "blocking",
            "latencyMode": "blocking",
        },
        "rpc-unary": {
            "benchmark": "BenchmarkRPCUnary/gofly_rpc",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
        "rpc-stream-aggregate": {
            "benchmark": "BenchmarkRPCStreamGovernance",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
        "rpc-server-stream": {
            "benchmark": "BenchmarkRPCServerStreamGovernance",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
        "rpc-client-stream": {
            "benchmark": "BenchmarkRPCClientStreamGovernance",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
        "rpc-bidi-stream": {
            "benchmark": "BenchmarkRPCBidiStreamGovernance",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
        "gateway-proxy": {
            "benchmark": "BenchmarkGatewayProxy",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
        "cache-hot-path": {
            "benchmark": "BenchmarkCacheHotPath",
            "status": "candidate",
            "allocationMode": "report-only",
            "latencyMode": "report-only",
        },
    }
    r8_by_id = {
        item.get("id"): item
        for item in r8_surfaces
        if item.get("id")
    }
    require_policy(
        set(r8_depth.get("requiredSurfaces") or []) == set(expected_r8_surfaces),
        "r8PerformanceDepthMatrix requiredSurfaces mismatch",
    )
    require_policy(
        set(r8_by_id) == set(expected_r8_surfaces),
        f"r8PerformanceDepthMatrix surfaces drifted: {sorted(r8_by_id)}",
    )
    promoted_latency_names = set(promoted_latency)
    for surface_id, expected in expected_r8_surfaces.items():
        item = r8_by_id.get(surface_id) or {}
        for field in (
            "id",
            "area",
            "status",
            "allocationMode",
            "latencyMode",
            "promotionGate",
            "evidenceState",
            "rollbackAction",
        ):
            require_policy(bool(item.get(field)), f"r8PerformanceDepthMatrix {surface_id}: {field} is required")
        for field in ("benchmark", "status", "allocationMode", "latencyMode"):
            require_policy(
                item.get(field, "") == expected[field],
                f"r8PerformanceDepthMatrix {surface_id}: {field} mismatch",
            )
        require_policy(
            item.get("promotionGate") == "make bench-regression-check",
            f"r8PerformanceDepthMatrix {surface_id}: promotionGate must be make bench-regression-check",
        )
        benchmark = item.get("benchmark", "")
        status = item.get("status", "")
        if status in {"allocation-blocking", "latency-and-allocation-blocking"}:
            require_policy(benchmark in tracked, f"r8PerformanceDepthMatrix {surface_id}: blocking benchmark must be tracked")
            if item.get("latencyMode") == "blocking":
                require_policy(
                    benchmark in promoted_latency_names,
                    f"r8PerformanceDepthMatrix {surface_id}: blocking latency benchmark must be promoted",
                )
            else:
                require_policy(
                    benchmark in report_only,
                    f"r8PerformanceDepthMatrix {surface_id}: report-only latency benchmark must be in latencyPolicy.reportOnly",
                )
        elif status == "candidate":
            require_policy(
                benchmark in candidate_benchmark_names,
                f"r8PerformanceDepthMatrix {surface_id}: candidate benchmark must be in RPC or Gateway/Cache candidates",
            )
            require_policy(
                benchmark not in tracked,
                f"r8PerformanceDepthMatrix {surface_id}: candidate benchmark must stay out of trackedBenchmarks",
            )
        elif status == "unsupported-report-only":
            require_policy(
                not benchmark,
                f"r8PerformanceDepthMatrix {surface_id}: unsupported surface must not name a benchmark",
            )
        else:
            require_policy(False, f"r8PerformanceDepthMatrix {surface_id}: unknown status {status!r}")
        for field in ("evidenceState", "rollbackAction"):
            require_policy(
                len(str(item.get(field) or "").split()) >= 10,
                f"r8PerformanceDepthMatrix {surface_id}: {field} must be actionable",
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
            "performancePromotionEvidence": ratchet.get("performancePromotionEvidence", {}),
            "p10PerformanceBudgetRatchet": ratchet.get("p10PerformanceBudgetRatchet", {}),
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
        "performancePromotionEvidence": ratchet.get("performancePromotionEvidence", {}),
        "p10PerformanceBudgetRatchet": ratchet.get("p10PerformanceBudgetRatchet", {}),
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
		echo "| Gateway proxy | BenchmarkGatewayProxy | gofly gateway HTTP proxy | BENCH_PATTERN=BenchmarkGatewayProxy make bench-stat | Dedicated gateway proxy candidate evidence, report-only until promoted |"
		echo "| Cache hot path | BenchmarkCacheHotPath, BenchmarkCacheHotPathGetOrLoadHit | gofly cache hit path | BENCH_PATTERN=BenchmarkCacheHotPath make bench-stat | Dedicated cache hot-path candidate evidence, report-only until promoted |"
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
