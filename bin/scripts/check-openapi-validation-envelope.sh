#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "openapi-invalid-request-smoke.json"
checks = {
    pathlib.Path("docs/reference/openapi-validation-envelope.md"): [
        "gofly.openapi_validation_envelope.v1",
        "docs/reference/openapi-invalid-request-smoke.json",
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
    ],
    pathlib.Path("docs/guides/openapi.md"): [
        "rest.StructSchema",
        "rest.DefaultErrorResponses()",
        "required",
        "oneof",
    ],
    pathlib.Path("rest/binding_test.go"): [
        "TestOpenAPIValidationEnvelopeRuntimeGolden_BitsUT",
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
        "TestOpenAPIValidationEnvelopeSchemaGolden_BitsUT",
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
        "TestGeneratedServiceOpenAPIValidationEnvelopeContract_BitsUT",
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

require(manifest.get("schema") == "gofly.openapi_invalid_request_smoke.v1", "invalid request smoke schema mismatch")
require(manifest.get("status") == "blocking", "invalid request smoke status must be blocking")
require(manifest.get("blockingGate") == "make openapi-validation-check", "invalid request smoke blocking gate must be make openapi-validation-check")

envelope = manifest.get("runtimeEnvelope") or {}
require(envelope.get("type") == "rest.ErrorResponse", "runtime envelope type must be rest.ErrorResponse")
require(envelope.get("status") == 400, "runtime envelope status must be 400")
require(envelope.get("code") == "invalid_argument", "runtime envelope code must be invalid_argument")

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

required_surfaces = {"path", "query", "header", "body", "tag", "validator", "schema", "generated-service"}
actual_surfaces = {item.get("surface") for item in cases if isinstance(item, dict)}
require(required_surfaces <= actual_surfaces, f"invalid request smoke missing surfaces: {sorted(required_surfaces - actual_surfaces)!r}")

for item in cases:
    if not isinstance(item, dict):
        missing.append(f"invalid request smoke case must be an object: {item!r}")
        continue
    case_id = item.get("id", "<missing>")
    for field in ("id", "surface", "source", "expectedStatus", "expectedCode", "gate", "evidenceRefs"):
        require(item.get(field) not in ("", None, []), f"invalid request smoke {case_id}: {field} is required")
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
