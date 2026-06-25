# Dependency Upgrade Evidence

Schema: `gofly.dependency_upgrade_evidence.v1`

Dependency upgrades are release-risk changes because they can alter generated
output, security posture, transitive module graphs, and Docker-backed adapters.
The evidence contract lives in
[`dependency-upgrade-evidence.json`](dependency-upgrade-evidence.json).

## Gates

Run locally before merging dependency manifest changes:

```sh
make dependency-upgrade-evidence-check
make dependency-upgrade-check
```

`make dependency-upgrade-evidence-check` validates the evidence contract, CI
delegation, Makefile targets, and operator docs. `make dependency-upgrade-check`
runs module checksum verification, vulnerability scanning, and Docker-backed
integration tests unless the integration matrix is intentionally delegated.

CI uses:

```sh
make dependency-upgrade-check DEPENDENCY_UPGRADE_RUN_INTEGRATION=false
```

The `DEPENDENCY_UPGRADE_RUN_INTEGRATION=false` path is allowed only because the
required CI integration matrix owns Docker-backed coverage for storage, config,
discovery, MQ, and gateway packages.

## Required Evidence

| Evidence | Command | Artifact |
| --- | --- | --- |
| module verification | `go mod verify` | command log or CI step summary |
| vulnerability scan | `make govulncheck` | govulncheck output or security job summary |
| Docker-backed integration | `make integration-tests` | integration matrix job logs |
| root-dependency-policy | `make root-dependency-policy-check` | root dependency policy check output |

Root `go.mod` changes must also stay aligned with the root dependency policy so
generated-project-only dependencies remain in generated projects rather than the
framework root module.
