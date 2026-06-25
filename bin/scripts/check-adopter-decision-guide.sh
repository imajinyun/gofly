#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

missing = []

checks = {
    pathlib.Path("docs/explanation/adopter-decision-guide.md"): [
        "gofly.adopter_decision_guide.v1",
        "Migration path matrix",
        "When to choose gofly",
        "When to choose Gin",
        "When to keep Kitex",
        "How to migrate go-zero",
        "How to migrate Kratos",
        "Runnable migration case",
        "Compatibility caveat",
        "rollback note",
        "Gate command",
        "go test -C examples/migration-proof ./...",
        "go run -C examples/migration-proof .",
        "make examples-smoke",
        "make docs-check",
    ],
    pathlib.Path("docs/index.md"): [
        "explanation/adopter-decision-guide.md",
    ],
    pathlib.Path("README.md"): [
        "docs/explanation/adopter-decision-guide.md",
    ],
    pathlib.Path("docs/comparisons/gin.md"): [
        "rollback note",
        "examples/restserver",
    ],
    pathlib.Path("docs/comparisons/go-zero.md"): [
        "rollback note",
        "examples/production-orders",
    ],
    pathlib.Path("docs/comparisons/kratos.md"): [
        "rollback note",
        "examples/microshop",
    ],
    pathlib.Path("docs/comparisons/kitex.md"): [
        "rollback note",
        "examples/rpc-idl-matrix",
    ],
}

manual = pathlib.Path("docs/explanation/adopter-decision-guide.md")
if manual.is_file():
    text = manual.read_text(encoding="utf-8")
    migration_paths = {
        "Gin to gofly": [
            "examples/restserver",
            "Gin `:id` routes become gofly `{id}` routes",
            "go test -C examples/restserver ./...",
            "Keep the Gin route active",
        ],
        "go-zero to gofly": [
            "examples/production-orders",
            "Preserve `.api` request/response field names",
            "make generated-version-compat-check",
            "Keep the go-zero endpoint addressable",
        ],
        "Kratos to gofly": [
            "examples/microshop",
            "compare lifecycle hooks",
            "make cloud-native-render-check",
            "Restore the previous Kratos deployment target",
        ],
        "Kitex with gofly": [
            "examples/rpc-idl-matrix",
            "Do not migrate hot methods without `bench/` evidence",
            "make rpc-boundary-check",
            "Route latency-critical methods back to Kitex",
        ],
    }
    for name, terms in migration_paths.items():
        if name not in text:
            missing.append(f"{manual}: missing migration path {name!r}")
            continue
        for term in terms:
            if term not in text:
                missing.append(f"{manual}: migration path {name!r} missing {term!r}")

for path, needles in checks.items():
    if not path.is_file():
        missing.append(f"{path}: file is missing")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            missing.append(f"{path}: missing {needle!r}")

if missing:
    print("adopter decision guide check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("adopter decision guide governance ok")
PY
