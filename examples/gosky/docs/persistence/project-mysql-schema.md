# Project MySQL Persistence

`gosky` uses MySQL as the authoritative store for its Project read example.
The schema is intentionally small: it provides tenant isolation, resource
ownership, explicit collaboration, and durable authorization-operation audit
records without making Casbin policy storage persistent.

The reviewed migration is the `gofly migrate` pair
[`20261001042731_project_authorization.up.sql`](../../internal/migrations/20261001042731_project_authorization.up.sql)
and
[`20261001042731_project_authorization.down.sql`](../../internal/migrations/20261001042731_project_authorization.down.sql).
Apply it with the deployment's approved migration runner before enabling the
`projectStore` configuration. The service never runs migrations at startup.
From the gofly repository root, validate or apply this pair with `go run
./cmd/gofly migrate ... --dir examples/gosky/internal/migrations`; an installed
binary is usable only when its `migrate --help` lists `validate`, `up`, `down`,
and `status`. Only the deployment migration job may run `up` or `down`.
The runtime database account needs `SELECT` on `projects` and
`project_members`, plus `INSERT` on `opslogs`; it does not need
DDL or Project-delete privileges for the current read-only example.

## Tables

| Table | Responsibility | Key access paths |
| --- | --- | --- |
| `projects` | One globally identified Project, its tenant, owner, lifecycle state, and soft-delete timestamp. | Primary-key lookup by `project_id`; tenant/owner/state and tenant/update-time listing. |
| `project_members` | Active or revoked member/collaborator relationships. Owners are stored on `projects`, so they are not duplicated here. | Tenant-bound membership check by `(project_id, project_tenant_id, member_subject, member_state)`. |
| `opslogs` | Append-only application audit events for Project operations and authorization outcomes. | Incident lookup by project/time, tenant/time, operator/time, target/time, request ID, or trace ID. |

`projects.project_id` and `projects.tenant_id` are `BIGINT UNSIGNED` values.
Project path IDs and verified JWT `tenant_id` claims must therefore be positive
unsigned 64-bit decimal integers. This lets the service distinguish an unknown
Project (`404`) from an existing Project owned by another tenant (`403`)
without exposing its representation. Subjects remain opaque application-owned
strings; both subjects and tenant IDs must come from verified JWT claims or
trusted identity provisioning, never from request headers.

## Read authorization

For `GET /api/v1/projects/{id}`, the service first authenticates the JWT and
loads the Project by ID. It then requires the verified JWT tenant to equal
`projects.tenant_id`. Within that tenant, any one of these grants access:

1. `projects.owner_subject` equals the verified JWT `sub` claim;
2. an active `project_members` row exists for `(project_id, subject)`;
3. the tenant-scoped Casbin policy allows the stable route resource and `GET`
   action. The reference policy grants it to `reader`; `writer` inherits
   `reader`, and `admin` inherits `writer`.

The first two are resource-state ABAC checks; the third stays in Casbin. A
cross-tenant caller always receives `403`, regardless of owner or membership
data. A same-tenant caller with no grant receives `403`. A missing Project
receives `404` only after JWT authentication.

## Audit contract

Every Project-read decision records the verified actor subject and tenant,
Project ID, operation (`project.read`), outcome, authorization basis, and
request ID. It records neither bearer credentials nor response payloads. The
current read path fails closed when the audit write fails, returning `503`
instead of returning an unaudited representation or decision.

The membership foreign key binds `(project_id, project_tenant_id)` to
`projects(project_id, tenant_id)`, so a cross-tenant member row is impossible.
`projects.project_state = 'deleted'` is treated as not found by the read path;
archived Projects remain readable according to the same authorization rules.
`opslogs` deliberately has no Project foreign key because failed lookups and
soft-deleted Project access attempts must remain auditable. For a missing or
deleted Project, `opslogs.project_tenant_id = 0` means the resource tenant
could not be resolved; operator tenant and requested Project ID remain recorded.
