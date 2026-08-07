# API Client Generation Contract

schema: gofly.api_client_generation.v1

`gofly api client` and its language aliases generate typed clients from `.api`
contracts for these supported languages:

- `typescript`
- `javascript`
- `dart`
- `java`
- `kotlin`

The fixture source of truth is `testdata/api-client-matrix/shop.api`. It covers
path parameters, query parameters, repeated query values, header-tagged fields,
JSON request bodies, nested DTOs, and list response DTOs.

## Verification

The focused gate is:

```sh
make api-client-generation-check
```

The gate verifies:

- generator-level path/query/body handling for all client languages
- CLI-level aliases and output naming
- real generated files for TypeScript, JavaScript, Dart, Java, and Kotlin
- structural markers for nested DTOs, array fields, query builders, body
  serialization, and base URL handling

## Compatibility Rules

1. Existing language names and aliases must keep working.
2. Generated clients must preserve request and response shapes from the `.api`
   contract.
3. Query arrays must continue to serialize as repeated query parameters.
4. Java and Kotlin default filenames remain `APIClient.java` and
   `APIClient.kt`.
5. The gate is structural by default; language toolchain compilation can be
   added later without making Node, Java, Dart, or Kotlin mandatory locally.
