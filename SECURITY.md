# Security Policy

## Supported versions

Security fixes land on the current `main` branch. Tagged releases receive
fixes only while that release line is still documented in
[docs/releases/stable.md](docs/releases/stable.md).

## Reporting a vulnerability

Do not file a public GitHub issue for a suspected vulnerability.

Use GitHub's private advisory flow:

- <https://github.com/imajinyun/gofly/security/policy>

Include the gofly version or commit, a minimal reproduction, and the affected
surface (CLI, generator, plugin download, template sync, REST, RPC, or
control-plane). Do not attach live credentials, tokens, or customer data.

## Scope

Reports in this project are especially useful when they involve:

- path traversal or symlink escape in generators, templates, or plugins
- command injection through `exec.Command` argument assembly
- remote plugin or template downloads without HTTPS, size, or digest bounds
- secret leakage in logs, CLI JSON, support bundles, or control-plane snapshots
- TLS `InsecureSkipVerify` becoming an accidental default

## Response

Maintainers will acknowledge the report, reproduce it privately, and ship a
fix or a documented mitigation. Public discussion happens only after a fix or
an explicit decision that the report is out of scope.
