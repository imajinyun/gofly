#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

checks = {
    pathlib.Path(".goreleaser.yaml"): [
        "project_name: gofly",
        "cmd/gofly",
        "darwin",
        "linux",
        "windows",
        "amd64",
        "arm64",
        "checksum",
        "sboms",
        "dockers",
        "docker_manifests",
        "ghcr.io/gofly/gofly",
    ],
    pathlib.Path("docs/releases/stable.md"): [
        "release evidence manifest",
        "checksums.txt",
        "SBOM",
        "Docker image tags and digest",
        "provenance",
        "make release-snapshot",
    ],
    pathlib.Path("docs/releases/evidence-manifest.json"): [
        "gofly.release_evidence_manifest.v1",
        "schema_contract",
        "artifact_groups",
        "required_gates",
        "evidence_policy",
        "archives",
        "checksums",
        "sbom",
        "docker_digest",
        "docker_trivy",
        "docker_sbom",
        "provenance_attestation",
        "tag_governance",
    ],
    pathlib.Path("docs/releases/evidence-index.json"): [
        "gofly.release_evidence_index.v1",
        "schemaContract",
        "stableIdPolicy",
        "artifactPath",
        "producerJob",
        "localGate",
        "releaseRequired",
        "checksums",
        "archive-sbom",
        "docker-sbom",
        "checksums-attestation",
        "docker-attestation",
        "docker-digest",
        "trivy",
        "api-compat",
        "security",
        "race",
        "bench",
        "governance-dashboard",
    ],
    pathlib.Path("Makefile"): [
        "release-config-check",
        "check-release-config.sh",
        "release-snapshot: release-config-check",
    ],
    pathlib.Path(".github/workflows/ci.yml"): [
        "release (tagged)",
        "needs: [build-test, platform-smoke, lint, security, supply-chain, codeql, dependency-upgrade-validation, contract-check, governance, bench-fuzz, integration, docker, scorecard]",
        "make release-artifacts-check",
        "Collect release Docker digest evidence",
        "release-docker-digests.json",
        "Collect release Docker SBOM evidence",
        "Trivy release image scan",
        "Download Docker and Trivy evidence",
        "Attest release checksums",
        "Attest Docker release manifest",
        "Verify release attestations",
        "checksums-attestation-verification.json",
        "release-docker-attestation-verification.json",
        "RELEASE_REQUIRE_DOCKER_EVIDENCE",
        "Upload release verification evidence",
        "release-evidence/docker/**",
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
    print("release config check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

manifest_path = pathlib.Path("docs/releases/evidence-manifest.json")
if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    required_fields = set((manifest.get("schema_contract") or {}).get("required_fields") or [])
    for field in (
        "schema",
        "archives",
        "checksums",
        "sbom",
        "docker_digest",
        "docker_trivy",
        "docker_sbom",
        "provenance_attestation",
        "tag_governance",
        "artifact_groups",
        "required_gates",
        "evidence_policy",
    ):
        if field not in manifest:
            missing.append(f"docs/releases/evidence-manifest.json: missing field {field!r}")
        if field not in required_fields:
            missing.append(f"docs/releases/evidence-manifest.json: schema_contract.required_fields missing {field!r}")
    if (manifest.get("schema_contract") or {}).get("version") != "1":
        missing.append("docs/releases/evidence-manifest.json: schema_contract.version must be 1")
    if len(manifest.get("archives") or []) < 6:
        missing.append("docs/releases/evidence-manifest.json: archives must cover at least 6 platform archives")
    required_gates = set(manifest.get("required_gates") or [])
    for gate in (
        "make release-config-check",
        "make release-snapshot",
        "make release-artifacts-check",
        "make release-artifacts-test",
    ):
        if gate not in required_gates:
            missing.append(f"docs/releases/evidence-manifest.json: required_gates missing {gate!r}")
    groups = {item.get("id"): item for item in manifest.get("artifact_groups") or [] if isinstance(item, dict)}
    for group_id in ("binary-archives", "release-container", "provenance-attestations"):
        group = groups.get(group_id)
        if not group:
            missing.append(f"docs/releases/evidence-manifest.json: artifact_groups missing {group_id!r}")
            continue
        if group.get("required") is not True:
            missing.append(f"docs/releases/evidence-manifest.json: artifact group {group_id!r} must be required")
        if not group.get("gate"):
            missing.append(f"docs/releases/evidence-manifest.json: artifact group {group_id!r} missing gate")
        if not group.get("artifacts"):
            missing.append(f"docs/releases/evidence-manifest.json: artifact group {group_id!r} missing artifacts")
    policy = manifest.get("evidence_policy") or {}
    if policy.get("allow_empty_artifacts") is not False:
        missing.append("docs/releases/evidence-manifest.json: evidence_policy.allow_empty_artifacts must be false")
    if policy.get("allow_release_gate_skips") is not False:
        missing.append("docs/releases/evidence-manifest.json: evidence_policy.allow_release_gate_skips must be false")
    for directory in ("dist", "release-evidence"):
        if directory not in (policy.get("volatile_output_directories") or []):
            missing.append(f"docs/releases/evidence-manifest.json: evidence_policy.volatile_output_directories missing {directory!r}")

index_path = pathlib.Path("docs/releases/evidence-index.json")
if index_path.is_file():
    index = json.loads(index_path.read_text(encoding="utf-8"))
    if index.get("schema") != "gofly.release_evidence_index.v1":
        missing.append("docs/releases/evidence-index.json: schema must be gofly.release_evidence_index.v1")
    contract = index.get("schemaContract") or {}
    if contract.get("version") != "1":
        missing.append("docs/releases/evidence-index.json: schemaContract.version must be 1")
    required_fields = set(contract.get("requiredFields") or [])
    for field in ("id", "artifactPath", "producerJob", "localGate", "releaseRequired"):
        if field not in required_fields:
            missing.append(f"docs/releases/evidence-index.json: schemaContract.requiredFields missing {field!r}")
    evidence = index.get("evidence") or []
    required_ids = {
        "checksums",
        "archive-sbom",
        "docker-sbom",
        "checksums-attestation",
        "docker-attestation",
        "docker-digest",
        "trivy",
        "api-compat",
        "security",
        "race",
        "bench",
        "governance-dashboard",
    }
    seen_ids = set()
    for item in evidence:
        if not isinstance(item, dict):
            missing.append(f"docs/releases/evidence-index.json: evidence entry must be an object: {item!r}")
            continue
        item_id = item.get("id", "")
        if item_id in seen_ids:
            missing.append(f"docs/releases/evidence-index.json: duplicate evidence id {item_id!r}")
        seen_ids.add(item_id)
        for field in ("id", "artifactPath", "producerJob", "localGate"):
            if not item.get(field):
                missing.append(f"docs/releases/evidence-index.json: evidence {item_id or '<missing>'} missing {field!r}")
        if item.get("releaseRequired") is not True:
            missing.append(f"docs/releases/evidence-index.json: evidence {item_id or '<missing>'} must be releaseRequired")
    for item_id in sorted(required_ids - seen_ids):
        missing.append(f"docs/releases/evidence-index.json: missing evidence id {item_id!r}")

workflow = pathlib.Path(".github/workflows/ci.yml").read_text(encoding="utf-8")
release_start = workflow.find("  release:\n")
if release_start == -1:
    missing.append(".github/workflows/ci.yml: missing release job")
else:
    next_job_match = re.search(r"\n  [A-Za-z0-9_-]+:\n", workflow[release_start + len("  release:\n"):])
    if next_job_match:
        next_job = release_start + len("  release:\n") + next_job_match.start()
        release_job = workflow[release_start:next_job]
    else:
        release_job = workflow[release_start:]
    required_needs = {
        "build-test",
        "platform-smoke",
        "lint",
        "security",
        "supply-chain",
        "codeql",
        "dependency-upgrade-validation",
        "contract-check",
        "governance",
        "bench-fuzz",
        "integration",
        "docker",
        "scorecard",
    }
    needs_line = next((line for line in release_job.splitlines() if line.strip().startswith("needs:")), "")
    missing_needs = sorted(need for need in required_needs if need not in needs_line)
    for need in missing_needs:
        missing.append(f".github/workflows/ci.yml: release job missing required need {need!r}")
    release_permissions = {
        "contents: write",
        "id-token: write",
        "attestations: write",
        "packages: write",
    }
    for permission in sorted(release_permissions):
        if permission not in release_job:
            missing.append(f".github/workflows/ci.yml: release job missing permission {permission!r}")
    forbidden_release_skips = [
        "GOVERNANCE_SKIP_SECURITY: \"true\"",
        "GOVERNANCE_SKIP_RACE: \"true\"",
        "API_COMPAT_REQUIRED: \"false\"",
        "RELEASE_REQUIRE_DOCKER_EVIDENCE: \"false\"",
    ]
    for forbidden in forbidden_release_skips:
        if forbidden in release_job:
            missing.append(f".github/workflows/ci.yml: release job must not set {forbidden!r}")

if missing:
    print("release config check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("release config governance ok")
PY
