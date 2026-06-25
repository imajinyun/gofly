#!/usr/bin/env sh
set -eu

mode="${REFERENCE_APP_MODE:-memory}"
compose_file="examples/production-orders/compose.yaml"

python3 - "$mode" <<'PY'
import json
import pathlib
import sys

mode = sys.argv[1]
root = pathlib.Path(".").resolve()
manifest_path = root / "docs" / "reference" / "reference-app-topology.json"
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
        "SQL outbox",
        "Redis cache",
        "Kafka",
        "RabbitMQ",
        "Redis Stream",
        "Consul",
        "etcd",
        "Nacos",
        "OpenTelemetry collector",
        "REFERENCE_APP_MODE=memory make reference-app-smoke",
        "REFERENCE_APP_MODE=docker make reference-app-smoke",
        "topology_evidence",
        "fallback note",
    ],
    pathlib.Path("examples/production-orders/main.go"): [
        "REST API accepts order creation requests",
        "RPC service reserves inventory",
        "profile config and memory discovery",
        "Docker-backed topology wires SQL outbox",
        "outbox relays committed events",
        "observability exposes metrics",
        "cache",
        "/topology",
        "topologyEvidence",
    ],
    pathlib.Path("examples/production-orders/main_test.go"): [
        "TestCreateOrderSuccessPublishesOutbox",
        "TestBuildRESTServerOrderRouteBoundaries",
        "TestProductionOrderReferenceAppContract",
        "TestProductionTopologyModes",
        "TestProductionTopologyEvidenceContract",
        "REST",
        "RPC",
        "MQ",
        "rollback",
    ],
    pathlib.Path("examples/production-orders/compose.yaml"): [
        "postgres",
        "redis",
        "kafka",
        "rabbitmq",
        "consul",
        "etcd",
        "nacos",
        "otel-collector",
        "REFERENCE_APP_MODE: docker",
    ],
    pathlib.Path("examples/production-orders/otel-collector.yaml"): [
        "receivers",
        "otlp",
        "debug",
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
    missing.append("docs/reference/reference-app-topology.json: file is missing")

require(manifest.get("schema") == "gofly.reference_app_topology.v1", "reference app topology schema mismatch")
require(manifest.get("status") == "blocking", "reference app topology status must be blocking")
require(manifest.get("referenceApp") == "examples/production-orders", "reference app topology must target examples/production-orders")
require(manifest.get("blockingGate") == "make reference-app-smoke", "reference app topology blocking gate must be make reference-app-smoke")

modes = manifest.get("modes") or []
mode_ids = {item.get("id") for item in modes if isinstance(item, dict)}
require({"memory", "docker"} <= mode_ids, f"reference app topology missing modes: {sorted({'memory', 'docker'} - mode_ids)!r}")

required_mode_components = {
    "memory": {"REST", "RPC", "MQ", "outbox", "saga", "config", "discovery", "cache", "observability", "K8s", "rollback"},
    "docker": {"SQL outbox", "Redis cache", "Kafka", "RabbitMQ", "Redis Stream", "Consul", "etcd", "Nacos", "OpenTelemetry collector"},
}
for item in modes:
    if not isinstance(item, dict):
        missing.append(f"reference app topology mode must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "gate", "components", "evidenceRefs"):
        require(item.get(field) not in ("", None, []), f"reference app topology mode {item_id}: {field} is required")
    require(required_mode_components.get(item_id, set()) <= set(item.get("components") or []), f"reference app topology mode {item_id}: missing components")
    for ref in item.get("evidenceRefs") or []:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        require(bool(ref_path), f"reference app topology mode {item_id}: ref path is required")
        require(bool(needles), f"reference app topology mode {item_id}: ref contains list is required for {ref_path}")
        if not ref_path:
            continue
        path = root / ref_path
        if not path.is_file():
            missing.append(f"reference app topology mode {item_id}: ref file missing: {ref_path}")
            continue
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            if needle not in text:
                missing.append(f"reference app topology mode {item_id}: {ref_path} missing {needle!r}")

surfaces = manifest.get("surfaces") or []
required_surfaces = {"rest", "rpc", "mq-outbox", "saga", "topology", "cloud-native"}
actual_surfaces = {item.get("id") for item in surfaces if isinstance(item, dict)}
require(required_surfaces <= actual_surfaces, f"reference app topology missing surfaces: {sorted(required_surfaces - actual_surfaces)!r}")
for item in surfaces:
    if not isinstance(item, dict):
        missing.append(f"reference app topology surface must be an object: {item!r}")
        continue
    item_id = item.get("id", "<missing>")
    for field in ("id", "name", "route", "gate", "evidenceRefs"):
        require(item.get(field) not in ("", None, []), f"reference app topology surface {item_id}: {field} is required")
    for ref in item.get("evidenceRefs") or []:
        ref_path = ref.get("path", "")
        needles = ref.get("contains") or []
        require(bool(ref_path), f"reference app topology surface {item_id}: ref path is required")
        require(bool(needles), f"reference app topology surface {item_id}: ref contains list is required for {ref_path}")
        if not ref_path:
            continue
        path = root / ref_path
        if not path.is_file():
            missing.append(f"reference app topology surface {item_id}: ref file missing: {ref_path}")
            continue
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            if needle not in text:
                missing.append(f"reference app topology surface {item_id}: {ref_path} missing {needle!r}")

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
if missing:
    print("reference app smoke failed:", file=sys.stderr)
    for item in missing:
        print("  " + item, file=sys.stderr)
    sys.exit(1)

print(f"reference app smoke ok ({mode})")
PY

if [ "$mode" = "memory" ]; then
	(cd examples/production-orders && go test -count=1 ./...)
	exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
	echo "reference app docker mode static topology verified; docker not found, skipping live compose smoke"
	exit 0
fi
if ! docker info >/dev/null 2>&1; then
	echo "reference app docker mode static topology verified; docker daemon unavailable, skipping live compose smoke"
	exit 0
fi

compose_log="$(mktemp)"
if ! docker compose -f "$compose_file" up -d --build production-orders >"$compose_log" 2>&1; then
	if grep -E 'docker-credential|credential helper|Pulling|pull access denied|network.*timeout|TLS handshake timeout' "$compose_log" >/dev/null 2>&1; then
		echo "reference app docker mode static topology verified; docker compose dependency pull failed, skipping live compose smoke"
		cat "$compose_log"
		exit 0
	fi
	cat "$compose_log"
	exit 1
fi
cleanup() {
	rm -f "$compose_log"
	docker compose -f "$compose_file" down -v --remove-orphans
}
trap cleanup EXIT INT TERM

PRODUCTION_ORDERS_URL="${PRODUCTION_ORDERS_URL:-http://127.0.0.1:18090}" \
PRODUCTION_ORDERS_ADMIN_URL="${PRODUCTION_ORDERS_ADMIN_URL:-http://127.0.0.1:18091}" \
	sh examples/production-orders/scripts/smoke.sh
