# Repository Test Fixtures

This directory contains data-only fixtures shared by repository tests and
governance gates. Go test source remains next to the package it verifies;
standalone executable integration harnesses live under `tests/integration/`.
[`catalog.json`](catalog.json) is the machine-readable ownership inventory and
is enforced by `make testdata-layout-check`.

| Family | Purpose | Primary consumers |
| --- | --- | --- |
| `api/client/compatibility` | Base/target API compatibility cases | API client compatibility gate |
| `api/client/matrix` | Multi-language client generation input | API client generation gate |
| `api/client/toolchains` | Minimal offline language toolchain shims | API client toolchain gate |
| `api/openapi/roundtrip` | OpenAPI import/export round-trip fixtures | Generator OpenAPI tests and contract gate |
| `api/semantic/goctl` | Pinned goctl API semantic oracle | Generator and REST runtime semantic tests |
| `compatibility/generated` | Old/current/future generated project inputs | Generated-version compatibility gates |
| `migration/goctl-replay` | Pinned real-project migration inputs and provenance | goctl replay tests and gates |
| `model/goctl/cache-key-oracle` | Primary and unique cache-key expectations | Model generator parity test |
| `model/goctl/datasource-replay` | Offline MySQL/PostgreSQL datasource snapshots | Model benchmark and parity gate |
| `rpc/zrpc/proto-matrix` | zRPC proto import and mapping inputs | RPC generator compatibility tests |

Benchmark-sized API inputs are owned by `bench/testdata/api-generator`.
The executable zRPC interop module is owned by
`tests/integration/zrpc-interop`.

When moving or updating a fixture, update its Go test, shell/Python gate, and
`docs/reference` path in the same change. Preserve pinned upstream versions and
content digests unless the fixture content itself intentionally changes.
