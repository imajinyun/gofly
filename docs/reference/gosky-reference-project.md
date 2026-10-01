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
membership, and operation-log tables; an authenticated same-tenant Project
owner, active member/collaborator, or Casbin `editor`/`admin` may read a
Project. The schema and fail-closed audit contract are documented in
[`examples/gosky/docs/persistence/project-mysql-schema.md`](../../examples/gosky/docs/persistence/project-mysql-schema.md).

This is deliberately not a persistent Casbin policy-management example. Policy
mutations, audit history for policy changes, and multi-instance watchers remain
separate capabilities so their control-plane requirements are not hidden inside
the Project data path.

Run the baseline locally:

```sh
make gosky-check
```

This target runs the gosky module test suite and a real-process authorization
smoke. It is included in the Go 1.26 GitHub Actions build-and-test job. The
Casbin demonstration covers tenant-scoped REST, unary HTTP-RPC, and the
explicitly enabled experimental mux stream probe. Project owner/member ABAC is
backed by MySQL. Persistent Casbin policy administration and distributed policy
refresh remain separate, explicitly tested evolutions.
