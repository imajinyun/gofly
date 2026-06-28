#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import subprocess
import sys

missing = []

def normalize(value):
    normalized = re.sub(r"[^a-z0-9]+", " ", value.lower())
    normalized = re.sub(r"\band\b", " ", normalized)
    return re.sub(r"\s+", " ", normalized).strip()

def contains_normalized(haystack, needle):
    return normalize(needle).rstrip(" .") in normalize(haystack)

checks = {
    pathlib.Path("docs/explanation/adopter-decision-guide.md"): [
        "gofly.adopter_decision_guide.v1",
        "Migration path matrix",
        "When to choose gofly",
        "When to choose Gin",
        "When to keep Kitex",
        "How to migrate go-zero",
        "How to migrate Kratos",
        "Runnable migration case",
        "Compatibility caveat",
        "rollback note",
        "Gate command",
        "go test -C examples/migration-proof ./...",
        "go run -C examples/migration-proof .",
        "make examples-smoke",
        "make docs-check",
    ],
    pathlib.Path("docs/index.md"): [
        "explanation/adopter-decision-guide.md",
    ],
    pathlib.Path("README.md"): [
        "docs/explanation/adopter-decision-guide.md",
    ],
    pathlib.Path("docs/comparisons/gin.md"): [
        "rollback note",
        "examples/restserver",
    ],
    pathlib.Path("docs/comparisons/go-zero.md"): [
        "rollback note",
        "examples/production-orders",
    ],
    pathlib.Path("docs/comparisons/kratos.md"): [
        "rollback note",
        "examples/microshop",
    ],
    pathlib.Path("docs/comparisons/kitex.md"): [
        "rollback note",
        "examples/rpc-idl-matrix",
    ],
}

manual = pathlib.Path("docs/explanation/adopter-decision-guide.md")
if manual.is_file():
    text = manual.read_text(encoding="utf-8")
    adopter_proof_path = pathlib.Path("docs/reference/framework-gap-adopter-proof.json")
    support_bundle_path = pathlib.Path("docs/reference/dx-support-bundle.json")
    dashboard_path = pathlib.Path("docs/reference/governance-dashboard-contract.json")
    adopter_decisions = {}
    claim_ids = set()
    support_bundle_text = ""
    dashboard_text = ""
    if not adopter_proof_path.is_file():
        missing.append(f"{adopter_proof_path}: file is missing")
        adopter_proof = {}
    else:
        try:
            adopter_proof = json.loads(adopter_proof_path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            missing.append(f"{adopter_proof_path}: invalid JSON: {exc}")
            adopter_proof = {}
    if support_bundle_path.is_file():
        support_bundle_text = support_bundle_path.read_text(encoding="utf-8")
    else:
        missing.append(f"{support_bundle_path}: file is missing")
    if dashboard_path.is_file():
        dashboard_text = dashboard_path.read_text(encoding="utf-8")
    else:
        missing.append(f"{dashboard_path}: file is missing")

    provenance = adopter_proof.get("capabilityClaimProvenance") or {}
    if provenance.get("schema") != "gofly.capability_claim_provenance.v1":
        missing.append(f"{adopter_proof_path}: capabilityClaimProvenance schema mismatch")
    for claim in provenance.get("claims") or []:
        if isinstance(claim, dict) and claim.get("id"):
            claim_ids.add(claim["id"])

    decision_contract = adopter_proof.get("adopterDecisionEvidence") or {}
    if decision_contract.get("schema") != "gofly.adopter_decision_evidence.v1":
        missing.append(f"{adopter_proof_path}: adopterDecisionEvidence schema mismatch")
    if decision_contract.get("acceptanceGate") != "make adopter-decision-check":
        missing.append(f"{adopter_proof_path}: adopterDecisionEvidence acceptanceGate mismatch")
    if decision_contract.get("guide") != str(manual):
        missing.append(f"{adopter_proof_path}: adopterDecisionEvidence guide must be {manual}")
    if decision_contract.get("migrationProofCommand") != "go run -C examples/migration-proof .":
        missing.append(f"{adopter_proof_path}: adopterDecisionEvidence migrationProofCommand mismatch")
    if decision_contract.get("supportBundleSource") != str(support_bundle_path):
        missing.append(f"{adopter_proof_path}: supportBundleSource mismatch")
    if decision_contract.get("supportBundleCommand") != "gofly bug --json":
        missing.append(f"{adopter_proof_path}: supportBundleCommand mismatch")
    if decision_contract.get("supportBundleSchema") != "gofly.support_bundle.v1":
        missing.append(f"{adopter_proof_path}: supportBundleSchema mismatch")
    if decision_contract.get("dashboardSource") != str(dashboard_path):
        missing.append(f"{adopter_proof_path}: dashboardSource mismatch")
    for field in ("supportBundle", "supportBundle.redaction", "nextActions"):
        if field not in (decision_contract.get("supportBundleRequiredFields") or []):
            missing.append(f"{adopter_proof_path}: supportBundleRequiredFields missing {field!r}")
        if field not in support_bundle_text:
            missing.append(f"{support_bundle_path}: missing support bundle field {field!r}")
    if "gofly.support_bundle.v1" not in support_bundle_text:
        missing.append(f"{support_bundle_path}: missing gofly.support_bundle.v1")
    if "gofly.adopter_decision_evidence.v1" not in text:
        missing.append(f"{manual}: missing adopter decision evidence schema")

    for item in decision_contract.get("decisions") or []:
        if not isinstance(item, dict):
            missing.append(f"{adopter_proof_path}: adopterDecisionEvidence decisions must be objects")
            continue
        source = item.get("source")
        if not source:
            missing.append(f"{adopter_proof_path}: adopter decision missing source")
            continue
        if source in adopter_decisions:
            missing.append(f"{adopter_proof_path}: duplicate adopter decision source {source!r}")
        adopter_decisions[source] = item
        for field in (
            "id",
            "manualPath",
            "migrationProofExample",
            "compatibilityCaveat",
            "rollbackAction",
            "supportBundleAction",
        ):
            if not item.get(field):
                missing.append(f"{adopter_proof_path}: {source} missing {field}")
        for field in ("claimProvenanceIds", "dashboardReportFields", "gateCommands"):
            if not item.get(field):
                missing.append(f"{adopter_proof_path}: {source} missing non-empty {field}")
        for claim_id in item.get("claimProvenanceIds") or []:
            if claim_id not in claim_ids:
                missing.append(f"{adopter_proof_path}: {source} unknown claim provenance id {claim_id!r}")
            if claim_id not in text:
                missing.append(f"{manual}: decision contract missing claim provenance id {claim_id!r}")
        for dashboard_field in item.get("dashboardReportFields") or []:
            if dashboard_field not in dashboard_text:
                missing.append(f"{dashboard_path}: missing dashboard report field {dashboard_field!r}")
            if dashboard_field not in text:
                missing.append(f"{manual}: decision contract missing dashboard field {dashboard_field!r}")
        for gate in item.get("gateCommands") or []:
            if gate not in text:
                missing.append(f"{manual}: decision contract missing gate {gate!r}")
        for field in ("manualPath", "migrationProofExample", "rollbackAction", "supportBundleAction"):
            value = item.get(field)
            if value and not contains_normalized(text, value):
                missing.append(f"{manual}: decision contract missing {source} {field} text")
        caveat = item.get("compatibilityCaveat") or ""
        caveat_prefix = caveat.split(";")[0].rstrip(".")
        if caveat_prefix and not contains_normalized(text, caveat_prefix):
            missing.append(f"{manual}: decision contract missing {source} compatibility caveat text")

    migration_paths = {
        "Gin to gofly": [
            "examples/restserver",
            "Gin `:id` routes become gofly `{id}` routes",
            "go test -C examples/restserver ./...",
            "Keep the Gin route active",
        ],
        "go-zero to gofly": [
            "examples/production-orders",
            "Preserve `.api` request/response field names",
            "make generated-version-compat-check",
            "Keep the go-zero endpoint addressable",
        ],
        "Kratos to gofly": [
            "examples/microshop",
            "compare lifecycle hooks",
            "make cloud-native-render-check",
            "Restore the previous Kratos deployment target",
        ],
        "Kitex with gofly": [
            "examples/rpc-idl-matrix",
            "Do not migrate hot methods without `bench/` evidence",
            "make rpc-boundary-check",
            "Route latency-critical methods back to Kitex",
        ],
    }
    for name, terms in migration_paths.items():
        if name not in text:
            missing.append(f"{manual}: missing migration path {name!r}")
            continue
        for term in terms:
            if term not in text:
                missing.append(f"{manual}: migration path {name!r} missing {term!r}")

    proof = subprocess.run(
        ["go", "run", "-C", "examples/migration-proof", "."],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if proof.returncode != 0:
        missing.append("examples/migration-proof runnable case failed:\n" + proof.stdout)
    else:
        try:
            report = json.loads(proof.stdout)
        except json.JSONDecodeError as exc:
            missing.append(f"examples/migration-proof emitted invalid JSON: {exc}")
            report = {}
        if report.get("schema") != "gofly.migration_proof.v1":
            missing.append("examples/migration-proof schema mismatch")
        cases = {item.get("source"): item for item in report.get("cases") or [] if isinstance(item, dict)}
        expected_cases = {
            "gin": {
                "manualPath": "Gin to gofly",
                "example": "examples/restserver",
                "gate": "go test -C examples/restserver ./...",
            },
            "go-zero": {
                "manualPath": "go-zero to gofly",
                "example": "examples/production-orders",
                "gate": "make generated-version-compat-check",
            },
            "kratos": {
                "manualPath": "Kratos to gofly",
                "example": "examples/microshop",
                "gate": "make cloud-native-render-check",
            },
            "kitex": {
                "manualPath": "Kitex with gofly",
                "example": "examples/rpc-idl-matrix",
                "gate": "make rpc-boundary-check",
            },
        }
        if set(cases) != set(expected_cases):
            missing.append(f"examples/migration-proof sources = {sorted(cases)}, want {sorted(expected_cases)}")
        for source, expected in expected_cases.items():
            item = cases.get(source) or {}
            decision = adopter_decisions.get(source) or {}
            if not decision:
                missing.append(f"{adopter_proof_path}: adopterDecisionEvidence missing {source!r}")
            if item.get("example") != expected["example"]:
                missing.append(f"examples/migration-proof {source}: example = {item.get('example')!r}, want {expected['example']!r}")
            if decision.get("manualPath") != expected["manualPath"]:
                missing.append(f"{adopter_proof_path}: {source} manualPath = {decision.get('manualPath')!r}, want {expected['manualPath']!r}")
            if decision.get("migrationProofExample") != expected["example"]:
                missing.append(f"{adopter_proof_path}: {source} migrationProofExample = {decision.get('migrationProofExample')!r}, want {expected['example']!r}")
            if expected["manualPath"] not in text:
                missing.append(f"{manual}: missing decision table row {expected['manualPath']!r}")
            if expected["gate"] not in (item.get("gateCommands") or []):
                missing.append(f"examples/migration-proof {source}: gateCommands missing {expected['gate']!r}")
            for gate in decision.get("gateCommands") or []:
                if gate not in (item.get("gateCommands") or []):
                    missing.append(f"examples/migration-proof {source}: gateCommands missing adopter contract gate {gate!r}")
            rollback = item.get("rollback") or ""
            if decision.get("rollbackAction") and not contains_normalized(rollback, decision["rollbackAction"]):
                missing.append(f"examples/migration-proof {source}: rollback does not match adopter contract")
            caveats = " ".join(item.get("compatibilityCaveats") or [])
            decision_caveat = decision.get("compatibilityCaveat") or ""
            first_caveat_phrase = decision_caveat.split(";")[0].rstrip(".")
            if first_caveat_phrase and not contains_normalized(caveats, first_caveat_phrase):
                missing.append(f"examples/migration-proof {source}: compatibility caveats do not match adopter contract")
            for field in ("rollback", "compatibilityCaveats", "decisionTable"):
                if not item.get(field):
                    missing.append(f"examples/migration-proof {source}: missing {field}")
            decision = item.get("decisionTable") or {}
            for field in ("chooseWhen", "keepSourceWhen", "adopterAction", "rollbackTrigger"):
                if not decision.get(field):
                    missing.append(f"examples/migration-proof {source}: decisionTable missing {field}")
            if not item.get("validation"):
                missing.append(f"examples/migration-proof {source}: validation must include smoke commands")

for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("adopter decision guide check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("adopter decision guide governance ok")
PY
