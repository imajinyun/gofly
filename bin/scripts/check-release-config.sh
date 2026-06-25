#!/usr/bin/env sh
set -eu

python3 - <<'PY'
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
        "archives",
        "checksums",
        "sbom",
        "docker_digest",
        "provenance_attestation",
        "tag_governance",
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
