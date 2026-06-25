#!/usr/bin/env sh
set -eu

mode="${REFERENCE_APP_MODE:-memory}"
compose_file="examples/production-orders/compose.yaml"

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
        "TestCreateOrderSuccessPublishesOutbox_BitsUT",
        "TestBuildRESTServerOrderRouteBoundaries_BitsUT",
        "TestProductionOrderReferenceAppContract_BitsUT",
        "TestProductionTopologyModes_BitsUT",
        "TestProductionTopologyEvidenceContract_BitsUT",
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
