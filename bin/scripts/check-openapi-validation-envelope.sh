#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "openapi-invalid-request-smoke.json"
http_migration_path = root / "docs" / "reference" / "http-migration-dx.json"
checks = {
    pathlib.Path("docs/reference/openapi-validation-envelope.md"): [
        "gofly.openapi_validation_envelope.v1",
        "docs/reference/openapi-invalid-request-smoke.json",
        "docs/reference/http-migration-dx.json",
        "gofly.http_migration_dx.v1",
        "Gin",
        "Echo",
        "Fiber",
        "Hertz",
        "path",
        "query",
        "header",
        "body",
        "tag",
        "schema",
        "error code",
        "validator adapter",
        "rest.ErrorResponse",
        "golden tests",
        "generated service invalid request smoke",
    ],
    pathlib.Path("docs/guides/rest.md"): [
        "rest.ErrorResponse",
        "rest.Config.Validator",
        "ctx.BindRequest",
        "rest.JSONErrorResponse",
        "docs/reference/http-migration-dx.json",
        "Gin",
        "Echo",
        "Fiber",
        "Hertz",
    ],
    pathlib.Path("docs/guides/openapi.md"): [
        "rest.StructSchema",
        "rest.DefaultErrorResponses()",
        "required",
        "oneof",
    ],
    pathlib.Path("rest/binding_test.go"): [
        "TestOpenAPIValidationEnvelopeRuntimeGolden",
        "BindRequest",
        "path",
        "query",
        "header",
        "body schema decode failure",
        "validator adapter field failure",
        "rest.ErrorResponse",
        "coreerrors.CodeInvalidArgument",
    ],
    pathlib.Path("rest/openapi_test.go"): [
        "TestOpenAPIValidationEnvelopeSchemaGolden",
        "OpenAPI",
        "required",
        "ErrorResponse",
        "path",
        "query",
        "header",
        "JSONBodySchema",
        "DefaultErrorResponses",
        "oneof=pending paid canceled",
    ],
    pathlib.Path("cmd/gofly/internal/generator/service_test.go"): [
        "TestGeneratedServiceOpenAPIValidationEnvelopeContract",
        "assertInvalidRequestEnvelope(t)",
        "waitOpenAPI(t, ctx",
        "invalid request",
        "rest.ErrorResponse",
        "coreerrors.CodeInvalidArgument",
        "http.StatusBadRequest",
        "OpenAPI",
    ],
}

missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
else:
    manifest = {}
    missing.append("docs/reference/openapi-invalid-request-smoke.json: file is missing")
if http_migration_path.is_file():
    http_migration = json.loads(http_migration_path.read_text(encoding="utf-8"))
else:
    http_migration = {}
    missing.append("docs/reference/http-migration-dx.json: file is missing")

require(manifest.get("schema") == "gofly.openapi_invalid_request_smoke.v1", "invalid request smoke schema mismatch")
require(manifest.get("status") == "blocking", "invalid request smoke status must be blocking")
require(manifest.get("blockingGate") == "make openapi-validation-check", "invalid request smoke blocking gate must be make openapi-validation-check")
require(http_migration.get("schema") == "gofly.http_migration_dx.v1", "HTTP migration DX schema mismatch")
require(http_migration.get("status") == "blocking", "HTTP migration DX status must be blocking")
require(http_migration.get("blockingGate") == "make openapi-validation-check", "HTTP migration DX blocking gate must be make openapi-validation-check")

http_scope = http_migration.get("scope") or {}
for excluded in ("GitHub stars", "download counts", "community size", "brand awareness", "transport-layer RPC parity"):
    require(excluded in set(http_scope.get("excluded") or []), f"HTTP migration DX scope.excluded missing {excluded!r}")
for included in ("route path migration", "request binding migration", "middleware chain migration", "stable error envelope migration", "OpenAPI schema migration", "invalid request smoke", "rollback guidance"):
    require(included in set(http_scope.get("included") or []), f"HTTP migration DX scope.included missing {included!r}")

required_http_frameworks = {"Gin", "Echo", "Fiber", "Hertz"}
require(set(http_migration.get("referenceFrameworks") or []) == required_http_frameworks, "HTTP migration DX referenceFrameworks mismatch")
require(
    set(http_migration.get("acceptanceGates") or []) == {
        "make openapi-validation-check",
        "make api-example-consistency-check",
        "make examples-smoke",
    },
    "HTTP migration DX acceptanceGates mismatch",
)
require(
    http_migration.get("migrationOrder") == [
        "route-paths",
        "binding-sources",
        "middleware-chain",
        "error-envelope",
        "openapi-schema",
        "invalid-request-smoke",
        "traffic-switch",
    ],
    "HTTP migration DX migrationOrder mismatch",
)

r8_matrix = http_migration.get("r8CompatibilityMatrix") or {}
require(r8_matrix.get("aiflowTask") == "GOFLY-GOV-10R8-02", "HTTP migration DX R8 matrix aiflowTask mismatch")
require(r8_matrix.get("acceptanceGate") == "make openapi-validation-check", "HTTP migration DX R8 matrix acceptanceGate mismatch")
r8_surfaces = r8_matrix.get("surfaces") or []
required_r8_surface_ids = {
    "route-groups",
    "path-query-header-binding",
    "json-body-binding",
    "middleware-ordering",
    "error-envelope",
    "openapi-schema",
    "invalid-request-smoke",
}
actual_r8_surface_ids = {item.get("id") for item in r8_surfaces if isinstance(item, dict)}
require(required_r8_surface_ids == actual_r8_surface_ids, f"HTTP migration DX R8 surface ids mismatch: {sorted(actual_r8_surface_ids)!r}")
for item in r8_surfaces:
    if not isinstance(item, dict):
        missing.append(f"HTTP migration DX R8 surface must be an object: {item!r}")
        continue
    surface_id = item.get("id", "<missing>")
    for field in (
        "id",
        "surface",
        "frameworks",
        "compatibilityInvariant",
        "evidence",
        "gate",
        "adopterAction",
        "rollbackOrEscalation",
    ):
        require(item.get(field) not in ("", None, []), f"HTTP migration DX R8 surface {surface_id}: {field} is required")
    require(set(item.get("frameworks") or []) == required_http_frameworks, f"HTTP migration DX R8 surface {surface_id}: frameworks mismatch")
    require(
        item.get("gate") in {"make openapi-validation-check", "make examples-smoke"},
        f"HTTP migration DX R8 surface {surface_id}: unsupported gate {item.get('gate')!r}",
    )
    for field in ("compatibilityInvariant", "adopterAction", "rollbackOrEscalation"):
        require(
            len(str(item.get(field) or "").split()) >= 10,
            f"HTTP migration DX R8 surface {surface_id}: {field} must be actionable",
        )
    for evidence in item.get("evidence") or []:
        path = root / evidence
        require(path.exists(), f"HTTP migration DX R8 surface {surface_id}: evidence path missing: {evidence}")

framework_rows = http_migration.get("frameworkMapping") or []
seen_http_frameworks = set()
for item in framework_rows:
    if not isinstance(item, dict):
        missing.append(f"HTTP migration DX framework row must be an object: {item!r}")
        continue
    framework = item.get("framework", "")
    seen_http_frameworks.add(framework)
    for field in ("framework", "routePattern", "bindingPattern", "middlewarePattern", "errorPattern", "openAPIPattern", "gate", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"HTTP migration DX {framework}: {field} is required")
    require(item.get("gate") == "make openapi-validation-check", f"HTTP migration DX {framework}: gate must be make openapi-validation-check")
    for field in ("routePattern", "bindingPattern", "middlewarePattern", "errorPattern", "openAPIPattern", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 8, f"HTTP migration DX {framework}: {field} must be actionable")
require(seen_http_frameworks == required_http_frameworks, f"HTTP migration DX frameworkMapping mismatch: {sorted(seen_http_frameworks)!r}")

step_rows = http_migration.get("migrationSteps") or []
required_steps = {
    "route-paths",
    "binding-sources",
    "middleware-chain",
    "error-envelope",
    "openapi-schema",
    "traffic-switch",
}
seen_steps = set()
for item in step_rows:
    if not isinstance(item, dict):
        missing.append(f"HTTP migration DX step must be an object: {item!r}")
        continue
    step_id = item.get("id", "")
    seen_steps.add(step_id)
    for field in ("id", "surface", "currentEvidence", "adopterAction", "gate", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"HTTP migration DX step {step_id}: {field} is required")
    gate = item.get("gate", "")
    require(gate in {"make openapi-validation-check", "make examples-smoke"}, f"HTTP migration DX step {step_id}: unsupported gate {gate!r}")
    for field in ("adopterAction", "rollbackOrEscalation"):
        require(len(str(item.get(field) or "").split()) >= 8, f"HTTP migration DX step {step_id}: {field} must be actionable")
    for evidence in item.get("currentEvidence") or []:
        path = root / evidence
        require(path.exists(), f"HTTP migration DX step {step_id}: evidence path missing: {evidence}")
require(required_steps <= seen_steps, f"HTTP migration DX missing steps: {sorted(required_steps - seen_steps)!r}")

envelope = manifest.get("runtimeEnvelope") or {}
require(envelope.get("type") == "rest.ErrorResponse", "runtime envelope type must be rest.ErrorResponse")
require(envelope.get("status") == 400, "runtime envelope status must be 400")
require(envelope.get("code") == "invalid_argument", "runtime envelope code must be invalid_argument")

adopter_contract = manifest.get("adopterContract") or {}
require(
    adopter_contract.get("schema") == "gofly.rest_adopter_contract.v1",
    "adopterContract schema must be gofly.rest_adopter_contract.v1",
)
require(
    set(adopter_contract.get("acceptanceGates") or []) == {
        "make openapi-validation-check",
        "make api-example-consistency-check",
    },
    "adopterContract acceptanceGates mismatch",
)
require(
    adopter_contract.get("dashboardReportField") == "restAdoption.openapiValidation",
    "adopterContract dashboardReportField mismatch",
)
require(
    adopter_contract.get("exampleIndex") == "docs/reference/api-example-consistency.json",
    "adopterContract exampleIndex must point to api-example-consistency.json",
)
require(
    set(adopter_contract.get("stableEnvelopeFields") or []) == {"code", "text", "message", "status", "fields"},
    "adopterContract stableEnvelopeFields mismatch",
)
for field in ("publishPolicy", "rollbackPolicy"):
    require(
        len(str(adopter_contract.get(field) or "").split()) >= 12,
        f"adopterContract {field} must be actionable",
    )

p13_closeout = manifest.get("p13RestValidationEnvelopeCloseout") or {}
require(
    p13_closeout.get("schema") == "gofly.rest_validation_envelope_p13_closeout.v1",
    "p13RestValidationEnvelopeCloseout schema mismatch",
)
require(
    p13_closeout.get("aiflowTask") == "GOFLY-P13-03-REST-BINDING-VALIDATION-ENVELOPE",
    "p13RestValidationEnvelopeCloseout aiflowTask mismatch",
)
require(
    p13_closeout.get("status") == "blocking-contract",
    "p13RestValidationEnvelopeCloseout status must be blocking-contract",
)
expected_p13_sources = {
    "docs/reference/openapi-invalid-request-smoke.json",
    "docs/reference/openapi-validation-envelope.md",
    "docs/reference/http-migration-dx.json",
    "rest/binding_test.go",
    "rest/openapi_test.go",
    "cmd/gofly/internal/generator/service_test.go",
}
require(
    set(p13_closeout.get("sourceOfTruth") or []) == expected_p13_sources,
    "p13RestValidationEnvelopeCloseout sourceOfTruth mismatch",
)
for source in p13_closeout.get("sourceOfTruth") or []:
    require((root / source).exists(), f"p13RestValidationEnvelopeCloseout source path missing: {source}")
require(
    p13_closeout.get("acceptanceGate") == "make openapi-validation-check",
    "p13RestValidationEnvelopeCloseout acceptanceGate mismatch",
)
p13_envelope = p13_closeout.get("runtimeEnvelope") or {}
require(p13_envelope.get("type") == envelope.get("type"), "p13 runtimeEnvelope.type must match root runtimeEnvelope")
require(p13_envelope.get("status") == envelope.get("status"), "p13 runtimeEnvelope.status must match root runtimeEnvelope")
require(p13_envelope.get("code") == envelope.get("code"), "p13 runtimeEnvelope.code must match root runtimeEnvelope")
require(
    set(p13_envelope.get("stableFields") or []) == set(adopter_contract.get("stableEnvelopeFields") or []),
    "p13 runtimeEnvelope.stableFields must match adopter stableEnvelopeFields",
)

p13_surfaces = {
    item.get("id"): item
    for item in p13_closeout.get("alignmentSurfaces") or []
    if isinstance(item, dict) and item.get("id")
}
expected_p13_surfaces = {
    "path-binding",
    "query-binding",
    "header-binding",
    "body-binding",
    "tag-validation",
    "validator-adapter",
    "generated-service-smoke",
}
require(set(p13_surfaces) == expected_p13_surfaces, f"p13RestValidationEnvelopeCloseout surfaces mismatch: {sorted(p13_surfaces)!r}")

cases = manifest.get("smokeCases") or []
required_cases = {
    "path-parse-failure",
    "query-validation-failure",
    "header-validation-failure",
    "body-decode-failure",
    "body-tag-validation-failure",
    "validator-adapter-failure",
    "openapi-schema-alignment",
    "generated-service-invalid-request",
}
actual_cases = {item.get("id") for item in cases if isinstance(item, dict)}
require(required_cases <= actual_cases, f"invalid request smoke missing cases: {sorted(required_cases - actual_cases)!r}")
cases_by_id = {
    item.get("id"): item
    for item in cases
    if isinstance(item, dict) and item.get("id")
}

required_surfaces = {"path", "query", "header", "body", "tag", "validator", "schema", "generated-service"}
actual_surfaces = {item.get("surface") for item in cases if isinstance(item, dict)}
require(required_surfaces <= actual_surfaces, f"invalid request smoke missing surfaces: {sorted(required_surfaces - actual_surfaces)!r}")

expected_p13_surface_mapping = {
    "path-binding": ("path", "path-parse-failure"),
    "query-binding": ("query", "query-validation-failure"),
    "header-binding": ("header", "header-validation-failure"),
    "body-binding": ("body", "body-decode-failure"),
    "tag-validation": ("tag", "body-tag-validation-failure"),
    "validator-adapter": ("validator", "validator-adapter-failure"),
    "generated-service-smoke": ("generated-service", "generated-service-invalid-request"),
}
for surface_id, (surface_name, runtime_case_id) in expected_p13_surface_mapping.items():
    item = p13_surfaces.get(surface_id) or {}
    for field in ("id", "surface", "runtimeEvidence", "schemaEvidence", "gate", "rollbackOrEscalation"):
        require(item.get(field), f"p13RestValidationEnvelopeCloseout {surface_id}: {field} is required")
    require(item.get("surface") == surface_name, f"p13RestValidationEnvelopeCloseout {surface_id}: surface mismatch")
    require(
        item.get("runtimeEvidence") == runtime_case_id,
        f"p13RestValidationEnvelopeCloseout {surface_id}: runtimeEvidence mismatch",
    )
    require(
        item.get("schemaEvidence") == "openapi-schema-alignment",
        f"p13RestValidationEnvelopeCloseout {surface_id}: schemaEvidence must be openapi-schema-alignment",
    )
    require(
        runtime_case_id in cases_by_id,
        f"p13RestValidationEnvelopeCloseout {surface_id}: runtimeEvidence case missing",
    )
    require(
        "openapi-schema-alignment" in cases_by_id,
        f"p13RestValidationEnvelopeCloseout {surface_id}: schemaEvidence case missing",
    )
    require(item.get("gate") == "make openapi-validation-check", f"p13RestValidationEnvelopeCloseout {surface_id}: gate mismatch")
    require(
        len(str(item.get("rollbackOrEscalation") or "").split()) >= 10,
        f"p13RestValidationEnvelopeCloseout {surface_id}: rollbackOrEscalation must be actionable",
    )

p13_promotion_policy = str(p13_closeout.get("promotionPolicy") or "")
for needle in (
    "path",
    "query",
    "header",
    "body",
    "tag",
    "schema",
    "error-code",
    "validator adapter",
    "generated-service invalid request smoke",
    "stable rest.ErrorResponse fields",
):
    require(needle in p13_promotion_policy, f"p13RestValidationEnvelopeCloseout promotionPolicy missing {needle!r}")
p13_runtime_policy = str(p13_closeout.get("runtimeArtifactPolicy") or "")
for needle in ("runtime evidence", "ignored temporary paths"):
    require(needle in p13_runtime_policy, f"p13RestValidationEnvelopeCloseout runtimeArtifactPolicy missing {needle!r}")

for item in cases:
    if not isinstance(item, dict):
        missing.append(f"invalid request smoke case must be an object: {item!r}")
        continue
    case_id = item.get("id", "<missing>")
    for field in ("id", "surface", "source", "expectedStatus", "expectedCode", "gate", "evidenceRefs"):
        require(item.get(field) not in ("", None, []), f"invalid request smoke {case_id}: {field} is required")
    for field in ("alignmentInvariant", "consumerAction", "rollbackOrEscalation"):
        require(
            len(str(item.get(field) or "").split()) >= 8,
            f"invalid request smoke {case_id}: {field} must be actionable",
        )
    require(item.get("expectedStatus") == 400, f"invalid request smoke {case_id}: expectedStatus must be 400")
    require(item.get("expectedCode") == "invalid_argument", f"invalid request smoke {case_id}: expectedCode must be invalid_argument")
    refs = item.get("evidenceRefs") or []
    require(refs, f"invalid request smoke {case_id}: evidenceRefs must not be empty")
    for ref in refs:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        require(bool(ref_path), f"invalid request smoke {case_id}: ref path is required")
        require(bool(needles), f"invalid request smoke {case_id}: ref contains list is required for {ref_path}")
        if not ref_path:
            continue
        path = root / ref_path
        if not path.is_file():
            missing.append(f"invalid request smoke {case_id}: ref file is missing: {ref_path}")
            continue
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            if needle not in text:
                missing.append(f"invalid request smoke {case_id}: {ref_path} missing {needle!r}")

for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("openapi validation envelope check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("openapi validation envelope governance ok")
PY
