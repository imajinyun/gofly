# October 2026 security patch convergence

The previous package-level scan reported vulnerabilities in Go 1.26.8 and
`golang.org/x/net v0.59.0`. The reviewed fixes are Go 1.26.9 (or Go 1.27.2)
and x/net v0.60.0:

- [Go security release announcement](https://seclists.org/oss-sec/2026/q4/101)
- [x/net v0.59.0 to v0.60.0 changes](https://go.googlesource.com/net/+log/refs/tags/v0.59.0..refs/tags/v0.60.0)
- [HTTP/2 vulnerability and fixed versions](https://pkg.go.dev/vuln/GO-2026-6612)

x/net v0.60.0 fixes HTTP/2 HPACK races, flow-control accounting, excessive work
on initial-window changes, trailer limits and framing-related headers. Its
module requirements are unchanged from v0.59.0; no companion x/crypto, x/sys
or x/text upgrade is necessary for this patch.

## Version selection

The root and Gosky modules recommend `toolchain go1.26.9`, retaining the
existing `go 1.26.7` language/module compatibility declaration. The pinned
setup-go action honors the toolchain directive for `go-version-file` jobs;
the explicit `1.26` and `stable` matrix entries already select current patches.
Release verification must use a fixed compiler, not an older local override.

The CLI Docker builder is pinned to Go 1.26.9 Alpine's verified multi-platform
digest. This matters because the builder sets `GOTOOLCHAIN=local`. Root and
standalone modules declaring x/net v0.59.0 are aligned to v0.60.0, with their
checksums refreshed through `go mod tidy`. Gosky's existing SM4 dependency is
also correctly classified as a direct import; it is not a new dependency.

## Verification and advisory scope

The existing gates remain authoritative:

```sh
GOTOOLCHAIN=go1.26.9 make govulncheck
GOTOOLCHAIN=go1.26.9 make -C examples/gosky check
GOTOOLCHAIN=go1.26.9 make governance-10-rounds
```

Package-level scans for the root and Gosky report zero affected packages after
the patch. The scanner also reports module advisory GO-2026-5932 for the
unmaintained `golang.org/x/crypto/openpgp` package. Neither module imports that
package (`go list -deps ./...`); required x/crypto algorithms remain in use.
This module-only advisory is retained in the evidence, not suppressed or
misreported as a reachable application vulnerability.

The coverage floor stays at 90.5%. New CLI behavior tests and cleanup/output
regressions are run through normal package tests; no profile blocks or statement
counts are excluded to satisfy the ratchet. See
[CLI coverage convergence](cli-coverage-convergence.md) and
[cleanup/output failure contracts](api-cleanup-safety.md).

The completed Go 1.26.9 governance run passed formatting, full root tests and
race detection, vet, lint, root dependency policy and tidy/verify, generated
project checks, all 22 standalone example modules, coverage, govulncheck and
gosec. No governance stage was skipped. the former root `make gosky-check` target also passed all five
required disposable-MySQL suites. The separate `tests/integration/zrpc-interop`
module passed its six integration suites with `-tags=integration -race
-shuffle=on -count=1`, covering unary calls, all stream modes and discovery.
