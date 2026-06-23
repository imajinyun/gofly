#!/usr/bin/env sh
set -eu

script_dir=$(unset CDPATH && cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(unset CDPATH && cd -- "$script_dir/../.." && pwd)
check_script="$script_dir/check-release-artifacts.sh"

tmp_root=$(mktemp -d)
trap 'rm -rf "$tmp_root"' EXIT INT HUP TERM

make_fixture() {
  fixture_dir=$1
  mode=${2:-valid}
  dist_dir="$fixture_dir/dist"
  evidence_dir="$fixture_dir/release-evidence/docker"

  mkdir -p "$dist_dir" "$evidence_dir/ci"

  python3 - "$dist_dir" "$evidence_dir" "$mode" <<'PY'
import hashlib
import json
import pathlib
import sys

dist = pathlib.Path(sys.argv[1])
evidence = pathlib.Path(sys.argv[2])
mode = sys.argv[3]

archive = dist / "gofly_Darwin_x86_64.tar.gz"
archive.write_bytes(b"gofly release archive fixture\n")
archive_sbom = dist / f"{archive.name}.spdx.json"
archive_sbom.write_text(
    json.dumps({"SPDXID": "SPDXRef-DOCUMENT", "packages": [{"name": "gofly"}]}, sort_keys=True) + "\n",
    encoding="utf-8",
)

checksums = dist / "checksums.txt"
with checksums.open("w", encoding="utf-8") as fh:
    for path in (archive, archive_sbom):
        fh.write(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n")

manifest_digest = "sha256:" + "a" * 64
(evidence / "release-docker-digests.json").write_text(
    json.dumps(
        {
            "schema": "gofly.release_docker_digest_evidence.v1",
            "manifest_digest": manifest_digest,
            "platforms": ["linux/amd64", "linux/arm64"],
        },
        sort_keys=True,
    )
    + "\n",
    encoding="utf-8",
)
(evidence / "release-trivy-results.json").write_text(
    json.dumps({"ArtifactName": f"ghcr.io/example/gofly@{manifest_digest}", "Results": []}, sort_keys=True) + "\n",
    encoding="utf-8",
)
(evidence / "release-docker-sbom.spdx.json").write_text(
    json.dumps({"SPDXID": "SPDXRef-DOCKER", "packages": [{"name": "gofly-container"}]}, sort_keys=True) + "\n",
    encoding="utf-8",
)
(evidence / "ci" / "trivy-results.sarif").write_text("{\"version\":\"2.1.0\"}\n", encoding="utf-8")
(evidence / "ci" / "docker-build-evidence.json").write_text(
    json.dumps(
        {
            "schema": "gofly.docker_build_evidence.v1",
            "image_ref": "gofly:fixture",
            "image_id": "sha256:" + "d" * 64,
            "repo_digests": [],
            "repo_tags": ["gofly:fixture"],
            "build_metadata": {},
        },
        sort_keys=True,
    )
    + "\n",
    encoding="utf-8",
)

checksums_digest = hashlib.sha256(checksums.read_bytes()).hexdigest()
if mode == "bad-checksums-attestation":
    checksums_digest = "b" * 64

checksums_attestation = [
    {"verificationResult": {"statement": {"subject": [{"digest": {"sha256": checksums_digest}}]}}}
]
(evidence.parent / "checksums-attestation-verification.json").write_text(
    json.dumps(checksums_attestation, sort_keys=True) + "\n",
    encoding="utf-8",
)

docker_digest = manifest_digest.removeprefix("sha256:")
if mode == "bad-docker-attestation":
    docker_digest = "c" * 64
docker_attestation = [
    {"verificationResult": {"statement": {"subject": [{"digest": {"sha256": docker_digest}}]}}}
]
(evidence / "release-docker-attestation-verification.json").write_text(
    json.dumps(docker_attestation, sort_keys=True) + "\n",
    encoding="utf-8",
)

if mode == "trivy-vulnerability":
    (evidence / "release-trivy-results.json").write_text(
        json.dumps(
            {
                "ArtifactName": f"ghcr.io/example/gofly@{manifest_digest}",
                "Results": [{"Vulnerabilities": [{"VulnerabilityID": "CVE-FIXTURE", "Severity": "HIGH"}]}],
            },
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
PY
}

run_success() {
  name=$1
  fixture_dir="$tmp_root/$name"
  out="$tmp_root/$name.out"
  make_fixture "$fixture_dir" valid
  RELEASE_DIST_DIR="$fixture_dir/dist" \
    RELEASE_EVIDENCE_DIR="$fixture_dir/release-evidence/docker" \
    RELEASE_REQUIRE_DOCKER_EVIDENCE=true \
    sh "$check_script" >"$out"
}

run_failure() {
  name=$1
  mode=$2
  want=$3
  fixture_dir="$tmp_root/$name"
  out="$tmp_root/$name.out"
  make_fixture "$fixture_dir" "$mode"
  if RELEASE_DIST_DIR="$fixture_dir/dist" \
    RELEASE_EVIDENCE_DIR="$fixture_dir/release-evidence/docker" \
    RELEASE_REQUIRE_DOCKER_EVIDENCE=true \
    sh "$check_script" >"$out" 2>&1; then
    echo "expected $mode fixture to fail" >&2
    return 1
  fi
  if ! grep -F "$want" "$out" >/dev/null; then
    echo "expected $mode failure to contain: $want" >&2
    echo "actual output:" >&2
    cat "$out" >&2
    return 1
  fi
}

run_success "valid-release-evidence"
run_failure "bad-checksums-attestation" "bad-checksums-attestation" "checksums attestation does not bind"
run_failure "bad-docker-attestation" "bad-docker-attestation" "Docker attestation does not bind"
run_failure "trivy-vulnerability" "trivy-vulnerability" "HIGH/CRITICAL vulnerabilities"

printf 'release artifact provenance fixture tests passed for %s\n' "$repo_root"
