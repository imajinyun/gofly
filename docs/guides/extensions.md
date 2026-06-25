# Extensions and stable SPI

gofly exposes stable extension points through `github.com/imajinyun/gofly/spi`.
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

## Compatibility matrix

The host supports external generator plugin protocol `1`. Registry authors should publish every plugin's declared compatibility so operators can reject unsafe upgrades before install or execution.

| Declaration | Host behavior |
| --- | --- |
| `compatibleVersions: ["0"]` | Rejected as an old protocol declaration. |
| `compatibleVersions: ["1"]` | Accepted and selected as the current protocol. |
| `compatibleVersions: ["2", "1"]` | Accepted by selecting `1`; the future declaration remains visible for migration planning. |
| `compatibleVersions: ["2"]` | Rejected until the host supports that future protocol. |

## Publishable registry contract

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
      "protocol": "1",
      "checksum": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "source": "https://github.com/example/gofly-redis-cache",
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

The registry is a publishable protocol contract, not only an example index.
Registry publishers should treat every field as release metadata for a specific
plugin version, and consumers should validate it before install or execution.
The registry command only searches and validates registry metadata. It does not
download, install, execute, write files, or apply patches.

Required registry fields:

- `name`, `remote`, and `version` identify a version-pinned install target.
- `protocol` records the external plugin protocol selected by the host.
- `checksum` records the expected binary digest in `sha256:<hex>` format.
- `source` records the reviewable source repository or release page.
- `manifest.compatibleVersions`, `manifest.capabilities`, and `manifest.permissions` make protocol negotiation and filesystem permissions auditable.

Publishable registry checklist:

- Old protocol declarations such as `compatibleVersions: ["0"]` are rejected.
- Current protocol declarations such as `compatibleVersions: ["1"]` are accepted.
- Future-plus-current declarations such as `compatibleVersions: ["2", "1"]` are accepted by selecting the current protocol.
- Future-only declarations such as `compatibleVersions: ["2"]` are rejected until the host supports that protocol.
- `checksum`, `source`, `protocol`, `capabilities`, `permissions`, and the template contract must be present before release.
- Plugin release notes must state protocol compatibility, digest provenance, signature provenance, permission rationale, template contract, and rollback and failure isolation behavior.

`docs/reference/plugin-publishing-ux.json` defines the gateable publishing UX
contract for third-party plugins and template packs. It requires a
least-privilege permission review, dry-run support, registry metadata, template
metadata, digest and signature provenance, compatibility evidence, and
failure-isolation evidence before a plugin is treated as publishable.

Run the copyable plugin ecosystem example to inspect the registry contract, code-generation plugin example, post-generation patching example, and third-party template directory contract:

```sh
go test -C examples/plugin-ecosystem ./...
go run -C examples/plugin-ecosystem .
```

## Third-party template directory contract

Template packs should publish a small metadata file at the template directory root. The example contract lives at `examples/plugin-ecosystem/templates/service/gofly.template.json`:

```json
{
  "schema": "gofly.third_party_template_directory.v1",
  "name": "company-service",
  "version": "v1.4.2",
  "protocol": "1",
  "entrypoints": ["service/go.mod.tpl", "service/main.go.tpl"],
  "permissions": ["filesystem:write-relative"],
  "checksum": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
  "source": "https://github.com/example/gofly-company-template-pack"
}
```

The host still enforces path safety when syncing or applying templates. Template directories must not be dangerous replacement targets, must not be symlinks, and copied template files must stay under the selected template root.

## Security boundary

- Remote plugin installs require a version-pinned identity: `<repo-or-url>@<version>`.
- HTTPS is required for non-localhost plugin and registry URLs.
- Registry entries must include protocol, checksum, source, capabilities, and permissions.
- Plugin stdout and stderr are bounded.
- External plugin processes receive a minimized environment.
- File and patch outputs must use relative paths and are rechecked for parent-directory traversal and symlink traversal before writes.
