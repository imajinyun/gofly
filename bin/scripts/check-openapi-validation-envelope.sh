#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/reference/openapi-validation-envelope.md"): [
        "gofly.openapi_validation_envelope.v1",
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
