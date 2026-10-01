# gosky Authorization

`gosky` is the API/RPC reference project for gofly. Authorization is an
explicit project extension, not a modification to generated source files.

The initial policy model is tenant-scoped RBAC:

```text
request: subject, tenant, route template, HTTP method
policy:  role,    tenant, route template, HTTP method
group:   subject, role,   tenant
```

The service authenticates a bearer JWT first, derives `subject` from `sub` and
the tenant from `tenant_id`, then asks Casbin for a decision. A missing or
invalid identity returns `401`; an authenticated but unauthorized identity
returns `403`. Policies never trust a tenant request header.

gofly's REST router registers parameters as `{id}` while the Casbin matcher
uses its standard `:id` path notation. The authorization boundary converts the
registered route template before policy matching; it never matches the raw
request path.

Unary HTTP-RPC methods use the same verified JWT principal and tenant domain.
Their Casbin resources are deployment-owned method names in the form
`rpc:<service>/<method>` with the `CALL` action. For example,
`greeter/SayHello` is checked as `rpc:greeter/SayHello` and `CALL`.
Authorization metadata is transport input only: a client must provide a bearer
token under `authorization`, but any metadata named `tenant_id` is ignored for
policy-domain selection. Missing or invalid credentials return
`Unauthenticated`; a valid identity without a matching tenant policy returns
`PermissionDenied`.

The opt-in experimental mux probe `greeter/Watch` applies the same boundary
before its business handler receives the first message. The mux client carries
request metadata in its routing frame; the adapter restores that metadata into
the server stream context and runs method-scoped middleware. The stream is
checked as `rpc:greeter/Watch` with the `STREAM` action. Missing or invalid
credentials close it with `Unauthenticated`; a verified principal without the
tenant-scoped `STREAM` policy closes it with `PermissionDenied`. Routing
metadata can carry a bearer token but can never choose a tenant: only the
verified JWT `tenant_id` claim selects the Casbin domain.

The Casbin demonstration uses a version-controlled file adapter so its policy
behaviour is reproducible. Project reads additionally use MySQL resource state:
the verified JWT tenant must match the persisted Project tenant, then the
caller must be its owner, an active member/collaborator, or a Casbin-authorized
tenant reader/writer/admin. Roles form one hierarchy per tenant: `writer`
inherits `reader`, and `admin` inherits `writer`. The Project service records every final read decision in
its operation log and fails closed if that audit record cannot be written. See
[Project MySQL Persistence](../persistence/project-mysql-schema.md).

The Project persistence model uses positive `BIGINT UNSIGNED` identifiers for
both Project and tenant IDs. Its demonstration Project policy uses numeric
tenant `1001` with `project-reader`, `project-writer`, and `project-admin`; the string-tenant
`demo-*` identities remain scoped to the generic RPC examples.

Production Casbin policy persistence, policy mutations, and distributed refresh
remain separate rollouts.

Run the real-process API/RPC smoke with:

```sh
make authorization-smoke
```

See [failure-mode-inventory.md](failure-mode-inventory.md) for the executable
contract that must be preserved as gosky and gofly evolve.
