#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
out_dir="${GOVERNANCE_REPORT_DIR:-$root/.tmp-test/governance-report}"
json_out="${GOVERNANCE_REPORT_JSON:-$out_dir/governance-report.json}"
md_out="${GOVERNANCE_REPORT_MD:-$out_dir/governance-report.md}"
check="${GOVERNANCE_REPORT_CHECK:-false}"

mkdir -p "$(dirname -- "$json_out")" "$(dirname -- "$md_out")"

python3 - "$root" "$json_out" "$md_out" "$check" <<'PY'
import json
import os
import pathlib
import re
import subprocess
import sys
from datetime import datetime, timezone

root = pathlib.Path(sys.argv[1])
json_out = pathlib.Path(sys.argv[2])
md_out = pathlib.Path(sys.argv[3])
check_mode = sys.argv[4].lower() == "true"


def read_text(path):
    try:
        return path.read_text(encoding="utf-8")
    except FileNotFoundError:
        return ""


def read_json(path):
    text = read_text(path)
    if not text:
        return None
    return json.loads(text)


def git_value(*args):
    try:
        return subprocess.check_output(
            ["git", *args],
            cwd=root,
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
    except Exception:
        return "unknown"


def make_var(name, default=""):
    makefile = read_text(root / "Makefile")
    pattern = re.compile(rf"^{re.escape(name)}\s*\?=\s*(.+)$", re.MULTILINE)
    match = pattern.search(makefile)
    return match.group(1).strip() if match else default


def status(path):
    return "present" if (root / path).is_file() else "missing"


def extract_tiers():
    text = read_text(root / "docs/reference/api-surface.md")
    tiers = []
    for line in text.splitlines():
        if not line.startswith("| Tier "):
            continue
        cells = [cell.strip() for cell in line.strip("|").split("|")]
        if len(cells) < 4 or cells[0] == "Tier":
            continue
        tiers.append({
            "tier": cells[0],
            "surface": cells[1],
            "compatibility": cells[3],
        })
    return tiers


def count_benchmarks():
    text = read_text(root / "bench/evidence.md")
    names = set()
    for match in re.finditer(r"^(Benchmark[A-Za-z0-9_]+)/(?:gofly|net_http|gin|echo|chi|fiber|hertz)", text, re.MULTILINE):
        names.add(match.group(1))
    return sorted(names)


def aiflow_queue():
    data = read_json(root / ".harness/store.json")
    if not isinstance(data, dict):
        return {"status": "unavailable", "reason": ".harness/store.json is not present"}
    runs = data.get("runs") or data.get("Runs")
    if not isinstance(runs, dict):
        runs = data
    statuses = {}
    current = []
    for run_id, run in runs.items():
        if not isinstance(run, dict) or not run_id.startswith("GOFLY-GAP-"):
            continue
        status_value = run.get("Status", "unknown")
        statuses[status_value] = statuses.get(status_value, 0) + 1
        if status_value in {"queued", "running"}:
            current.append({
                "id": run_id,
                "status": status_value,
                "priority": run.get("Priority", 0),
                "phase": run.get("Phase", ""),
            })
    current.sort(key=lambda item: (-int(item.get("priority") or 0), item["id"]))
    return {
        "status": "present",
        "summary": statuses,
        "active": current[:10],
    }


def release_evidence():
    manifest = read_json(root / "docs/releases/evidence-manifest.json") or {}
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/releases/evidence-manifest.json",
        "requiredCommand": (manifest.get("tag_governance") or {}).get("required_command", ""),
        "forbiddenSkips": (manifest.get("tag_governance") or {}).get("forbidden_skips", []),
        "artifacts": {
            "archives": bool(manifest.get("archives")),
            "checksums": bool(manifest.get("checksums")),
            "sbom": bool(manifest.get("sbom")),
            "dockerDigest": bool(manifest.get("docker_digest")),
            "provenanceAttestation": bool(manifest.get("provenance_attestation")),
        },
    }


def security_evidence():
    baseline = read_json(root / "bin/scripts/gosec-exception-baseline.json") or {}
    return {
        "gosec": {
            "blockingGate": "make gosec",
            "baseline": "bin/scripts/gosec-exception-baseline.json",
            "baselineSchema": baseline.get("schema", ""),
            "allowedExceptions": len(baseline.get("allowed_exceptions") or []),
        },
        "govulncheck": {
            "blockingGate": "make govulncheck",
            "scanMode": make_var("GOVULNCHECK_SCAN", "package"),
        },
    }


def coverage_evidence():
    manifest = read_json(root / "docs/reference/coverage-trend.json") or {}
    policy = manifest.get("ratchetPolicy") or {}
    return {
        "gate": "make cover-check",
        "trendGate": "make coverage-trend-check",
        "threshold": make_var("COVERAGE_THRESHOLD", "60"),
        "ratchet": make_var("COVERAGE_RATCHET", "90"),
        "manifest": "docs/reference/coverage-trend.json",
        "schema": manifest.get("schema", ""),
        "policy": {
            "blockingGate": policy.get("blockingGate", ""),
            "trendGate": policy.get("trendGate", ""),
            "volatileArtifacts": policy.get("volatileArtifacts", []),
        },
        "evidenceCount": len(manifest.get("evidence") or []),
    }


def ci_required_check_evidence():
    manifest = read_json(root / "docs/reference/ci-required-check-evidence.json") or {}
    checks = manifest.get("checks") or []
    artifacts = sorted({
        item.get("artifact", "")
        for item in checks
        if isinstance(item, dict) and item.get("artifact")
    })
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/ci-required-check-evidence.json",
        "gate": "make ci-required-check-evidence-check",
        "driftGate": "make required-checks-drift-check",
        "checkCount": len(checks),
        "releasePrerequisiteCount": len(manifest.get("releasePrerequisites") or []),
        "artifacts": artifacts,
    }


def runtime_slo_evidence():
    manifest = read_json(root / "docs/reference/runtime-slo.json") or {}
    signals = manifest.get("goldenSignals") or []
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/runtime-slo.json",
        "gate": "make runtime-slo-check",
        "exampleGate": (manifest.get("verification") or {}).get("observabilityExample", ""),
        "productionGate": (manifest.get("verification") or {}).get("productionGate", ""),
        "signals": [
            item.get("id", "")
            for item in signals
            if isinstance(item, dict) and item.get("id")
        ],
        "signalCount": len(signals),
    }


def generated_upgrade_dry_run_evidence():
    manifest = read_json(root / "docs/reference/generated-upgrade-dry-run.json") or {}
    profiles = manifest.get("profiles") or []
    categories = (manifest.get("diffReportContract") or {}).get("categories") or []
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/generated-upgrade-dry-run.json",
        "docs": "docs/reference/generated-upgrade-dry-run.md",
        "gate": "make generated-upgrade-dry-run-check",
        "compatibilityGate": "make generated-version-compat-check",
        "profileCount": len(profiles),
        "profiles": [
            item.get("profile", "")
            for item in profiles
            if isinstance(item, dict) and item.get("profile")
        ],
        "diffCategoryCount": len(categories),
        "diffCategories": [
            item.get("category", "")
            for item in categories
            if isinstance(item, dict) and item.get("category")
        ],
        "rollbackNoteCount": sum(
            1
            for item in profiles
            if isinstance(item, dict) and (item.get("diffReport") or {}).get("rollbackNote")
        ),
    }


def docs_evidence():
    required = [
        "docs/reference/api-surface.md",
        "docs/reference/performance-governance.md",
        "docs/reference/coverage-trend.md",
        "docs/reference/ci-required-check-evidence.md",
        "docs/reference/runtime-slo.md",
        "docs/reference/cli-json-contracts.md",
        "docs/reference/generated-version-compat.md",
        "docs/reference/generated-upgrade-dry-run.md",
        "docs/releases/evidence-manifest.json",
        "docs/operations/troubleshooting.md",
    ]
    return [{"path": path, "status": status(path)} for path in required]


report = {
    "schema": "gofly.governance_report.v1",
    "generatedAt": datetime.now(timezone.utc).isoformat(),
    "git": {
        "commit": git_value("rev-parse", "--short", "HEAD"),
        "branch": git_value("rev-parse", "--abbrev-ref", "HEAD"),
        "dirty": git_value("status", "--porcelain") != "",
    },
    "apiSurface": {
        "source": "docs/reference/api-surface.md",
        "gate": "make stable-surface-check",
        "tiers": extract_tiers(),
    },
    "coverage": coverage_evidence(),
    "coverageTrend": coverage_evidence(),
    "ciRequiredChecks": ci_required_check_evidence(),
    "runtimeSLO": runtime_slo_evidence(),
    "generatedUpgradeDryRun": generated_upgrade_dry_run_evidence(),
    "benchmark": {
        "gate": "make bench-evidence-check",
        "trendGate": "make bench-trend",
        "regressionGate": "make bench-regression-check",
        "evidence": "bench/evidence.md",
        "regressionReport": "bench/regression-report.json",
        "benchmarks": count_benchmarks(),
        "evidenceStatus": status("bench/evidence.md"),
    },
    "security": security_evidence(),
    "release": release_evidence(),
    "aiflow": aiflow_queue(),
    "docs": {
        "gate": "make docs-check",
        "evidence": docs_evidence(),
    },
    "gates": [
        "make governance-report-check",
        "make stable-surface-check",
        "make generated-version-compat-check",
        "make generated-upgrade-dry-run-check",
        "make bench-evidence-check",
        "make coverage-trend-check",
        "make ci-required-check-evidence-check",
        "make runtime-slo-check",
        "make cover-check",
        "make govulncheck",
        "make gosec",
        "make release-snapshot",
    ],
}

json_out.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")

md_lines = [
    "# Governance Report",
    "",
    f"Schema: `{report['schema']}`",
    "",
    "## Summary",
    "",
    f"- Commit: `{report['git']['commit']}` on `{report['git']['branch']}`",
    f"- Dirty worktree when generated: `{str(report['git']['dirty']).lower()}`",
    f"- Coverage ratchet: `{report['coverage']['ratchet']}%`",
    f"- Benchmark evidence: `{report['benchmark']['evidenceStatus']}`",
    f"- Release evidence schema: `{report['release']['schema']}`",
    f"- Generated upgrade dry-run profiles: `{report['generatedUpgradeDryRun']['profileCount']}`",
    "",
    "## API Surface",
    "",
]
for tier in report["apiSurface"]["tiers"]:
    md_lines.append(f"- `{tier['tier']}`: {tier['surface']}")
md_lines.extend([
    "",
    "## Gates",
    "",
])
for gate in report["gates"]:
    md_lines.append(f"- `{gate}`")
md_lines.extend([
    "",
    "## Aiflow Queue",
    "",
    f"- Status: `{report['aiflow']['status']}`",
    f"- Summary: `{json.dumps(report['aiflow'].get('summary', {}), sort_keys=True)}`",
    "",
    "## Evidence Files",
    "",
])
for item in report["docs"]["evidence"]:
    md_lines.append(f"- `{item['path']}`: `{item['status']}`")
md_lines.append("")
md_out.write_text("\n".join(md_lines), encoding="utf-8")

missing = []
if report["schema"] != "gofly.governance_report.v1":
    missing.append("unexpected governance report schema")
if not report["apiSurface"]["tiers"]:
    missing.append("apiSurface.tiers is empty")
if report["coverage"]["ratchet"] == "":
    missing.append("coverage.ratchet is empty")
if report["coverageTrend"]["schema"] != "gofly.coverage_trend.v1":
    missing.append("coverage trend schema mismatch")
if report["coverageTrend"]["policy"]["blockingGate"] != "make cover-check":
    missing.append("coverage trend blocking gate mismatch")
if report["coverageTrend"]["evidenceCount"] < 5:
    missing.append("coverage trend evidence is incomplete")
if report["ciRequiredChecks"]["schema"] != "gofly.ci_required_check_evidence.v1":
    missing.append("CI required-check evidence schema mismatch")
if report["ciRequiredChecks"]["checkCount"] < 20:
    missing.append("CI required-check evidence is incomplete")
if report["ciRequiredChecks"]["releasePrerequisiteCount"] < 13:
    missing.append("CI release prerequisite evidence is incomplete")
if report["runtimeSLO"]["schema"] != "gofly.runtime_slo.v1":
    missing.append("runtime SLO evidence schema mismatch")
if report["runtimeSLO"]["signalCount"] < 7:
    missing.append("runtime SLO evidence is incomplete")
if report["generatedUpgradeDryRun"]["schema"] != "gofly.generated_upgrade_dry_run.v1":
    missing.append("generated upgrade dry-run schema mismatch")
if report["generatedUpgradeDryRun"]["profileCount"] != 3:
    missing.append("generated upgrade dry-run profile count mismatch")
if report["generatedUpgradeDryRun"]["diffCategoryCount"] != 4:
    missing.append("generated upgrade dry-run diff category count mismatch")
if report["generatedUpgradeDryRun"]["rollbackNoteCount"] != 3:
    missing.append("generated upgrade dry-run rollback note count mismatch")
if report["benchmark"]["evidenceStatus"] != "present":
    missing.append("benchmark evidence is missing")
if report["release"]["schema"] != "gofly.release_evidence_manifest.v1":
    missing.append("release evidence manifest schema mismatch")
if report["security"]["gosec"]["baselineSchema"] != "gofly.gosec_exception_baseline.v1":
    missing.append("gosec exception baseline schema mismatch")
if report["aiflow"]["status"] == "present" and not report["aiflow"].get("summary"):
    missing.append("aiflow queue status summary is empty")
for item in report["docs"]["evidence"]:
    if item["status"] != "present":
        missing.append(f"{item['path']} is missing")
if not md_out.read_text(encoding="utf-8").startswith("# Governance Report"):
    missing.append("markdown report missing title")

if missing:
    print("governance report check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

if check_mode:
    print(f"governance report contract ok: {json_out} {md_out}")
else:
    print(f"governance report written: {json_out}")
    print(f"governance report markdown written: {md_out}")
PY
