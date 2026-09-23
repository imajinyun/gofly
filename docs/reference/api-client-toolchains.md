# API Client Toolchain Verification

schema: gofly.api_client_toolchains.v1

The executable client gate complements the structural
`api-client-generation-check`. It generates clients from
`testdata/goctl-api-semantic/contract.api` and validates them with language
toolchains instead of relying only on source markers.

## Local verification

Run every locally installed toolchain:

```sh
make api-client-toolchain-check
```

Select one toolchain and require it:

```sh
API_CLIENT_TOOLCHAIN=javascript \
API_CLIENT_TOOLCHAIN_REQUIRED=true \
API_CLIENT_TOOLCHAIN_REPORT=/tmp/api-client-javascript.json \
make api-client-toolchain-check
```

Local runs classify missing optional tools as `unavailable` and finish with
overall status `partial`. Required mode treats an unavailable tool as `fail`.
The JSON report is always written after contract validation starts, including
generation or compiler failures.

## Verification matrix

| Language | Verification | Evidence boundary |
| --- | --- | --- |
| JavaScript | Node runtime against a loopback HTTP server | path/query/header/body, 201 JSON, 204 empty response, error text |
| TypeScript | strict `tsc --noEmit` | generated declarations and DOM request APIs compile |
| Java | `javac` with a bounded `ObjectMapper` fixture | generated Java syntax, models, collections, and JDK HTTP APIs compile |
| Kotlin | `kotlinc` with bounded serialization fixtures | generated Kotlin syntax, models, collections, and JDK HTTP APIs compile |
| Dart | `dart analyze` with a local path-based `http` fixture | null safety, recursive models, request methods, and JSON conversion analyze cleanly |

The Java, Kotlin, and Dart fixtures model only the imported library surface used
by generated clients. They do not claim runtime compatibility for Jackson,
kotlinx.serialization, or package:http. JavaScript is the runtime proof; the
other four rows are compiler or analyzer proofs.

## CI policy

CI runs a five-language matrix with fixed versions from
`docs/reference/api-client-toolchains.json`. Each job selects one language, sets
`API_CLIENT_TOOLCHAIN_REQUIRED=true`, and uploads its JSON report even when the
verification fails. An unavailable toolchain therefore cannot become a passing
CI row.

## Rollback

The executable gate is additive. It can be removed independently while keeping
the structural client-generation gate and the API semantic fixture intact.
