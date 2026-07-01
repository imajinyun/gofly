#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
matrix_path = root / "docs" / "reference" / "discovery-adapter-matrix.json"
missing = []

expected_providers = {
    "memory": "implemented",
    "consul": "implemented",
    "etcdv3": "implemented",
    "nacos": "config-only",
    "dns": "planned",
    "kubernetes": "planned",
    "static": "planned",
}
required_capabilities = {
    "register",
    "deregister",
    "resolve",
    "watch",
    "ttlLease",
    "healthFiltering",
    "tagVersionZoneFiltering",
    "failover",
}
expected_owners = {
    "memory": ("runtime-governance", "local-tier1"),
    "consul": ("runtime-governance", "network-tier1"),
    "etcdv3": ("runtime-governance", "network-tier1"),
    "nacos": ("config-governance", "config-only"),
    "dns": ("runtime-governance", "planned"),
    "kubernetes": ("cloud-native-governance", "planned"),
    "static": ("runtime-governance", "planned"),
}


def read_text(path):
    if not path.is_file():
        missing.append(f"{path.relative_to(root)} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


def make_target_names(makefile):
    return set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))


def gate_is_known(gate, targets):
    if gate.startswith("make "):
        parts = gate.removeprefix("make ").split()
        return bool(parts) and parts[0] in targets
    return gate.startswith("go test ")


if matrix_path.is_file():
    matrix = json.loads(matrix_path.read_text(encoding="utf-8"))
else:
    matrix = {}
    missing.append("docs/reference/discovery-adapter-matrix.json is missing")

makefile = read_text(root / "Makefile")
guide = read_text(root / "docs" / "guides" / "discovery.md")
docs_index = read_text(root / "docs" / "index.md")
framework_long_term = read_text(root / "docs" / "reference" / "framework-gap-long-term-adoption.json")
dependency_evidence = read_text(root / "docs" / "reference" / "dependency-upgrade-evidence.json")
targets = make_target_names(makefile)
docs_check_line = next((line for line in makefile.splitlines() if line.startswith("docs-check:")), "")

require(matrix.get("schema") == "gofly.discovery_adapter_matrix.v1", "schema must be gofly.discovery_adapter_matrix.v1")
require(matrix.get("sourceOfTruth") == "docs/guides/discovery.md", "sourceOfTruth must be docs/guides/discovery.md")
require(matrix.get("acceptanceGate") == "make discovery-adapter-matrix-check", "acceptanceGate must be make discovery-adapter-matrix-check")
require("discovery-adapter-matrix-check" in targets, "Makefile must expose discovery-adapter-matrix-check")
require("discovery-adapter-matrix-check" in docs_check_line, "docs-check must depend on discovery-adapter-matrix-check")
require("check-discovery-adapter-matrix.sh" in makefile, "Makefile must call check-discovery-adapter-matrix.sh")

status_policy = matrix.get("statusPolicy") or {}
for status in {"implemented", "planned", "config-only"}:
    require(status in status_policy, f"statusPolicy must document {status}")

governance_coverage = matrix.get("governanceCoverage") or {}
require(
    governance_coverage.get("adapterMatrixGate") == "make discovery-adapter-matrix-check",
    "governanceCoverage.adapterMatrixGate must be make discovery-adapter-matrix-check",
)
require(
    governance_coverage.get("requiredChecksGate") == "make required-checks-drift-check",
    "governanceCoverage.requiredChecksGate must be make required-checks-drift-check",
)
require(
    governance_coverage.get("frameworkGapEvidence") == "docs/reference/framework-gap-long-term-adoption.json",
    "governanceCoverage.frameworkGapEvidence must point to framework-gap-long-term-adoption.json",
)
require(
    governance_coverage.get("dependencyEvidence") == "docs/reference/dependency-upgrade-evidence.json",
    "governanceCoverage.dependencyEvidence must point to dependency-upgrade-evidence.json",
)

r8_matrix = matrix.get("r8RunnableProofMatrix") or {}
require(
    r8_matrix.get("schema") == "gofly.discovery_runnable_proof_matrix.v1",
    "r8RunnableProofMatrix.schema must be gofly.discovery_runnable_proof_matrix.v1",
)
require(
    r8_matrix.get("aiflowTask") == "GOFLY-GOV-10R8-05",
    "r8RunnableProofMatrix.aiflowTask must be GOFLY-GOV-10R8-05",
)
require(
    r8_matrix.get("status") == "blocking-contract",
    "r8RunnableProofMatrix.status must be blocking-contract",
)
require(
    r8_matrix.get("acceptanceGate") == "make discovery-adapter-matrix-check",
    "r8RunnableProofMatrix.acceptanceGate must be make discovery-adapter-matrix-check",
)
r8_rows = {
    item.get("id"): item
    for item in r8_matrix.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
expected_r8_rows = {
    "memory-discovery-proof": ("memory", "runnable", "go test -shuffle=on ./core/discovery/..."),
    "consul-discovery-proof": ("consul", "runnable", "go test -shuffle=on ./core/discovery/..."),
    "etcdv3-discovery-proof": ("etcdv3", "runnable", "go test -shuffle=on ./core/discovery/..."),
    "nacos-config-only-proof": ("nacos", "config-only", "make discovery-adapter-matrix-check"),
    "dns-discovery-proof": ("dns", "planned", "make discovery-adapter-matrix-check"),
    "kubernetes-discovery-proof": ("kubernetes", "planned", "make required-checks-drift-check"),
    "static-discovery-proof": ("static", "planned", "make discovery-adapter-matrix-check"),
}
require(
    set(r8_rows) == set(expected_r8_rows),
    f"r8RunnableProofMatrix rows drifted: missing={sorted(set(expected_r8_rows) - set(r8_rows))} extra={sorted(set(r8_rows) - set(expected_r8_rows))}",
)
for row_id, (provider, classification, gate) in expected_r8_rows.items():
    row = r8_rows.get(row_id) or {}
    require(row.get("provider") == provider, f"{row_id}: provider must be {provider}")
    require(row.get("classification") == classification, f"{row_id}: classification must be {classification}")
    require(row.get("runnableGate") == gate, f"{row_id}: runnableGate mismatch")
    require(gate_is_known(str(row.get("runnableGate") or ""), targets), f"{row_id}: runnableGate is not known")
    for field in ("dependencyBoundary", "observabilityEvidence", "fallbackBehavior", "rollbackOrEscalation"):
        require(len(str(row.get(field) or "").split()) >= 10, f"{row_id}: {field} must be actionable")
    require("Fallback" in str(row.get("fallbackBehavior") or ""), f"{row_id}: fallbackBehavior must name fallback")
    require(
        provider in str(row.get("rollbackOrEscalation") or "")
        or provider in {"memory", "kubernetes", "static"},
        f"{row_id}: rollbackOrEscalation should name the provider or platform boundary",
    )
    if classification == "runnable":
        require("dependency" in str(row.get("dependencyBoundary") or "").lower(), f"{row_id}: dependencyBoundary must name dependency ownership")
    if classification in {"planned", "config-only"}:
        require(
            "until" in str(row.get("rollbackOrEscalation") or "").lower(),
            f"{row_id}: planned/config-only rollbackOrEscalation must describe promotion boundary",
        )

p10_closeout = matrix.get("p10DiscoveryAdapterCloseout") or {}
require(
    p10_closeout.get("schema") == "gofly.discovery_adapter_p10_closeout.v1",
    "p10DiscoveryAdapterCloseout schema mismatch",
)
require(
    p10_closeout.get("aiflowTask") == "GOFLY-P10-5-DISCOVERY-ADAPTER-MATRIX",
    "p10DiscoveryAdapterCloseout aiflowTask mismatch",
)
require(p10_closeout.get("status") == "blocking-contract", "p10DiscoveryAdapterCloseout status must be blocking-contract")
require(
    p10_closeout.get("acceptanceGate") == "make discovery-adapter-matrix-check",
    "p10DiscoveryAdapterCloseout acceptanceGate mismatch",
)
p10_rows = {
    item.get("id"): item
    for item in p10_closeout.get("providers") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p10_classification = {
    "memory": "implemented-local",
    "consul": "implemented-network",
    "etcdv3": "implemented-network",
    "nacos": "config-only",
    "dns": "planned",
    "kubernetes": "planned",
    "static": "planned",
}
require(set(p10_rows) == set(expected_p10_classification), f"p10DiscoveryAdapterCloseout providers mismatch: {sorted(p10_rows)!r}")
for provider_id, classification in expected_p10_classification.items():
    row = p10_rows.get(provider_id) or {}
    require(row.get("classification") == classification, f"p10DiscoveryAdapterCloseout {provider_id}: classification mismatch")
    for field in ("id", "classification", "evidence", "gate", "promotionBoundary", "rollbackOrEscalation"):
        require(row.get(field), f"p10DiscoveryAdapterCloseout {provider_id}: {field} is required")
    require(gate_is_known(str(row.get("gate") or ""), targets), f"p10DiscoveryAdapterCloseout {provider_id}: gate is not known")
    require(len(str(row.get("promotionBoundary") or "").split()) >= 10, f"p10DiscoveryAdapterCloseout {provider_id}: promotionBoundary must be actionable")
    require(len(str(row.get("rollbackOrEscalation") or "").split()) >= 8, f"p10DiscoveryAdapterCloseout {provider_id}: rollbackOrEscalation must be actionable")
    evidence_text = " ".join(row.get("evidence") or [])
    if classification.startswith("implemented"):
        for capability in ("register", "resolve", "watch", "ttlLease"):
            require(capability in evidence_text, f"p10DiscoveryAdapterCloseout {provider_id}: evidence missing {capability}")
    if classification in {"planned", "config-only"}:
        require(
            "until" in str(row.get("promotionBoundary") or "").lower()
            or "must" in str(row.get("promotionBoundary") or "").lower(),
            f"p10DiscoveryAdapterCloseout {provider_id}: promotionBoundary must explain non-promoted status",
        )
policy_text = str(p10_closeout.get("promotionPolicy") or "")
for needle in ("implementation status", "register/resolve/watch/lease", "failover", "dependency ownership", "rollback notes"):
    require(needle in policy_text, f"p10DiscoveryAdapterCloseout promotionPolicy missing {needle!r}")
runtime_policy = str(p10_closeout.get("runtimeArtifactPolicy") or "")
for needle in ("runtime evidence", "must not be committed"):
    require(needle in runtime_policy, f"p10DiscoveryAdapterCloseout runtimeArtifactPolicy missing {needle!r}")

providers = matrix.get("providers") or []
provider_map = {
    item.get("id"): item
    for item in providers
    if isinstance(item, dict) and item.get("id")
}
require(set(provider_map) == set(expected_providers), f"providers drifted: missing={sorted(set(expected_providers) - set(provider_map))} extra={sorted(set(provider_map) - set(expected_providers))}")

for provider_id, expected_status in expected_providers.items():
    item = provider_map.get(provider_id) or {}
    require(item.get("status") == expected_status, f"{provider_id}: status must be {expected_status}")
    require(provider_id in guide, f"docs/guides/discovery.md must document provider {provider_id}")
    owner = item.get("owner") or {}
    expected_team, expected_support_class = expected_owners[provider_id]
    require(owner.get("team") == expected_team, f"{provider_id}: owner.team must be {expected_team}")
    require(owner.get("supportClass") == expected_support_class, f"{provider_id}: owner.supportClass must be {expected_support_class}")
    require(str(owner.get("surface") or ""), f"{provider_id}: owner.surface is required")
    promotion_gate = str(owner.get("promotionGate") or "")
    require(gate_is_known(promotion_gate, targets), f"{provider_id}: owner.promotionGate is not known: {promotion_gate!r}")
    require(
        len(str(owner.get("dependencyUpgradeTrigger") or "").split()) >= 3,
        f"{provider_id}: owner.dependencyUpgradeTrigger must be actionable",
    )
    capabilities = item.get("capabilities") or {}
    require(set(capabilities) == required_capabilities, f"{provider_id}: capabilities drifted")
    failover = str(capabilities.get("failover") or "")
    require(len(failover.split()) >= 6, f"{provider_id}: failover behavior must be actionable")
    rollback = str(item.get("rollbackNote") or "")
    require(len(rollback.split()) >= 8, f"{provider_id}: rollbackNote must be actionable")
    gates = item.get("gates") or []
    require(gates, f"{provider_id}: gates are required")
    for gate in gates:
        require(gate_is_known(str(gate), targets), f"{provider_id}: gate is not known: {gate!r}")
    if expected_status == "implemented":
        for field in ("package", "implementation", "tests"):
            require(bool(item.get(field)), f"{provider_id}: {field} is required for implemented providers")
        require((root / item.get("implementation", "")).is_file(), f"{provider_id}: implementation path is missing")
        for test_path in item.get("tests") or []:
            require((root / test_path).is_file(), f"{provider_id}: test path is missing: {test_path}")
        for capability in (
            "register",
            "deregister",
            "resolve",
            "watch",
            "ttlLease",
            "healthFiltering",
            "tagVersionZoneFiltering",
        ):
            require(capabilities.get(capability) is True, f"{provider_id}: {capability} must be true")
    elif expected_status == "planned":
        require(not item.get("implementation"), f"{provider_id}: planned provider must not point to an implementation")
        require(not item.get("tests"), f"{provider_id}: planned provider must not claim tests")
        require(len(item.get("promotionCriteria") or []) >= 3, f"{provider_id}: promotionCriteria must include at least three items")
        require(owner.get("promotionGate") in {
            "make discovery-adapter-matrix-check",
            "make required-checks-drift-check",
        }, f"{provider_id}: planned promotion gate must stay in discovery or required-check governance")
        for capability in (
            "register",
            "deregister",
            "resolve",
            "watch",
            "ttlLease",
            "healthFiltering",
            "tagVersionZoneFiltering",
        ):
            require(capabilities.get(capability) is False, f"{provider_id}: {capability} must be false until implemented")
    elif expected_status == "config-only":
        require(item.get("package"), f"{provider_id}: config-only provider must name the owning package")
        require("config" in str(item.get("package")), f"{provider_id}: config-only provider must stay scoped to config")
        require(len(item.get("promotionCriteria") or []) >= 3, f"{provider_id}: promotionCriteria must include at least three items")
        require(capabilities.get("resolve") is False, f"{provider_id}: config-only provider must not claim discovery resolve")
        require(owner.get("supportClass") == "config-only", f"{provider_id}: config-only supportClass must stay config-only")

release_gates = matrix.get("releaseGates") or []
for gate in (
    "go test -shuffle=on ./core/discovery/...",
    "make discovery-adapter-matrix-check",
    "make required-checks-drift-check",
):
    require(gate in release_gates, f"releaseGates missing {gate!r}")
for gate in release_gates:
    require(gate_is_known(str(gate), targets), f"release gate is not known: {gate!r}")

for needle in [
    "Discovery adapter matrix",
    "docs/reference/discovery-adapter-matrix.json",
    "make discovery-adapter-matrix-check",
    "implemented",
    "planned",
    "config-only",
    "failover",
    "rollback",
]:
    require(needle in guide, f"docs/guides/discovery.md missing {needle!r}")

require(
    "[Discovery adapter matrix](reference/discovery-adapter-matrix.json)" in docs_index,
    "docs/index.md must link the discovery adapter matrix",
)
require(
    "docs/reference/discovery-adapter-matrix.json" in framework_long_term,
    "long-term framework adoption evidence must link discovery adapter matrix",
)
require(
    "dependency changes affect storage, config, discovery, MQ, or gateway packages" in dependency_evidence,
    "dependency upgrade evidence must cover discovery packages",
)
require(
    "discovery" in dependency_evidence and "make integration-tests" in dependency_evidence,
    "dependency upgrade evidence must define discovery integration delegation",
)

if missing:
    print("discovery adapter matrix check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    sys.exit(1)

print("discovery adapter matrix OK")
PY
