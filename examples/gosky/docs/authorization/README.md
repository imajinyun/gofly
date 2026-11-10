# gosky Authorization

The current admin contract is [explicit permissions](explicit-admin-permissions.md): no role inheritance, operation permission plus resource scope, independently authorized management buttons, and audited Project update/soft-delete. Front Project reads retain relation-only access.


`gosky` is the API/RPC reference project for gofly. Authorization is an
explicit project extension, not a modification to generated source files.

The initial policy model is tenant-scoped RBAC:

```text
request: subject, tenant, capability, execute
policy:  role,    tenant, capability, execute
group:   subject, role,   tenant
```

The service authenticates a bearer JWT first, derives `subject` from `sub` and
the tenant from `tenant_id`, then asks Casbin for a decision. A missing or
invalid identity returns `401`; an authenticated but unauthorized identity
returns `403`. Policies never trust a tenant request header.

The deployment-owned capability registry maps a protected REST route, unary
RPC method, or mux stream to an abstract capability before Casbin runs. The
current mappings include `project.read`, `project.member.manage`,
`authorization.role.manage`, `greeter.hello`, and `greeter.watch`; Casbin sees the canonical `EXECUTE`
action, never a raw HTTP method, route template, or RPC transport name. An
entry missing from that registry is a startup/configuration defect and fails
closed with `503`.

The browser-facing `GET /api/admin/auth/session` endpoint is an authenticated
navigation projection rather than a transport capability itself. It first
passes `AdminInterceptor`, then evaluates each code-owned navigation and point
entry against its fixed capability. A normal denial omits only that entry; an
unavailable policy decision fails the entire projection with `503`. The JSON
contains verified identity fields, logical component keys, visible navigation,
and visible points only. It never exposes a Casbin row, role hierarchy, bearer
credential, refresh token, or Project ownership/collaboration relation.

SmartAdmin compatibility uses the same verified `admin` identity for two
catalogs: the code-owned Access Control catalog under
`authorization.role.read` (advertising `/system/role` and `/system/menu`) and
the Organization catalog under `system.user.manage` (advertising
`/system/employee`, `/system/department`, and `/system/position`). `GET
/menu/query` returns the active tenant catalog, while `GET
/menu/tree?onlyMenu=` returns a catalog/page-only or complete
catalog/page/function-point tree. With a tenant directory wired the catalog
also contains tenant-created catalog/page rows, but it never publishes raw
transport identifiers; `/menu/auth/url` stays empty. Seeded system rows are
immutable; tenant rows may change through `/menu/add`, `/menu/update`, and
`/menu/batchDelete?menuIdList=...` under `system.menu.manage`, and deployments
without a directory still receive `failed_precondition`. Every capability the
role permission API accepts, including `greeter.watch`, appears once in the
complete tree so saved role policy is always representable.

The target-role capability tree uses labels from the same definitions that
translate menu IDs into Casbin grants. The 21 current-operator button
permissions are projected separately at login/session restoration, each under
its own capability (`authorization.role.manage`, `authorization.role.scope.manage`,
the two role-member capabilities, `system.menu.manage`, or `system.user.manage`);
their IDs cannot be saved as capabilities and the tree omits them. All
displayed structural IDs, including the Menu Catalog and organization pages,
are accepted by the role save API without creating policies. The selection
response describes direct grants; backend role inheritance is disabled. See
the [role-menu mapping and E2E command](../../README.md#smartadmin-local-compatibility)
for the fixed identifiers, browser refresh note and repeatable MySQL evidence.

Role membership orchestration is owned by `app.RoleService`. SmartAdmin's
adapter converts browser IDs and DTOs, while the application service validates
user-ID batches, checks role-management permission before directory access,
and resolves active identities through `app.TenantUserDirectory`. The SQL
implementation lives in `internal/model/repo/tenant_user_directory.go`; canonical subjects
stay inside the application. Project scope updates preserve non-Project
capabilities through the same audited `authz.PolicyService` used by native
role APIs. The compatibility layer cannot import persistence packages, as
enforced by `TestCompatibilityPersistenceBoundary`.

Authentication persistence is defined by `authn.IdentityRepository`, with
service-owned login/session projections, audit inputs and result errors in
`authn/repository.go`. `model/repo.MySQLIdentityRepository` implements this
contract and retains the SQL/transaction boundary; generated table structs
remain in `model/entity`. `TestAuthenticationPersistenceBoundary` prevents
authn from importing repository implementations. Password processing is
isolated in `authn/password.go`; valid hashes retain the same Argon2id parameters
and verification algorithm. The isolated identity lifecycle E2E checks login, refresh
rotation/replay, scoped session listing/revocation, disabled memberships and
logout against real MySQL.
Malformed stored hashes, including empty salts, fail authentication before
narrowing decoded lengths or numeric parameters. Configuration defaults apply
to password generation settings, not to missing salt bytes in a stored hash.

Unary HTTP-RPC methods use the same verified JWT principal and tenant domain.
For example, `greeter/SayHello` is mapped from its deployment-owned transport
identifier to `greeter.hello` and checked with `EXECUTE`.
Authorization metadata is transport input only: a client must provide a bearer
token under `authorization`, but any metadata named `tenant_id` is ignored for
policy-domain selection. Missing or invalid credentials return
`Unauthenticated`; a valid identity without a matching tenant policy returns
`PermissionDenied`.

The opt-in experimental mux probe `greeter/Watch` applies the same boundary
before its business handler receives the first message and re-evaluates its
fixed capability after every transport receive but before exposing that message
to the business handler. The mux client carries
request metadata in its routing frame; the adapter restores that metadata into
the server stream context and runs method-scoped middleware. The stream is
mapped from `rpc:greeter/Watch` to `greeter.watch` with the `EXECUTE` action. Missing or invalid
credentials close it with `Unauthenticated`; a verified principal without the
tenant-scoped `greeter.watch` capability closes it with `PermissionDenied`. A
revocation observed by the local policy state rejects the next unread business
message before the handler consumes it. Routing metadata can carry a bearer
token but can never choose a tenant: only the
verified JWT `tenant_id` claim selects the Casbin domain.

The checked-in development configuration uses the MySQL policy backend so the
SmartAdmin role page and browser identity store share the same tenant-scoped
policy state. The version-controlled CSV adapter remains available for focused
tests and local policy-only experiments. MySQL keeps standard Casbin `p`/`g`
rows in `casbin_rules` and records each mutation in `casbin_policy_events`.
Project reads additionally use MySQL resource state:
the verified JWT tenant must match the persisted Project tenant, then the
caller must be its owner, an active `viewer`/`editor`, or a tenant `admin`
with the fixed `project.read.any` capability. A tenant `reader` or `writer`
is an operation-eligibility role, never an implicit grant to every Project in
the tenant. Roles have complete explicit grants; no role inherits another role. Admin reads require `project.read` before any owner, member or all-tenant scope can authorize access. The Project service records every final read decision in
its operation log and fails closed if that audit record cannot be written. See
[Project MySQL Persistence](../persistence/project-mysql-schema.md).

The Project persistence model uses positive `BIGINT UNSIGNED` identifiers for
both Project and tenant IDs. Its demonstration Project policy uses numeric
tenant `1001` with `project-reader`, `project-writer`, and `project-admin`; the string-tenant
`demo-*` identities remain scoped to the generic RPC examples.

In MySQL mode, policy writes are accepted only through the service guarded by explicit permission-edit, scope-edit or employee-add/remove capabilities. The service validates the verified JWT domain, keeps
the last tenant `admin`, commits the rule and audit/outbox event together, then
reloads the local enforcer. Other instances watch the monotonic event sequence
and reload the complete policy. A watcher read or reload failure makes the
authorizer fail closed; there is no CSV fallback. See
[MySQL Casbin Policy Store](../../README.md#mysql-casbin-policy-store).

Project collaboration is deliberately separate from tenant RBAC. `PUT` and
`DELETE /api/admin/projects/{id}/members` accept a validated `subject` and one of
the `viewer` or `editor` relationship roles. Every caller needs `project.member.manage`, combined with Project ownership or the explicitly granted `project.manage.any` scope. A reader or writer has no implicit member
management permission, and cross-tenant callers receive `403`. The relation
write and its `opslogs` operation record (`project.member.upsert` or
`project.member.revoke`) commit in the same MySQL transaction.

`GET /api/admin/projects` is a typed data-scope query, rather than an
application-side filter over a tenant-wide result. It accepts keyset pagination
through `limit` (1–100, default 20) and `cursor` (the previous Project ID).
A caller with both `project.read` and `project.read.any` receives active Projects in its verified tenant; a caller with only `project.read` receives Projects it owns or has an active
`viewer`/`editor` relationship with. A reader/writer without such a relation
receives an empty collection, never another Project's metadata.

The default MySQL pair is `casbin_rules` and `casbin_policy_events`. A
deployment may configure a different pair through
`authorization.mysql.rulesTable` and `authorization.mysql.eventsTable`; both
must be set together, use distinct single ASCII identifiers, and refer to
tables created by the deployment migration. The policy Adapter, transactional
store, audit query, and watcher always use the same validated pair.

Run the real-process API/RPC smoke with:

```sh
make authorization-smoke
```

See [failure-mode-inventory.md](failure-mode-inventory.md) for the executable
contract that must be preserved as gosky and gofly evolve.

The standard Gosky `make -C examples/gosky check` target also runs the isolated
HTTP/MySQL E2Es through `examples/gosky/bin/scripts/check-gosky-mysql.py`. Docker supplies a
fresh migrated database; the gate requires every named suite to pass and
retains JSON status, migration hashes and test transcripts under
`.tmp-test/gosky-mysql/`. Reports remain available on both success and failure; gofly CI does not run
these application checks.

**Tenant-managed roles, menus, and users (dynamic RBAC).** Operators can
create additional roles, edit a tenant menu catalog, assign role menus to
capabilities, and administer tenant users while the capability vocabulary and
its transport mapping remain deployment-owned:

- native API: `/api/admin/system/roles`, `/api/admin/system/menus`,
  `/api/admin/system/users` (state and password-reset sub-resources);
- SmartAdmin compatibility: `/role/getAll` lists fixed plus tenant roles,
  `/role/menu/*` reads and writes role grants, and `/menu/add`, `/menu/update`,
  `/menu/batchDelete` operate on non-seeded catalog rows;
- role/menu capability edits, user creation, password reset, and membership
  disable commit with their Casbin/audit changes in one transaction; disabling
  a membership or resetting a password immediately revokes the affected
  refresh sessions;
- the fixed `reader`/`writer`/`admin` roles, seeded menus, and the operator
  button points stay immutable, and the final complete-management-set
  administrator can never be removed.

Design, identifier ranges, and transactions: [dynamic-rbac.md](dynamic-rbac.md);
executable contracts: `DYNA-*` rows in
[failure-mode-inventory.md](failure-mode-inventory.md). The Gosky
`make -C examples/gosky check` gate now runs the registered MySQL E2E suites, including the
five dynamic RBAC lifecycle suites.
