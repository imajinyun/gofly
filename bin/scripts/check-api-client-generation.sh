#!/usr/bin/env sh
set -eu

go_cmd="${GO:-go}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/gofly-api-client-XXXXXX")"
trap 'rm -rf "$tmp_root"' EXIT
mkdir -p "$tmp_root/gocache" "$tmp_root/gotmp" "$tmp_root/out"

run_go_test() {
	pkg="$1"
	pattern="$2"
	printf 'api-client-generation: %s %s\n' "$pkg" "$pattern"
	(
		cd "$root"
		GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" test -count=1 -shuffle=on "$pkg" -run "$pattern"
	)
}

run_go_test ./cmd/gofly/internal/generator 'Test(GenerateAPIClientPathAndQueryParams|GeneratedAPIClientsPreserveRequestContracts)'
run_go_test ./cmd/gofly/internal/command 'TestExecuteAPIClientGeneration'

api_file="$root/testdata/api-client-matrix/shop.api"
for lang in typescript javascript dart java kotlin; do
	(
		cd "$root"
		GOCACHE="${GOCACHE:-$tmp_root/gocache}" GOTMPDIR="${GOTMPDIR:-$tmp_root/gotmp}" "$go_cmd" run ./cmd/gofly api client --file "$api_file" --dir "$tmp_root/out/$lang" --language "$lang" --base-url "https://api.example.com"
	)
done

python3 - "$tmp_root/out" "$root" <<'PY'
import pathlib
import sys

out = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


def read(path):
    if not path.is_file():
        missing.append(f"{path} is missing")
        return ""
    return path.read_text(encoding="utf-8")


doc = read(root / "docs" / "reference" / "api-client-generation.md")
for marker in (
    "schema: gofly.api_client_generation.v1",
    "typescript",
    "javascript",
    "dart",
    "java",
    "kotlin",
    "make api-client-generation-check",
    "testdata/api-client-matrix/shop.api",
):
    require(marker in doc, f"api client generation doc missing {marker!r}")

ts = read(out / "typescript" / "shop_client.ts")
for marker in (
    "export interface CreateOrderRequest",
    "items?: LineItem[]",
    "async listOrders",
    "new URLSearchParams()",
    'for (const item of req.tags) query.append("tags", String(item));',
    'path.replace("{id}"',
    "https://api.example.com",
):
    require(marker in ts, f"typescript client missing {marker!r}")

js = read(out / "javascript" / "shop_client.js")
for marker in (
    "export class APIClient",
    "async createOrder",
    "async listOrders",
    "new URLSearchParams()",
    'for (const item of req.tags) query.append("tags", String(item));',
):
    require(marker in js, f"javascript client missing {marker!r}")

dart = read(out / "dart" / "shop_client.dart")
for marker in (
    "class LineItem",
    "final List<LineItem>? items;",
    "Future<ListOrdersResponse> listOrders",
    "final query = <String, List<String>>{};",
    'addQuery("tags", req.tags);',
):
    require(marker in dart, f"dart client missing {marker!r}")

java = read(out / "java" / "APIClient.java")
for marker in (
    "public static class LineItem",
    "public java.util.List<LineItem> items;",
    "public ListOrdersResponse listOrders",
    "StringBuilder query = new StringBuilder();",
    'appendQuery(query, "tags", req.tags);',
):
    require(marker in java, f"java client missing {marker!r}")

kotlin = read(out / "kotlin" / "APIClient.kt")
for marker in (
    "data class LineItem",
    "val items: List<LineItem>? = null",
    "fun listOrders",
    "val query = StringBuilder()",
    'appendQuery(query, "tags", req.tags)',
):
    require(marker in kotlin, f"kotlin client missing {marker!r}")

if missing:
    print("api client generation check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    raise SystemExit(1)

print("api client generation OK")
PY
