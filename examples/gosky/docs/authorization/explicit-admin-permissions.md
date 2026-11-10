# Explicit Admin Permissions Implementation Plan

> Execution: continue the approved implementation in the existing Gosky workspace. Preserve unrelated edits. Do not commit or push without the user's commit-message confirmation.

**Goal:** Make every admin operation depend on directly assigned permissions, with resource scope and browser actions matching the backend decision.

**Architecture:** Casbin accepts direct subject-to-role membership only; roles never inherit permissions. Project services combine explicit operation grants with tenant and resource scope. The SmartWeb adapter projects independently checked operations without changing browser page source.

**Tech stack:** Existing Go, Casbin, MySQL and gofly HTTP services; no new dependency or schema is needed.

## Contract

- The fixed roles are `reader`, `writer`, and `admin`. Role membership is many-to-many within a verified tenant. Effective permissions are the union of directly assigned roles only. Role names are reserved and cannot be user subjects or membership sources.
- Old role-to-role links never grant runtime access, even before conversion. New bootstrap writes complete explicit policies per role, without grouping roles together.
- A separate, audited operator conversion materializes existing legacy inheritance into direct grants, expands the old combined management grant into explicit management operations, and removes legacy role links atomically. It runs only when legacy role links exist, so retry cannot resurrect revoked grants. It never runs on service startup.
- Admin Project read requires `project.read` AND (owner, active viewer/editor, or `project.read.any`). Front Project reads retain relation-only access.
- Admin Project update requires `project.update` AND (owner, active editor, or `project.manage.any`). Delete requires `project.delete` AND (owner or `project.manage.any`). Collaborator administration requires `project.member.manage` AND (owner or `project.manage.any`). Cross-tenant access is denied before any relation can authorize it.
- Project GET/list responses add `allowedActions`, computed with the same decisions used for read/update/delete/member operations. They contain stable action names, not role hierarchy or raw policy rows.
- Project PUT updates name and description; DELETE soft-deletes. Both recheck mutable tenant/state/relation inside the transaction and commit the operation audit with the data change. Client input cannot set tenant, owner, state or permission flags.
- Role management is split into read, permission editing, Project scope editing, employee add, employee remove, and policy-event read. `authorization.role.manage` means permission editing only; it is not a wildcard for all management operations.
- Project read scope changes only `project.read.any`; they never enable `project.read` or other operation grants. Related scope is the absence of the all-tenant read grant.
- SmartWeb navigation requires role-read permission; each existing management button requires its own capability. Menu IDs remain stable and new capability leaves are additive. Save replaces only explicitly selected managed grants; structural nodes grant nothing.
- Keep at least one explicitly assigned subject with role-read, permission-edit, employee-add and employee-remove permissions; employee-only grants/removals cannot delegate stronger role authority than the operator possesses. Explicit permission editors may configure any registered capability.
- Policy writes retain durable events and local reload; watchers remain polling-based. New requests enforce revoked permissions after the relevant instance reloads. Existing front behavior is not expanded.

## Failure-mode inventory (written before tests)

| Boundary | Failure | Expected invariant / verification |
| --- | --- | --- |
| Direct membership | Legacy `admin -> writer -> reader`, role-name subject, or attempted role binding | No inherited or self-role authorization; explicit user grants still work. HTTP/MySQL E2E and direct authorizer boundary test. |
| Revocation | Owner/editor retains relationship after operation permission removed | Admin API denies and `allowedActions` omits operation; front relation read remains allowed. |
| Scope | Scope widened while operation permission is absent | No access is created merely by scope selection. |
| Management | Employee assigner edits policy, removes employees, grants stronger role or mutates another tenant | Denied without changing policy or audit state; only explicitly permitted operations appear in login navigation. |
| Resource mutation | Viewer update, editor delete, cross-tenant owner, deleted row, malformed body | Denied or rejected, unchanged data, durable denial audit where applicable. |
| Transaction | Relationship changes before mutation; audit insert fails | Recheck under lock; mutation cannot commit without audit. |
| Upgrade | Custom tables, duplicate conversion, revoked grant after first conversion | Tenant-only conversion, audited once, idempotent retry with no resurrected policies. |
| Last manager | Remove final administrative membership or required management permission | Reject and preserve recoverability. |

Real HTTP/MySQL workflows are the primary feature tests, retaining `go test -json` output and database assertions. Isolated authorizer checks are needed for forged legacy role links and reserved subjects without requiring DB fixtures; they enforce the same direct-only invariant.

## Implementation steps

- [x] Add failing HTTP/MySQL scenarios and direct authorizer regression from the inventory; retain red output.
- [x] Implement direct-only Casbin evaluation, explicit bootstrap, controlled legacy conversion, and dedicated management capabilities.
- [x] Align SmartWeb role reads, scope writes, membership writes and button projection; retain existing browser contracts.
- [x] Consolidate Project decisions and action projection; add audited update and soft-delete repository methods and routes.
- [x] Update old test fixtures to assign the permissions each scenario actually needs; remove inheritance expectations.
- [ ] Run Gosky tests/race/vet/lint/security and disposable MySQL E2Es; run matching repository gates and document evidence.

## Verification

Use `make -C examples/gosky check`, the required-suite registry in `examples/gosky/bin/scripts/check-gosky-mysql.py` for Gosky validation. The toolkit uses `make governance-10-rounds` independently. Run with a temporary `GOCACHE`/`GOTMPDIR`, `GOFLAGS=-count=1`, `GOPROXY=direct` and shuffled tests. Keep the coverage ratchet unchanged. This document specifies contracts implemented by those existing gates.

## Compatibility and operator conversion

Existing user-role assignments keep their IDs. Backend role inheritance no longer works even if old `g(role, role, tenant)` rows remain. The separate conversion preserves existing effective grants as direct role policies and removes those rows; it does not grant the new Project update/delete operations. A permission editor explicitly assigns those operations through the role capability tree.

```sh
# Run from examples/gosky with the deployment's GOSKY_MYSQL_DSN configured.
GOSKY_POLICY_BOOTSTRAP_TENANT=1001 \
GOSKY_POLICY_BOOTSTRAP_ACTOR=local-operator \
GOSKY_POLICY_UPGRADE_EXPLICIT=true \
go run ./cmd/gosky-policy-bootstrap
```

The command honors `GOSKY_POLICY_RULES_TABLE` and `GOSKY_POLICY_EVENTS_TABLE`. It operates only on the selected tenant and records every converted policy and removed role edge. Review the explicit role selections after conversion. New requests use updated policies after local reload or the next successful watcher poll; refresh browser login information to update cached buttons.

New admin endpoints are `PUT /api/admin/projects/{id}` with `{ "name": "New name", "description": "Optional description" }`, and `DELETE /api/admin/projects/{id}` for soft deletion. Successful writes return `{ "changed": true }`. GET/list items include `allowedActions` drawn from `read`, `update`, `delete`, and `members.manage`. Each write reauthorizes regardless of a previously returned action list. A duplicate active Project name returns 409; validation errors return 400, denied access 403, and deleted/absent Projects 404.

## Database test isolation

Policy integration tests create random validated table names with `CREATE TABLE ... LIKE` and drop only those owned tables. They never clear default policy/event tables. The disposable MySQL runner remains the canonical CI path. During this change, a legacy fixture cleared the local test database's default tables; its nine original policies and nine original audit rows were restored atomically from MySQL row-binlog before-images, then compared column-by-column. No legacy cleanup helper remains.
