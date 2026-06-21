# Stable Releases

This page is the stable release entry point for installing and verifying gofly.

## Install

```sh
go install github.com/gofly/gofly/cmd/gofly@latest
gofly version
```

Tagged releases publish CLI binaries with SHA-256 checksums and SBOM metadata through GoReleaser.

## Verify Binary Artifacts

```sh
sha256sum -c checksums.txt
```

Each release should record:

- version, commit, and build timestamp;
- `checksums.txt` digest list;
- archive SBOM files;
- Docker image tags and digest;
- security scan status.

## Docker Image

```sh
docker pull ghcr.io/gofly/gofly:latest
docker run --rm ghcr.io/gofly/gofly:latest version
```

## Compatibility Policy

- CLI JSON output, generated project layout, admin control-plane fields, and public Go APIs are treated as compatibility surfaces.
- Breaking changes require release notes and migration guidance.
- Generated projects should continue to compile across the supported Go version line.
- See the full [Compatibility Policy](../reference/compatibility.md) and [Stable API Surface Reference](../reference/api-surface.md).

## Benchmark Evidence

Before tagging a release that changes REST, RPC, gateway, governance, or code generation hot paths:

```sh
make bench-stat
make bench-trend
make bench-matrix
```

Attach raw benchmark output and the generated trend summary to release notes. See [Benchmark Matrix Reference](../reference/benchmark-matrix.md).
