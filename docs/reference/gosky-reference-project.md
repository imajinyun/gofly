# gosky Reference Project

`examples/gosky` is gofly's committed API/RPC reference project. It starts as
a production `gofly new service` scaffold and retains the resulting REST,
gRPC, OpenAPI, governance, observability, and deployment boundaries.

New gofly capabilities that affect generated projects must first be checked
against gosky. A contribution must preserve its independent module build and
tests and add a repeatable gosky contract, smoke case, fixture, or documented
exception when it changes a relevant boundary.

## Project Authorization Reference

The Project REST example demonstrates the boundary between tenant-scoped Casbin
RBAC and persisted resource-state authorization. It uses MySQL Project,
membership, and operation-log tables. Admin reads require explicit `project.read` plus an owner/viewer/editor relationship or `project.read.any`; front reads use project relationships. Backend roles never inherit grants. Project updates, soft deletes and collaborator administration combine explicit operation grants with resource scope, and GET/list responses expose matching `allowedActions`. Tenant reader/writer roles alone do not grant access to every Project.
The schema and fail-closed audit contract are documented in
[`examples/gosky/docs/persistence/project-mysql-schema.md`](../../examples/gosky/docs/persistence/project-mysql-schema.md).

The reference also contains a MySQL Casbin policy-management slice: current
rules, immutable policy mutation events, last-admin protection, and
cross-instance watcher refresh. These remain separate from Project data state:
owner/member/collaborator authorization stays in the Project repository rather
than being copied into Casbin policy rows.

Run the baseline locally:

```sh
make -C examples/gosky check
```

This target requires Go, Python 3 and a reachable Docker daemon. It runs the
Gosky module tests, schema validation, runtime authorization smoke and all
required HTTP/MySQL E2Es. The MySQL runner creates a digest-pinned disposable
database, applies the checked-in migrations, rejects skipped/missing suites,
and removes its container and anonymous volumes even on failure. It never
uses an inherited application DSN for migration. Run just that gate with
`make -C examples/gosky mysql-e2e`.

Gofly CI does not invoke this target or host example workflows. Run it explicitly
when verifying Gosky. Reports remain under
`examples/gosky/.tmp-test/gosky-mysql/run-*/`; `summary.json` includes test status,
migration hashes, image digest and cleanup state, with raw test/migration output.
The authoritative command and suite list live in
`examples/gosky/bin/scripts/check-gosky-mysql.py`.

The Casbin demonstration covers tenant-scoped REST, unary HTTP-RPC, and the
explicitly enabled experimental mux stream probe. Project owner/member ABAC is
backed by MySQL. Persistent Casbin policy administration and distributed policy
refresh are also exercised against MySQL as explicit, independent contracts.
