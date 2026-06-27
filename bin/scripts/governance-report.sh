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
observability_manifest_path = root / "docs" / "reference" / "observability-production-governance.json"


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
    evidence_index = read_json(root / "docs/releases/evidence-index.json") or {}
    readiness_policy = read_json(root / "docs/releases/readiness-score.json") or {}
    evidence_items = evidence_index.get("evidence") or []
    release = {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/releases/evidence-manifest.json",
        "indexSchema": evidence_index.get("schema", ""),
        "index": "docs/releases/evidence-index.json",
        "indexGate": "make release-evidence-index-check",
        "evidenceCount": len(evidence_items),
        "releaseRequiredCount": sum(
            1
            for item in evidence_items
            if isinstance(item, dict) and item.get("releaseRequired") is True
        ),
        "stableIds": [
            item.get("id", "")
            for item in evidence_items
            if isinstance(item, dict) and item.get("id")
        ],
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
    release["readinessScore"] = release_readiness_score(manifest, evidence_index, readiness_policy)
    return release


def release_evidence_consumption():
    manifest = read_json(root / "docs/releases/evidence-consumption.json") or {}
    items = [
        item
        for item in manifest.get("items") or []
        if isinstance(item, dict)
    ]
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/releases/evidence-consumption.json",
        "sourceOfTruth": manifest.get("sourceOfTruth", ""),
        "acceptanceGate": manifest.get("acceptanceGate", ""),
        "consumers": manifest.get("consumers", []),
        "itemCount": len(items),
        "stableIds": [
            item.get("id", "")
            for item in items
            if item.get("id")
        ],
        "items": items,
    }


def release_readiness_score(manifest, evidence_index, policy):
    evidence_items = [
        item
        for item in evidence_index.get("evidence") or []
        if isinstance(item, dict)
    ]
    evidence_ids = {
        item.get("id", "")
        for item in evidence_items
        if item.get("id")
    }
    local_gates = {
        item.get("localGate", "")
        for item in evidence_items
        if item.get("localGate")
    }
    manifest_gates = set(manifest.get("required_gates") or [])
    artifact_groups = {
        item.get("id", ""): item
        for item in manifest.get("artifact_groups") or []
        if isinstance(item, dict) and item.get("id")
    }
    forbidden_skips = set((manifest.get("tag_governance") or {}).get("forbidden_skips") or [])
    evidence_policy = manifest.get("evidence_policy") or {}

    score = 0
    components = []
    for component in policy.get("components") or []:
        if not isinstance(component, dict):
            continue
        component_id = component.get("id", "")
        weight = int(component.get("weight") or 0)
        passed = True
        failures = []

        if component_id == "release-evidence-index":
            minimum = int(component.get("requiredEvidenceMinimum") or 0)
            if evidence_index.get("schema") != "gofly.release_evidence_index.v1":
                passed = False
                failures.append("release evidence index schema mismatch")
            if len(evidence_items) < minimum:
                passed = False
                failures.append(f"release evidence count {len(evidence_items)} is below {minimum}")
            if component.get("requiresAllEvidenceReleaseRequired") is True:
                non_required = [
                    item.get("id", "<missing>")
                    for item in evidence_items
                    if item.get("releaseRequired") is not True
                ]
                if non_required:
                    passed = False
                    failures.append("non-release-required evidence ids: " + ", ".join(non_required))
        elif component_id == "artifact-groups":
            for group_id in component.get("requiredGroups") or []:
                group = artifact_groups.get(group_id)
                if not group:
                    passed = False
                    failures.append(f"missing artifact group {group_id}")
                    continue
                if group.get("required") is not True:
                    passed = False
                    failures.append(f"artifact group {group_id} is not required")
                if not group.get("gate"):
                    passed = False
                    failures.append(f"artifact group {group_id} is missing gate")
        elif component_id == "supply-chain-artifacts":
            for artifact_id in component.get("requiredArtifacts") or []:
                if artifact_id not in evidence_ids:
                    passed = False
                    failures.append(f"missing release evidence id {artifact_id}")
        elif component_id == "blocking-gates":
            available_gates = manifest_gates | local_gates
            required_command = (manifest.get("tag_governance") or {}).get("required_command")
            if required_command:
                available_gates.add(required_command)
            for gate in component.get("requiredGates") or []:
                if gate not in available_gates:
                    passed = False
                    failures.append(f"missing blocking gate {gate}")
        elif component_id == "skip-policy":
            for skip in component.get("requiredForbiddenSkips") or []:
                if skip not in forbidden_skips:
                    passed = False
                    failures.append(f"missing forbidden skip {skip}")
            if (
                component.get("requiresAllowReleaseGateSkipsFalse") is True
                and evidence_policy.get("allow_release_gate_skips") is not False
            ):
                passed = False
                failures.append("release gate skips are not explicitly disabled")
        else:
            passed = False
            failures.append(f"unknown release readiness component {component_id}")

        if passed:
            score += weight
        components.append({
            "id": component_id,
            "gate": component.get("gate", ""),
            "passed": passed,
            "weight": weight,
            "failures": failures,
        })

    max_score = int(policy.get("maxScore") or 0)
    minimum_score = int(policy.get("minimumScore") or max_score)
    status_policy = policy.get("statusPolicy") or {}
    status_value = (
        status_policy.get("readyStatus", "ready")
        if score >= minimum_score
        else status_policy.get("blockedStatus", "blocked")
    )
    return {
        "schema": policy.get("schema", ""),
        "policy": "docs/releases/readiness-score.json",
        "maxScore": max_score,
        "minimumScore": minimum_score,
        "score": score,
        "status": status_value,
        "components": components,
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
    runbook = read_json(root / "docs/reference/operator-runbook-drills.json") or {}
    signals = manifest.get("goldenSignals") or []
    drills = runbook.get("drills") or []
    incidents = runbook.get("incidentRehearsals") or []
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/runtime-slo.json",
        "runbook": "docs/reference/operator-runbook-drills.json",
        "runbookSchema": runbook.get("schema", ""),
        "gate": "make runtime-slo-check",
        "exampleGate": (manifest.get("verification") or {}).get("observabilityExample", ""),
        "productionGate": (manifest.get("verification") or {}).get("productionGate", ""),
        "signals": [
            item.get("id", "")
            for item in signals
            if isinstance(item, dict) and item.get("id")
        ],
        "signalCount": len(signals),
        "operatorDrills": [
            item.get("id", "")
            for item in drills
            if isinstance(item, dict) and item.get("id")
        ],
        "operatorDrillCount": len(drills),
        "incidentRehearsals": [
            item.get("id", "")
            for item in incidents
            if isinstance(item, dict) and item.get("id")
        ],
        "incidentRehearsalCount": len(incidents),
    }


def production_defaults_evidence():
    required_assets = [
        "examples/observability/main.go",
        "examples/observability/README.md",
        "examples/observability/prometheus.yaml",
        "examples/observability/otel-collector.yaml",
        "examples/observability/grafana-dashboard.json",
        "k8s/servicemonitor.yaml",
        "charts/gofly/templates/servicemonitor.yaml",
        "docs/operations/observability.md",
        "docs/operations/production-checklist.md",
    ]
    capabilities = [
        {
            "id": "health-probes",
            "evidence": ["examples/observability/main.go", "docs/operations/production-checklist.md"],
            "operatorAction": "verify /healthz, /readyz and startup probes before production rollout",
        },
        {
            "id": "metrics",
            "evidence": ["core/observability/metrics/prometheus.go", "examples/observability/prometheus.yaml"],
            "operatorAction": "scrape /debug/metrics and review request, error, inflight and latency signals",
        },
        {
            "id": "traces",
            "evidence": ["core/observability/trace/trace.go", "examples/observability/otel-collector.yaml"],
            "operatorAction": "propagate traceparent and correlate trace_id across REST, RPC and logs",
        },
        {
            "id": "structured-logs",
            "evidence": ["core/observability/observer.go", "docs/operations/observability.md"],
            "operatorAction": "emit request_id and trace_id without raw secrets or high-cardinality payloads",
        },
        {
            "id": "admin-diagnostics",
            "evidence": ["core/governance/admin.go", "docs/operations/observability.md"],
            "operatorAction": "inspect /admin/control-plane and diagnostics during rollout or rollback decisions",
        },
        {
            "id": "profiles",
            "evidence": ["core/observability/setup.go", "docs/operations/production-checklist.md"],
            "operatorAction": "enable pprof only for trusted admin access and disable or restrict it in hardened profiles",
        },
        {
            "id": "rollback-drills",
            "evidence": ["docs/reference/operator-runbook-drills.json", "docs/operations/troubleshooting.md"],
            "operatorAction": "follow operator drill rollback triggers when SLO, control-plane or release evidence regresses",
        },
    ]
    missing_assets = [
        path
        for path in required_assets
        if not (root / path).exists()
    ]
    return {
        "schema": "gofly.production_defaults.v1",
        "source": "docs/reference/runtime-slo.json",
        "gate": "make runtime-slo-check",
        "reportGate": "make governance-report-check",
        "exampleGate": "go test -C examples/observability ./...",
        "productionGate": "make p1-growth-check",
        "requiredAssets": required_assets,
        "assetCount": len(required_assets),
        "missingAssets": sorted(missing_assets),
        "capabilities": capabilities,
        "capabilityCount": len(capabilities),
    }


def governance_convergence_evidence():
    manifest = read_json(root / "docs/reference/governance-boundary-inventory.json") or {}
    tasks = [
        item
        for item in manifest.get("aiflowTasks") or []
        if isinstance(item, dict)
    ]
    ignored_paths = manifest.get("ignoredRuntimePaths") or []
    return {
        "schema": manifest.get("schema", ""),
        "source": "docs/reference/governance-boundary-inventory.json",
        "gate": "make governance-boundary-inventory-check",
        "finalGate": "make governance-10-rounds",
        "activeAiflowBatch": manifest.get("activeAiflowBatch", ""),
        "taskCount": len(tasks),
        "tasks": tasks,
        "baselineGates": manifest.get("baselineGates", []),
        "ignoredRuntimePaths": ignored_paths,
        "ignoredRuntimePathCount": len(ignored_paths),
        "timeoutPolicy": manifest.get("timeoutPolicy", {}),
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


def governance_dashboard_contract():
    manifest = read_json(root / "docs/reference/governance-dashboard-contract.json") or {}
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/governance-dashboard-contract.json",
        "sourceReport": manifest.get("sourceReport", ""),
        "acceptanceGate": manifest.get("acceptanceGate", ""),
        "summaryFields": manifest.get("summaryFields", []),
        "releaseReadiness": manifest.get("releaseReadiness", {}),
        "apiTiers": manifest.get("apiTiers", {}),
        "benchmarkRatchet": manifest.get("benchmarkRatchet", {}),
        "coverage": manifest.get("coverage", {}),
        "security": manifest.get("security", {}),
        "productionDefaults": manifest.get("productionDefaults", {}),
        "governanceConvergence": manifest.get("governanceConvergence", {}),
        "aiflow": manifest.get("aiflow", {}),
        "aiNativeWorkflow": manifest.get("aiNativeWorkflow", {}),
        "productionReadinessScorecard": production_readiness_scorecard(manifest.get("productionReadinessScorecard", {})),
        "evidenceTraceability": evidence_traceability(manifest.get("evidenceTraceability", {})),
        "outputs": manifest.get("outputs", []),
    }


def evidence_traceability(contract):
    claims = [
        item
        for item in contract.get("claims") or []
        if isinstance(item, dict)
    ]
    missing_sources = []
    risk_counts = {}
    for item in claims:
        risk_class = item.get("riskClass", "")
        risk_counts[risk_class] = risk_counts.get(risk_class, 0) + 1
        source = item.get("sourceManifest", "")
        if source and not (root / source).exists():
            missing_sources.append(source)
    return {
        "schema": contract.get("schema", ""),
        "source": contract.get("source", ""),
        "gate": contract.get("gate", ""),
        "claimCount": len(claims),
        "riskClassCounts": risk_counts,
        "missingSources": sorted(missing_sources),
        "claims": claims,
    }


def observability_production_governance_contract():
    manifest = read_json(observability_manifest_path) or {}
    surfaces = [
        item
        for item in manifest.get("surfaces") or []
        if isinstance(item, dict)
    ]
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/observability-production-governance.json",
        "aiflowTask": manifest.get("aiflowTask", ""),
        "acceptanceGate": manifest.get("acceptanceGate", ""),
        "aggregateGates": manifest.get("aggregateGates", []),
        "surfaceCount": len(surfaces),
        "surfaces": [
            item.get("id", "")
            for item in surfaces
            if item.get("id")
        ],
    }


def production_readiness_scorecard(contract):
    surfaces = [
        item
        for item in contract.get("surfaces") or []
        if isinstance(item, dict)
    ]
    risk_counts = {}
    missing_sources = []
    for item in surfaces:
        risk_class = item.get("riskClass", "")
        risk_counts[risk_class] = risk_counts.get(risk_class, 0) + 1
        source = item.get("source", "")
        if source and not (root / source).exists():
            missing_sources.append(source)
    return {
        "schema": contract.get("schema", ""),
        "source": contract.get("source", ""),
        "gate": contract.get("gate", ""),
        "requiredRiskClasses": contract.get("requiredRiskClasses", []),
        "surfaceCount": len(surfaces),
        "riskClassCounts": risk_counts,
        "missingSources": sorted(missing_sources),
        "surfaces": surfaces,
    }


def dx_support_bundle_evidence():
    manifest = read_json(root / "docs/reference/dx-support-bundle.json") or {}
    surfaces = [
        item
        for item in manifest.get("surfaces") or []
        if isinstance(item, dict)
    ]
    stable_field_refs = sorted({
        field
        for item in surfaces
        for field in item.get("stableFields") or []
    })
    remediation_hints = []
    for item in surfaces:
        command = item.get("command", "")
        guidance = item.get("failureGuidance", "")
        if guidance:
            remediation_hints.append({
                "command": command,
                "failureGuidance": guidance,
                "nextActionRequired": item.get("nextActionRequired") is True,
            })
    generated_failure = manifest.get("generatedFailureReport") or {}
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/reference/dx-support-bundle.json",
        "gate": manifest.get("acceptanceGate", ""),
        "surfaceCount": len(surfaces),
        "commands": [
            item.get("command", "")
            for item in surfaces
            if item.get("command")
        ],
        "stableFieldRefs": stable_field_refs,
        "supportWorkflow": manifest.get("supportWorkflow", []),
        "generatedFailureReport": {
            "schema": generated_failure.get("schema", ""),
            "boundedOutput": generated_failure.get("boundedOutput") is True,
            "redactionRequired": generated_failure.get("redactionRequired") is True,
            "rerunGuidanceField": generated_failure.get("rerunGuidanceField", ""),
        },
        "remediationHints": remediation_hints,
        "remediationHintCount": len(remediation_hints),
        "controlPlaneEvidence": {
            "source": "docs/case-studies/ai-control-plane-drift.md",
            "status": status("docs/case-studies/ai-control-plane-drift.md"),
        },
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
        "docs/reference/governance-dashboard-contract.json",
        "docs/releases/evidence-manifest.json",
        "docs/releases/evidence-index.json",
        "docs/releases/evidence-consumption.json",
        "docs/releases/readiness-score.json",
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
    "dxSupportBundle": dx_support_bundle_evidence(),
    "productionDefaults": production_defaults_evidence(),
    "observabilityProductionGovernance": observability_production_governance_contract(),
    "governanceConvergence": governance_convergence_evidence(),
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
    "releaseEvidenceConsumption": release_evidence_consumption(),
    "aiflow": aiflow_queue(),
    "dashboard": governance_dashboard_contract(),
    "docs": {
        "gate": "make docs-check",
        "evidence": docs_evidence(),
    },
    "gates": [
        "make governance-report-check",
        "make stable-surface-check",
        "make generated-version-compat-check",
        "make generated-upgrade-dry-run-check",
        "make release-evidence-index-check",
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
    f"- Release evidence index items: `{report['release']['evidenceCount']}`",
    f"- Release evidence consumption items: `{report['releaseEvidenceConsumption']['itemCount']}`",
    f"- Release readiness score: `{report['release']['readinessScore']['score']}/{report['release']['readinessScore']['maxScore']}` (`{report['release']['readinessScore']['status']}`)",
    f"- Production readiness surfaces: `{report['dashboard']['productionReadinessScorecard']['surfaceCount']}`",
    f"- Production default capabilities: `{report['productionDefaults']['capabilityCount']}`",
    f"- DX support bundle surfaces: `{report['dxSupportBundle']['surfaceCount']}`",
    f"- Governance convergence rounds: `{report['governanceConvergence']['taskCount']}`",
    f"- Evidence traceability claims: `{report['dashboard']['evidenceTraceability']['claimCount']}`",
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
if report["runtimeSLO"]["runbookSchema"] != "gofly.operator_runbook_drills.v1":
    missing.append("runtime SLO runbook schema mismatch")
if report["runtimeSLO"]["operatorDrillCount"] < 6:
    missing.append("runtime SLO operator drill evidence is incomplete")
if report["runtimeSLO"]["incidentRehearsalCount"] < 4:
    missing.append("runtime SLO incident rehearsal evidence is incomplete")
dx_support_bundle = report["dxSupportBundle"]
if dx_support_bundle["schema"] != "gofly.dx_support_bundle.v1":
    missing.append("dx support bundle schema mismatch")
if dx_support_bundle["gate"] != "make dx-troubleshooting-check":
    missing.append("dx support bundle gate mismatch")
required_dx_commands = {
    "gofly doctor --json",
    "gofly bug --json",
    "gofly release check --json --strict",
    "gofly ai new --json --apply --verify",
}
if set(dx_support_bundle.get("commands") or []) != required_dx_commands:
    missing.append("dx support bundle commands mismatch")
required_workflow_steps = {
    "run gofly doctor --json",
    "run gofly release check --json --strict",
    "run gofly bug --json",
    "attach generated project verification failure reports when available",
}
if set(dx_support_bundle.get("supportWorkflow") or []) != required_workflow_steps:
    missing.append("dx support bundle supportWorkflow mismatch")
required_stable_field_refs = {
    "nextActions",
    "supportBundle",
    "error.remediation",
    "data.verification",
    "data.nextActions",
}
if not required_stable_field_refs.issubset(set(dx_support_bundle.get("stableFieldRefs") or [])):
    missing.append("dx support bundle stableFieldRefs missing AI-native remediation fields")
failure_report = dx_support_bundle.get("generatedFailureReport") or {}
if failure_report.get("schema") != "gofly.generated_project_failure_report.v1":
    missing.append("dx support bundle generated failure report schema mismatch")
if failure_report.get("boundedOutput") is not True or failure_report.get("redactionRequired") is not True:
    missing.append("dx support bundle generated failure report must keep bounded output and redaction")
if failure_report.get("rerunGuidanceField") != "nextActions":
    missing.append("dx support bundle generated failure report rerun guidance mismatch")
if dx_support_bundle.get("remediationHintCount") != len(dx_support_bundle.get("remediationHints") or []):
    missing.append("dx support bundle remediationHintCount mismatch")
if dx_support_bundle.get("remediationHintCount", 0) < 4:
    missing.append("dx support bundle remediation hints are incomplete")
if (dx_support_bundle.get("controlPlaneEvidence") or {}).get("status") != "present":
    missing.append("dx support bundle control-plane evidence is missing")
convergence = report["governanceConvergence"]
if convergence["schema"] != "gofly.governance_boundary_inventory.v1":
    missing.append("governance convergence schema mismatch")
if convergence["gate"] != "make governance-boundary-inventory-check":
    missing.append("governance convergence gate mismatch")
if convergence["finalGate"] != "make governance-10-rounds":
    missing.append("governance convergence finalGate mismatch")
if convergence["taskCount"] != 10:
    missing.append("governance convergence must track 10 rounds")
active_aiflow_batch = convergence.get("activeAiflowBatch", "")
if not active_aiflow_batch:
    missing.append("governance convergence activeAiflowBatch is required")
expected_round_ids = [f"{active_aiflow_batch}-{idx:02d}" for idx in range(1, 11)]
actual_round_ids = [
    item.get("id", "")
    for item in convergence.get("tasks") or []
    if isinstance(item, dict)
]
if actual_round_ids != expected_round_ids:
    missing.append(
        "governance convergence task ids mismatch: "
        f"expected={expected_round_ids} got={actual_round_ids}"
    )
for item in convergence.get("tasks") or []:
    if not isinstance(item, dict):
        missing.append(f"governance convergence task must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    for field in ("id", "round", "title", "gate"):
        if not item.get(field):
            missing.append(f"governance convergence {item_id or '<missing>'}: {field} is required")
ignored_runtime_paths = set(convergence.get("ignoredRuntimePaths") or [])
expected_ignored_runtime_paths = {
    "docs/superpowers/",
    ".aiflow/",
    ".harness/",
    ".tmp-test/",
    ".trae/",
    "coverage.out",
}
if ignored_runtime_paths != expected_ignored_runtime_paths:
    missing.append(
        "governance convergence ignoredRuntimePaths drifted: "
        f"missing={sorted(expected_ignored_runtime_paths - ignored_runtime_paths)} "
        f"extra={sorted(ignored_runtime_paths - expected_ignored_runtime_paths)}"
    )
if convergence.get("ignoredRuntimePathCount") != len(expected_ignored_runtime_paths):
    missing.append("governance convergence ignoredRuntimePathCount mismatch")
if "governance-boundary-inventory-check" not in (convergence.get("timeoutPolicy") or {}).get("fallback", ""):
    missing.append("governance convergence timeout fallback must mention governance-boundary-inventory-check")
production_defaults = report["productionDefaults"]
if production_defaults["schema"] != "gofly.production_defaults.v1":
    missing.append("production defaults schema mismatch")
if production_defaults["gate"] != "make runtime-slo-check":
    missing.append("production defaults gate mismatch")
if production_defaults["reportGate"] != "make governance-report-check":
    missing.append("production defaults report gate mismatch")
required_capabilities = {
    "health-probes",
    "metrics",
    "traces",
    "structured-logs",
    "admin-diagnostics",
    "profiles",
    "rollback-drills",
}
capabilities = production_defaults.get("capabilities") or []
actual_capabilities = {
    item.get("id", "")
    for item in capabilities
    if isinstance(item, dict) and item.get("id")
}
if actual_capabilities != required_capabilities:
    missing.append(
        "production defaults capabilities drifted: "
        f"missing={sorted(required_capabilities - actual_capabilities)} "
        f"extra={sorted(actual_capabilities - required_capabilities)}"
    )
if production_defaults.get("capabilityCount") != len(capabilities):
    missing.append("production defaults capabilityCount mismatch")
if production_defaults.get("assetCount", 0) < 9:
    missing.append("production defaults asset coverage is incomplete")
if production_defaults.get("missingAssets"):
    missing.append(f"production defaults missing assets: {production_defaults['missingAssets']}")
for item in capabilities:
    if not isinstance(item, dict):
        missing.append(f"production defaults capability must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    for field in ("id", "evidence", "operatorAction"):
        if not item.get(field):
            missing.append(f"production defaults {item_id or '<missing>'}: {field} is required")
    for evidence in item.get("evidence") or []:
        if not (root / evidence).exists():
            missing.append(f"production defaults {item_id}: evidence path is missing: {evidence}")
    if len(str(item.get("operatorAction") or "").split()) < 8:
        missing.append(f"production defaults {item_id}: operatorAction must be actionable")
observability_governance = report["observabilityProductionGovernance"]
if observability_governance["schema"] != "gofly.observability_production_governance.v1":
    missing.append("observability production governance schema mismatch")
if observability_governance["aiflowTask"] != "GOFLY-GOV-10R3-09":
    missing.append("observability production governance aiflowTask mismatch")
if observability_governance["acceptanceGate"] != "make governance-report-check":
    missing.append("observability production governance acceptanceGate mismatch")
required_observability_gates = {
    "make governance-report-check",
    "make runtime-slo-check",
    "go test -C examples/observability ./...",
    "make p1-growth-check",
}
if set(observability_governance.get("aggregateGates") or []) != required_observability_gates:
    missing.append("observability production governance aggregateGates mismatch")
required_observability_surfaces = {
    "runtime-slo-golden-signals",
    "production-defaults",
    "control-plane-and-runtime-snapshots",
    "observability-example-assets",
    "governance-dashboard-report",
}
if set(observability_governance.get("surfaces") or []) != required_observability_surfaces:
    missing.append("observability production governance surfaces mismatch")
if observability_governance.get("surfaceCount") != len(required_observability_surfaces):
    missing.append("observability production governance surfaceCount mismatch")
observability_manifest = read_json(observability_manifest_path) or {}
observability_policy = observability_manifest.get("policy") or {}
for key in (
    "runtimeSLOMustExposeSevenGoldenSignals",
    "operatorDrillsMustStayLinkedToRuntimeSLO",
    "productionDefaultsMustHaveRequiredAssets",
    "governanceReportMustEmitRuntimeSLOAndProductionDefaults",
    "controlPlaneSnapshotsMustStayDocumented",
    "observabilityExamplesMustRemainRunnable",
):
    if observability_policy.get(key) is not True:
        missing.append(f"observability production governance policy.{key} must be true")
for item in observability_manifest.get("surfaces") or []:
    if not isinstance(item, dict):
        missing.append(f"observability production governance surface must be an object: {item!r}")
        continue
    surface = item.get("id", "")
    for field in ("id", "risk", "gate", "evidenceRefs"):
        if not item.get(field):
            missing.append(f"observability production governance {surface or '<missing>'}: {field} is required")
    gate = str(item.get("gate") or "")
    if not (gate.startswith("make ") or gate.startswith("go test ")):
        missing.append(f"observability production governance {surface}: gate must be runnable")
    for ref in item.get("evidenceRefs") or []:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        if not ref_path:
            missing.append(f"observability production governance {surface}: ref path is required")
            continue
        if not needles:
            missing.append(f"observability production governance {surface}: ref contains list is required for {ref_path}")
        path = root / ref_path
        if not path.is_file():
            missing.append(f"observability production governance {surface}: evidence path is missing: {ref_path}")
            continue
        text = read_text(path)
        for needle in needles:
            if needle not in text:
                missing.append(f"observability production governance {surface}: {ref_path} missing {needle!r}")
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
if report["release"]["indexSchema"] != "gofly.release_evidence_index.v1":
    missing.append("release evidence index schema mismatch")
if report["release"]["evidenceCount"] < 12:
    missing.append("release evidence index is incomplete")
if report["release"]["releaseRequiredCount"] != report["release"]["evidenceCount"]:
    missing.append("release evidence index contains non-required release item")
consumption = report["releaseEvidenceConsumption"]
if consumption["schema"] != "gofly.release_evidence_consumption.v1":
    missing.append("release evidence consumption schema mismatch")
if consumption["sourceOfTruth"] != "docs/releases/evidence-index.json":
    missing.append("release evidence consumption sourceOfTruth mismatch")
if consumption["acceptanceGate"] != "make governance-report-check":
    missing.append("release evidence consumption acceptanceGate mismatch")
for consumer in ("adopter", "release-manager", "ci-agent"):
    if consumer not in consumption["consumers"]:
        missing.append(f"release evidence consumption missing consumer {consumer!r}")
index_by_id = {
    item.get("id", ""): item
    for item in (read_json(root / "docs/releases/evidence-index.json") or {}).get("evidence") or []
    if isinstance(item, dict) and item.get("id")
}
consumption_by_id = {
    item.get("id", ""): item
    for item in consumption["items"]
    if isinstance(item, dict) and item.get("id")
}
if set(consumption_by_id) != set(index_by_id):
    missing.append(
        "release evidence consumption ids mismatch: "
        f"missing={sorted(set(index_by_id) - set(consumption_by_id))} "
        f"extra={sorted(set(consumption_by_id) - set(index_by_id))}"
    )
for evidence_id, source in sorted(index_by_id.items()):
    item = consumption_by_id.get(evidence_id)
    if not item:
        continue
    if item.get("artifactPath") != source.get("artifactPath"):
        missing.append(f"release evidence consumption artifactPath mismatch for {evidence_id}")
    if item.get("localGate") != source.get("localGate"):
        missing.append(f"release evidence consumption localGate mismatch for {evidence_id}")
    for field in ("questionAnswered", "riskClass", "consumerAction", "rollbackOrEscalation"):
        if not item.get(field):
            missing.append(f"release evidence consumption {evidence_id} missing {field}")
readiness_score = report["release"]["readinessScore"]
if readiness_score["schema"] != "gofly.release_readiness_score.v1":
    missing.append("release readiness score schema mismatch")
if readiness_score["score"] != readiness_score["maxScore"]:
    missing.append(
        f"release readiness score is {readiness_score['score']}/{readiness_score['maxScore']}"
    )
if readiness_score["status"] != "ready":
    missing.append(f"release readiness score status is {readiness_score['status']!r}")
if readiness_score["minimumScore"] != 100:
    missing.append("release readiness score minimum must remain 100")
for component in readiness_score["components"]:
    if not component["passed"]:
        missing.append(
            "release readiness component "
            f"{component['id']} failed: {', '.join(component['failures'])}"
        )
if report["security"]["gosec"]["baselineSchema"] != "gofly.gosec_exception_baseline.v1":
    missing.append("gosec exception baseline schema mismatch")
if report["aiflow"]["status"] == "present" and not report["aiflow"].get("summary"):
    missing.append("aiflow queue status summary is empty")
dashboard = report["dashboard"]
if dashboard["schema"] != "gofly.governance_dashboard_contract.v1":
    missing.append("governance dashboard contract schema mismatch")
if dashboard["sourceReport"] != "gofly.governance_report.v1":
    missing.append("governance dashboard contract sourceReport mismatch")
if dashboard["acceptanceGate"] != "make governance-report-check":
    missing.append("governance dashboard contract acceptanceGate mismatch")
for field in (
    "release.readinessScore.status",
    "apiSurface.tiers",
    "benchmark.regressionGate",
    "coverage.ratchet",
    "security.gosec.blockingGate",
    "aiflow.status",
    "productionDefaults.capabilityCount",
    "productionDefaults.missingAssets",
    "runtimeSLO.operatorDrillCount",
    "runtimeSLO.incidentRehearsalCount",
    "dxSupportBundle.surfaceCount",
    "dxSupportBundle.remediationHintCount",
    "governanceConvergence.taskCount",
    "governanceConvergence.ignoredRuntimePathCount",
    "dashboard.productionReadinessScorecard.surfaceCount",
    "dashboard.evidenceTraceability.claimCount",
):
    if field not in dashboard["summaryFields"]:
        missing.append(f"governance dashboard contract summaryFields missing {field!r}")
release_contract = dashboard.get("releaseReadiness") or {}
if release_contract.get("schema") != "gofly.release_readiness_score.v1":
    missing.append("governance dashboard releaseReadiness schema mismatch")
if release_contract.get("requiredStatus") != "ready":
    missing.append("governance dashboard releaseReadiness requiredStatus mismatch")
if int(release_contract.get("minimumScore") or 0) != 100:
    missing.append("governance dashboard releaseReadiness minimumScore mismatch")
api_contract = dashboard.get("apiTiers") or {}
if api_contract.get("gate") != "make stable-surface-check":
    missing.append("governance dashboard apiTiers gate mismatch")
if len(report["apiSurface"]["tiers"]) < int(api_contract.get("minimumTierCount") or 0):
    missing.append("governance dashboard api tier count below contract")
benchmark_contract = dashboard.get("benchmarkRatchet") or {}
if benchmark_contract.get("gate") != "make bench-regression-check":
    missing.append("governance dashboard benchmarkRatchet gate mismatch")
ratchet = read_json(root / "bench/budget-ratchet.json") or {}
for field in benchmark_contract.get("requiredFields") or []:
    if field not in ratchet:
        missing.append(f"governance dashboard benchmark ratchet missing {field!r}")
coverage_contract = dashboard.get("coverage") or {}
if coverage_contract.get("gate") != "make coverage-trend-check":
    missing.append("governance dashboard coverage gate mismatch")
if coverage_contract.get("ratchetVar") != "COVERAGE_RATCHET":
    missing.append("governance dashboard coverage ratchetVar mismatch")
security_contract = dashboard.get("security") or {}
if security_contract.get("gosecGate") != report["security"]["gosec"]["blockingGate"]:
    missing.append("governance dashboard gosec gate mismatch")
if security_contract.get("govulncheckGate") != report["security"]["govulncheck"]["blockingGate"]:
    missing.append("governance dashboard govulncheck gate mismatch")
production_defaults_contract = dashboard.get("productionDefaults") or {}
if production_defaults_contract.get("schema") != "gofly.production_defaults.v1":
    missing.append("governance dashboard productionDefaults schema mismatch")
if production_defaults_contract.get("gate") != "make runtime-slo-check":
    missing.append("governance dashboard productionDefaults gate mismatch")
if set(production_defaults_contract.get("requiredCapabilities") or []) != required_capabilities:
    missing.append("governance dashboard productionDefaults requiredCapabilities mismatch")
if int(production_defaults_contract.get("requiredAssetMinimum") or 0) > production_defaults.get("assetCount", 0):
    missing.append("governance dashboard productionDefaults requiredAssetMinimum is not met")
convergence_contract = dashboard.get("governanceConvergence") or {}
if convergence_contract.get("schema") != "gofly.governance_boundary_inventory.v1":
    missing.append("governance dashboard governanceConvergence schema mismatch")
if convergence_contract.get("gate") != "make governance-boundary-inventory-check":
    missing.append("governance dashboard governanceConvergence gate mismatch")
if int(convergence_contract.get("requiredTaskCount") or 0) != 10:
    missing.append("governance dashboard governanceConvergence requiredTaskCount mismatch")
if set(convergence_contract.get("requiredIgnoredRuntimePaths") or []) != expected_ignored_runtime_paths:
    missing.append("governance dashboard governanceConvergence requiredIgnoredRuntimePaths mismatch")
aiflow_contract = dashboard.get("aiflow") or {}
if aiflow_contract.get("requiredStatusField") != "status":
    missing.append("governance dashboard aiflow requiredStatusField mismatch")
if aiflow_contract.get("requiredSummaryField") != "summary":
    missing.append("governance dashboard aiflow requiredSummaryField mismatch")
ai_native_contract = dashboard.get("aiNativeWorkflow") or {}
if ai_native_contract.get("schema") != "gofly.dx_support_bundle.v1":
    missing.append("governance dashboard aiNativeWorkflow schema mismatch")
if ai_native_contract.get("source") != "docs/reference/dx-support-bundle.json":
    missing.append("governance dashboard aiNativeWorkflow source mismatch")
if ai_native_contract.get("gate") != "make dx-troubleshooting-check":
    missing.append("governance dashboard aiNativeWorkflow gate mismatch")
if ai_native_contract.get("reportGate") != "make governance-report-check":
    missing.append("governance dashboard aiNativeWorkflow reportGate mismatch")
if set(ai_native_contract.get("requiredCommands") or []) != required_dx_commands:
    missing.append("governance dashboard aiNativeWorkflow requiredCommands mismatch")
if set(ai_native_contract.get("requiredWorkflowSteps") or []) != required_workflow_steps:
    missing.append("governance dashboard aiNativeWorkflow requiredWorkflowSteps mismatch")
if not set(ai_native_contract.get("requiredStableFields") or []).issubset(set(dx_support_bundle.get("stableFieldRefs") or [])):
    missing.append("governance dashboard aiNativeWorkflow requiredStableFields missing from report")
scorecard = dashboard.get("productionReadinessScorecard") or {}
if scorecard.get("schema") != "gofly.production_readiness_scorecard.v1":
    missing.append("production readiness scorecard schema mismatch")
if scorecard.get("source") != "docs/reference/governance-dashboard-contract.json":
    missing.append("production readiness scorecard source mismatch")
if scorecard.get("gate") != "make governance-report-check":
    missing.append("production readiness scorecard gate mismatch")
required_risk_classes = {"production-ready", "candidate", "report-only", "rollback-required"}
if set(scorecard.get("requiredRiskClasses") or []) != required_risk_classes:
    missing.append("production readiness scorecard risk classes mismatch")
surfaces = scorecard.get("surfaces") or []
if scorecard.get("surfaceCount") != len(surfaces):
    missing.append("production readiness scorecard surfaceCount mismatch")
if len(surfaces) < 10:
    missing.append("production readiness scorecard must contain at least 10 surfaces")
if scorecard.get("missingSources"):
    missing.append(f"production readiness scorecard missing sources: {scorecard['missingSources']}")
seen_surface_ids = set()
seen_risk_classes = set()
required_surface_ids = {
    "stable-api-surface",
    "generated-output",
    "runtime-operations",
    "cloud-native-deployment",
    "security-and-dependencies",
    "performance-budget",
    "release-evidence",
    "adoption-risk",
    "required-checks",
    "plugin-ecosystem",
    "ai-native-support-bundle",
}
for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"production readiness scorecard surface must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    if item_id in seen_surface_ids:
        missing.append(f"duplicate production readiness scorecard surface id: {item_id}")
    seen_surface_ids.add(item_id)
    risk_class = item.get("riskClass", "")
    seen_risk_classes.add(risk_class)
    if risk_class not in required_risk_classes:
        missing.append(f"production readiness scorecard {item_id}: unknown riskClass {risk_class!r}")
    for field in ("id", "riskClass", "source", "gate", "adopterAction"):
        if not item.get(field):
            missing.append(f"production readiness scorecard {item_id or '<missing>'}: {field} is required")
    gate = item.get("gate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        if re.search(rf"^{re.escape(target)}:", makefile, re.M) is None:
            missing.append(f"production readiness scorecard {item_id}: gate target {target!r} missing")
    else:
        missing.append(f"production readiness scorecard {item_id}: gate must be a make target")
    if len(str(item.get("adopterAction") or "").split()) < 10:
        missing.append(f"production readiness scorecard {item_id}: adopterAction must be actionable")
if required_surface_ids != seen_surface_ids:
    missing.append(
        "production readiness scorecard surface ids mismatch: "
        f"missing={sorted(required_surface_ids - seen_surface_ids)} "
        f"extra={sorted(seen_surface_ids - required_surface_ids)}"
    )
if seen_risk_classes != required_risk_classes:
    missing.append(
        "production readiness scorecard does not cover all risk classes: "
        f"missing={sorted(required_risk_classes - seen_risk_classes)}"
    )
traceability = dashboard.get("evidenceTraceability") or {}
if traceability.get("schema") != "gofly.evidence_traceability.v1":
    missing.append("evidence traceability schema mismatch")
if traceability.get("source") != "docs/reference/governance-dashboard-contract.json":
    missing.append("evidence traceability source mismatch")
if traceability.get("gate") != "make governance-report-check":
    missing.append("evidence traceability gate mismatch")
claims = traceability.get("claims") or []
if traceability.get("claimCount") != len(claims):
    missing.append("evidence traceability claimCount mismatch")
if len(claims) < 8:
    missing.append("evidence traceability must contain at least 8 claims")
if traceability.get("missingSources"):
    missing.append(f"evidence traceability missing sources: {traceability['missingSources']}")
required_claim_ids = {
    "stable-surface-contract",
    "release-readiness",
    "required-checks-drift",
    "runtime-operations",
    "generated-upgrade-dry-run",
    "dependency-ownership",
    "performance-budget",
    "production-readiness-scorecard",
    "ai-native-support-workflow",
}
seen_claim_ids = set()
for item in claims:
    if not isinstance(item, dict):
        missing.append(f"evidence traceability claim must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    if item_id in seen_claim_ids:
        missing.append(f"duplicate evidence traceability claim id: {item_id}")
    seen_claim_ids.add(item_id)
    for field in ("id", "claim", "sourceManifest", "reportField", "gate", "riskClass", "rollbackOrEscalation"):
        if not item.get(field):
            missing.append(f"evidence traceability {item_id or '<missing>'}: {field} is required")
    if item.get("riskClass") not in required_risk_classes:
        missing.append(f"evidence traceability {item_id}: unknown riskClass {item.get('riskClass')!r}")
    source = item.get("sourceManifest", "")
    if source and not (root / source).exists():
        missing.append(f"evidence traceability {item_id}: sourceManifest is missing: {source}")
    gate = item.get("gate", "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        if re.search(rf"^{re.escape(target)}:", makefile, re.M) is None:
            missing.append(f"evidence traceability {item_id}: gate target {target!r} missing")
    else:
        missing.append(f"evidence traceability {item_id}: gate must be a make target")
    for field in ("claim", "rollbackOrEscalation"):
        if len(str(item.get(field) or "").split()) < 8:
            missing.append(f"evidence traceability {item_id}: {field} must be actionable")
if seen_claim_ids != required_claim_ids:
    missing.append(
        "evidence traceability claim ids mismatch: "
        f"missing={sorted(required_claim_ids - seen_claim_ids)} "
        f"extra={sorted(seen_claim_ids - required_claim_ids)}"
    )
for output in (dashboard.get("outputs") or []):
    if not output.startswith(".tmp-test/governance-report/"):
        missing.append(f"governance dashboard output must stay under .tmp-test/governance-report: {output}")
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
