# Stable Releases

This page is the stable release entry point for installing and verifying gofly.

## Install

```sh
go install github.com/imajinyun/gofly/cmd/gofly@latest
gofly version
```

Tagged releases publish CLI binaries with SHA-256 checksums and SBOM metadata through GoReleaser.

Local release convergence starts with:

```sh
make release-snapshot
```

The release evidence manifest lives at
[`docs/releases/evidence-manifest.json`](evidence-manifest.json). It names the
multi-platform archives, `checksums.txt`, archive SBOM files, Docker image tags
and digest, and provenance attestation evidence expected before a tag is
promoted.

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

## Release Compatibility Checklist

Before tagging a release, record compatibility evidence for each affected adoption tier:

| Tier | Required evidence |
| --- | --- |
| Tier 0 | Generated production service smoke test, generated `go test ./...`, and generated verification target results. |
| Tier 1 | API compatibility report, CLI JSON contract checks, control-plane contract checks, and OpenAPI/RPC breaking checks when contracts changed. |
| Tier 2 | Subsystem smoke tests, migration notes for breaking changes, and generated-project or example updates that prove the new behavior. |
| Tier 3 | Experimental labels in docs/help/generated files and pinning guidance for users who depend on the surface. |

For v1 candidate surfaces, also run:

```sh
make stable-surface-check
make deprecation-lifecycle-check
```

The [stable surface governance](../reference/stable-surface.md) checklist is the
release evidence manifest for `rest`, `core/governance`, `core/controlplane`,
CLI JSON, and the generated production service. Release notes for these surfaces
must name the deprecation status, coexistence window, rollback guidance, and
whether the change is a compatible addition, behavioral fix, deprecation, or
breaking candidate.

If a stable or Tier 1 surface is deprecated, the release note must include the replacement, coexistence window, first deprecated version, and expected removal version. Security-driven removals must include the risk and mitigation.
`make deprecation-lifecycle-check` validates the
`gofly.deprecation_lifecycle.v1` manifest so active deprecations include
rollback guidance, validation gates, and a one-minor-release coexistence window.

## Benchmark Evidence

Before tagging a release that changes REST, RPC, gateway, governance, or code generation hot paths:

```sh
make bench-stat
make bench-trend
make bench-matrix
make bench-regression-check
make bench-publish-check
```

Attach raw benchmark output, the generated trend summary, and
`bench/regression-report.json` to release notes. `bench/publishing.json`
defines the `gofly.benchmark_publishing.v1` machine-readable publishing
contract for release automation. See [Benchmark Matrix Reference](../reference/benchmark-matrix.md).

## Fuzz Robustness Evidence

Before tagging a release that changes parser or REST binding behavior:

```sh
make fuzz-robustness-check
make fuzz-smoke
```

`make fuzz-robustness-check` validates the `gofly.fuzz_robustness.v1`
manifest, existing fuzz targets, CI smoke commands, and release documentation.
`make fuzz-smoke` runs bounded 20-second fuzz smoke coverage for `.api` parser,
`.proto` parser, JSON binding, and query binding surfaces.
