# API Go-Native Type System

schema: gofly.api_type_system.v1

gofly parses `.api` type declarations with the Go standard library
(`go/parser` and `go/ast`). Type declarations therefore follow Go semantics
instead of the go-zero/goctl legacy type grammar. The Go-native extension is a
gofly enhancement: goctl remains a route and binding oracle, not a
type-grammar compatibility target.

## Supported declarations

```api
type (
    JSONValue any                    // defined type with an interface underlying type
    RawJSON   = any                  // Go alias
    UserID    int64                  // defined type
    Tags      []string               // defined type
    Meta      struct { ... }         // named struct
    MetaAlias = struct { ... }       // alias to an anonymous struct
    MetaPtr   = *Meta                // pointer alias
)
```

Supported field forms:

```api
type CreateRequest {
    ID       string         `json:"id"`
    Payload  any            `json:"payload"`
    Extra    interface{}    `json:"extra,omitempty"`
    Labels   map[string]any `json:"labels"`
    A, B     string         `json:"-"`
    Meta                    `json:"meta"`
    *Audit                  `json:"audit"`
    Attrs    struct {
        Source string `json:"source"`
    } `json:"attrs"`
    Items    []struct {
        Name string `json:"name"`
    } `json:"items"`
    Index    map[string]struct {
        Count int `json:"count"`
    } `json:"index"`
}
```

## Alias semantics

- `type X T` declares a defined type. Generated Go uses `type X T`.
- `type X = T` declares a Go alias. Generated Go uses `type X = T`.
- Named structs, including `type X = struct{...}`, are represented as messages
  so formatting, OpenAPI, clients, and diff share one representation.
- Routes must reference a named struct. Alias chains are resolved; an alias or
  defined type whose terminal target is not a struct is rejected.

## Multi-name fields

`A, B string` expands to two generated Go fields. The expansion is accepted
only when the shared tag is absent or uses `json:"-"` (likewise `form`, `path`,
or `header`). Sharing a real wire name such as `json:"shared"` is rejected
because Go's `encoding/json` would silently drop both fields.

## OpenAPI mapping

| `.api` type | OpenAPI 3 schema |
|---|---|
| `any` / `interface{}` | `{}` (free-form) |
| `map[string]any` | `{"type":"object","additionalProperties":{}}` |
| `type X any` | named component `X` with schema `{}` |
| `type X = User` | named component `X` with `$ref: User` |
| direct inline struct | nested object schema |
| `[]struct{...}` | array of nested object schemas |
| `map[K]struct{...}` | object with nested `additionalProperties` |

OpenAPI import maps a free-form object (no `properties`, no
`additionalProperties`) to `any`, and object `additionalProperties` to
`map[string]T`.

## Client mapping

| `.api` type | TypeScript | Dart | Java | Kotlin |
|---|---|---|---|---|
| `any` / `interface{}` | `unknown` | `Object` | `Object` | `Any` |
| `map[string]any` | `Record<string, unknown>` | `Map<String, Object>` | `java.util.Map<String, Object>` | `Map<String, Any>` |
| `type X any` | `export type X = unknown` | `typedef X = Object` | inline `Object` | `typealias X = Any` |
| `type X int64` | `export type X = number` | `typedef X = int` | inline `Long` | `typealias X = Long` |
| direct inline struct | inline object | companion class | nested `static class` | nested `data class` |
| `[]struct{...}` | inline object array | `List<Companion>` | `java.util.List<Companion>` | `List<Companion>` |
| `map[K]struct{...}` | `Record<K, object>` | `Map<K, Companion>` | `java.util.Map<K, Companion>` | `Map<K, Companion>` |

Java inlines aliases because Java has no type-alias construct. Dart emits
`typedef`; TypeScript emits `export type`; Kotlin emits `typealias`.

## Stable error classes

```text
generics are not supported in .api type declarations
non-empty interface types are not supported
unsupported Go type in .api: func
unsupported Go type in .api: chan
unsupported Go type in .api: unsafe.Pointer
unsupported Go type in .api: complex
unsupported package-qualified type <pkg>.<name>
type alias cycle: A -> B -> A
recursive type cycle: A -> B -> A
unknown type alias target <name>
duplicate type <name>
multi-name field A, B must not share tag json:"shared"
route <name> request type <T> is not a struct
route <name> references unknown request type any
```

Parse errors from Go declarations include the original `.api` file and line
through `//line` directives, including files with multi-line block comments.

## Verification

```sh
make api-semantic-parity-check
make api-client-generation-check
make openapi-validation-check
make openapi-roundtrip-check
```

The focused Go tests are:

```sh
go test -count=1 -shuffle=on ./cmd/gofly/internal/generator \
  -run 'TestAPITypeAST|TestAPITypeSystem'
```

## Compatibility notes

- Previously rejected aliases and inline structs now parse; this is additive.
- OpenAPI changed `any` from `string` to `{}` and Java changed `any` from
  `Any` to `Object`; both are generated-output fixes that require release
  notes.
- Source-comment preservation remains outside the formatter contract.
