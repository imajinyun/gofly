# Project MySQL Persistence

`gosky` uses MySQL as the authoritative store for its Project read example.
The schema is intentionally small: it provides tenant isolation, resource
ownership, explicit collaboration, and durable authorization-operation audit
records. Persistent Casbin policy storage is intentionally separate from the
Project resource tables and lives in the subsequent `casbin_rules` and
`casbin_policy_events` migration pair.

## Application and repository boundary

`app.ProjectService` owns authorization and the four Project repository
interfaces. `repo.MySQLProjectRepository` in `internal/model/repo/project_mysql.go`
is the implementation wired by `cmd/gosky/main.go`. SQL, row scanning and the
relationship/audit transaction live in `repo`; application code does not import
`database/sql`, `core/storage` or `repo`. `TestApplicationPersistenceBoundary`
enforces that direction in ordinary Go tests. Scope, relationship-role and
subject validation are shared application-domain rules, not copied validators.

The generated `ProjectRepo`/`ProjectModel`, `ProjectMemberRepo` and `OpslogRepo`
are currently unconnected generator output. Production Project use cases do
not call their constructors. Their generic `int64` model IDs and CRUD methods
do not implement the current `uint64` identifier, tenant/relation filtering or
atomic relationship/audit contracts, so they are not interchangeable with the
custom Project repository. Their files remain intact for a later explicit
generator-output decision; moving the custom repository does not activate a
second persistence path.

DB structs and generated column metadata live in `internal/model/entity` with
package name `entity`. Both generated repository code and custom SQL live in
`internal/model/repo` with package name `repo`, matching gofly's default model
layout. The previous flat layout came from the `go_zero` compatibility profile.
Custom `project_mysql.go`, `identity.go` and `tenant_user_directory.go` remain
separate from generated table files. The layout migration changes package paths
and entity package declarations only; table fields, SQL and transaction
semantics remain intact.

Run the isolated HTTP/MySQL boundary E2E against an already migrated test DB:

```sh
# Requires GOSKY_MYSQL_DSN; creates fresh tenants, identities and policy tables.
GOPROXY=direct GOFLY_CACHE_DISABLED=true sh ./bin/scripts/go-test.sh \
  -json -race -shuffle=on -tags=integration ./internal/routes \
  -run '^TestMySQLProjectPersistenceBoundaryIntegration$' \
  > /tmp/gosky-project-persistence-e2e.jsonl
```

The test covers owner/viewer/editor/admin reads, tenant boundaries, cursor
pagination, front-realm relation scope, stale-role revocation and soft deletion.
A dedicated strict-mode connection injects an audit-column failure after a
relationship write and verifies transaction rollback. Cleanup is limited to
fresh fixture tenants, including their list-operation logs with `project_id=0`.
The JSON transcript retains the assertions and audit-outcome counts.

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
`project_members`, `INSERT`/`UPDATE` on `project_members` for collaboration
management, and `INSERT` on `opslogs`; it does not need DDL or Project-delete
privileges.

## Tables

| Table | Responsibility | Key access paths |
| --- | --- | --- |
| `projects` | One globally identified Project, its tenant, owner, lifecycle state, and soft-delete timestamp. | Primary-key lookup by `project_id`; tenant/owner/state and tenant/update-time listing. |
| `project_members` | Active or revoked `viewer`/`editor` relationships. Owners are stored on `projects`, so they are not duplicated here. | Tenant-bound relation lookup by `(project_id, project_tenant_id, member_subject, member_state)`. |
| `opslogs` | Append-only application audit events for Project operations and authorization outcomes. | Incident lookup by project/time, tenant/time, operator/time, target/time, request ID, or trace ID. |
| `casbin_rules` | Current tenant-scoped Casbin RBAC policy snapshot. | Unique normalized policy key; domain/type lookup and administrator-binding lock lookup. |
| `casbin_policy_events` | Append-only policy mutation audit and cross-instance reload outbox. | Monotonic event ID and domain/event lookup. |

`casbin_rules` and `casbin_policy_events` are the default names created by the
standard migration. A deployment may select another rule/event pair through
`authorization.mysql.rulesTable` and `authorization.mysql.eventsTable`, but it
must create that pair with this exact schema in a reviewed migration before
starting gosky. Both names are required together, must differ, and cannot be
schema-qualified.

`projects.project_id` and `projects.tenant_id` are `BIGINT UNSIGNED` values.
Project path IDs and verified JWT `tenant_id` claims must therefore be positive
unsigned 64-bit decimal integers. This lets the service distinguish an unknown
Project (`404`) from an existing Project owned by another tenant (`403`)
without exposing its representation. Subjects remain opaque application-owned
strings; both subjects and tenant IDs must come from verified JWT claims or
trusted identity provisioning, never from request headers.

## Read authorization

For `GET /api/admin/projects/{id}`, the service first authenticates the JWT and
loads the Project by ID. It then requires the verified JWT tenant to equal
`projects.tenant_id`. Within that tenant, any one of these grants access:

1. `projects.owner_subject` equals the verified JWT `sub` claim;
2. an active `project_members` relation has role `viewer` or `editor` for
   `(project_id, subject)`;
3. the caller has the fixed tenant-admin capability `project.read.any`.

The first two are resource-state ABAC checks; the third stays in Casbin. A
tenant `reader` or `writer` alone never grants access to every Project. A
cross-tenant caller always receives `403`, regardless of owner or membership
data. A same-tenant caller with no grant receives `403`. A missing Project
receives `404` only after JWT authentication.

For `GET /api/admin/projects`, the repository receives a typed scope containing
the verified tenant, actor subject, tenant-admin flag, keyset cursor, and page
limit. Its SQL either selects all active tenant Projects for an admin or joins
the actor's active relationship and returns only owner/viewer/editor rows.
The request cannot supply another tenant or a visibility predicate. A caller
without any matching relation gets `200` with an empty collection rather than
a cross-project denial; `limit` is constrained to 1–100 and `cursor` is a
positive Project ID.

## Collaboration management

`PUT /api/admin/projects/{id}/members` creates, reactivates, or changes a
non-owner relationship. `DELETE /api/admin/projects/{id}/members` revokes a
matching active relationship. Both bodies contain `subject` and a `role` of
`viewer` or `editor`; the delete role makes stale clients unable to revoke a
relationship after it has been changed to another role.

Within the verified Project tenant, the owner may perform either mutation.
Any other actor must hold the fixed Casbin capability
`project.member.manage`, which the bootstrap policy grants only to `admin`.
`reader` and `writer` do not inherit member-management access. The owner is
not duplicated in `project_members` and cannot be added, changed, or revoked
through these endpoints. Cross-tenant calls are rejected with `403` before
the member repository is invoked.

The repository executes the `project_members` upsert/revoke and its allowed
`opslogs` row in one MySQL transaction. If either statement fails, neither the
relation nor the audit row is committed. Denied and not-found requests do not
mutate the relation but retain their ordinary authorization audit records.

## Audit contract

Every Project-read or member-management decision records the verified actor
subject and tenant, Project ID, operation, outcome, authorization basis, and
request ID. Member mutation records additionally store the target subject and
validated relation role. It records neither bearer credentials nor response
payloads. The read path fails closed when its audit write fails. Successful
member mutation paths fail closed when their transaction cannot persist both
the relationship and its audit event, returning `503` rather than an
unaudited relation state.

The membership foreign key binds `(project_id, project_tenant_id)` to
`projects(project_id, tenant_id)`, so a cross-tenant member row is impossible.
`projects.project_state = 'deleted'` is treated as not found by the read path;
archived Projects remain readable according to the same authorization rules.
`opslogs` deliberately has no Project foreign key because failed lookups and
soft-deleted Project access attempts must remain auditable. For a missing or
deleted Project, `opslogs.project_tenant_id = 0` means the resource tenant
could not be resolved; operator tenant and requested Project ID remain recorded.
