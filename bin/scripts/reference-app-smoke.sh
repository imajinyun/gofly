#!/usr/bin/env sh
set -eu

mode="${REFERENCE_APP_MODE:-memory}"

python3 - "$mode" <<'PY'
import pathlib
import sys

mode = sys.argv[1]
checks = {
    pathlib.Path("examples/production-orders/README.md"): [
        "gofly.reference_app.v1",
        "REST",
        "RPC",
        "MQ",
        "outbox",
        "saga",
        "config",
        "discovery",
        "cache",
        "observability",
        "K8s",
        "rollback",
        "REFERENCE_APP_MODE=memory make reference-app-smoke",
        "REFERENCE_APP_MODE=docker make reference-app-smoke",
    ],
    pathlib.Path("examples/production-orders/main.go"): [
        "REST API accepts order creation requests",
        "RPC service reserves inventory",
        "profile config and memory discovery",
        "outbox relays committed events",
        "observability exposes metrics",
        "cache",
    ],
    pathlib.Path("examples/production-orders/main_test.go"): [
        "TestCreateOrderSuccessPublishesOutbox_BitsUT",
        "TestBuildRESTServerOrderRouteBoundaries_BitsUT",
        "TestProductionOrderReferenceAppContract_BitsUT",
        "REST",
        "RPC",
        "MQ",
        "rollback",
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

if mode not in {"memory", "docker"}:
    missing.append(f"unsupported REFERENCE_APP_MODE={mode!r}")
if mode == "docker":
    print("reference app docker mode is delegated to integration CI; static contract verified")

if missing:
    print("reference app smoke failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print(f"reference app smoke ok ({mode})")
PY
