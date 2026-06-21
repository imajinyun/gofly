# Extensions and stable SPI

gofly exposes stable extension points through `github.com/gofly/gofly/spi`.
The current contract version is `gofly.spi.v1`.

## Stable SPI surface

| Extension point | Stable interface | Runtime compatibility |
| --- | --- | --- |
| Config source | `spi.ConfigSource[T]`, `spi.ConfigWatcher[T]` | Compatible with `core/config.Provider[T]` and watch providers |
| Discovery provider | `spi.DiscoveryProvider` | Compatible with `core/discovery.Registry` implementations |
| Governance provider | `spi.GovernanceProvider`, `spi.GovernanceSaver` | Compatible with `core/governance.RuleProvider` and `RuleSaver` |
| Control-plane contributor | `spi.ControlPlaneContributor` | Compatible with `core/controlplane.SnapshotContributor` |
| HTTP middleware | `spi.HTTPMiddleware` | Adapt with `spi.RESTMiddleware` |
| RPC interceptor | `spi.RPCInterceptor` | Adapt with `spi.EndpointMiddleware` |
| Generator plugin | `spi.GeneratorPlugin` | Mirrors the external JSON plugin manifest/request/response contract |

## Generator plugin manifest

External generator plugins must declare compatibility and capabilities before the host trusts their output:

```json
{
  "name": "redis-cache",
  "version": "v0.1.0",
  "compatibleVersions": ["1"],
  "capabilities": ["generate:file"],
  "permissions": ["filesystem:write-relative"],
  "requiresDryRun": true
}
```

Supported capabilities:

- `generate:file` — plugin may return relative file writes.
- `generate:patch` — plugin may return anchored file patches.

Supported permissions:

- `filesystem:write-relative` — host validates all plugin output paths under the target project.

## Registry prototype

`gofly plugin search` reads a JSON registry from an HTTPS URL, localhost HTTP URL, or local file:

```sh
gofly plugin search --registry ./plugins.json redis --json
```

Registry entries use version-pinned remotes so install and run flows stay reproducible:

```json
{
  "version": "v1",
  "plugins": [
    {
      "name": "redis-cache",
      "remote": "https://example.com/gofly-redis-cache",
      "version": "v0.1.0",
      "description": "Redis cache generator",
      "tags": ["redis", "cache"],
      "manifest": {
        "name": "redis-cache",
        "version": "v0.1.0",
        "compatibleVersions": ["1"],
        "capabilities": ["generate:file"],
        "permissions": ["filesystem:write-relative"]
      }
    }
  ]
}
```

The registry command only searches and validates registry metadata. It does not download, install, execute, write files, or apply patches.

## Security boundary

- Remote plugin installs require a version-pinned identity: `<repo-or-url>@<version>`.
- HTTPS is required for non-localhost plugin and registry URLs.
- Plugin stdout and stderr are bounded.
- External plugin processes receive a minimized environment.
- File and patch outputs must use relative paths and are rechecked for parent-directory traversal and symlink traversal before writes.
