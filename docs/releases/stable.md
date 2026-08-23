# Stable release

Install a tagged CLI with:

```sh
go install github.com/imajinyun/gofly/cmd/gofly@latest
gofly version
```

Verify a local snapshot without publishing:

```sh
make release-snapshot
```

Release artifacts include checksums and SBOM output from GoReleaser. Docker
images are not pushed from pull requests.

## Release Compatibility Checklist

- [ ] Tier 0 generated service layout still passes `make generated-service-layout-check`
- [ ] Tier 1 generator compatibility still passes `make goctl-generator-compat-check`
- [ ] CLI JSON goldens still pass `make cli-json-contract-goldens-check`
- [ ] API compatibility report from `make api-compat` is attached or skipped with a recorded reason
- [ ] RPC latency claims remain report-only unless budget promotion evidence exists
- [ ] go-zero users keep a coexistence window: original services stay routable until replay smoke passes

See [ROADMAP.md](../../ROADMAP.md) and [compatibility.md](../reference/compatibility.md).
