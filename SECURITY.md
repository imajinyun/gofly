# Security Policy

## Supported Versions

gofly is currently pre-1.0. Security fixes are prioritized for the default branch and the latest tagged release line when tags are available.

## Reporting a Vulnerability

Please report suspected vulnerabilities privately to the maintainers instead of opening a public issue.

Include:

- Affected package, command, generated template, or runtime component.
- Reproduction steps or proof of concept.
- Expected impact and any known mitigations.
- Version, commit, and relevant configuration.

Maintainers should acknowledge reports within 3 business days, triage severity, and coordinate disclosure timing with the reporter.

## Security Expectations

- Admin endpoints must require a non-placeholder token in production or be restricted to localhost-only access.
- Sensitive headers, tokens, credentials, and secrets must be redacted from admin responses and logs.
- New network clients and servers must use bounded timeouts and request-size limits.
- New dependencies must be reviewed for license, maintenance status, and known vulnerabilities before adoption.
