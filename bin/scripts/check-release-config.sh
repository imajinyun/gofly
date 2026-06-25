#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
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

print("release config governance ok")
PY
