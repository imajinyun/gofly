#!/usr/bin/env sh
set -eu

dist="${RELEASE_DIST_DIR:-dist}"
evidence_dir="${RELEASE_EVIDENCE_DIR:-release-evidence/docker}"
require_docker_evidence="${RELEASE_REQUIRE_DOCKER_EVIDENCE:-false}"

python3 - "$dist" "$evidence_dir" "$require_docker_evidence" <<'PY'
import hashlib
import json
import pathlib
import sys

dist = pathlib.Path(sys.argv[1])
evidence_dir = pathlib.Path(sys.argv[2])
require_docker_evidence = sys.argv[3].lower() == "true"

def load_attestation_entries(path):
    if not path.is_file() or path.stat().st_size == 0:
        raise SystemExit(f"missing release attestation verification evidence: {path}")
    entries = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(entries, list) or not entries:
        raise SystemExit(f"empty release attestation verification evidence: {path}")
    return entries

def attestation_subject_matches(entries, sha256_digest):
    for entry in entries:
        statement = entry.get("verificationResult", {}).get("statement", {})
        for subject in statement.get("subject", []) or []:
            if subject.get("digest", {}).get("sha256") == sha256_digest:
                return True
    return False

if not dist.is_dir():
    raise SystemExit(f"release dist directory {dist} does not exist")

archives = sorted([p for p in dist.iterdir() if p.suffix in {".gz", ".zip"} or p.name.endswith(".tar.gz")])
if not archives:
    raise SystemExit("release dist contains no archives")
expected_sboms = [dist / f"{archive.name}.spdx.json" for archive in archives]

checksums = dist / "checksums.txt"
if not checksums.is_file() or checksums.stat().st_size == 0:
    raise SystemExit("release dist is missing non-empty checksums.txt")

checksum_entries = {}
for line in checksums.read_text(encoding="utf-8").splitlines():
    parts = line.split()
    if len(parts) >= 2:
        checksum_entries[parts[-1]] = parts[0]

missing_sboms = [sbom.name for sbom in expected_sboms if not sbom.is_file()]
if missing_sboms:
    raise SystemExit(f"release dist is missing archive SBOM(s): {', '.join(missing_sboms)}")

checksum_required_files = archives + expected_sboms
missing = [p.name for p in checksum_required_files if p.name not in checksum_entries]
if missing:
    raise SystemExit(f"checksums.txt does not cover release file(s): {', '.join(missing)}")

stale_checksums = [name for name in checksum_entries if not (dist / name).is_file()]
if stale_checksums:
    raise SystemExit(f"checksums.txt references missing release file(s): {', '.join(stale_checksums)}")

for release_file in checksum_required_files:
    got = hashlib.sha256(release_file.read_bytes()).hexdigest()
    want = checksum_entries[release_file.name]
    if got != want:
        raise SystemExit(f"checksum mismatch for {release_file.name}: got {got}, want {want}")

for sbom in expected_sboms:
    data = json.loads(sbom.read_text(encoding="utf-8"))
    if not data.get("SPDXID") or not data.get("packages"):
        raise SystemExit(f"SBOM {sbom.name} is missing SPDXID or packages")

docker_digest_files = sorted(dist.glob("*.docker_digest")) + sorted(dist.glob("**/digest"))
docker_sboms = sorted(dist.glob("*docker*sbom*")) + sorted(dist.glob("*.cosign.json"))
release_digest_report = evidence_dir / "release-docker-digests.json"
release_trivy = evidence_dir / "release-trivy-results.json"
release_docker_attestation = evidence_dir / "release-docker-attestation-verification.json"
checksums_attestation = evidence_dir.parent / "checksums-attestation-verification.json"
ci_trivy = evidence_dir / "ci" / "trivy-results.sarif"
ci_build_evidence = evidence_dir / "ci" / "docker-build-evidence.json"
if require_docker_evidence:
    if not release_digest_report.is_file() or release_digest_report.stat().st_size == 0:
        raise SystemExit(f"missing release Docker digest evidence: {release_digest_report}")
    digest_data = json.loads(release_digest_report.read_text(encoding="utf-8"))
    if digest_data.get("schema") != "gofly.release_docker_digest_evidence.v1":
        raise SystemExit(f"unexpected Docker digest evidence schema in {release_digest_report}")
    manifest_digest = digest_data.get("manifest_digest", "")
    if not manifest_digest.startswith("sha256:") or len(manifest_digest) != len("sha256:") + 64:
        raise SystemExit(f"invalid Docker manifest digest in {release_digest_report}: {manifest_digest}")
    platforms = set(digest_data.get("platforms") or [])
    if not {"linux/amd64", "linux/arm64"}.issubset(platforms):
        raise SystemExit(f"Docker digest evidence missing linux/amd64 or linux/arm64 platforms: {sorted(platforms)}")
    for required in (ci_trivy, ci_build_evidence):
        if not required.is_file() or required.stat().st_size == 0:
            raise SystemExit(f"missing Docker CI evidence artifact: {required}")
    ci_build_data = json.loads(ci_build_evidence.read_text(encoding="utf-8"))
    if ci_build_data.get("schema") != "gofly.docker_build_evidence.v1":
        raise SystemExit(f"unexpected Docker CI build evidence schema in {ci_build_evidence}")
    for field in ("image_ref", "image_id", "repo_digests", "repo_tags", "build_metadata"):
        if field not in ci_build_data:
            raise SystemExit(f"Docker CI build evidence missing field {field!r}: {ci_build_evidence}")
    if not release_trivy.is_file() or release_trivy.stat().st_size == 0:
        raise SystemExit(f"missing release Docker Trivy evidence: {release_trivy}")
    trivy_text = release_trivy.read_text(encoding="utf-8")
    trivy_data = json.loads(trivy_text)
    artifact_name = trivy_data.get("ArtifactName", "")
    if manifest_digest not in trivy_text and manifest_digest not in artifact_name:
        raise SystemExit(f"release Docker Trivy evidence does not reference {manifest_digest}: {release_trivy}")
    vulnerabilities = [
        vuln
        for result in trivy_data.get("Results", []) or []
        for vuln in result.get("Vulnerabilities", []) or []
    ]
    if vulnerabilities:
        raise SystemExit(f"release Docker Trivy evidence contains {len(vulnerabilities)} HIGH/CRITICAL vulnerabilities: {release_trivy}")
    release_docker_sboms = sorted(evidence_dir.glob("release-docker-sbom*.spdx.json"))
    if not release_docker_sboms:
        raise SystemExit(f"missing release Docker SBOM evidence in {evidence_dir}")
    for sbom in release_docker_sboms:
        data = json.loads(sbom.read_text(encoding="utf-8"))
        if not data.get("SPDXID") or not data.get("packages"):
            raise SystemExit(f"Docker SBOM {sbom} is missing SPDXID or packages")
    checksums_entries = load_attestation_entries(checksums_attestation)
    checksums_sha256 = hashlib.sha256(checksums.read_bytes()).hexdigest()
    if not attestation_subject_matches(checksums_entries, checksums_sha256):
        raise SystemExit(f"checksums attestation does not bind to dist/checksums.txt sha256: {checksums_attestation}")
    docker_entries = load_attestation_entries(release_docker_attestation)
    if not attestation_subject_matches(docker_entries, manifest_digest.removeprefix("sha256:")):
        raise SystemExit(f"Docker attestation does not bind to {manifest_digest}: {release_docker_attestation}")
print(f"release archives verified: {len(archives)}")
print(f"release SBOMs verified: {len(expected_sboms)}")
if docker_digest_files:
    print(f"docker digest evidence files: {len(docker_digest_files)}")
else:
    print("docker digest evidence not present in local dist; CI release image provenance/trivy gates provide container traceability")
if docker_sboms:
    print(f"docker SBOM/provenance evidence files: {len(docker_sboms)}")
else:
    print("docker SBOM evidence not present in local dist; GoReleaser docker manifest plus CI Trivy/provenance gates remain required")
if require_docker_evidence:
    print(f"release Docker digest evidence verified: {release_digest_report}")
    print(f"release Docker Trivy evidence verified: {release_trivy}")
    print(f"Docker CI Trivy/build evidence verified: {ci_trivy}, {ci_build_evidence}")
    print(f"release Docker SBOM evidence verified: {len(release_docker_sboms)}")
    print(f"release attestation verification evidence verified: {checksums_attestation}, {release_docker_attestation}")
PY
