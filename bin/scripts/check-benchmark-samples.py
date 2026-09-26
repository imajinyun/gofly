#!/usr/bin/env python3
"""Reject incomplete raw benchmark inputs before budget or benchstat comparison."""

import argparse
from collections import Counter
import json
import re
import sys
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--budget", type=Path, required=True)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--current", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--minimum-samples", type=int, default=1)
    args = parser.parse_args()
    if args.minimum_samples < 1:
        parser.error("minimum samples must be positive")

    failures, rows = [], []
    try:
        budget = json.loads(args.budget.read_text(encoding="utf-8"))
        tracked = budget.get("trackedBenchmarks")
        if not isinstance(tracked, list) or not tracked or not all(
            isinstance(name, str) and name.startswith("Benchmark") for name in tracked
        ):
            raise ValueError("budget must declare nonempty trackedBenchmarks")
        blocking = set(tracked)
        for row in budget.get("latencyPolicy", {}).get("promoted", []):
            if row.get("mode") == "blocking":
                blocking.add(row["benchmark"])
        for row in budget.get("performancePromotionEvidence", {}).get("promotedAllocationBudgets", []):
            if row.get("mode") == "blocking":
                blocking.add(row["benchmark"])

        # Keep the accepted units/number format aligned with benchstat.sh's regression parser.
        number = r"(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)"
        sample = re.compile(
            rf"^(Benchmark\S+)-\d+\s+[1-9]\d*\s+{number}\s+ns/op"
            rf"\s+{number}\s+B/op\s+{number}\s+allocs/op$"
        )
        for label, path in (("baseline", args.baseline), ("current", args.current)):
            counts = Counter()
            for line in path.read_text(encoding="utf-8").splitlines():
                match = sample.fullmatch(line.strip())
                if match:
                    counts[match[1]] += 1
            for name in sorted(blocking):
                count = counts[name]
                rows.append({"input": label, "benchmark": name, "samples": count})
                if count == 0:
                    failures.append(f"{label} missing {name}: no valid samples")
                elif count < args.minimum_samples:
                    failures.append(
                        f"{label} {name}: {count} samples; requires {args.minimum_samples}"
                    )
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        failures.append(str(error))

    report = {
        "schema": "gofly.benchmark_sample_check.v1",
        "status": "failed" if failures else "passed",
        "budget": str(args.budget), "baselineFile": str(args.baseline),
        "currentFile": str(args.current), "minimumSamples": args.minimum_samples,
        "checks": rows, "failures": failures,
    }
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    for failure in failures:
        print(failure, file=sys.stderr)
    print(f"Benchmark samples {report['status']}; evidence: {args.report}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
