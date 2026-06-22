#!/usr/bin/env sh
set -eu

dist="${RELEASE_DIST_DIR:-dist}"

python3 - "$dist" <<'PY'
import hashlib
import json
import pathlib
import sys

dist = pathlib.Path(sys.argv[1])
if not dist.is_dir():
    raise SystemExit(f"release dist directory {dist} does not exist")

archives = sorted([p for p in dist.iterdir() if p.suffix in {".gz", ".zip"} or p.name.endswith(".tar.gz")])
if not archives:
    raise SystemExit("release dist contains no archives")

checksums = dist / "checksums.txt"
if not checksums.is_file() or checksums.stat().st_size == 0:
    raise SystemExit("release dist is missing non-empty checksums.txt")

checksum_entries = {}
for line in checksums.read_text(encoding="utf-8").splitlines():
    parts = line.split()
    if len(parts) >= 2:
        checksum_entries[parts[-1]] = parts[0]

missing = [p.name for p in archives if p.name not in checksum_entries]
if missing:
    raise SystemExit(f"checksums.txt does not cover archive(s): {', '.join(missing)}")

for archive in archives:
    got = hashlib.sha256(archive.read_bytes()).hexdigest()
    want = checksum_entries[archive.name]
    if got != want:
        raise SystemExit(f"checksum mismatch for {archive.name}: got {got}, want {want}")

sboms = sorted(dist.glob("*.spdx.json"))
if not sboms:
    raise SystemExit("release dist contains no archive SBOM (*.spdx.json)")
for sbom in sboms:
    data = json.loads(sbom.read_text(encoding="utf-8"))
    if not data.get("SPDXID") or not data.get("packages"):
        raise SystemExit(f"SBOM {sbom.name} is missing SPDXID or packages")

docker_digest_files = sorted(dist.glob("*.docker_digest")) + sorted(dist.glob("**/digest"))
docker_sboms = sorted(dist.glob("*docker*sbom*")) + sorted(dist.glob("*.cosign.json"))
print(f"release archives verified: {len(archives)}")
print(f"release SBOMs verified: {len(sboms)}")
if docker_digest_files:
    print(f"docker digest evidence files: {len(docker_digest_files)}")
else:
    print("docker digest evidence not present in local dist; CI release image provenance/trivy gates provide container traceability")
if docker_sboms:
    print(f"docker SBOM/provenance evidence files: {len(docker_sboms)}")
else:
    print("docker SBOM evidence not present in local dist; GoReleaser docker manifest plus CI Trivy/provenance gates remain required")
PY
