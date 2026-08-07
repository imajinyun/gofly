#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import re
import sys

root = pathlib.Path(".").resolve()
missing = []


def read(path):
    if not path.is_file():
        missing.append(f"{path} is missing")
        return ""
    return path.read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        missing.append(message)


guide = read(root / "docs" / "reference" / "from-go-zero-migration.md")
matrix_path = root / "docs" / "reference" / "migration-fidelity-matrix.json"
matrix = json.loads(read(matrix_path)) if matrix_path.is_file() else {}
makefile = read(root / "Makefile")
targets = set(re.findall(r"^([A-Za-z0-9_-]+):", makefile, re.M))

require("migration-docs-check" in targets, "Makefile must expose migration-docs-check")
require("check-migration-docs.sh" in makefile, "Makefile must call check-migration-docs.sh")
require("schema: gofly.go_zero_migration_guide.v1" in guide, "go-zero migration guide schema marker is missing")

required_paths = (
    "docs/reference/goctl-generator-compatibility.json",
    "docs/reference/goctl-real-project-replay.json",
    "docs/reference/zrpc-proto-compatibility.json",
    "docs/reference/api-client-generation.md",
    "docs/reference/rest-middleware-profiles.md",
    "docs/reference/generated-service-layout.md",
    "docs/reference/generated-upgrade-dry-run.json",
)
for path in required_paths:
    require(path in guide, f"go-zero migration guide missing evidence reference {path!r}")
    require((root / path).exists(), f"evidence path missing: {path}")

for command in (
    "make goctl-generator-compat-check",
    "make goctl-real-project-replay-check",
    "make zrpc-proto-compatibility-check",
    "make api-client-generation-check",
    "make rest-profile-check",
    "make generated-service-layout-check",
    "make generated-upgrade-dry-run-check",
    "make migration-docs-check",
):
    require(command in guide, f"go-zero migration guide missing command {command!r}")
    target = command.removeprefix("make ").split()[0]
    require(target in targets, f"migration guide command target missing from Makefile: {target}")

for marker in (
    ".api",
    "handler/logic/svc/types",
    "ServiceContext",
    "zRPC",
    "external-proto-imports",
    "google-well-known-types",
    "degraded",
    "Rollback",
):
    require(marker in guide, f"go-zero migration guide missing marker {marker!r}")

require(matrix.get("schema") == "gofly.generated_migration_fidelity.v1", "migration fidelity matrix schema mismatch")
gozero = next((item for item in matrix.get("paths") or [] if item.get("framework") == "go-zero"), None)
require(gozero is not None, "migration fidelity matrix must contain go-zero path")
if gozero:
    docs = set(gozero.get("docs") or [])
    require("docs/reference/from-go-zero-migration.md" in docs, "go-zero migration path must reference docs/reference/from-go-zero-migration.md")
    require("docs/reference/goctl-generator-compatibility.json" in docs, "go-zero migration path must keep goctl compatibility evidence")
    require("make goctl-generator-compat-check" in set(gozero.get("smokeGates") or []), "go-zero migration path must keep goctl generator gate")
    require("make migration-docs-check" in set(gozero.get("smokeGates") or []), "go-zero migration path must include migration docs gate")

if missing:
    print("migration docs check failed:", file=sys.stderr)
    for item in missing:
        print(f"- {item}", file=sys.stderr)
    raise SystemExit(1)
print("migration docs OK")
PY
