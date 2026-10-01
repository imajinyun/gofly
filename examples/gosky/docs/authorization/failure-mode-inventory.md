# Authorization Failure-Mode Inventory

This inventory defines the observable behaviour before the Casbin integration
is implemented. `gosky` is deny-by-default: an authorization integration must
never turn an unavailable policy decision into an allow decision.

| ID | Condition | Expected outcome | Evidence |
| --- | --- | --- | --- |
| AUTHZ-001 | `Authorization` is missing | `401 unauthenticated`; the handler is not called | REST/RPC smoke report |
| AUTHZ-002 | Bearer JWT is malformed, expired, or signed with another key | `401 unauthenticated`; no policy lookup is attempted | REST/RPC smoke report |
| AUTHZ-003 | JWT has no non-empty `sub` claim | `401 unauthenticated` | REST/RPC smoke report |
| AUTHZ-004 | JWT has no non-empty `tenant_id` claim | `403 permission_denied`; a request header cannot supply a tenant | REST/RPC smoke report |
| AUTHZ-005 | Authenticated user has no matching policy | `403 permission_denied` and the handler is not called | REST/RPC smoke report |
| AUTHZ-006 | Authenticated user is assigned a permitted role in the same tenant | The protected REST route returns `200` | REST/RPC smoke report |
| AUTHZ-007 | A valid policy exists only for another tenant | `403 permission_denied` | REST/RPC smoke report |
| AUTHZ-008 | A route contains a concrete identifier | Policy matching uses the registered route template, never the raw identifier | REST/RPC smoke report |
| AUTHZ-009 | The policy model or policy source cannot load | Service construction fails; it never starts in an allow-all state | startup test |
| AUTHZ-010 | A policy-management request would remove the final tenant administrator | The request is rejected and the prior policy set remains active | policy-store test |
| AUTHZ-011 | RPC caller lacks an authenticated principal or matching policy | RPC returns `Unauthenticated` or `PermissionDenied`, never `Unknown` | RPC integration test |
| AUTHZ-012 | A policy decision or denial is logged | Logs include low-cardinality decision fields only; JWTs and Authorization headers are redacted | log assertion |
| AUTHZ-013 | RPC metadata claims a different tenant from the verified JWT | The JWT tenant remains authoritative and cross-tenant access is denied | RPC integration test |
| AUTHZ-014 | An experimental mux stream opens without a bearer token | The stream is closed with `Unauthenticated` before its business handler reads a message | mux integration test |
| AUTHZ-015 | An experimental mux stream has a malformed or incorrectly signed bearer token | The stream is closed with `Unauthenticated` before its business handler runs | mux integration test |
| AUTHZ-016 | An authenticated mux principal has no policy for the stream resource | The stream is closed with `PermissionDenied` before its business handler runs | mux integration test |
| AUTHZ-017 | Mux routing metadata claims a tenant different from the verified JWT | The JWT tenant remains authoritative and the stream is closed with `PermissionDenied` | mux integration test |
| AUTHZ-018 | An authorized mux caller opens a registered stream | Routing metadata is propagated to the stream middleware, the Casbin `STREAM` policy is enforced, and the handler can process the first business message | mux integration test and generated-service smoke |
| AUTHZ-019 | An authenticated project owner belongs to the project's tenant but has no Casbin role | `GET /api/v1/projects/{id}` returns `200`; the owner relationship is evaluated from the persisted project record | HTTP authorization contract |
| AUTHZ-020 | An authenticated active project member belongs to the project's tenant but has no Casbin role | `GET /api/v1/projects/{id}` returns `200`; a member row is evaluated after the project tenant check | HTTP authorization contract |
| AUTHZ-021 | An authenticated `reader`, `writer`, or `admin` has an allowed Casbin policy in the Project tenant | `GET /api/v1/projects/{id}` returns `200`, even without a member row; `writer` inherits `reader` and `admin` inherits `writer` | HTTP authorization contract |
| AUTHZ-022 | The project exists but its persisted tenant differs from the verified JWT tenant | `403 permission_denied`; a request header cannot override the tenant and no project representation is returned | HTTP authorization contract |
| AUTHZ-023 | The project exists in the caller tenant but the caller is neither owner, active member, nor Casbin-authorized reader/writer/admin | `403 permission_denied` | HTTP authorization contract |
| AUTHZ-024 | A project identifier does not exist | `404 not_found` after authentication; no synthetic project response is returned | HTTP authorization contract |
| AUTHZ-025 | The authorization decision has been made for a Project read | A durable operation-log row records the actor, verified tenant, project identifier, operation, outcome, authorization basis, and request correlation ID; it never stores bearer credentials or response bodies | repository and HTTP authorization contract |
| AUTHZ-026 | The durable operation-log write fails | The Project read fails closed with `503 unavailable`; a successful representation is never returned without its corresponding audit record | HTTP authorization contract |
| AUTHZ-027 | A Project path identifier is not a valid unsigned 64-bit integer | `400 invalid_argument`; no lookup or audit insert is attempted because it cannot be represented by the MySQL `BIGINT UNSIGNED` contract | HTTP authorization contract |
| AUTHZ-028 | The verified JWT `tenant_id` is missing, non-numeric, zero, or exceeds unsigned 64-bit range | `403 permission_denied`; no request header can supply a replacement and no audit insert is attempted | HTTP authorization contract |
| AUTHZ-029 | A Project has `project_state = 'deleted'` | `404 not_found` after authentication; neither the resource representation nor its deleted state is disclosed | MySQL integration test |
| AUTHZ-030 | An active membership row has a different `project_tenant_id` than the Project | The database rejects the inconsistent row through the composite foreign key; runtime membership checks also bind Project ID and tenant ID | MySQL schema and integration test |
| AUTHZ-031 | A Project authorization decision is persisted | `opslogs` records numeric operator/project tenant IDs, numeric Project ID, actor, outcome, authorization basis, and request ID; error bodies, bearer credentials, and responses remain empty | MySQL integration test |

## Scope

The first iteration covers REST authorization, unary HTTP-RPC authorization,
and the explicitly enabled experimental mux stream probe through the shared
authorization service. REST resources use a registered route template and HTTP
method; unary RPC resources use the stable `rpc:<service>/<method>` format and
the `CALL` action; mux streams use the same resource format and the `STREAM`
action. The mux adapter carries request metadata only in its routing frame and
restores it into the server stream context before middleware runs.

Project ownership and membership checks use the MySQL-backed Project repository.
Policy management, persistent Casbin policies, and distributed refresh remain
separate follow-up work.

## Trust Boundaries

- The bearer token is untrusted until signature and claim validation succeeds.
- `subject` and `tenant_id` come only from verified JWT claims.
- Request headers, query parameters, request bodies, and RPC metadata cannot
  select a tenant or a role.
- Mux routing metadata is untrusted transport input. It may carry a bearer
  token, but the JWT signature and verified claims are the only identity and
  tenant source. The stream handler never receives an authorization failure.
- Casbin policy files are trusted deployment inputs. Their path is fixed by
  application configuration and is never accepted from a request.
- Project tenant, owner, and membership data are trusted only after the MySQL
  repository returns them. The request path identifies a Project but never
  selects its tenant, owner, or authorization basis.
- Project operation logs record verified identity and authorization metadata;
  they do not persist JWTs, HTTP Authorization headers, or response bodies.
