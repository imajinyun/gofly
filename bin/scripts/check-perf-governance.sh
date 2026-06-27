#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/performance-governance.md"): [
        "gofly.performance_governance.v1",
        "REST router",
        "path params",
        "JSON binding",
        "middleware",
        "OpenAPI",
        "governance overhead",
        "pprof",
        "allocation",
        "regression budget",
        "make bench-trend",
        "make bench-regression-check",
        "gofly.benchmark_regression_report.v1",
    ],
    pathlib.Path("bench/matrix.md"): [
        "BenchmarkHTTPHello",
        "BenchmarkHTTPPathParams",
        "BenchmarkHTTPJSONBinding",
        "BenchmarkHTTPMiddlewareChain",
        "BenchmarkHTTPOpenAPI",
        "BenchmarkHTTPGovernance",
        "allocations",
    ],
    pathlib.Path("bin/scripts/benchstat.sh"): [
        "gofly.benchmark_regression_report.v1",
        "--regression-check",
        "validate_ratchet_policy",
        "allocationPolicy.blocking must be true",
        "latencyPolicy.defaultMode must remain report-only",
        "RPC candidate must not enter trackedBenchmarks before promotion",
        "surfacePolicy missing required surface",
        "BenchmarkHTTPHello",
        "BenchmarkHTTPPathParams",
        "BenchmarkHTTPJSONBinding",
        "BenchmarkHTTPMiddlewareChain",
        "BenchmarkHTTPOpenAPI",
        "BenchmarkHTTPGovernance",
    ],
    pathlib.Path("bench/budget-ratchet.json"): [
        "gofly.benchmark_budget_ratchet.v1",
        "allocs/op median",
        "report-only",
        "minimumBaselineSamples",
        "requiredBlockingGate",
        "rollbackGuidance",
        "currentBlocker",
        "surfacePolicy",
        "unsupported-report-only",
        "gateway-proxy",
        "cache-hot-path",
    ],
}

missing = []
for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("performance governance check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("performance governance ok")
PY
