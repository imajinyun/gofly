# SmartAdmin Compatibility Module Template

This is the required pattern for every support/business module ported from
smart-api into gosky behind the unmodified smart-web client (tasks T5–T19).
Follow the checklist in order; deviations need an explicit decision in the
task report.

## 1. Contract first

1. Enumerate every endpoint the client calls in `src/api/**/*-api.ts`
   (method, path, query names, body field names, response fields the views
   actually read). Keep all of them unchanged.
2. Identify the SmartAdmin response shape for the module:

   - success: `smartweb.OK[T]` → `{code:0, ok:true, msg:"操作成功", data:T, dataType:1}`
   - list pages: the data shape used by the view (`list/total` or a plain
     page object) — do not invent new field names.
   - failure: `ctx.Error(coreerrors...)` for business errors; status codes
     remain 400/403/404/503.

3. Path and parameter quirks stay compatible:
   - batch deletes accept the client's actual query name (e.g.
     `?menuIdList=`), legacy aliases may be kept;
   - SmartAdmin uses GET for some mutations (`/role/delete/{id}`,
     `/menu/batchDelete`) — preserve them.

## 2. Capability and authorization design

Capability vocabulary: `<domain>.<resource>.<action>`, lower-case dotted.

- Existing domains: `project.*`, `greeter.*`, `authorization.*`,
  `system.user.manage`, `system.menu.manage`.
- New support modules use **two capabilities per module**:
  - `support.<module>.read` — list/detail endpoints and page visibility;
  - `support.<module>.manage` — create/update/delete/execute.
  Example for dictionaries: `support.dict.read`, `support.dict.manage`.
- Add them to `authz.ManagedCapabilities()` so they appear in the role
  permission tree; each new capability must have exactly one menu row with
  a non-empty `capability` (never a webPerms button).
- Default role grants are decided per module in its task; update
  `DefaultRoleCapabilities` deliberately — new capabilities are **not**
  granted to readers by default.
- Every service method calls `authorizeContext(ctx, authorizer, capability)`
  before touching storage. Handlers never make the capability decision.

## 3. Menu seeding

System menu IDs stay inside the fixed `810000xxx` range; tenant-created IDs
start above `820000000`.

1. Add rows to `repo.seedMenus` (Go) and a paired migration
   `internal/migrations/<version>_<name>.{up,down}.sql` using
   `INSERT IGNORE ... SELECT FROM tenants`.
2. Add the ID to `operatorButtonIDs` **iff** it is a current-operator
   webPerms button; target-role capability leaves must never be buttons.
3. Register webPerms → capability in
   `smartweb.managementButtonCapabilities`; structural page gating goes in
   `smartweb.managementPageCapabilities`.
4. Update `TestSeedCatalogConsistency` (button count) and add the ID to both
   i18n catalogs `system.menu.id.<id>` (zh-CN and en-US).
5. Keep the Go seed and migration SQL in sync by inspection; migration file
   content is checksummed, so never edit an applied migration — add a new
   pair instead.

ID allocation: ask before picking numbers; current allocations are
`...001..007` structural, `...011..037` capability leaves, `...021..028`
role/menu buttons, `...041..043` menu buttons, `...051..054` department
buttons, `...061..066` employee buttons.

## 4. Layered implementation

```text
internal/api/http/v1/smart<module>/   # HTTP binding only: parse, smartweb.OK/Error
internal/smartweb/                   # service/DTO translation when shared
internal/app/                        # use case: authorize, validate, orchestrate
internal/model/repo/                 # SQL repository: parameterized queries only
internal/migrations/                 # paired up/down SQL
```

- The `smartweb` package must not import `database/sql`, `core/storage`, or
  `repo` (AST architecture test enforces this).
- Subjects, tenant IDs, and capabilities originate from the verified
  principal, never from request fields.
- Mutations write audit events through the existing audited services; do
  not create parallel log tables where `admin_audit_logs` /
  `casbin_policy_events` already fit.

## 5. Pagination

Use `app.NormalizePage` / `app.Paginate` (or `app.PageBounds`) for every
in-memory paged list: 1-based `pageNum`, default size 20, max 100, overflow
pages return an empty page rather than wrapping. Prefer SQL-level
`LIMIT/OFFSET` for large tables with the same defaults.

## 6. i18n

- Backend-delivered system labels (new fixed menu IDs, fixed roles): both
  `src/i18n/lang/{zh-CN,en-US}/system/menu.ts` (and `role.ts` where
  relevant); render through `useSystemLabels()`.
- Page static copy: per-domain keys under `system.page.<module>.*` in both
  languages; extract hardcoded Chinese in the touched views.
- New error codes: `system.error.code.<code>` in both languages; unknown
  codes automatically fall back to the server message via
  `i18n/error-message.ts`.

## 7. Auth boundary for compat routes

Authenticated compatibility routes use
`rest.WithMiddlewares(smartweb.AuthMiddleware(interceptor))`, not
`rest.WithAuth`. The middleware emits the numeric 401 codes the client
recognizes:

- `30007` for missing/malformed credentials,
- `30008` for invalid/expired/revoked sessions,

and still establishes the same admin principal for handlers.

## 8. Tests and evidence (failure-mode inventory first)

1. Write the failure-mode list before implementation (invalid input,
   cross-tenant access, unauthorized capability, unknown ID, pagination
   overflow, idempotent re-run).
2. Unit tests: service mapping and authorization; handler contract
   (status/body/parameter aliases); seed consistency.
3. MySQL E2E under `//go:build integration`: isolated fixture tables or
   uniquely named tenants; never mutate default policy rows. Record the
   exact `-run` pattern and required env (`GOSKY_MYSQL_DSN`) in the report.
4. Gate: `go vet`, `gosec -quiet`, `-shuffle=on -race` on touched packages,
   frontend `npm run build:test`.

## 9. Documentation

Update:

- `README.md` SmartAdmin section (endpoint list, capabilities, seed IDs);
- `docs/authorization/dynamic-rbac.md` for menu/button changes;
- this file when the template itself evolves;
- the Feishu startup guide only when seed/bootstrap steps change.
