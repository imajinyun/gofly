#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

checks = {
    pathlib.Path("docs/explanation/adopter-decision-guide.md"): [
        "gofly.adopter_decision_guide.v1",
        "When to choose gofly",
        "When to choose Gin",
        "When to keep Kitex",
        "How to migrate go-zero",
        "How to migrate Kratos",
        "runnable example",
        "rollback note",
        "gate command",
        "make docs-check",
    ],
    pathlib.Path("docs/index.md"): [
        "explanation/adopter-decision-guide.md",
    ],
    pathlib.Path("README.md"): [
        "docs/explanation/adopter-decision-guide.md",
    ],
    pathlib.Path("docs/comparisons/gin.md"): [
        "rollback",
        "examples/restserver",
    ],
    pathlib.Path("docs/comparisons/go-zero.md"): [
        "rollback",
        "examples/production-orders",
    ],
    pathlib.Path("docs/comparisons/kratos.md"): [
        "rollback",
        "examples/microshop",
    ],
    pathlib.Path("docs/comparisons/kitex.md"): [
        "rollback",
        "examples/rpc-idl-matrix",
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
    print("adopter decision guide check failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print("adopter decision guide governance ok")
PY
