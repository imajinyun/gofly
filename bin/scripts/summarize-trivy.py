#!/usr/bin/env python3
"""Report all vulnerability severities from full Trivy JSON without hiding unfixed findings."""

import argparse
import json
import sys
from pathlib import Path


SEVERITIES = ("UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    counts = dict.fromkeys(SEVERITIES, 0)
    fixed_counts = dict.fromkeys(SEVERITIES, 0)
    try:
        scan = json.loads(args.input.read_text(encoding="utf-8"))
        if scan.get("SchemaVersion") != 2 or not isinstance(scan.get("Results"), list):
            raise ValueError("expected Trivy SchemaVersion 2 and a Results array")
        for result in scan["Results"]:
            vulnerabilities = result.get("Vulnerabilities")
            if vulnerabilities is None:
                continue
            if not isinstance(vulnerabilities, list):
                raise ValueError("Vulnerabilities must be an array or null")
            for vulnerability in vulnerabilities:
                severity = vulnerability.get("Severity")
                if severity not in counts:
                    raise ValueError(f"unrecognized vulnerability severity: {severity!r}")
                counts[severity] += 1
                if str(vulnerability.get("FixedVersion") or "").strip():
                    fixed_counts[severity] += 1
    except (OSError, ValueError, TypeError, AttributeError) as error:
        print(f"Trivy summary failed: {error}", file=sys.stderr)
        return 1

    blocking = fixed_counts["HIGH"] + fixed_counts["CRITICAL"]
    report = {
        "schema": "gofly.trivy_severity_summary.v1", "source": str(args.input),
        "imageRef": scan.get("ArtifactName", ""), "counts": counts,
        "fixedCounts": fixed_counts, "total": sum(counts.values()),
        "blockingCount": blocking,
        "policy": {"severities": ["HIGH", "CRITICAL"], "ignoreUnfixed": True},
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print("## Trivy vulnerability evidence\n")
    print("Counts include every package finding in the full scan, including unfixed findings.\n")
    print("| Severity | All findings | With a fix |")
    print("| --- | ---: | ---: |")
    for severity in SEVERITIES:
        print(f"| {severity} | {counts[severity]} | {fixed_counts[severity]} |")
    print(f"\nFixed HIGH/CRITICAL findings: **{blocking}**. The separate enforcing scan determines the gate result.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
