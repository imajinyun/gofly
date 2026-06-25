#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
index_path = root / "docs" / "releases" / "evidence-index.json"
manifest_path = root / "docs" / "releases" / "evidence-manifest.json"
missing = []

if not index_path.is_file():
    missing.append("docs/releases/evidence-index.json is missing")
    index = {}
else:
    index = json.loads(index_path.read_text(encoding="utf-8"))

if index.get("schema") != "gofly.release_evidence_index.v1":
    missing.append("docs/releases/evidence-index.json: schema must be gofly.release_evidence_index.v1")

contract = index.get("schemaContract") or {}
if contract.get("version") != "1":
    missing.append("docs/releases/evidence-index.json: schemaContract.version must be 1")
if not contract.get("stableIdPolicy"):
    missing.append("docs/releases/evidence-index.json: schemaContract.stableIdPolicy is required")

required_fields = set(contract.get("requiredFields") or [])
for field in ("id", "artifactPath", "producerJob", "localGate", "releaseRequired"):
    if field not in required_fields:
        missing.append(f"docs/releases/evidence-index.json: schemaContract.requiredFields missing {field!r}")

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
expected_jobs = {
    "checksums": "release",
    "archive-sbom": "release",
    "docker-sbom": "release",
    "checksums-attestation": "release",
    "docker-attestation": "release",
    "docker-digest": "release",
    "trivy": "release",
    "api-compat": "contract-check",
    "security": "security",
    "race": "governance",
    "bench": "bench-fuzz",
    "governance-dashboard": "governance",
}
expected_gates = {
    "checksums": "make release-artifacts-check",
    "archive-sbom": "make release-artifacts-check",
    "docker-sbom": "RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check",
    "checksums-attestation": "RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check",
    "docker-attestation": "RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check",
    "docker-digest": "RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check",
    "trivy": "RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check",
    "api-compat": "make api-compat",
    "security": "make govulncheck && make gosec",
    "race": "go test -race ./...",
    "bench": "make bench-evidence-check",
    "governance-dashboard": "make governance-report-check",
}

seen_ids = set()
for item in index.get("evidence") or []:
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
    if item_id in expected_jobs and item.get("producerJob") != expected_jobs[item_id]:
        missing.append(
            f"docs/releases/evidence-index.json: evidence {item_id}: producerJob = "
            f"{item.get('producerJob')!r}, want {expected_jobs[item_id]!r}"
        )
    if item_id in expected_gates and item.get("localGate") != expected_gates[item_id]:
        missing.append(
            f"docs/releases/evidence-index.json: evidence {item_id}: localGate = "
            f"{item.get('localGate')!r}, want {expected_gates[item_id]!r}"
        )

for item_id in sorted(required_ids - seen_ids):
    missing.append(f"docs/releases/evidence-index.json: missing evidence id {item_id!r}")

if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("schema") != "gofly.release_evidence_manifest.v1":
        missing.append("docs/releases/evidence-manifest.json: schema must be gofly.release_evidence_manifest.v1")
    manifest_text = manifest_path.read_text(encoding="utf-8")
    for item in index.get("evidence") or []:
        artifact_path = item.get("artifactPath", "")
        if artifact_path.startswith("release-evidence/docker/") and artifact_path not in manifest_text:
            missing.append(
                "docs/releases/evidence-manifest.json: missing release Docker evidence path "
                f"{artifact_path!r}"
            )
else:
    missing.append("docs/releases/evidence-manifest.json is missing")

makefile = (root / "Makefile").read_text(encoding="utf-8") if (root / "Makefile").is_file() else ""
if "release-evidence-index-check:" not in makefile:
    missing.append("Makefile: release-evidence-index-check target is missing")
if "check-release-evidence-index.sh" not in makefile:
    missing.append("Makefile: release-evidence-index-check must call check-release-evidence-index.sh")
release_artifacts_line = next(
    (line for line in makefile.splitlines() if line.startswith("release-artifacts-check:")),
    "",
)
if "release-evidence-index-check" not in release_artifacts_line:
    missing.append("Makefile: release-artifacts-check must depend on release-evidence-index-check")

workflow = (root / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")
for marker in (
    "release-evidence/docker/release-docker-digests.json",
    "release-evidence/docker/release-docker-sbom.spdx.json",
    "release-evidence/docker/release-trivy-results.json",
    "release-evidence/checksums-attestation-verification.json",
    "release-evidence/docker/release-docker-attestation-verification.json",
    "Upload release verification evidence",
):
    if marker not in workflow:
        missing.append(f".github/workflows/ci.yml: missing release evidence marker {marker!r}")

stable_doc = (root / "docs" / "releases" / "stable.md").read_text(encoding="utf-8")
for marker in ("evidence-index.json", "producer job", "local gate"):
    if marker not in stable_doc:
        missing.append(f"docs/releases/stable.md: missing {marker!r}")

if missing:
    print("release evidence index check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("release evidence index governance ok")
PY
