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

makefile_text = (root / "Makefile").read_text(encoding="utf-8")
make_targets = set(__import__("re").findall(r"^([A-Za-z0-9_.-]+):", makefile_text, __import__("re").M))
adopter_proof = manifest.get("adopterProof") or {}
require(
    adopter_proof.get("schema") == "gofly.reference_app_adopter_proof.v1",
    "reference app adopter proof schema mismatch",
)
require(
    adopter_proof.get("status") == "blocking",
    "reference app adopter proof status must be blocking",
)
require(
    adopter_proof.get("blockingGate") == "make reference-app-smoke",
    "reference app adopter proof blocking gate must be make reference-app-smoke",
)
for source in adopter_proof.get("sourceEvidence") or []:
    require((root / source).exists(), f"reference app adopter proof source evidence missing: {source}")
proof_chains = adopter_proof.get("proofChains")
if not isinstance(proof_chains, list):
    missing.append("reference app adopter proof proofChains must be a list")
    proof_chains = []
required_proof_chains = {
    "mode-proof",
    "dependency-topology-proof",
    "runtime-slo-proof",
    "cloud-native-rollout-proof",
    "incident-drill-proof",
    "rollback-proof",
}
actual_proof_chains = {item.get("id") for item in proof_chains if isinstance(item, dict)}
require(
    actual_proof_chains == required_proof_chains,
    "reference app adopter proof chains drifted: "
    f"missing={sorted(required_proof_chains - actual_proof_chains)!r} "
    f"extra={sorted(actual_proof_chains - required_proof_chains)!r}",
)
for item in proof_chains:
    if not isinstance(item, dict):
        missing.append(f"reference app adopter proof chain must be an object: {item!r}")
        continue
    chain_id = item.get("id", "<missing>")
    for field in ("id", "surface", "gate", "evidence", "adopterAction", "rollbackOrEscalation"):
        require(item.get(field) not in ("", None, []), f"reference app adopter proof {chain_id}: {field} is required")
    gate = str(item.get("gate") or "")
    require(gate.startswith("make "), f"reference app adopter proof {chain_id}: gate must be a make target")
    if gate.startswith("make "):
        gate_target = gate.split()[1]
        require(gate_target in make_targets, f"reference app adopter proof {chain_id}: gate target missing: {gate_target}")
    for evidence in item.get("evidence") or []:
        require((root / evidence).exists(), f"reference app adopter proof {chain_id}: evidence path missing: {evidence}")
    for field in ("adopterAction", "rollbackOrEscalation"):
        require(
            len(str(item.get(field) or "").split()) >= 10,
            f"reference app adopter proof {chain_id}: {field} must be actionable",
        )

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
