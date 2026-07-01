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


def benchmark_surface_policy():
    ratchet = read_json(root / "bench/budget-ratchet.json") or {}
    adopter_contract = ratchet.get("adopterPerformanceContract") or {}
    surfaces = [
        item
        for item in ratchet.get("surfacePolicy") or []
        if isinstance(item, dict)
    ]
    status_counts = {}
    latency_mode_counts = {}
    unsupported = []
    for item in surfaces:
        status_value = item.get("status", "")
        latency_mode = item.get("latencyMode", "")
        status_counts[status_value] = status_counts.get(status_value, 0) + 1
        latency_mode_counts[latency_mode] = latency_mode_counts.get(latency_mode, 0) + 1
        if status_value == "unsupported-report-only":
            unsupported.append(item.get("id", ""))
    return {
        "source": "bench/budget-ratchet.json",
        "gate": ratchet.get("acceptanceGate", "make bench-regression-check"),
        "adopterPerformanceContract": {
            "schema": adopter_contract.get("schema", ""),
            "source": adopter_contract.get("source", ""),
            "dashboardReportField": adopter_contract.get("dashboardReportField", ""),
            "acceptanceGates": adopter_contract.get("acceptanceGates", []),
            "policy": adopter_contract.get("policy", ""),
            "blockingSurfaceCount": len(adopter_contract.get("blockingSurfaces") or []),
            "reportOnlySurfaceCount": len(adopter_contract.get("reportOnlySurfaces") or []),
            "unsupportedSurfaceCount": len(adopter_contract.get("unsupportedSurfaces") or []),
            "promotionRules": adopter_contract.get("promotionRules", []),
            "blockingSurfaces": adopter_contract.get("blockingSurfaces", []),
            "reportOnlySurfaces": adopter_contract.get("reportOnlySurfaces", []),
            "unsupportedSurfaces": adopter_contract.get("unsupportedSurfaces", []),
        },
        "surfaceCount": len(surfaces),
        "statusCounts": status_counts,
        "latencyModeCounts": latency_mode_counts,
        "unsupportedReportOnly": unsupported,
        "surfaces": surfaces,
    }


def aiflow_queue():
    source = ".aiflow/store.json"
    data = read_json(root / source)
    if not isinstance(data, dict):
        source = ".harness/store.json"
        data = read_json(root / source)
    if not isinstance(data, dict):
        return {
            "status": "unavailable",
            "source": ".aiflow/store.json",
            "fallbackSource": ".harness/store.json",
            "reason": "neither .aiflow/store.json nor .harness/store.json is present",
        }
    runs = data.get("runs") or data.get("Runs")
    if not isinstance(runs, dict):
        runs = data
    statuses = {}
    current = []
    for run_id, run in runs.items():
        if not isinstance(run, dict) or not run_id.startswith("GOFLY-"):
            continue
        status_value = run.get("Status", "unknown")
        statuses[status_value] = statuses.get(status_value, 0) + 1
        if status_value in {"pending", "queued", "running"}:
            current.append({
                "id": run_id,
                "status": status_value,
                "priority": run.get("Priority", 0),
                "phase": run.get("Phase", ""),
            })
    current.sort(key=lambda item: (-int(item.get("priority") or 0), item["id"]))
    return {
        "status": "present",
        "source": source,
        "fallbackSource": ".harness/store.json",
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
    evidence_index = read_json(root / "docs/releases/evidence-index.json") or {}
    required_checks = read_json(root / "docs/reference/ci-required-check-evidence.json") or {}
    items = [
        item
        for item in manifest.get("items") or []
        if isinstance(item, dict)
    ]
    evidence_items = [
        item
        for item in evidence_index.get("evidence") or []
        if isinstance(item, dict)
    ]
    drift_contract = manifest.get("driftClosure") or {}
    tag_ci_closure = manifest.get("tagCIClosure") or {}
    tag_ci_stages = [
        item
        for item in tag_ci_closure.get("stages") or []
        if isinstance(item, dict)
    ]
    release_prerequisites = set(required_checks.get("releasePrerequisites") or [])
    covered_ids = []
    explicit_release_artifact_ids = []
    uncovered_ids = []
    index_by_id = {
        item.get("id", ""): item
        for item in evidence_items
        if item.get("id")
    }
    for item in items:
        evidence_id = item.get("id", "")
        producer = (index_by_id.get(evidence_id) or {}).get("producerJob", "")
        if producer in release_prerequisites:
            covered_ids.append(evidence_id)
        elif producer == "release":
            explicit_release_artifact_ids.append(evidence_id)
        else:
            uncovered_ids.append(evidence_id)
    required_ids = [
        item.get("id", "")
        for item in evidence_items
        if item.get("id")
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
        "driftClosure": {
            "schema": drift_contract.get("schema", ""),
            "requiredCheckSource": drift_contract.get("requiredCheckSource", ""),
            "releasePrerequisiteSource": drift_contract.get("releasePrerequisiteSource", ""),
            "driftGate": drift_contract.get("driftGate", ""),
            "dashboardReportField": drift_contract.get("dashboardReportField", ""),
            "policy": drift_contract.get("policy", ""),
            "requiredEvidenceIds": drift_contract.get("requiredEvidenceIds", []),
            "requiredEvidenceCount": len(drift_contract.get("requiredEvidenceIds") or []),
            "releasePrerequisiteCoverage": sorted(covered_ids),
            "explicitReleaseArtifacts": sorted(explicit_release_artifact_ids),
            "uncoveredEvidenceIds": sorted(uncovered_ids),
            "sourceEvidenceIds": sorted(required_ids),
        },
        "tagCIClosure": {
            "schema": tag_ci_closure.get("schema", ""),
            "aiflowTask": tag_ci_closure.get("aiflowTask", ""),
            "acceptanceGate": tag_ci_closure.get("acceptanceGate", ""),
            "dashboardReportField": tag_ci_closure.get("dashboardReportField", ""),
            "policy": tag_ci_closure.get("policy", ""),
            "requiredLocalGates": tag_ci_closure.get("requiredLocalGates", []),
            "requiredEvidenceIds": tag_ci_closure.get("requiredEvidenceIds", []),
            "stageCount": len(tag_ci_stages),
            "stages": tag_ci_stages,
        },
        "items": items,
    }


def release_adoption_contract():
    manifest = read_json(root / "docs/releases/adoption-contract.json") or {}
    decisions = [
        item
        for item in manifest.get("decisions") or []
        if isinstance(item, dict)
    ]
    enforcement_rows = [
        item
        for item in manifest.get("supplyChainEnforcementRows") or []
        if isinstance(item, dict)
    ]
    tag_ci_rows = [
        item
        for item in manifest.get("tagCIClosureRows") or []
        if isinstance(item, dict)
    ]
    risk_counts = {}
    for item in decisions:
        risk_class = item.get("riskClass", "")
        risk_counts[risk_class] = risk_counts.get(risk_class, 0) + 1
    return {
        "schema": manifest.get("schema", ""),
        "manifest": "docs/releases/adoption-contract.json",
        "status": manifest.get("status", ""),
        "sourceOfTruth": manifest.get("sourceOfTruth", ""),
        "acceptanceGate": manifest.get("acceptanceGate", ""),
        "dashboardReportField": manifest.get("dashboardReportField", ""),
        "requiredEvidenceSources": manifest.get("requiredEvidenceSources", []),
        "policy": manifest.get("policy", ""),
        "decisionCount": len(decisions),
        "riskClassCounts": risk_counts,
        "tagCIClosureRowCount": len(tag_ci_rows),
        "tagCIClosureRows": tag_ci_rows,
        "supplyChainEnforcementCount": len(enforcement_rows),
        "supplyChainEnforcementRows": enforcement_rows,
        "decisions": decisions,
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
    p13_closure = manifest.get("p13HostedReleaseSupplyChainClosure") or {}
    p13_rows = [
        item
        for item in p13_closure.get("rows") or []
        if isinstance(item, dict)
    ]
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
        "p13HostedReleaseSupplyChainClosure": {
            "schema": p13_closure.get("schema", ""),
            "aiflowTask": p13_closure.get("aiflowTask", ""),
            "status": p13_closure.get("status", ""),
            "acceptanceGates": p13_closure.get("acceptanceGates", []),
            "sourceContracts": p13_closure.get("sourceContracts", []),
            "releaseJob": p13_closure.get("releaseJob", ""),
            "uploadArtifact": p13_closure.get("uploadArtifact", ""),
            "requiredArtifactFamilies": p13_closure.get("requiredArtifactFamilies", []),
            "artifactFamilyCount": len(p13_closure.get("requiredArtifactFamilies") or []),
            "rowCount": len(p13_rows),
            "rows": p13_rows,
            "forbiddenSkipPolicy": p13_closure.get("forbiddenSkipPolicy", ""),
            "dashboardReportField": p13_closure.get("dashboardReportField", ""),
            "completionPolicy": p13_closure.get("completionPolicy", ""),
        },
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
    adopter_proof = manifest.get("adopterUpgradeProof") or {}
    proof_paths = [
        item
        for item in adopter_proof.get("paths") or []
        if isinstance(item, dict)
    ]
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
        "adopterUpgradeProof": {
            "schema": adopter_proof.get("schema", ""),
            "source": adopter_proof.get("source", ""),
            "compatibilityMatrix": adopter_proof.get("compatibilityMatrix", ""),
            "acceptanceGates": adopter_proof.get("acceptanceGates", []),
            "dashboardReportField": adopter_proof.get("dashboardReportField", ""),
            "policy": adopter_proof.get("policy", ""),
            "pathCount": len(proof_paths),
            "profiles": [
                item.get("profile", "")
                for item in proof_paths
                if item.get("profile")
            ],
            "paths": proof_paths,
        },
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
        "releaseAdoptionContract": manifest.get("releaseAdoptionContract", {}),
        "releaseDashboardConsumption": release_dashboard_consumption(
            manifest.get("releaseDashboardConsumption", {})
        ),
        "benchmarkRatchet": manifest.get("benchmarkRatchet", {}),
        "coverage": manifest.get("coverage", {}),
        "security": manifest.get("security", {}),
        "releaseEvidenceConsumption": manifest.get("releaseEvidenceConsumption", {}),
        "productionDefaults": manifest.get("productionDefaults", {}),
        "governanceConvergence": manifest.get("governanceConvergence", {}),
        "aiflow": manifest.get("aiflow", {}),
        "aiNativeWorkflow": manifest.get("aiNativeWorkflow", {}),
        "productionReadinessScorecard": production_readiness_scorecard(manifest.get("productionReadinessScorecard", {})),
        "evidenceTraceability": evidence_traceability(manifest.get("evidenceTraceability", {})),
        "outputs": manifest.get("outputs", []),
    }


def release_dashboard_consumption(contract):
    rows = [
        item
        for item in contract.get("consumerRows") or []
        if isinstance(item, dict)
    ]
    field_refs = sorted({
        field
        for item in rows
        for field in item.get("sourceReportFields") or []
        if field
    })
    return {
        "schema": contract.get("schema", ""),
        "aiflowTask": contract.get("aiflowTask", ""),
        "status": contract.get("status", ""),
        "source": contract.get("source", ""),
        "sourceReport": contract.get("sourceReport", ""),
        "reportField": contract.get("reportField", ""),
        "acceptanceGate": contract.get("acceptanceGate", ""),
        "driftGate": contract.get("driftGate", ""),
        "owner": contract.get("owner", ""),
        "policy": contract.get("policy", ""),
        "requiredGates": contract.get("requiredGates", []),
        "requiredGateCount": len(contract.get("requiredGates") or []),
        "requiredStableFields": contract.get("requiredStableFields", []),
        "stableFieldCount": len(contract.get("requiredStableFields") or []),
        "consumerRowCount": len(rows),
        "consumerRows": rows,
        "consumerFieldRefs": field_refs,
        "runtimeStatePolicy": contract.get("runtimeStatePolicy", ""),
        "commitPolicy": contract.get("commitPolicy", ""),
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
        "aiflowExecution": manifest.get("aiflowExecution", {}),
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
    remediation_handoff = manifest.get("remediationHandoff") or {}
    adoption_loop = manifest.get("troubleshootingAdoptionLoop") or {}
    remediation_loop = manifest.get("remediationLoopContract") or {}
    p10_support = manifest.get("p10AINativeSupportBundle") or {}
    p13_closeout = manifest.get("p13CliDoctorTroubleshootingLoop") or {}
    adoption_steps = [
        item
        for item in adoption_loop.get("steps") or []
        if isinstance(item, dict)
    ]
    source_contracts = [
        item
        for item in remediation_loop.get("sourceContracts") or []
        if isinstance(item, dict)
    ]
    migration_routes = [
        item
        for item in remediation_loop.get("migrationRoutes") or []
        if isinstance(item, dict)
    ]
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
        "remediationHandoff": {
            "schema": remediation_handoff.get("schema", ""),
            "aiflowTask": remediation_handoff.get("aiflowTask", ""),
            "supersedes": remediation_handoff.get("supersedes", []),
            "owner": remediation_handoff.get("owner", ""),
            "commitPolicy": remediation_handoff.get("commitPolicy", ""),
            "allowedActions": remediation_handoff.get("allowedActions", []),
            "forbiddenActions": remediation_handoff.get("forbiddenActions", []),
            "inputs": remediation_handoff.get("inputs", []),
            "requiredGates": remediation_handoff.get("requiredGates", []),
            "outputFields": remediation_handoff.get("outputFields", []),
            "completionPolicy": remediation_handoff.get("completionPolicy", ""),
        },
        "p9RemediationCloseout": manifest.get("p9RemediationCloseout", {}),
        "p10AINativeSupportBundle": p10_support,
        "p13CliDoctorTroubleshootingLoop": p13_closeout,
        "troubleshootingAdoptionLoop": {
            "schema": adoption_loop.get("schema", ""),
            "acceptanceGate": adoption_loop.get("acceptanceGate", ""),
            "dashboardReportField": adoption_loop.get("dashboardReportField", ""),
            "aiflowHandoff": adoption_loop.get("aiflowHandoff", ""),
            "commitOwner": adoption_loop.get("commitOwner", ""),
            "runtimeStatePolicy": adoption_loop.get("runtimeStatePolicy", ""),
            "stepCount": len(adoption_steps),
            "steps": adoption_steps,
        },
        "remediationLoopContract": {
            "schema": remediation_loop.get("schema", ""),
            "acceptanceGate": remediation_loop.get("acceptanceGate", ""),
            "dashboardReportField": remediation_loop.get("dashboardReportField", ""),
            "purpose": remediation_loop.get("purpose", ""),
            "sourceContractCount": len(source_contracts),
            "migrationRouteCount": len(migration_routes),
            "sourceContracts": source_contracts,
            "migrationRoutes": migration_routes,
            "dashboardEvidence": remediation_loop.get("dashboardEvidence", []),
            "aiflowQueuePolicy": remediation_loop.get("aiflowQueuePolicy", {}),
            "nextActionPolicy": remediation_loop.get("nextActionPolicy", ""),
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
        "trendSummary": "bench/summary.md",
        "benchmarks": count_benchmarks(),
        "evidenceStatus": status("bench/evidence.md"),
        "regressionReportStatus": status("bench/regression-report.json"),
        "trendSummaryStatus": status("bench/summary.md"),
        "surfacePolicy": benchmark_surface_policy(),
    },
    "security": security_evidence(),
    "release": release_evidence(),
    "releaseEvidenceConsumption": release_evidence_consumption(),
    "releaseAdoptionContract": release_adoption_contract(),
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
    f"- Benchmark trend summary: `{report['benchmark']['trendSummaryStatus']}`",
    f"- Benchmark surface policies: `{report['benchmark']['surfacePolicy']['surfaceCount']}`",
    f"- Release evidence schema: `{report['release']['schema']}`",
    f"- Release evidence index items: `{report['release']['evidenceCount']}`",
    f"- Release evidence consumption items: `{report['releaseEvidenceConsumption']['itemCount']}`",
    f"- Release adoption decisions: `{report['releaseAdoptionContract']['decisionCount']}`",
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
p13_supply_chain = report["ciRequiredChecks"].get("p13HostedReleaseSupplyChainClosure") or {}
if p13_supply_chain.get("schema") != "gofly.hosted_release_supply_chain_p13.v1":
    missing.append("CI required-check P13 hosted release supply-chain schema mismatch")
if p13_supply_chain.get("aiflowTask") != "GOFLY-P13-12-HOSTED-RELEASE-SUPPLY-CHAIN":
    missing.append("CI required-check P13 hosted release supply-chain aiflowTask mismatch")
if p13_supply_chain.get("status") != "release-blocking-contract":
    missing.append("CI required-check P13 hosted release supply-chain status mismatch")
for gate in ("make required-checks-drift-check", "make governance-report-check"):
    if gate not in set(p13_supply_chain.get("acceptanceGates") or []):
        missing.append(f"CI required-check P13 hosted release supply-chain acceptanceGates missing {gate!r}")
expected_p13_artifact_families = {
    "checksums",
    "sbom",
    "provenance",
    "docker-digest",
    "trivy",
    "release-evidence-manifest",
    "required-check-drift",
}
if set(p13_supply_chain.get("requiredArtifactFamilies") or []) != expected_p13_artifact_families:
    missing.append("CI required-check P13 hosted release supply-chain requiredArtifactFamilies mismatch")
if p13_supply_chain.get("artifactFamilyCount") != len(expected_p13_artifact_families):
    missing.append("CI required-check P13 hosted release supply-chain artifactFamilyCount mismatch")
if p13_supply_chain.get("rowCount") != len(expected_p13_artifact_families):
    missing.append("CI required-check P13 hosted release supply-chain rowCount mismatch")
if p13_supply_chain.get("releaseJob") != "release":
    missing.append("CI required-check P13 hosted release supply-chain releaseJob mismatch")
if p13_supply_chain.get("uploadArtifact") != "release-dist-evidence":
    missing.append("CI required-check P13 hosted release supply-chain uploadArtifact mismatch")
if p13_supply_chain.get("dashboardReportField") != "ciRequiredChecks.p13HostedReleaseSupplyChainClosure":
    missing.append("CI required-check P13 hosted release supply-chain dashboardReportField mismatch")
for contract in (
    "hostedReleaseEvidence",
    "releasePrerequisiteDrift",
    "docs/releases/evidence-index.json",
    "docs/releases/evidence-consumption.json",
    "docs/releases/adoption-contract.json",
):
    if contract not in set(p13_supply_chain.get("sourceContracts") or []):
        missing.append(f"CI required-check P13 hosted release supply-chain sourceContracts missing {contract!r}")
p13_rows = {
    item.get("id"): item
    for item in p13_supply_chain.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
if set(p13_rows) != expected_p13_artifact_families:
    missing.append("CI required-check P13 hosted release supply-chain rows mismatch")
for row_id, row in p13_rows.items():
    for field in ("hostedEvidenceRows", "releaseEvidenceIds", "producerJobs", "localGate", "workflowMarkers", "blockDecision", "rollbackOrEscalation"):
        if not row.get(field):
            missing.append(f"CI required-check P13 hosted release supply-chain {row_id}: {field} is required")
    for field in ("blockDecision", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 10:
            missing.append(f"CI required-check P13 hosted release supply-chain {row_id}: {field} must be actionable")
for needle in ("Tag release CI", "release artifact upload", "checksums", "SBOM", "provenance", "Docker digest", "Trivy", "required-check drift", "governance dashboard"):
    if needle not in str(p13_supply_chain.get("forbiddenSkipPolicy") or ""):
        missing.append(f"CI required-check P13 hosted release supply-chain forbiddenSkipPolicy missing {needle!r}")
for needle in ("GOFLY-P13-12-HOSTED-RELEASE-SUPPLY-CHAIN", "required-checks-drift-check", "governance-report-check", "hosted release artifact proof", "dashboard"):
    if needle not in str(p13_supply_chain.get("completionPolicy") or ""):
        missing.append(f"CI required-check P13 hosted release supply-chain completionPolicy missing {needle!r}")
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

handoff = dx_support_bundle.get("remediationHandoff") or {}
if handoff.get("schema") != "gofly.remediation_handoff.v1":
    missing.append("dx support bundle remediation handoff schema mismatch")
if handoff.get("aiflowTask") != "GOFLY-GOV-10P9-09":
    missing.append("dx support bundle remediation handoff aiflowTask mismatch")
for previous in ("GOFLY-KG-07-AI-REMEDIATION-AIFLOW", "GOFLY-GOV-10R7-09"):
    if previous not in set(handoff.get("supersedes") or []):
        missing.append(f"dx support bundle remediation handoff supersedes missing {previous!r}")
if handoff.get("owner") != "human-or-current-agent":
    missing.append("dx support bundle remediation handoff owner mismatch")
commit_policy = str(handoff.get("commitPolicy") or "")
for needle in ("must not create commits", "current agent or human", "gates pass"):
    if needle not in commit_policy:
        missing.append(f"dx support bundle remediation handoff commitPolicy missing {needle!r}")
for action in ("git commit", "git push", "modify docs/superpowers/", "stage runtime state"):
    if action not in set(handoff.get("forbiddenActions") or []):
        missing.append(f"dx support bundle remediation handoff forbiddenActions missing {action!r}")
for gate in ("make dx-troubleshooting-check", "make governance-report-check"):
    if gate not in set(handoff.get("requiredGates") or []):
        missing.append(f"dx support bundle remediation handoff requiredGates missing {gate!r}")
if not any("TestCLIJSONContractGoldens" in gate for gate in handoff.get("requiredGates") or []):
    missing.append("dx support bundle remediation handoff must include CLI JSON contract tests")
for field in ("taskId", "sourceCommand", "remediation", "nextActions", "gates", "commitPolicy"):
    if field not in set(handoff.get("outputFields") or []):
        missing.append(f"dx support bundle remediation handoff outputFields missing {field!r}")
if "no runtime state staged" not in str(handoff.get("completionPolicy") or ""):
    missing.append("dx support bundle remediation handoff completionPolicy must reject runtime state staging")
if dx_support_bundle.get("remediationHintCount") != len(dx_support_bundle.get("remediationHints") or []):
    missing.append("dx support bundle remediationHintCount mismatch")
if dx_support_bundle.get("remediationHintCount", 0) < 4:
    missing.append("dx support bundle remediation hints are incomplete")

p9_closeout = dx_support_bundle.get("p9RemediationCloseout") or {}
if p9_closeout.get("schema") != "gofly.p9_remediation_closeout.v1":
    missing.append("dx support bundle P9 remediation closeout schema mismatch")
if p9_closeout.get("aiflowTask") != "GOFLY-GOV-10P9-09":
    missing.append("dx support bundle P9 remediation closeout aiflowTask mismatch")
if p9_closeout.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("dx support bundle P9 remediation closeout acceptanceGate mismatch")
for source in ("nextActions", "error.remediation", "data.nextActions"):
    if source not in set(p9_closeout.get("requiredNextActionSources") or []):
        missing.append(f"dx support bundle P9 remediation closeout missing next action source {source!r}")

p10_support = dx_support_bundle.get("p10AINativeSupportBundle") or {}
if p10_support.get("schema") != "gofly.ai_native_support_bundle_p10.v1":
    missing.append("dx support bundle P10 AI-native support schema mismatch")
if p10_support.get("aiflowTask") != "GOFLY-P10-6-AI_NATIVE_SUPPORT_BUNDLE":
    missing.append("dx support bundle P10 AI-native support aiflowTask mismatch")
if p10_support.get("status") != "blocking-contract":
    missing.append("dx support bundle P10 AI-native support status mismatch")
if p10_support.get("acceptanceGate") != "make governance-report-check":
    missing.append("dx support bundle P10 AI-native support acceptanceGate mismatch")
for command in (
    "gofly doctor --json",
    "gofly release check --json --strict",
    "gofly bug --json",
    "gofly ai control-plane --json",
    "gofly ai new --json --apply --verify",
):
    if command not in set(p10_support.get("sourceSurfaces") or []):
        missing.append(f"dx support bundle P10 AI-native support sourceSurfaces missing {command!r}")
for field in (
    "dxSupportBundle.surfaceCount",
    "dxSupportBundle.remediationHintCount",
    "dxSupportBundle.remediationHandoff.schema",
    "dxSupportBundle.p10AINativeSupportBundle.status",
    "dashboard.evidenceTraceability.claimCount",
    "releaseEvidence.evidenceCount",
):
    if field not in set(p10_support.get("dashboardFields") or []):
        missing.append(f"dx support bundle P10 AI-native support dashboardFields missing {field!r}")
for evidence in (
    "docs/reference/control-plane-contracts.md",
    "docs/case-studies/ai-control-plane-drift.md",
    "make generated-control-plane-smoke",
):
    if evidence not in set(p10_support.get("controlPlaneEvidence") or []):
        missing.append(f"dx support bundle P10 AI-native support controlPlaneEvidence missing {evidence!r}")
workflow_rows = {
    item.get("id"): item
    for item in p10_support.get("workflowRows") or []
    if isinstance(item, dict) and item.get("id")
}
for row_id in ("diagnose", "release-readiness", "support-bundle", "control-plane", "generated-failure"):
    row = workflow_rows.get(row_id) or {}
    if not row:
        missing.append(f"dx support bundle P10 AI-native support workflowRows missing {row_id!r}")
        continue
    for key in ("sourceCommand", "nextActionSource", "handoffAction"):
        if not row.get(key):
            missing.append(f"dx support bundle P10 AI-native support {row_id}: {key} is required")
    if len(str(row.get("handoffAction") or "").split()) < 8:
        missing.append(f"dx support bundle P10 AI-native support {row_id}: handoffAction must be actionable")
for needle in ("Authorization", "Cookie", "Set-Cookie", "token", "secret", "password"):
    if needle not in str(p10_support.get("redactionPolicy") or ""):
        missing.append(f"dx support bundle P10 AI-native support redactionPolicy missing {needle!r}")
for needle in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "docs/superpowers"):
    if needle not in str(p10_support.get("runtimeStatePolicy") or ""):
        missing.append(f"dx support bundle P10 AI-native support runtimeStatePolicy missing {needle!r}")
for needle in ("aiflow", "bounded failure reports", "commits remain owned", "gates pass"):
    if needle not in str(p10_support.get("commitPolicy") or ""):
        missing.append(f"dx support bundle P10 AI-native support commitPolicy missing {needle!r}")

p13_closeout = dx_support_bundle.get("p13CliDoctorTroubleshootingLoop") or {}
if p13_closeout.get("schema") != "gofly.cli_doctor_troubleshooting_p13.v1":
    missing.append("dx support bundle P13 CLI doctor troubleshooting schema mismatch")
if p13_closeout.get("aiflowTask") != "GOFLY-P13-11-CLI-DOCTOR-TROUBLESHOOTING-LOOP":
    missing.append("dx support bundle P13 CLI doctor troubleshooting aiflowTask mismatch")
if p13_closeout.get("status") != "blocking-contract":
    missing.append("dx support bundle P13 CLI doctor troubleshooting status mismatch")
for gate in ("make dx-troubleshooting-check", "make cli-json-contract-goldens-check", "make governance-report-check"):
    if gate not in set(p13_closeout.get("acceptanceGates") or []):
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting acceptanceGates missing {gate!r}")
for command in required_dx_commands:
    if command not in set(p13_closeout.get("requiredSourceSurfaces") or []):
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting requiredSourceSurfaces missing {command!r}")
for source in ("nextActions", "error.remediation", "data.nextActions"):
    if source not in set(p13_closeout.get("requiredNextActionSources") or []):
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting requiredNextActionSources missing {source!r}")
runtime_evidence = " ".join(p13_closeout.get("runtimeEvidence") or [])
for needle in ("fix_hint", "error.remediation", "gofly.support_bundle.v1", "bounded output", "data.nextActions"):
    if needle not in runtime_evidence:
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting runtimeEvidence missing {needle!r}")
p13_rows = {
    item.get("id"): item
    for item in p13_closeout.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p13_rows = {
    "doctor-json": ("gofly doctor --json", "nextActions", "make dx-troubleshooting-check"),
    "release-check-json": ("gofly release check --json --strict", "error.remediation", "make dx-troubleshooting-check"),
    "support-bundle-json": ("gofly bug --json", "nextActions", "make dx-troubleshooting-check"),
    "generated-failure-report": ("gofly ai new --json --apply --verify", "data.nextActions", "make cli-json-contract-goldens-check"),
}
if set(p13_rows) != set(expected_p13_rows):
    missing.append("dx support bundle P13 CLI doctor troubleshooting rows mismatch")
for row_id, (command, next_source, gate) in expected_p13_rows.items():
    row = p13_rows.get(row_id) or {}
    if row.get("sourceCommand") != command:
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting {row_id}: sourceCommand mismatch")
    if row.get("nextActionSource") != next_source:
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting {row_id}: nextActionSource mismatch")
    if row.get("gate") != gate:
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting {row_id}: gate mismatch")
    for field in ("stableFields", "failureMode", "remediation"):
        if not row.get(field):
            missing.append(f"dx support bundle P13 CLI doctor troubleshooting {row_id}: {field} is required")
p13_handoff = p13_closeout.get("aiflowHandoff") or {}
if p13_handoff.get("schema") != "gofly.remediation_handoff.v1":
    missing.append("dx support bundle P13 CLI doctor troubleshooting handoff schema mismatch")
if p13_handoff.get("owner") != "human-or-current-agent":
    missing.append("dx support bundle P13 CLI doctor troubleshooting handoff owner mismatch")
for action in ("git commit", "git push", "modify docs/superpowers/", "stage runtime state"):
    if action not in set(p13_handoff.get("forbiddenActions") or []):
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting forbiddenActions missing {action!r}")
for needle in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/current.txt", "bench/regression-report.json", "bench/summary.md", "bin/gofly", "docs/superpowers"):
    if needle not in str(p13_closeout.get("runtimeStatePolicy") or ""):
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting runtimeStatePolicy missing {needle!r}")
for needle in ("GOFLY-P13-11-CLI-DOCTOR-TROUBLESHOOTING-LOOP", "dx-troubleshooting-check", "CLI JSON golden", "governance-report-check", "no runtime state"):
    if needle not in str(p13_closeout.get("completionPolicy") or ""):
        missing.append(f"dx support bundle P13 CLI doctor troubleshooting completionPolicy missing {needle!r}")

adoption_loop = dx_support_bundle.get("troubleshootingAdoptionLoop") or {}
if adoption_loop.get("schema") != "gofly.troubleshooting_adoption_loop.v1":
    missing.append("dx support bundle troubleshooting adoption loop schema mismatch")
if adoption_loop.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("dx support bundle troubleshooting adoption loop acceptanceGate mismatch")
if adoption_loop.get("dashboardReportField") != "dxSupportBundle.troubleshootingAdoptionLoop":
    missing.append("dx support bundle troubleshooting adoption loop dashboardReportField mismatch")
if adoption_loop.get("aiflowHandoff") != "gofly.remediation_handoff.v1":
    missing.append("dx support bundle troubleshooting adoption loop aiflowHandoff mismatch")
if adoption_loop.get("commitOwner") != "human-or-current-agent":
    missing.append("dx support bundle troubleshooting adoption loop commitOwner mismatch")
runtime_policy = str(adoption_loop.get("runtimeStatePolicy") or "")
for needle in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "docs/superpowers"):
    if needle not in runtime_policy:
        missing.append(f"dx support bundle troubleshooting adoption loop runtimeStatePolicy missing {needle!r}")
expected_adoption_steps = {
    "diagnose-environment": "gofly doctor --json",
    "check-release-gates": "gofly release check --json --strict",
    "collect-support-bundle": "gofly bug --json",
    "attach-generated-failure": "gofly ai new --json --apply --verify",
}
steps_by_id = {
    item.get("id"): item
    for item in adoption_loop.get("steps") or []
    if isinstance(item, dict) and item.get("id")
}
if adoption_loop.get("stepCount") != len(expected_adoption_steps):
    missing.append("dx support bundle troubleshooting adoption loop stepCount mismatch")
if set(steps_by_id) != set(expected_adoption_steps):
    missing.append(
        "dx support bundle troubleshooting adoption loop steps mismatch: "
        f"missing={sorted(set(expected_adoption_steps) - set(steps_by_id))} "
        f"extra={sorted(set(steps_by_id) - set(expected_adoption_steps))}"
    )
for step_id, command in expected_adoption_steps.items():
    step = steps_by_id.get(step_id) or {}
    if step.get("sourceCommand") != command:
        missing.append(f"dx support bundle troubleshooting adoption loop {step_id}: sourceCommand mismatch")
    for field in ("evidenceArtifact", "requiredFields", "nextActionSource", "handoffBoundary"):
        if not step.get(field):
            missing.append(f"dx support bundle troubleshooting adoption loop {step_id}: {field} is required")
    if len(str(step.get("handoffBoundary") or "").split()) < 10:
        missing.append(f"dx support bundle troubleshooting adoption loop {step_id}: handoffBoundary must be actionable")
remediation_loop = dx_support_bundle.get("remediationLoopContract") or {}
if remediation_loop.get("schema") != "gofly.remediation_loop_contract.v1":
    missing.append("dx support bundle remediation loop contract schema mismatch")
if remediation_loop.get("acceptanceGate") != "make dx-troubleshooting-check":
    missing.append("dx support bundle remediation loop contract acceptanceGate mismatch")
if remediation_loop.get("dashboardReportField") != "dxSupportBundle.remediationLoopContract":
    missing.append("dx support bundle remediation loop contract dashboardReportField mismatch")
if remediation_loop.get("sourceContractCount") != 4:
    missing.append("dx support bundle remediation loop contract must track 4 source contracts")
if remediation_loop.get("migrationRouteCount") != 4:
    missing.append("dx support bundle remediation loop contract must track 4 migration routes")
expected_source_contracts = {
    "doctor-json": "gofly doctor --json",
    "release-check-json": "gofly release check --json --strict",
    "support-bundle-json": "gofly bug --json",
    "generated-failure-report": "gofly ai new --json --apply --verify",
}
source_contracts_by_id = {
    item.get("id"): item
    for item in remediation_loop.get("sourceContracts") or []
    if isinstance(item, dict) and item.get("id")
}
if set(source_contracts_by_id) != set(expected_source_contracts):
    missing.append("dx support bundle remediation loop source contracts mismatch")
for source_id, command in expected_source_contracts.items():
    item = source_contracts_by_id.get(source_id) or {}
    if item.get("sourceCommand") != command:
        missing.append(f"dx support bundle remediation loop {source_id}: sourceCommand mismatch")
    for field in ("evidenceArtifact", "requiredFields", "nextActionSource", "dashboardFields", "adopterAction"):
        if not item.get(field):
            missing.append(f"dx support bundle remediation loop {source_id}: {field} is required")
expected_migration_routes = {"gin-to-gofly", "go-zero-to-gofly", "kratos-to-gofly", "kitex-with-gofly"}
migration_routes_by_id = {
    item.get("id"): item
    for item in remediation_loop.get("migrationRoutes") or []
    if isinstance(item, dict) and item.get("id")
}
if set(migration_routes_by_id) != expected_migration_routes:
    missing.append("dx support bundle remediation loop migration routes mismatch")
for route_id, item in migration_routes_by_id.items():
    for field in ("example", "gate", "rollbackAction", "supportBundleAction"):
        if not item.get(field):
            missing.append(f"dx support bundle remediation loop {route_id}: {field} is required")
dashboard_evidence = set(remediation_loop.get("dashboardEvidence") or [])
for field in (
    "dxSupportBundle.remediationHandoff.schema",
    "dxSupportBundle.remediationHandoff.aiflowTask",
    "dxSupportBundle.troubleshootingAdoptionLoop",
    "releaseAdoptionContract.supplyChainEnforcementCount",
    "generatedUpgradeDryRun.profileCount",
    "benchmark.adopterPerformanceContract",
):
    if field not in dashboard_evidence:
        missing.append(f"dx support bundle remediation loop dashboardEvidence missing {field!r}")
queue_policy = remediation_loop.get("aiflowQueuePolicy") or {}
if queue_policy.get("taskPrefix") != "GOFLY-GOV-10P9-09":
    missing.append("dx support bundle remediation loop aiflow taskPrefix mismatch")
if "GOFLY-GOV-10R7-09" not in set(queue_policy.get("supersedes") or []):
    missing.append("dx support bundle remediation loop aiflow supersedes mismatch")
if queue_policy.get("commitOwner") != "human-or-current-agent":
    missing.append("dx support bundle remediation loop commit owner mismatch")
if (dx_support_bundle.get("controlPlaneEvidence") or {}).get("status") != "present":
    missing.append("dx support bundle control-plane evidence is missing")
convergence = report["governanceConvergence"]
if convergence["schema"] != "gofly.governance_boundary_inventory.v1":
    missing.append("governance convergence schema mismatch")
if convergence["gate"] != "make governance-boundary-inventory-check":
    missing.append("governance convergence gate mismatch")
if convergence["finalGate"] != "make governance-10-rounds":
    missing.append("governance convergence finalGate mismatch")
if convergence["taskCount"] != 3:
    missing.append("governance convergence must track the active P14 task set")
active_aiflow_batch = convergence.get("activeAiflowBatch", "")
if not active_aiflow_batch:
    missing.append("governance convergence activeAiflowBatch is required")
expected_round_ids = [
    "GOFLY-P14-01-P13-COMPLETION-HANDOFF",
    "GOFLY-P14-02-RPC-RELEASE-TRAIN-EVIDENCE",
    "GOFLY-P14-03-GENERATOR-ADOPTER-REPLAY-EVIDENCE",
]
if active_aiflow_batch != "GOFLY-P14":
    missing.append("governance convergence activeAiflowBatch must be GOFLY-P14")
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
    for field in ("id", "round", "title", "gate", "status", "priority", "commitPolicy"):
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
observability_execution = observability_governance.get("aiflowExecution") or {}
if observability_execution.get("status") != "aiflow-driven":
    missing.append("observability production governance aiflowExecution.status must be aiflow-driven")
if "GOFLY-GOV-10R3-09" not in str(observability_execution.get("driver") or ""):
    missing.append("observability production governance aiflowExecution.driver mismatch")
observability_completion_policy = str(observability_execution.get("completionPolicy") or "")
for needle in ("make governance-report-check", "make runtime-slo-check", "commit"):
    if needle not in observability_completion_policy:
        missing.append(f"observability production governance completionPolicy missing {needle!r}")
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
adopter_upgrade_proof = report["generatedUpgradeDryRun"].get("adopterUpgradeProof") or {}
if adopter_upgrade_proof.get("schema") != "gofly.generated_adopter_upgrade_proof.v1":
    missing.append("generated adopter upgrade proof schema mismatch")
if adopter_upgrade_proof.get("source") != "docs/reference/generated-upgrade-dry-run.json":
    missing.append("generated adopter upgrade proof source mismatch")
if adopter_upgrade_proof.get("compatibilityMatrix") != "testdata/generated-compat/matrix.json":
    missing.append("generated adopter upgrade proof compatibilityMatrix mismatch")
if set(adopter_upgrade_proof.get("acceptanceGates") or []) != {
    "make generated-upgrade-dry-run-check",
    "make generated-version-compat-check",
}:
    missing.append("generated adopter upgrade proof acceptanceGates mismatch")
if adopter_upgrade_proof.get("dashboardReportField") != "generatedUpgradeDryRun.adopterUpgradeProof":
    missing.append("generated adopter upgrade proof dashboardReportField mismatch")
if len(str(adopter_upgrade_proof.get("policy") or "").split()) < 20:
    missing.append("generated adopter upgrade proof policy must be actionable")
if adopter_upgrade_proof.get("pathCount") != 3:
    missing.append("generated adopter upgrade proof pathCount mismatch")
if set(adopter_upgrade_proof.get("profiles") or []) != {"old", "current", "future"}:
    missing.append("generated adopter upgrade proof profiles mismatch")
for proof in adopter_upgrade_proof.get("paths") or []:
    if not isinstance(proof, dict):
        missing.append(f"generated adopter upgrade proof path must be an object: {proof!r}")
        continue
    profile = proof.get("profile", "")
    for field in ("adopterDecision", "compatibilityGate", "dryRunGate", "expectedDiff", "dependencyBoundary", "rollbackAction"):
        if not proof.get(field):
            missing.append(f"generated adopter upgrade proof {profile or '<missing>'}: {field} is required")
    if proof.get("compatibilityGate") != "make generated-version-compat-check":
        missing.append(f"generated adopter upgrade proof {profile}: compatibilityGate mismatch")
    if proof.get("dryRunGate") != "make generated-upgrade-dry-run-check":
        missing.append(f"generated adopter upgrade proof {profile}: dryRunGate mismatch")
if report["benchmark"]["evidenceStatus"] != "present":
    missing.append("benchmark evidence is missing")
if report["benchmark"]["trendSummaryStatus"] != "present":
    missing.append("benchmark trend summary is missing")
if report["benchmark"]["regressionReportStatus"] != "present":
    missing.append("benchmark regression report is missing")
surface_policy = report["benchmark"].get("surfacePolicy") or {}
if surface_policy.get("gate") != "make bench-regression-check":
    missing.append("benchmark surface policy gate mismatch")
if surface_policy.get("surfaceCount", 0) < 5:
    missing.append("benchmark surface policy must track REST, governance, RPC, gateway and cache surfaces")
adopter_performance = surface_policy.get("adopterPerformanceContract") or {}
if adopter_performance.get("schema") != "gofly.benchmark_adopter_performance_contract.v1":
    missing.append("benchmark adopter performance contract schema mismatch")
if adopter_performance.get("source") != "bench/budget-ratchet.json":
    missing.append("benchmark adopter performance contract source mismatch")
if adopter_performance.get("dashboardReportField") != "benchmark.adopterPerformanceContract":
    missing.append("benchmark adopter performance contract dashboardReportField mismatch")
if set(adopter_performance.get("acceptanceGates") or []) != {
    "make bench-regression-check",
    "make bench-evidence-check",
    "make bench-trend",
}:
    missing.append("benchmark adopter performance contract acceptanceGates mismatch")
if len(str(adopter_performance.get("policy") or "").split()) < 20:
    missing.append("benchmark adopter performance contract policy must be actionable")
if adopter_performance.get("blockingSurfaceCount") != 2:
    missing.append("benchmark adopter performance contract blockingSurfaceCount mismatch")
if adopter_performance.get("reportOnlySurfaceCount") != 3:
    missing.append("benchmark adopter performance contract reportOnlySurfaceCount mismatch")
if adopter_performance.get("unsupportedSurfaceCount") != 0:
    missing.append("benchmark adopter performance contract unsupportedSurfaceCount mismatch")
if not {"minimum 5 baseline samples", "minimum 3 current trend samples", "no allocation regression under bench-regression-check"}.issubset(set(adopter_performance.get("promotionRules") or [])):
    missing.append("benchmark adopter performance contract promotionRules missing required gates")
for collection, expected_ids in (
    ("blockingSurfaces", {"rest-route-hot-path", "governance-rule-match"}),
    ("reportOnlySurfaces", {"http-latency-report-only", "rpc-candidate-report-only", "gateway-cache-candidate-report-only"}),
    ("unsupportedSurfaces", set()),
):
    items = [
        item
        for item in adopter_performance.get(collection) or []
        if isinstance(item, dict)
    ]
    actual_ids = {
        item.get("id", "")
        for item in items
        if item.get("id")
    }
    if actual_ids != expected_ids:
        missing.append(f"benchmark adopter performance contract {collection} ids mismatch")
    for item in items:
        item_id = item.get("id", "")
        for field in ("adopterAction", "rollbackAction"):
            if len(str(item.get(field) or "").split()) < 10:
                missing.append(f"benchmark adopter performance contract {item_id}: {field} must be actionable")
        if collection == "unsupportedSurfaces" and len(str(item.get("requiredEvidence") or "").split()) < 8:
            missing.append(f"benchmark adopter performance contract {item_id}: requiredEvidence must be actionable")
required_surface_policy_statuses = {
    "allocation-blocking",
    "latency-and-allocation-blocking",
    "candidate",
}
if not required_surface_policy_statuses.issubset(set((surface_policy.get("statusCounts") or {}).keys())):
    missing.append("benchmark surface policy statusCounts missing required status classes")
if surface_policy.get("unsupportedReportOnly"):
    missing.append("benchmark surface policy unsupported surfaces must be promoted or removed after P13-06")
for item in surface_policy.get("surfaces") or []:
    if not isinstance(item, dict):
        missing.append(f"benchmark surface policy item must be an object: {item!r}")
        continue
    item_id = item.get("id", "")
    for field in ("id", "surface", "status", "latencyMode", "promotionGate"):
        if not item.get(field):
            missing.append(f"benchmark surface policy {item_id or '<missing>'}: {field} is required")
    if item.get("promotionGate") != "make bench-regression-check":
        missing.append(f"benchmark surface policy {item_id}: promotionGate mismatch")
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
drift_closure = consumption.get("driftClosure") or {}
if drift_closure.get("schema") != "gofly.release_evidence_drift_closure.v1":
    missing.append("release evidence drift closure schema mismatch")
if drift_closure.get("requiredCheckSource") != "docs/reference/ci-required-check-evidence.json":
    missing.append("release evidence drift closure requiredCheckSource mismatch")
if drift_closure.get("driftGate") != "make required-checks-drift-check":
    missing.append("release evidence drift closure driftGate mismatch")
if drift_closure.get("dashboardReportField") != "releaseEvidenceConsumption.driftClosure":
    missing.append("release evidence drift closure dashboardReportField mismatch")
if len(str(drift_closure.get("policy") or "").split()) < 20:
    missing.append("release evidence drift closure policy must be actionable")
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
if set(drift_closure.get("requiredEvidenceIds") or []) != set(index_by_id):
    missing.append(
        "release evidence drift closure requiredEvidenceIds mismatch: "
        f"missing={sorted(set(index_by_id) - set(drift_closure.get('requiredEvidenceIds') or []))} "
        f"extra={sorted(set(drift_closure.get('requiredEvidenceIds') or []) - set(index_by_id))}"
    )
if drift_closure.get("requiredEvidenceCount") != len(index_by_id):
    missing.append("release evidence drift closure requiredEvidenceCount mismatch")
covered_by_release = set(drift_closure.get("releasePrerequisiteCoverage") or []) | set(
    drift_closure.get("explicitReleaseArtifacts") or []
)
if covered_by_release != set(index_by_id):
    missing.append(
        "release evidence drift closure coverage mismatch: "
        f"missing={sorted(set(index_by_id) - covered_by_release)} "
        f"extra={sorted(covered_by_release - set(index_by_id))}"
    )
if drift_closure.get("uncoveredEvidenceIds"):
    missing.append(f"release evidence drift closure uncovered ids: {drift_closure['uncoveredEvidenceIds']}")
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
tag_ci_closure = consumption.get("tagCIClosure") or {}
if tag_ci_closure.get("schema") != "gofly.release_tag_ci_closure.v1":
    missing.append("release evidence tag CI closure schema mismatch")
if tag_ci_closure.get("aiflowTask") != "GOFLY-GOV-10R8-08":
    missing.append("release evidence tag CI closure aiflowTask mismatch")
if tag_ci_closure.get("acceptanceGate") != "make governance-report-check":
    missing.append("release evidence tag CI closure acceptanceGate mismatch")
if tag_ci_closure.get("dashboardReportField") != "releaseEvidenceConsumption.tagCIClosure":
    missing.append("release evidence tag CI closure dashboardReportField mismatch")
if len(str(tag_ci_closure.get("policy") or "").split()) < 20:
    missing.append("release evidence tag CI closure policy must be actionable")
expected_tag_ci_gates = {
    "make release-snapshot",
    "make release-artifacts-check",
    "make required-checks-drift-check",
    "make governance-report-check",
}
if set(tag_ci_closure.get("requiredLocalGates") or []) != expected_tag_ci_gates:
    missing.append("release evidence tag CI closure requiredLocalGates mismatch")
if set(tag_ci_closure.get("requiredEvidenceIds") or []) != set(index_by_id):
    missing.append(
        "release evidence tag CI closure requiredEvidenceIds mismatch: "
        f"missing={sorted(set(index_by_id) - set(tag_ci_closure.get('requiredEvidenceIds') or []))} "
        f"extra={sorted(set(tag_ci_closure.get('requiredEvidenceIds') or []) - set(index_by_id))}"
    )
expected_tag_ci_stages = {
    "tag-trigger-and-snapshot": {
        "producerJobs": {"release"},
        "requiredGate": "make release-snapshot",
        "requiredEvidenceIds": {"checksums", "archive-sbom"},
    },
    "artifact-integrity-and-sbom": {
        "producerJobs": {"release"},
        "requiredGate": "make release-artifacts-check",
        "requiredEvidenceIds": {"checksums", "archive-sbom", "docker-sbom"},
    },
    "docker-digest-trivy-provenance": {
        "producerJobs": {"release"},
        "requiredGate": "RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check",
        "requiredEvidenceIds": {"docker-digest", "trivy", "checksums-attestation", "docker-attestation"},
    },
    "required-checks-and-dashboard": {
        "producerJobs": {"contract-check", "security", "governance", "bench-fuzz"},
        "requiredGate": "make governance-report-check",
        "requiredEvidenceIds": {"api-compat", "security", "race", "bench", "governance-dashboard"},
    },
}
tag_ci_stages = {
    item.get("id", ""): item
    for item in tag_ci_closure.get("stages") or []
    if isinstance(item, dict) and item.get("id")
}
if tag_ci_closure.get("stageCount") != len(expected_tag_ci_stages):
    missing.append("release evidence tag CI closure stageCount mismatch")
if set(tag_ci_stages) != set(expected_tag_ci_stages):
    missing.append(
        "release evidence tag CI closure stages mismatch: "
        f"missing={sorted(set(expected_tag_ci_stages) - set(tag_ci_stages))} "
        f"extra={sorted(set(tag_ci_stages) - set(expected_tag_ci_stages))}"
    )
for stage_id, expected in expected_tag_ci_stages.items():
    stage = tag_ci_stages.get(stage_id) or {}
    for field in (
        "id",
        "producerJobs",
        "requiredGate",
        "requiredEvidenceIds",
        "closureInvariant",
        "publishDecision",
        "blockDecision",
        "rollbackOrEscalation",
    ):
        if not stage.get(field):
            missing.append(f"release evidence tag CI closure {stage_id}: {field} is required")
    if set(stage.get("producerJobs") or []) != expected["producerJobs"]:
        missing.append(f"release evidence tag CI closure {stage_id}: producerJobs mismatch")
    if stage.get("requiredGate") != expected["requiredGate"]:
        missing.append(f"release evidence tag CI closure {stage_id}: requiredGate mismatch")
    if set(stage.get("requiredEvidenceIds") or []) != expected["requiredEvidenceIds"]:
        missing.append(f"release evidence tag CI closure {stage_id}: requiredEvidenceIds mismatch")
    if not set(stage.get("requiredEvidenceIds") or []).issubset(set(consumption_by_id)):
        missing.append(f"release evidence tag CI closure {stage_id}: evidence ids must exist in release evidence consumption")
    for field in ("closureInvariant", "publishDecision", "blockDecision", "rollbackOrEscalation"):
        if len(str(stage.get(field) or "").split()) < 10:
            missing.append(f"release evidence tag CI closure {stage_id}: {field} must be actionable")
adoption_contract = report["releaseAdoptionContract"]
if adoption_contract["schema"] != "gofly.release_adoption_contract.v1":
    missing.append("release adoption contract schema mismatch")
if adoption_contract["status"] != "blocking":
    missing.append("release adoption contract status mismatch")
if adoption_contract["sourceOfTruth"] != "docs/releases/evidence-consumption.json":
    missing.append("release adoption contract sourceOfTruth mismatch")
if adoption_contract["acceptanceGate"] != "make governance-report-check":
    missing.append("release adoption contract acceptanceGate mismatch")
if adoption_contract["dashboardReportField"] != "releaseAdoptionContract":
    missing.append("release adoption contract dashboardReportField mismatch")
if len(str(adoption_contract.get("policy") or "").split()) < 20:
    missing.append("release adoption contract policy must be actionable")
for source in adoption_contract.get("requiredEvidenceSources") or []:
    if not (root / source).exists():
        missing.append(f"release adoption contract evidence source is missing: {source}")
expected_release_decisions = {
    "upgrade": {"api-compat", "governance-dashboard"},
    "publish": {"checksums", "archive-sbom", "checksums-attestation", "docker-sbom", "docker-attestation", "docker-digest"},
    "block": {"trivy", "security", "race", "bench"},
    "rollback": {"docker-digest", "trivy", "bench", "governance-dashboard"},
}
decisions_by_id = {
    item.get("id", ""): item
    for item in adoption_contract.get("decisions") or []
    if isinstance(item, dict) and item.get("id")
}
if set(decisions_by_id) != set(expected_release_decisions):
    missing.append(
        "release adoption contract decisions mismatch: "
        f"missing={sorted(set(expected_release_decisions) - set(decisions_by_id))} "
        f"extra={sorted(set(decisions_by_id) - set(expected_release_decisions))}"
    )
if adoption_contract.get("decisionCount") != len(expected_release_decisions):
    missing.append("release adoption contract decisionCount mismatch")
required_release_risk_classes = {"contract", "supply-chain", "security", "deployment"}
if set((adoption_contract.get("riskClassCounts") or {}).keys()) != required_release_risk_classes:
    missing.append("release adoption contract risk classes mismatch")
all_consumed_ids = set(consumption_by_id)
consumption_gates = {item.get("localGate", "") for item in consumption_by_id.values()}
for decision_id, expected_ids in expected_release_decisions.items():
    decision = decisions_by_id.get(decision_id) or {}
    for field in ("id", "riskClass", "requiredEvidenceIds", "requiredGates", "adopterAction", "rollbackOrEscalation"):
        if not decision.get(field):
            missing.append(f"release adoption contract {decision_id}: {field} is required")
    if set(decision.get("requiredEvidenceIds") or []) != expected_ids:
        missing.append(f"release adoption contract {decision_id}: requiredEvidenceIds mismatch")
    if not set(decision.get("requiredEvidenceIds") or []).issubset(all_consumed_ids):
        missing.append(f"release adoption contract {decision_id}: evidence ids must exist in release evidence consumption")
    for gate in decision.get("requiredGates") or []:
        if gate not in consumption_gates and gate != "make governance-report-check":
            missing.append(f"release adoption contract {decision_id}: gate {gate!r} is not backed by release evidence")
    for field in ("adopterAction", "rollbackOrEscalation"):
        if len(str(decision.get(field) or "").split()) < 10:
            missing.append(f"release adoption contract {decision_id}: {field} must be actionable")
tag_ci_rows = {
    item.get("id", ""): item
    for item in adoption_contract.get("tagCIClosureRows") or []
    if isinstance(item, dict) and item.get("id")
}
if adoption_contract.get("tagCIClosureRowCount") != len(expected_tag_ci_stages):
    missing.append("release adoption contract tagCIClosureRowCount mismatch")
if set(tag_ci_rows) != set(expected_tag_ci_stages):
    missing.append(
        "release adoption contract tagCIClosureRows mismatch: "
        f"missing={sorted(set(expected_tag_ci_stages) - set(tag_ci_rows))} "
        f"extra={sorted(set(tag_ci_rows) - set(expected_tag_ci_stages))}"
    )
for row_id, expected in expected_tag_ci_stages.items():
    row = tag_ci_rows.get(row_id) or {}
    stage = tag_ci_stages.get(row_id) or {}
    expected_source = f"releaseEvidenceConsumption.tagCIClosure.stages[{row_id}]"
    for field in (
        "id",
        "sourceStage",
        "requiredGate",
        "requiredEvidenceIds",
        "artifactProducer",
        "publishDecision",
        "blockDecision",
        "rollbackOrEscalation",
    ):
        if not row.get(field):
            missing.append(f"release adoption contract tag CI closure {row_id}: {field} is required")
    if row.get("sourceStage") != expected_source:
        missing.append(f"release adoption contract tag CI closure {row_id}: sourceStage mismatch")
    if row.get("requiredGate") != stage.get("requiredGate"):
        missing.append(f"release adoption contract tag CI closure {row_id}: requiredGate must match release evidence stage")
    if set(row.get("requiredEvidenceIds") or []) != set(stage.get("requiredEvidenceIds") or []):
        missing.append(f"release adoption contract tag CI closure {row_id}: requiredEvidenceIds must match release evidence stage")
    if set(row.get("requiredEvidenceIds") or []) != expected["requiredEvidenceIds"]:
        missing.append(f"release adoption contract tag CI closure {row_id}: requiredEvidenceIds mismatch")
    if not set(row.get("requiredEvidenceIds") or []).issubset(all_consumed_ids):
        missing.append(f"release adoption contract tag CI closure {row_id}: evidence ids must exist in release evidence consumption")
    row_text = json.dumps(row, sort_keys=True).lower()
    for marker in ("producer", "publish", "block", "rollback"):
        if marker not in row_text:
            missing.append(f"release adoption contract tag CI closure {row_id}: missing marker {marker!r}")
    for field in ("artifactProducer", "publishDecision", "blockDecision", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 8:
            missing.append(f"release adoption contract tag CI closure {row_id}: {field} must be actionable")
expected_supply_chain_enforcement = {
    "archive-checksum-sbom-enforcement": {
        "producerJob": "release",
        "requiredCheck": "release-artifacts-test",
        "requiredEvidenceIds": {"checksums", "archive-sbom"},
    },
    "docker-digest-trivy-enforcement": {
        "producerJob": "release",
        "requiredCheck": "release-artifacts-test",
        "requiredEvidenceIds": {"docker-sbom", "docker-digest", "trivy"},
    },
    "provenance-attestation-enforcement": {
        "producerJob": "release",
        "requiredCheck": "release-artifacts-test",
        "requiredEvidenceIds": {"checksums-attestation", "docker-attestation"},
    },
    "required-check-release-blocking-enforcement": {
        "producerJob": "governance",
        "requiredCheck": "governance gates",
        "requiredEvidenceIds": {"api-compat", "security", "race", "bench", "governance-dashboard"},
    },
}
enforcement_rows = {
    item.get("id", ""): item
    for item in adoption_contract.get("supplyChainEnforcementRows") or []
    if isinstance(item, dict) and item.get("id")
}
if set(enforcement_rows) != set(expected_supply_chain_enforcement):
    missing.append(
        "release adoption contract supplyChainEnforcementRows mismatch: "
        f"missing={sorted(set(expected_supply_chain_enforcement) - set(enforcement_rows))} "
        f"extra={sorted(set(enforcement_rows) - set(expected_supply_chain_enforcement))}"
    )
if adoption_contract.get("supplyChainEnforcementCount") != len(expected_supply_chain_enforcement):
    missing.append("release adoption contract supplyChainEnforcementCount mismatch")
for row_id, expected in expected_supply_chain_enforcement.items():
    row = enforcement_rows.get(row_id) or {}
    for field in (
        "id",
        "producerJob",
        "requiredCheck",
        "requiredEvidenceIds",
        "securityScan",
        "provenanceEvidence",
        "publishDecision",
        "blockDecision",
        "rollbackOrEscalation",
    ):
        if not row.get(field):
            missing.append(f"release adoption contract supply-chain enforcement {row_id}: {field} is required")
    for field in ("producerJob", "requiredCheck"):
        if row.get(field) != expected[field]:
            missing.append(f"release adoption contract supply-chain enforcement {row_id}: {field} mismatch")
    if set(row.get("requiredEvidenceIds") or []) != expected["requiredEvidenceIds"]:
        missing.append(f"release adoption contract supply-chain enforcement {row_id}: requiredEvidenceIds mismatch")
    if not set(row.get("requiredEvidenceIds") or []).issubset(all_consumed_ids):
        missing.append(f"release adoption contract supply-chain enforcement {row_id}: evidence ids must exist in release evidence consumption")
    row_text = json.dumps(row, sort_keys=True).lower()
    for marker in ("producer", "check", "security", "provenance", "publish", "block", "rollback"):
        if marker not in row_text:
            missing.append(f"release adoption contract supply-chain enforcement {row_id}: missing marker {marker!r}")
    for field in ("securityScan", "provenanceEvidence", "publishDecision", "blockDecision", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 8:
            missing.append(f"release adoption contract supply-chain enforcement {row_id}: {field} must be actionable")
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
    "releaseEvidenceConsumption.driftClosure.driftGate",
    "releaseEvidenceConsumption.driftClosure.requiredEvidenceCount",
    "releaseEvidenceConsumption.driftClosure.releasePrerequisiteCoverage",
    "releaseEvidenceConsumption.tagCIClosure.stageCount",
    "releaseEvidenceConsumption.tagCIClosure.requiredLocalGates",
    "ciRequiredChecks.p13HostedReleaseSupplyChainClosure.status",
    "ciRequiredChecks.p13HostedReleaseSupplyChainClosure.aiflowTask",
    "ciRequiredChecks.p13HostedReleaseSupplyChainClosure.artifactFamilyCount",
    "releaseAdoptionContract.decisionCount",
    "releaseAdoptionContract.riskClassCounts",
    "releaseAdoptionContract.tagCIClosureRowCount",
    "releaseAdoptionContract.supplyChainEnforcementCount",
    "dashboard.releaseDashboardConsumption.schema",
    "dashboard.releaseDashboardConsumption.status",
    "dashboard.releaseDashboardConsumption.aiflowTask",
    "dashboard.releaseDashboardConsumption.stableFieldCount",
    "dashboard.releaseDashboardConsumption.consumerRowCount",
    "dashboard.releaseDashboardConsumption.requiredGateCount",
    "apiSurface.tiers",
    "benchmark.regressionGate",
    "benchmark.trendSummaryStatus",
    "benchmark.surfacePolicy.surfaceCount",
    "benchmark.surfacePolicy.statusCounts",
    "benchmark.surfacePolicy.adopterPerformanceContract.blockingSurfaceCount",
    "benchmark.surfacePolicy.adopterPerformanceContract.reportOnlySurfaceCount",
    "benchmark.surfacePolicy.adopterPerformanceContract.unsupportedSurfaceCount",
    "coverage.ratchet",
    "security.gosec.blockingGate",
    "aiflow.status",
    "productionDefaults.capabilityCount",
    "productionDefaults.missingAssets",
    "runtimeSLO.operatorDrillCount",
    "runtimeSLO.incidentRehearsalCount",
    "dxSupportBundle.surfaceCount",
    "dxSupportBundle.remediationHintCount",
    "dxSupportBundle.p13CliDoctorTroubleshootingLoop.status",
    "dxSupportBundle.p13CliDoctorTroubleshootingLoop.aiflowTask",
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
release_consumption_contract = dashboard.get("releaseEvidenceConsumption") or {}
if release_consumption_contract.get("schema") != "gofly.release_evidence_consumption.v1":
    missing.append("governance dashboard releaseEvidenceConsumption schema mismatch")
if release_consumption_contract.get("source") != "docs/releases/evidence-consumption.json":
    missing.append("governance dashboard releaseEvidenceConsumption source mismatch")
if release_consumption_contract.get("sourceOfTruth") != "docs/releases/evidence-index.json":
    missing.append("governance dashboard releaseEvidenceConsumption sourceOfTruth mismatch")
if release_consumption_contract.get("acceptanceGate") != "make governance-report-check":
    missing.append("governance dashboard releaseEvidenceConsumption acceptanceGate mismatch")
if release_consumption_contract.get("driftGate") != "make required-checks-drift-check":
    missing.append("governance dashboard releaseEvidenceConsumption driftGate mismatch")
if release_consumption_contract.get("reportField") != "releaseEvidenceConsumption.driftClosure":
    missing.append("governance dashboard releaseEvidenceConsumption reportField mismatch")
if release_consumption_contract.get("requiredCheckSource") != "docs/reference/ci-required-check-evidence.json":
    missing.append("governance dashboard releaseEvidenceConsumption requiredCheckSource mismatch")
for field in release_consumption_contract.get("requiredDashboardFields") or []:
    if field not in dashboard["summaryFields"]:
        missing.append(f"governance dashboard releaseEvidenceConsumption summaryFields missing {field!r}")
if "releaseEvidenceConsumption.tagCIClosure.stageCount" not in release_consumption_contract.get("requiredDashboardFields", []):
    missing.append("governance dashboard releaseEvidenceConsumption must expose tag CI closure stageCount")
if "releaseEvidenceConsumption.tagCIClosure.requiredLocalGates" not in release_consumption_contract.get("requiredDashboardFields", []):
    missing.append("governance dashboard releaseEvidenceConsumption must expose tag CI closure requiredLocalGates")
release_adoption_dashboard_contract = dashboard.get("releaseAdoptionContract") or {}
if release_adoption_dashboard_contract.get("schema") != "gofly.release_adoption_contract.v1":
    missing.append("governance dashboard releaseAdoptionContract schema mismatch")
if release_adoption_dashboard_contract.get("source") != "docs/releases/adoption-contract.json":
    missing.append("governance dashboard releaseAdoptionContract source mismatch")
if release_adoption_dashboard_contract.get("sourceOfTruth") != "docs/releases/evidence-consumption.json":
    missing.append("governance dashboard releaseAdoptionContract sourceOfTruth mismatch")
if release_adoption_dashboard_contract.get("reportField") != "releaseAdoptionContract":
    missing.append("governance dashboard releaseAdoptionContract reportField mismatch")
if release_adoption_dashboard_contract.get("acceptanceGate") != "make governance-report-check":
    missing.append("governance dashboard releaseAdoptionContract acceptanceGate mismatch")
if set(release_adoption_dashboard_contract.get("requiredDecisions") or []) != set(expected_release_decisions):
    missing.append("governance dashboard releaseAdoptionContract requiredDecisions mismatch")
for field in release_adoption_dashboard_contract.get("requiredDashboardFields") or []:
    if field not in dashboard["summaryFields"]:
        missing.append(f"governance dashboard releaseAdoptionContract summaryFields missing {field!r}")
if "releaseAdoptionContract.tagCIClosureRowCount" not in release_adoption_dashboard_contract.get("requiredDashboardFields", []):
    missing.append("governance dashboard releaseAdoptionContract must expose tag CI closure row count")
release_dashboard_consumption = dashboard.get("releaseDashboardConsumption") or {}
if release_dashboard_consumption.get("schema") != "gofly.release_dashboard_consumption.v1":
    missing.append("governance dashboard releaseDashboardConsumption schema mismatch")
if release_dashboard_consumption.get("aiflowTask") != "GOFLY-P10-9-RELEASE-DASHBOARD-CONSUMPTION":
    missing.append("governance dashboard releaseDashboardConsumption aiflowTask mismatch")
if release_dashboard_consumption.get("status") != "blocking-contract":
    missing.append("governance dashboard releaseDashboardConsumption status mismatch")
if release_dashboard_consumption.get("source") != "docs/reference/governance-dashboard-contract.json":
    missing.append("governance dashboard releaseDashboardConsumption source mismatch")
if release_dashboard_consumption.get("sourceReport") != "gofly.governance_report.v1":
    missing.append("governance dashboard releaseDashboardConsumption sourceReport mismatch")
if release_dashboard_consumption.get("reportField") != "dashboard.releaseDashboardConsumption":
    missing.append("governance dashboard releaseDashboardConsumption reportField mismatch")
if release_dashboard_consumption.get("acceptanceGate") != "make governance-report-check":
    missing.append("governance dashboard releaseDashboardConsumption acceptanceGate mismatch")
if release_dashboard_consumption.get("driftGate") != "make required-checks-drift-check":
    missing.append("governance dashboard releaseDashboardConsumption driftGate mismatch")
if release_dashboard_consumption.get("owner") != "human-or-current-agent":
    missing.append("governance dashboard releaseDashboardConsumption owner mismatch")
required_release_dashboard_gates = {
    "make governance-report-check",
    "make required-checks-drift-check",
}
if set(release_dashboard_consumption.get("requiredGates") or []) != required_release_dashboard_gates:
    missing.append("governance dashboard releaseDashboardConsumption requiredGates mismatch")
if release_dashboard_consumption.get("requiredGateCount") != len(required_release_dashboard_gates):
    missing.append("governance dashboard releaseDashboardConsumption requiredGateCount mismatch")
required_release_dashboard_fields = {
    "release.readinessScore.status",
    "release.readinessScore.score",
    "apiSurface.tiers",
    "security.gosec.blockingGate",
    "security.govulncheck.blockingGate",
    "coverage.ratchet",
    "coverageTrend.policy.blockingGate",
    "benchmark.regressionGate",
    "benchmark.surfacePolicy.adopterPerformanceContract.blockingSurfaceCount",
    "benchmark.surfacePolicy.adopterPerformanceContract.reportOnlySurfaceCount",
    "benchmark.surfacePolicy.adopterPerformanceContract.unsupportedSurfaceCount",
    "aiflow.status",
    "aiflow.summary",
}
if set(release_dashboard_consumption.get("requiredStableFields") or []) != required_release_dashboard_fields:
    missing.append("governance dashboard releaseDashboardConsumption requiredStableFields mismatch")
if release_dashboard_consumption.get("stableFieldCount") != len(required_release_dashboard_fields):
    missing.append("governance dashboard releaseDashboardConsumption stableFieldCount mismatch")
if not required_release_dashboard_fields.issubset(set(dashboard["summaryFields"])):
    missing.append("governance dashboard releaseDashboardConsumption stable fields must be summary fields")
if not required_release_dashboard_fields.issubset(set(release_dashboard_consumption.get("consumerFieldRefs") or [])):
    missing.append("governance dashboard releaseDashboardConsumption consumerFieldRefs missing stable fields")
expected_release_dashboard_rows = {
    "release-readiness": {
        "release.readinessScore.status",
        "release.readinessScore.score",
    },
    "api-tier-consumption": {"apiSurface.tiers"},
    "security-gate-consumption": {
        "security.gosec.blockingGate",
        "security.govulncheck.blockingGate",
    },
    "coverage-ratchet-consumption": {
        "coverage.ratchet",
        "coverageTrend.policy.blockingGate",
    },
    "benchmark-ratchet-consumption": {
        "benchmark.regressionGate",
        "benchmark.surfacePolicy.adopterPerformanceContract.blockingSurfaceCount",
        "benchmark.surfacePolicy.adopterPerformanceContract.reportOnlySurfaceCount",
        "benchmark.surfacePolicy.adopterPerformanceContract.unsupportedSurfaceCount",
    },
    "aiflow-task-status-consumption": {
        "aiflow.status",
        "aiflow.summary",
    },
}
release_dashboard_rows = {
    item.get("id", ""): item
    for item in release_dashboard_consumption.get("consumerRows") or []
    if isinstance(item, dict) and item.get("id")
}
if set(release_dashboard_rows) != set(expected_release_dashboard_rows):
    missing.append(
        "governance dashboard releaseDashboardConsumption rows mismatch: "
        f"missing={sorted(set(expected_release_dashboard_rows) - set(release_dashboard_rows))} "
        f"extra={sorted(set(release_dashboard_rows) - set(expected_release_dashboard_rows))}"
    )
if release_dashboard_consumption.get("consumerRowCount") != len(expected_release_dashboard_rows):
    missing.append("governance dashboard releaseDashboardConsumption consumerRowCount mismatch")
for row_id, expected_fields in expected_release_dashboard_rows.items():
    row = release_dashboard_rows.get(row_id) or {}
    for field in ("id", "sourceReportFields", "gate", "adopterAction", "rollbackOrEscalation"):
        if not row.get(field):
            missing.append(f"governance dashboard releaseDashboardConsumption {row_id}: {field} is required")
    if set(row.get("sourceReportFields") or []) != expected_fields:
        missing.append(f"governance dashboard releaseDashboardConsumption {row_id}: sourceReportFields mismatch")
    if not set(row.get("sourceReportFields") or []).issubset(set(dashboard["summaryFields"])):
        missing.append(f"governance dashboard releaseDashboardConsumption {row_id}: sourceReportFields must be summary fields")
    gate = str(row.get("gate") or "")
    if gate.startswith("make "):
        target = gate.removeprefix("make ").split()[0]
        makefile = read_text(root / "Makefile")
        if re.search(rf"^{re.escape(target)}:", makefile, re.M) is None:
            missing.append(f"governance dashboard releaseDashboardConsumption {row_id}: gate target {target!r} missing")
    else:
        missing.append(f"governance dashboard releaseDashboardConsumption {row_id}: gate must be a make target")
    for field in ("adopterAction", "rollbackOrEscalation"):
        if len(str(row.get(field) or "").split()) < 10:
            missing.append(f"governance dashboard releaseDashboardConsumption {row_id}: {field} must be actionable")
policy_text = str(release_dashboard_consumption.get("policy") or "").lower()
for needle in ("release readiness", "api tiers", "security gates", "coverage ratchets", "benchmark ratchets", "aiflow task status"):
    if needle not in policy_text:
        missing.append(f"governance dashboard releaseDashboardConsumption policy missing {needle!r}")
runtime_policy = str(release_dashboard_consumption.get("runtimeStatePolicy") or "")
for needle in (".aiflow", ".harness", ".tmp-test", ".trae", "coverage.out", "bench/regression-report.json", "docs/superpowers"):
    if needle not in runtime_policy:
        missing.append(f"governance dashboard releaseDashboardConsumption runtimeStatePolicy missing {needle!r}")
commit_policy = str(release_dashboard_consumption.get("commitPolicy") or "")
for needle in ("current agent or human", "governance-report-check", "required-checks-drift-check", "runtime state must not be staged"):
    if needle not in commit_policy:
        missing.append(f"governance dashboard releaseDashboardConsumption commitPolicy missing {needle!r}")
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
if int(convergence_contract.get("requiredTaskCount") or 0) != 3:
    missing.append("governance dashboard governanceConvergence requiredTaskCount mismatch")
if convergence_contract.get("requiredActiveBatch") != "GOFLY-P14":
    missing.append("governance dashboard governanceConvergence requiredActiveBatch mismatch")
if convergence_contract.get("latestCompletedBatch") != "GOFLY-P13":
    missing.append("governance dashboard governanceConvergence latestCompletedBatch mismatch")
if convergence_contract.get("activeRoadmap") != "docs/reference/governance-p14-roadmap.json":
    missing.append("governance dashboard governanceConvergence activeRoadmap mismatch")
if set(convergence_contract.get("requiredIgnoredRuntimePaths") or []) != expected_ignored_runtime_paths:
    missing.append("governance dashboard governanceConvergence requiredIgnoredRuntimePaths mismatch")
aiflow_contract = dashboard.get("aiflow") or {}
if aiflow_contract.get("source") != ".aiflow/store.json":
    missing.append("governance dashboard aiflow source mismatch")
if aiflow_contract.get("fallbackSource") != ".harness/store.json":
    missing.append("governance dashboard aiflow fallbackSource mismatch")
if aiflow_contract.get("requiredStatusField") != "status":
    missing.append("governance dashboard aiflow requiredStatusField mismatch")
if aiflow_contract.get("requiredSummaryField") != "summary":
    missing.append("governance dashboard aiflow requiredSummaryField mismatch")
if "never be committed" not in str(aiflow_contract.get("requiredRuntimePolicy") or ""):
    missing.append("governance dashboard aiflow requiredRuntimePolicy must reject committed runtime state")
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
if ai_native_contract.get("requiredHandoffSchema") != "gofly.remediation_handoff.v1":
    missing.append("governance dashboard aiNativeWorkflow requiredHandoffSchema mismatch")
if ai_native_contract.get("requiredCommitOwner") != "human-or-current-agent":
    missing.append("governance dashboard aiNativeWorkflow requiredCommitOwner mismatch")
if ai_native_contract.get("handoffReportField") != "dxSupportBundle.remediationHandoff":
    missing.append("governance dashboard aiNativeWorkflow handoffReportField mismatch")
if ai_native_contract.get("remediationLoopReportField") != "dxSupportBundle.remediationLoopContract":
    missing.append("governance dashboard aiNativeWorkflow remediationLoopReportField mismatch")
for gate in ai_native_contract.get("requiredHandoffGates") or []:
    if gate not in set(handoff.get("requiredGates") or []):
        missing.append(f"governance dashboard aiNativeWorkflow requiredHandoffGates missing from report: {gate}")
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
    "release-evidence-drift-closure",
    "release-tag-ci-closure",
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
