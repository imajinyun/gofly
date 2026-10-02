#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/gofly-api-semantic-XXXXXX")"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

export GOCACHE="${GOCACHE:-$tmp/gocache}"
export GOTMPDIR="${GOTMPDIR:-$tmp/gotmp}"
mkdir -p "$GOCACHE" "$GOTMPDIR"
report="${API_SEMANTIC_REPORT:-$tmp/api-semantic-parity-report.json}"

cd "$root"
go test -count=1 -shuffle=on ./cmd/gofly/internal/generator -run 'Test(APITypeRef|ParseAPIGoctl|ParseAPIRouteLocal|ParseAPIRoutesWithout|FormatAPIGoctl|GenerateAPISemanticFixture|APISemanticReplay)'
go test -count=1 -shuffle=on ./rest -run 'Test(ContextBindGoZeroRequest|BindRequestSourceOrderingAndMethodBodyBranches|OpenAPI.*Struct|RuntimeSemanticFailureModes)'

python3 - "$root" "$report" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
report_path = pathlib.Path(sys.argv[2])
expectations = json.loads((root / "testdata/api/semantic/goctl/expectations.json").read_text(encoding="utf-8"))
contract = (root / "testdata/api/semantic/goctl/contract.api").read_text(encoding="utf-8")
common = (root / "testdata/api/semantic/goctl/types/common.api").read_text(encoding="utf-8")
required_contract = [
    "import (",
    "type (",
    "@handler deleteItem",
    "delete /items/:id (CreateRequest)",
    "jwt:        Auth",
]
required_common = [
    "Item {",
    "Labels map[string][]string",
    'RequestID string `header:"X-Request-ID"`',
]
missing = [marker for marker in required_contract if marker not in contract]
missing.extend(marker for marker in required_common if marker not in common)
if expectations.get("schema") != "gofly.api_semantic_fixture.v1":
    missing.append("semantic fixture schema")
routes = expectations.get("routes") or []
if len(routes) != 3:
    missing.append("expected exactly three semantic routes")
if not any(row.get("status") == 204 and row.get("hasResponse") is False for row in routes):
    missing.append("response-less 204 route")

runtime_semantics = expectations.get("runtimeSemantics") or []
required_runtime = {
    "jwt-missing-invalid": (401, "unauthenticated"),
    "middleware-short-circuit-order": (401, "unauthenticated"),
    "validation-error-envelope": (400, "invalid_argument"),
    "typed-business-error-envelope": (404, "not_found"),
    "panic-recovery-envelope": (500, "internal"),
    "response-less-success": (204, None),
    "request-cancellation": (499, "canceled"),
    "handler-deadline": (504, "deadline_exceeded"),
    "downstream-timeout": (504, "deadline_exceeded"),
}
runtime_by_id = {row.get("id"): row for row in runtime_semantics if isinstance(row, dict)}
if set(runtime_by_id) != set(required_runtime):
    missing.append("complete runtime semantic failure-mode inventory")
for case_id, (status, code) in required_runtime.items():
    row = runtime_by_id.get(case_id) or {}
    if row.get("status") != status:
        missing.append(f"runtime semantic {case_id} status {status}")
    if code is not None and row.get("code") != code:
        missing.append(f"runtime semantic {case_id} code {code}")
    correlation = set(row.get("correlation") or [])
    if not {"request-id", "trace-id"}.issubset(correlation):
        missing.append(f"runtime semantic {case_id} correlation identifiers")

report = {
    "schema": "gofly.api_semantic_parity_report.v1",
    "fixture": "testdata/api/semantic/goctl/contract.api",
    "profiles": ["gofly-ai", "gozero-compatible"],
    "routes": routes,
    "fieldLocations": ["path", "query", "header", "body"],
    "typeShapes": expectations.get("types") or {},
    "runtime": {
        "generatedProjectCompile": "pass" if not missing else "fail",
        "httpSmoke": "pass" if not missing else "fail",
        "invalidRequestEnvelope": "pass" if not missing else "fail",
        "failureModes": runtime_semantics,
        "assertions": ["status", "stable-error-envelope", "request-id", "trace-id", "body-shape", "context-cancellation"],
        "excludedAssertions": ["log-timing", "implementation-only-fields"],
    },
    "openapi": {
        "locations": "pass" if not missing else "fail",
        "responseStatuses": "pass" if not missing else "fail",
        "responseLessContent": "pass" if not missing else "fail",
    },
    "status": "pass" if not missing else "fail",
    "missing": missing,
}
report_path.parent.mkdir(parents=True, exist_ok=True)
report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
if missing:
    for item in missing:
        print(f"api semantic parity missing: {item}", file=sys.stderr)
    raise SystemExit(1)
print("api semantic parity OK")
PY

if [ -n "${GOZERO_ROOT:-}" ]; then
	if [ ! -f "$GOZERO_ROOT/go.mod" ] || [ ! -f "$GOZERO_ROOT/tools/goctl/go.mod" ]; then
		printf 'GOZERO_ROOT is not a go-zero checkout: %s\n' "$GOZERO_ROOT" >&2
		exit 1
	fi
	gozero_commit="$(git -C "$GOZERO_ROOT" rev-parse HEAD)"
	expected_commit="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["gozeroCommit"])' "$root/testdata/api/semantic/goctl/expectations.json")"
	if [ "$gozero_commit" != "$expected_commit" ]; then
		printf 'go-zero oracle commit mismatch: got %s want %s\n' "$gozero_commit" "$expected_commit" >&2
		exit 1
	fi
	oracle_out="$tmp/goctl-oracle"
	oracle_source="$tmp/goctl-source"
	cp -R "$root/testdata/api/semantic/goctl" "$oracle_source"
	mkdir -p "$oracle_out"
	(
		cd "$GOZERO_ROOT/tools/goctl"
		go run . api go --api "$oracle_source/contract.api" --dir "$oracle_out" --style gozero
	)
	if [ ! -f "$oracle_out/internal/handler/catalog/createitemhandler.go" ] || [ ! -f "$oracle_out/internal/handler/catalog/deleteitemhandler.go" ]; then
		printf 'go-zero semantic oracle handlers are missing under %s\n' "$oracle_out" >&2
		exit 1
	fi
	python3 - "$report" "$gozero_commit" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
data["oracle"] = {
    "available": True,
    "commit": sys.argv[2],
    "generation": "pass",
    "classification": "gofly-enhancement",
    "successStatus": {"goctl": 200, "gofly": [200, 201, 204]},
    "note": "Pinned goctl accepts the grammar and response-less route but emits httpx.Ok/OkJson with status 200; gofly preserves documented respCode values.",
}
path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
fi

printf 'api semantic report: %s\n' "$report"
