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

| Integration | Owner | Profiles | Required check | Release prerequisite |
| --- | --- | --- | --- | --- |
| `storage-sql` | `storage-runtime` | `mysql`, `postgres` | integration tests (storage-mysql-postgres) | `integration` |
| `config-discovery` | `runtime-governance` | `consul`, `nacos`, `etcd` | integration tests (config-consul-nacos-etcd) | `integration` |
| `mq-brokers` | `messaging-runtime` | `kafka`, `rabbitmq`, `redis-stream` | integration tests (mq-brokers) | `integration` |
| `gateway-transcode` | `rpc-runtime` | `gateway`, `grpc`, `rpc`, `http-transcode` | integration tests (gateway-transcode) | `integration` |
| `observability-release` | `observability-governance` | `otel`, `prometheus`, `runtime-slo`, `release-evidence` | governance gates | `governance` |
