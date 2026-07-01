# CI Required-Check Evidence

Schema: `gofly.ci_required_check_evidence.v1`

The required-check drift gate already validates the branch-protection set and
release prerequisites. This evidence contract adds the adopter-facing matrix:
each required check must map to a workflow job, a local gate or explicit hosted
service, and the artifact or log evidence reviewers should inspect. The
machine-readable manifest is
[`ci-required-check-evidence.json`](ci-required-check-evidence.json).

## Gate

```sh
make ci-required-check-evidence-check
```

`docs-check` depends on this gate, and the governance dashboard exposes the
same matrix so release notes can point to one stable source of CI evidence.

## Matrix

| Check | Workflow job | Local gate | Evidence |
| --- | --- | --- | --- |
| build & test (go 1.26) | `build-test` | `make ci-fast` | `coverage-go-1.26` |
| build & test (go stable) | `build-test` | `make ci-fast` | workflow logs and CLI version output |
| golangci-lint | `lint` | `make lint` | golangci-lint job logs |
| platform smoke (macos-latest) | `platform-smoke` | targeted `go test` smoke | platform smoke job logs |
| platform smoke (windows-latest) | `platform-smoke` | targeted `go test` smoke | platform smoke job logs |
| security (govulncheck + gosec) | `security` | `make security` | govulncheck and gosec output |
| supply-chain lint + OSV | `supply-chain` | `make supply-chain` | `api-compat-report` |
| CodeQL security analysis | `codeql` | GitHub CodeQL workflow | CodeQL code scanning result |
| dependency review | `dependency-review` | GitHub Dependency Review workflow | Dependency Review job summary |
| dependency upgrade validation | `dependency-upgrade-validation` | `make dependency-upgrade-check` | dependency upgrade job summary |
| branch protection required-check audit | `branch-protection-audit` | `make required-checks-drift-check` | `required-status-checks.json` |
| contract / api+rpc (check + breaking) | `contract-check` | `make stable-surface-check` | contract-check job logs |
| governance gates | `governance` | `make governance-10-rounds` | `governance-skip-report` |
| bench + fuzz smoke | `bench-fuzz` | `make bench-evidence-check` | `benchmark-smoke` |
| integration tests (storage-mysql-postgres) | `integration` | `make integration-tests` | integration matrix job logs |
| integration tests (config-consul-nacos-etcd) | `integration` | `make integration-tests` | integration matrix job logs |
| integration tests (mq-brokers) | `integration` | `make integration-tests` | integration matrix job logs |
| integration tests (gateway-transcode) | `integration` | `make integration-tests` | integration matrix job logs |
| cloud-native live render | `cloud-native-live-render` | `make cloud-native-render-check` | `cloud-native-live-render-evidence` |
| docker build + trivy | `docker` | `make docker` | `docker-trivy-evidence` |
| OSSF Scorecard | `scorecard` | OpenSSF Scorecard workflow | `scorecard-results` |

## Release Boundary

Tag releases depend on the tag-applicable jobs listed in
`releasePrerequisites`. Pull-request-only and default-branch-only checks still
remain required for branch protection, but they are not release job
prerequisites because they either need a pull request diff or live branch
protection API access.

## Integration Matrix

The `integrationMatrix` section maps production integration families to the
owner, supported profiles, required local gate, CI job, release prerequisite,
dependency upgrade trigger, and rollback note. This keeps storage, discovery,
message queue, gateway, RPC, observability, and release-evidence integrations
visible before adoption.

R6 integration ownership evidence lives in
[`integration-ownership-matrix.json`](integration-ownership-matrix.json) with
schema `gofly.integration_ownership_matrix.v1`. It expands the CI matrix into
seven adopter-facing integration families: SQL, Redis, MQ, discovery, gateway,
RPC, and observability. Each family must name the owner, supported profiles,
local gate, CI job, required check, release prerequisite, dependency upgrade
trigger, generated-project boundary, evidence paths, and rollback note.
`make required-checks-drift-check` validates this contract against the branch
protection checks, release prerequisites, Makefile targets, and evidence files.

| Integration | Owner | Profiles | Required check | Release prerequisite |
| --- | --- | --- | --- | --- |
| `storage-sql` | `storage-runtime` | `mysql`, `postgres` | integration tests (storage-mysql-postgres) | `integration` |
| `config-discovery` | `runtime-governance` | `consul`, `nacos`, `etcd` | integration tests (config-consul-nacos-etcd) | `integration` |
| `mq-brokers` | `messaging-runtime` | `kafka`, `rabbitmq`, `redis-stream` | integration tests (mq-brokers) | `integration` |
| `gateway-transcode` | `rpc-runtime` | `gateway`, `grpc`, `rpc`, `http-transcode` | integration tests (gateway-transcode) | `integration` |
| `observability-release` | `observability-governance` | `otel`, `prometheus`, `runtime-slo`, `release-evidence` | governance gates | `governance` |

## R6 Integration Ownership

The ownership contract intentionally separates Redis from the broader MQ row
and separates RPC from gateway transcoding. This keeps framework-adoption
decisions close to the actual blast radius: a Redis Stream change may require
MQ integration evidence, while generated Redis cache changes must also satisfy
DB/cache productization and generated-project dependency boundaries.

| Family | Owner | Local gate | Release prerequisite |
| --- | --- | --- | --- |
| `sql` | `storage-runtime` | `make db-cache-productization-check` | `integration` |
| `redis` | `cache-runtime` | `make db-cache-productization-check` | `integration` |
| `mq` | `messaging-runtime` | `make integration-tests` | `integration` |
| `discovery` | `runtime-governance` | `make discovery-adapter-matrix-check` | `integration` |
| `gateway` | `rpc-runtime` | `make api-contract-check` | `integration` |
| `rpc` | `rpc-runtime` | `make rpc-boundary-check` | `contract-check` |
| `observability` | `observability-governance` | `make governance-report-check` | `governance` |

## Release Prerequisite Drift

The `releasePrerequisiteDrift` section makes tag-release gate ownership
machine-readable. Each release prerequisite job must list its owner, local gate,
required checks, artifact, drift policy, and fallback policy. `make
required-checks-drift-check` verifies that these rows match the CI release
`needs` list, branch-protection expected checks, and the required-check evidence
matrix.

| Job | Owner | Drift policy |
| --- | --- | --- |
| `build-test` | `ci-platform` | Build checks, release needs, branch protection, and `make ci-fast` must stay aligned. |
| `platform-smoke` | `ci-platform` | macOS and Windows smoke checks must stay release-blocking together. |
| `lint` | `code-quality` | `golangci-lint` must remain represented by the same local lint gate. |
| `security` | `security-governance` | govulncheck and gosec evidence must remain release-blocking and unskipped. |
| `supply-chain` | `release-governance` | release artifact fixtures, API compatibility, and OSV evidence must stay aligned. |
| `codeql` | `security-governance` | hosted CodeQL evidence must remain required for branch protection and tags. |
| `dependency-upgrade-validation` | `dependency-governance` | integration delegation is valid only when required integration rows cover the affected family. |
| `contract-check` | `contract-governance` | stable-surface and API/RPC contract evidence must remain release-blocking. |
| `governance` | `governance-platform` | release-blocking skips must be rejected or have explicit compensating gates. |
| `bench-fuzz` | `performance-governance` | benchmark evidence, smoke, regression, trend, and fuzz ordering must stay stable. |
| `integration` | `integration-platform` | integration matrix areas, package lists, required checks, and dependency delegation must match. |
| `cloud-native-live-render` | `cloud-native-governance` | hosted Helm, Kustomize, kubeconform, render report, and release artifact download must stay aligned. |
| `docker` | `release-governance` | Docker build, Trivy, digest, SBOM, and provenance evidence must stay release-consumable. |
| `scorecard` | `security-governance` | hosted Scorecard evidence must remain required for branch protection and tags. |

## Hosted Release Evidence

The `hostedReleaseEvidence` section records the P9 hosted tag CI closure for
`GOFLY-GOV-10P9-05`. It ties the release job and hosted prerequisite checks to
the uploaded `release-dist-evidence` artifact, workflow markers, local gates,
and fallback policies. This makes the tag release path auditable without
requiring adopters to reverse-engineer the workflow.

`make ci-required-check-evidence-check` validates the row set, schema, workflow
markers, release job, upload artifact name, and missing-file policy. `make
required-checks-drift-check` also validates the same contract against branch
protection and release prerequisites.

| Evidence row | Producer job | Hosted evidence | Gate |
| --- | --- | --- | --- |
| `artifact-upload` | `release` | `release-dist-evidence` artifact upload | `make release-artifacts-check` |
| `checksums` | `release` | `dist/checksums.txt` | `make release-artifacts-check` |
| `sbom` | `release` | archive and Docker SBOM artifacts | `RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check` |
| `provenance` | `release` | checksum and Docker attestation verification | `RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check` |
| `docker-digest` | `release` | release Docker digest evidence | `RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check` |
| `trivy` | `release` | release Trivy JSON scan | `RELEASE_REQUIRE_DOCKER_EVIDENCE=true make release-artifacts-check` |
| `cloud-native-live-render` | `cloud-native-live-render` | `cloud-native-live-render-evidence` artifact | `make cloud-native-render-check` |
| `codeql` | `codeql` | CodeQL code scanning result | GitHub CodeQL workflow |
| `scorecard` | `scorecard` | `scorecard-results` SARIF upload | OpenSSF Scorecard workflow |
| `dependency-review` | `dependency-review` | Dependency Review job summary | GitHub Dependency Review workflow |
| `required-check-drift` | `branch-protection-audit` | `required-status-checks.json` | `make required-checks-drift-check` |
