# Dynamic RBAC: tenant-managed roles, menus, and user administration

This document freezes the design for making the admin-console **personnel,
role, menu, and permission** surface tenant-managed while preserving gosky's
deny-by-default authorization boundary. The executable contracts are the
`DYNA-*` rows in [failure-mode-inventory.md](failure-mode-inventory.md).

## What becomes dynamic, and what stays deployment-owned

| Surface | Before | After |
| --- | --- | --- |
| Roles | Fixed `reader` / `writer` / `admin` codes | Fixed codes remain reserved; tenants can create additional custom roles stored in `roles` |
| Role hierarchy | Code-owned `admin -> writer -> reader` | Unchanged. **Custom-role inheritance is explicitly out of scope for this iteration**; combination is achieved by binding one subject to multiple roles and taking the capability union |
| Menus | Go constants in `internal/smartweb` | Tenant rows in `menus`, seeded from the code-owned catalog; CRUD allowed for non-system rows |
| Role-to-capability grants | Casbin `p` rules edited via the fixed-role compatibility API | Same Casbin `p` rules, but the role may be any active role row; the role-menu page is the editing UI and `role_menus` is the browser-shaped read model |
| Capability vocabulary | `authz.ManagedCapabilities()` | Still code-owned. Tenants cannot invent capabilities, actions, transport paths, or URL permissions |
| Capability-to-transport mapping | `authz.CapabilityRegistry` | Still code-owned; adding a protected endpoint still requires a release |
| Users / memberships | Login and sessions only; provisioning via SQL/operator | Full admin CRUD: create users in the tenant, disable/enable, reset password, revoke sessions |
| Enforcement | Capability registry + Casbin middleware | Unchanged. Menu visibility never bypasses enforcement |

The durable boundary is: **Casbin decides whether a request is allowed; the
`roles` / `menus` / `role_menus` tables only decide how the console presents
capabilities and which custom roles exist.** A menu row cannot name a
capability outside `ManagedCapabilities()`, and a visible menu without the
matching `p` grant never authorizes a request.

## Identifier conventions

- `role_code`: unique per tenant as `(tenant_id, role_code)`, matching
  `^[a-z][a-z0-9_]{1,63}$`. `reader`, `writer`, and `admin` are reserved and
  cannot be created, renamed, or deleted. The same code may exist in two
  different tenants. Casbin stores this code verbatim in `v0` of `p` rows and
  `v1` of `g` rows, alongside the numeric domain.
- Built-in menus keep their existing fixed IDs (`810000001`..`810000007`,
  `810000011`..`810000043`, `810000051`..`810000054`, and
  `810000061`..`810000066`). Tenant-defined menus use positive IDs from the
  auto-increment sequence only; custom IDs are never accepted from a client,
  so they can never collide with the SmartAdmin hardcoded range.
- `menus.web_perms`/`component`/`path` values are validated strings intended
  for the existing compiled SmartAdmin allowlist. The server never returns a
  filesystem path or remote URL derived from tenant input.
- Subjects remain canonical user subjects, resolved only from active same-
  tenant memberships; browsers never submit subject strings for dynamic roles.

## Menu shape

`menus.menu_type` mirrors the existing SmartAdmin numeric contract:

1. catalog (`1`) — no capability, has children
2. page (`2`) — optional capability for the page-level guard
3. point (`3`) — function point; exactly one managed capability when not an
   operator button

The 22 current-operator operator buttons stay code-owned: they are seeded as
system rows, are not editable, and their IDs remain unacceptable as
target-role grants (DYNA-009/DYNA-011). The initial migration seeded the five
`system:role:*` buttons used by the role-permission panes; migration
`20261016120000_smartweb_mgmt_buttons` adds the role add/edit/delete buttons
(`system:role:add`, `system:role:update`, `system:role:delete`, parented by
the Role Management page) and the menu catalog buttons
(`system:menu:add`, `system:menu:update`, `system:menu:batchDelete`, parented
by the Menu Catalog page). Migration `20261016130000_smartweb_organization_pages`
adds the Organization catalog (`810000004`) with the Employee, Department, and
Position pages (`810000005`..`810000007`) plus the ten organization buttons
(`810000051`..`810000054`, `810000061`..`810000066`, including the unchanged
client's `support:department:query` string). Every organization button maps to
`system.user.manage`, the same capability the organization endpoints enforce.
Each button is projected to the login navigation only after the matching
capability check (`authorization.role.read` gates the role/menu pages,
`system.user.manage` the organization pages and buttons, and
`authorization.role.manage`/`system.menu.manage` the role/menu buttons);
catalogs with no visible descendant are pruned.

## Storage and transaction boundaries

New tables (one migration, paired up/down):

- `roles` — tenant-scoped role definitions, `system_flag` for reserved roles.
- `menus` — tenant-scoped catalog; seeded rows carry `system_flag = true`.
- `role_menus` — `(tenant_id, role_id, menu_id)` join; structural catalog/page
  nodes are not stored here unless a tenant explicitly grants them, because
  they never produce a `p` rule.
- `admin_audit_logs` — actor, tenant, operation, object type/id, before/after
  JSON, request/trace IDs.

The mutation rule for role grants is:

1. open one transaction;
2. lock the target role row and the tenant's role-management `p` rules
   `FOR UPDATE`;
3. replace `role_menus`;
4. replace the role's managed-capability `p` rules in `casbin_rules`;
5. append `casbin_policy_events` and `admin_audit_logs`;
6. commit, then have `PolicyService` reload the local enforcer.

Cross-instance propagation continues to use the existing
`casbin_policy_events` outbox watcher. Menu reads are per-request database
reads on small indexed tables; no cache is introduced in this iteration.

## Layering

- `authz` owns Casbin semantics. Its fixed-role validation is relaxed through
  an injected role-existence port: a role rule is valid when the code is one of
  the three reserved codes **or** the port reports an active row in the same
  domain. `authz` never imports `repo` or `app`.
- `app` owns use cases (role directory, menu catalog, user administration) and
  defines repository ports. It must not import `database/sql`, `core/storage`,
  or `repo`; the existing AST boundary tests are extended.
- `repo` owns SQL and implements the ports, including the cross-table
  transaction (`role_menus` + `casbin_rules` + events + audit) using
  `storage.SQLStore.Transact`.
- `smartweb` stays free of persistence imports and continues to speak
  SmartAdmin DTOs only.

## Last-administrator invariant

The invariant is unchanged in substance and now includes custom roles:

> A mutation commits only if, afterwards, at least one active tenant
> membership has, across its active roles in the tenant, the complete
> management set `authorization.role.read`, `authorization.role.manage`,
> `authorization.role.member.add`, and
> `authorization.role.member.remove`.

The check runs on post-mutation state inside the same transaction under
`FOR UPDATE` locks on every path that can remove it: deleting a role, removing
role members, replacing role capabilities, disabling a role, and disabling a
user or tenant membership. Disabling a custom role removes its subject
bindings (the capability template stays for re-enable and member
reassignment).

## User administration

- Creating a user inserts `users` and one `tenant_users` row atomically. The
  Argon2id hash uses the existing `authn` password parameters through an
  exported hashing entry point; plaintext passwords exist only in the request
  lifetime and are never logged, audited, or returned.
- Disabling an account or membership revokes that scope's sessions in the same
  transaction; the persisted-session interceptor already rejects later JWTs.
- Password reset rotates the hash and revokes sessions identically.
- Every user-administration operation requires the new `system.user.manage`
  capability; menu and role definition management requires
  `system.menu.manage`. Both new capabilities are included in
  `ManagedCapabilities()`, registered for their REST surface, and granted to
  the reserved `admin` role at bootstrap/default baseline (DYNA-019). They are
  not implicitly granted to `reader` or `writer`.

## Compatibility

- Native JSON responses add fields only; no existing field is removed or
  retyped.
- `/role/getAll` returns the fixed three roles plus the tenant's active custom
  roles; fixed IDs keep their SmartAdmin constants, custom roles use their
  database ID and a distinct code.
- `/menu/add`, `/menu/update`, and `/menu/batchDelete` become real operations
  for non-system rows instead of the previous unconditional
  `failed_precondition`; attempts against system rows keep that response.
- File/CSV policy mode and deployments without identity storage keep their
  current behavior; dynamic endpoints are simply not wired (DYNA-022).
- The gosky-native session projection (`GET /api/admin/auth/session`,
  `internal/navigation`) remains a deployment-owned list of gosky application
  pages. Tenant-managed rows drive the SmartAdmin console projection
  (`/login/getLoginInfo`, `/menu/query`, `/menu/tree`); the two surfaces share
  the same enforcement but not the same presentation catalog.

## Organization console (departments, positions, employees)

The migration `20261015120000_org_management` adds tenant-scoped
`departments` and `positions` tables, membership placement columns on
`tenant_users`, profile columns on `users` (gender/phone/email), and
menu presentation columns (`frame_url`/`frame_flag`/`cache_flag`).

The SmartAdmin console gains the full organization surface:

- `/department/{listAll,treeList,add,update,delete/{id}}`;
- `/position/{queryPage,add,update,delete/{id},batchDelete,queryList}`;
- `/employee/{query,queryAll,add,update,delete/{id},update/batch/*,
  update/disabled/{id},update/password/reset/{id},
  getPasswordComplexityEnabled,getAllEmployeeByDepartmentId/{id},
  update/center,update/avatar}`;
- `/role/{add,update,delete/{id}}` for tenant-managed roles; fixed
  reader/writer/admin roles remain read-only.

Employees are canonical `users` + `tenant_users` rows. New employees
receive an Argon2id hash (operator-supplied or generated random
password shown once), optional department/position placement, and
role bindings that reconcile fixed and tenant-managed Casbin roles.
Disabling or resetting a password revokes refresh sessions
transactionally; every mutation writes an `admin_audit_logs` row.
