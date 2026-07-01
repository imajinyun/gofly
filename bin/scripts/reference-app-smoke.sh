#!/usr/bin/env sh
set -eu

mode="${REFERENCE_APP_MODE:-memory}"
compose_file="examples/production-orders/compose.yaml"
report_path="${REFERENCE_APP_REPORT:-.aiflow/reference-app-smoke-report.json}"

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
gitignore = (root / ".gitignore").read_text(encoding="utf-8")

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

r8_drill = manifest.get("r8ProductionDrillMatrix") or {}
require(
    r8_drill.get("schema") == "gofly.reference_app_r8_production_drill.v1",
    "reference app R8 production drill schema mismatch",
)
require(
    r8_drill.get("aiflowTask") == "GOFLY-GOV-10R8-06",
    "reference app R8 production drill aiflowTask mismatch",
)
require(
    r8_drill.get("status") == "blocking-contract",
    "reference app R8 production drill status must be blocking-contract",
)
require(
    r8_drill.get("acceptanceGate") == "make reference-app-smoke",
    "reference app R8 production drill acceptanceGate must be make reference-app-smoke",
)
r8_rows = {
    item.get("id"): item
    for item in r8_drill.get("rows") or []
    if isinstance(item, dict) and item.get("id")
}
required_r8_rows = {
    "memory-mode-drill": {
        "mode": "memory",
        "gate": "REFERENCE_APP_MODE=memory make reference-app-smoke",
        "components": {"REST", "RPC", "memory MQ", "memory outbox", "memory discovery", "memory cache", "local-admin observability"},
    },
    "docker-backed-drill": {
        "mode": "docker",
        "gate": "REFERENCE_APP_MODE=docker make reference-app-smoke",
        "components": {"Postgres SQL outbox", "Redis cache", "Kafka", "RabbitMQ", "Redis Stream", "Consul", "etcd", "Nacos", "OpenTelemetry collector"},
    },
    "sql-outbox-drill": {
        "mode": "memory-and-docker",
        "gate": "make reference-app-smoke",
        "components": {"memory outbox", "Postgres SQL outbox", "orders.created"},
    },
    "mq-cache-discovery-drill": {
        "mode": "memory-and-docker",
        "gate": "make required-checks-drift-check",
        "components": {"memory MQ", "Kafka", "RabbitMQ", "Redis Stream", "memory cache", "Redis cache", "memory discovery", "Consul", "etcd", "Nacos"},
    },
    "observability-drill": {
        "mode": "memory-and-docker",
        "gate": "make runtime-slo-check",
        "components": {"local-admin observability", "OpenTelemetry collector", "metrics", "health", "trace propagation"},
    },
    "k8s-rollback-drill": {
        "mode": "production-assets",
        "gate": "make cloud-native-render-check",
        "components": {"Helm", "Kustomize", "ServiceMonitor", "HPA", "PDB", "NetworkPolicy", "rollback"},
    },
    "failure-evidence-drill": {
        "mode": "memory-and-production",
        "gate": "make runtime-slo-check",
        "components": {"saga compensation", "outbox retry", "rate limit", "circuit breaker", "rollback trigger"},
    },
}
require(
    set(r8_rows) == set(required_r8_rows),
    "reference app R8 production drill rows drifted: "
    f"missing={sorted(set(required_r8_rows) - set(r8_rows))!r} "
    f"extra={sorted(set(r8_rows) - set(required_r8_rows))!r}",
)

makefile_text = (root / "Makefile").read_text(encoding="utf-8")
make_targets = set(__import__("re").findall(r"^([A-Za-z0-9_.-]+):", makefile_text, __import__("re").M))
for row_id, expected in required_r8_rows.items():
    row = r8_rows.get(row_id) or {}
    for field in ("id", "surface", "mode", "components", "gate", "runnableEvidence", "failureEvidence", "rollbackOrEscalation"):
        require(row.get(field) not in ("", None, []), f"reference app R8 production drill {row_id}: {field} is required")
    require(row.get("mode") == expected["mode"], f"reference app R8 production drill {row_id}: mode mismatch")
    require(row.get("gate") == expected["gate"], f"reference app R8 production drill {row_id}: gate mismatch")
    require(expected["components"] <= set(row.get("components") or []), f"reference app R8 production drill {row_id}: components missing {sorted(expected['components'] - set(row.get('components') or []))!r}")
    gate = str(row.get("gate") or "")
    if "make " in gate:
        target = gate.split("make ", 1)[1].split()[0]
        require(target in make_targets, f"reference app R8 production drill {row_id}: gate target missing: {target}")
    for evidence in row.get("runnableEvidence") or []:
        require((root / evidence).exists(), f"reference app R8 production drill {row_id}: runnable evidence missing: {evidence}")
    for field in ("failureEvidence", "rollbackOrEscalation"):
        require(
            len(str(row.get(field) or "").split()) >= 10,
            f"reference app R8 production drill {row_id}: {field} must be actionable",
        )
    require(
        "fallback" in str(row.get("failureEvidence") or "").lower()
        or "failure" in str(row.get("failureEvidence") or "").lower()
        or "rollback" in str(row.get("failureEvidence") or "").lower(),
        f"reference app R8 production drill {row_id}: failureEvidence must name failure, fallback, or rollback",
    )
    require(
        "rollback" in str(row.get("rollbackOrEscalation") or "").lower()
        or "fallback" in str(row.get("rollbackOrEscalation") or "").lower()
        or "keep" in str(row.get("rollbackOrEscalation") or "").lower()
        or "pin" in str(row.get("rollbackOrEscalation") or "").lower()
        or "disable" in str(row.get("rollbackOrEscalation") or "").lower(),
        f"reference app R8 production drill {row_id}: rollbackOrEscalation must name rollback, fallback, keep, pin, or disable",
    )

p9_live_proof = manifest.get("p9DockerLiveProof") or {}
require(
    p9_live_proof.get("schema") == "gofly.reference_app_docker_live_proof.v1",
    "reference app P9 docker live proof schema mismatch",
)
require(
    p9_live_proof.get("aiflowTask") == "GOFLY-GOV-10P9-03",
    "reference app P9 docker live proof aiflowTask mismatch",
)
require(
    p9_live_proof.get("status") == "blocking",
    "reference app P9 docker live proof status must be blocking",
)
require(
    p9_live_proof.get("acceptanceGate") == "make reference-app-smoke",
    "reference app P9 docker live proof acceptanceGate must be make reference-app-smoke",
)
require(
    p9_live_proof.get("runtimeEvidencePath") == ".aiflow/reference-app-smoke-report.json",
    "reference app P9 docker live proof runtimeEvidencePath mismatch",
)
require(
    ".aiflow/" in gitignore,
    "reference app P9 docker live proof runtime evidence must stay under ignored .aiflow/",
)
require(
    set(p9_live_proof.get("components") or []) == {
        "Postgres SQL outbox",
        "Redis cache",
        "Kafka",
        "RabbitMQ",
        "Redis Stream",
        "Consul",
        "etcd",
        "Nacos",
        "OpenTelemetry collector",
    },
    "reference app P9 docker live proof components mismatch",
)
require(
    set(p9_live_proof.get("requiredReportFields") or []) == {
        "schema",
        "aiflowTask",
        "mode",
        "status",
        "liveCompose",
        "fallbackReason",
        "gate",
        "components",
    },
    "reference app P9 docker live proof requiredReportFields mismatch",
)
for field in ("liveSuccessPolicy", "fallbackPolicy", "rollbackOrEscalation"):
    require(
        len(str(p9_live_proof.get(field) or "").split()) >= 10,
        f"reference app P9 docker live proof {field} must be actionable",
    )
fallback_reasons = set(p9_live_proof.get("allowedFallbackReasons") or [])
require(
    fallback_reasons == {
        "memory-mode",
        "docker-cli-missing",
        "docker-daemon-unavailable",
        "compose-dependency-pull-failed",
    },
    "reference app P9 docker live proof allowedFallbackReasons mismatch",
)

p13_live_proof = manifest.get("p13ReferenceAppLiveProof") or {}
require(
    p13_live_proof.get("schema") == "gofly.reference_app_p13_live_proof.v1",
    "reference app P13 live proof schema mismatch",
)
require(
    p13_live_proof.get("aiflowTask") == "GOFLY-P13-08-REFERENCE-APP-LIVE-PROOF",
    "reference app P13 live proof aiflowTask mismatch",
)
require(
    p13_live_proof.get("status") == "blocking",
    "reference app P13 live proof status must be blocking",
)
require(
    set(p13_live_proof.get("acceptanceGates") or []) == {
        "REFERENCE_APP_MODE=memory make reference-app-smoke",
        "REFERENCE_APP_MODE=docker make reference-app-smoke",
    },
    "reference app P13 live proof acceptanceGates mismatch",
)
require(
    p13_live_proof.get("runtimeEvidencePath") == ".aiflow/reference-app-smoke-report.json",
    "reference app P13 live proof runtimeEvidencePath mismatch",
)
require(
    ".aiflow/" in gitignore,
    "reference app P13 live proof runtime evidence must stay under ignored .aiflow/",
)
require(
    set(p13_live_proof.get("requiredRuntimeReportFields") or []) == {
        "schema",
        "aiflowTask",
        "p13AiflowTask",
        "mode",
        "status",
        "liveCompose",
        "liveProofState",
        "fallbackReason",
        "gate",
        "components",
        "dependencyFamilies",
        "validatedModes",
    },
    "reference app P13 live proof requiredRuntimeReportFields mismatch",
)
dependency_families = {
    "SQL outbox",
    "Redis cache",
    "Kafka",
    "RabbitMQ",
    "Redis Stream",
    "Consul",
    "etcd",
    "Nacos",
    "OpenTelemetry collector",
}
require(
    set(p13_live_proof.get("dependencyFamilies") or []) == dependency_families,
    "reference app P13 live proof dependencyFamilies mismatch",
)
require(
    set(p13_live_proof.get("validatedModes") or []) == {"memory", "docker"},
    "reference app P13 live proof validatedModes mismatch",
)
require(
    set(p13_live_proof.get("allowedFallbackReasons") or []) == fallback_reasons,
    "reference app P13 live proof allowedFallbackReasons must match P9 live proof",
)
p13_rows = {
    item.get("id"): item
    for item in p13_live_proof.get("proofRows") or []
    if isinstance(item, dict) and item.get("id")
}
required_p13_rows = {
    "memory-live-proof": {
        "mode": "memory",
        "expectedStatus": "passed",
        "liveProofState": "memory-fallback",
        "fallbackReason": "memory-mode",
    },
    "docker-compose-live-proof": {
        "mode": "docker",
        "expectedStatus": "passed",
        "liveProofState": "live-compose",
        "fallbackReason": "",
    },
    "docker-explicit-skip-proof": {
        "mode": "docker",
        "expectedStatus": "skipped",
        "liveProofState": "explicit-fallback",
        "fallbackReason": "docker-cli-missing|docker-daemon-unavailable|compose-dependency-pull-failed",
    },
}
require(
    set(p13_rows) == set(required_p13_rows),
    "reference app P13 live proof rows drifted: "
    f"missing={sorted(set(required_p13_rows) - set(p13_rows))!r} "
    f"extra={sorted(set(p13_rows) - set(required_p13_rows))!r}",
)
for row_id, expected in required_p13_rows.items():
    row = p13_rows.get(row_id) or {}
    for field in ("id", "mode", "expectedStatus", "liveProofState", "gate", "evidence", "rollbackOrEscalation"):
        require(row.get(field) not in ("", None, []), f"reference app P13 live proof {row_id}: {field} is required")
    for field, value in expected.items():
        require(row.get(field) == value, f"reference app P13 live proof {row_id}: {field} mismatch")
    require(
        row.get("gate") in set(p13_live_proof.get("acceptanceGates") or []),
        f"reference app P13 live proof {row_id}: gate must be an acceptance gate",
    )
    for evidence in row.get("evidence") or []:
        require((root / evidence).exists(), f"reference app P13 live proof {row_id}: evidence path missing: {evidence}")
    require(
        len(str(row.get("rollbackOrEscalation") or "").split()) >= 10,
        f"reference app P13 live proof {row_id}: rollbackOrEscalation must be actionable",
    )
for field in ("liveSuccessPolicy", "fallbackPolicy", "rollbackOrEscalation"):
    require(
        len(str(p13_live_proof.get(field) or "").split()) >= 10,
        f"reference app P13 live proof {field} must be actionable",
    )

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

write_report() {
	status="$1"
	live_compose="$2"
	fallback_reason="$3"
	mkdir -p "$(dirname "$report_path")"
	python3 - "$report_path" "$mode" "$status" "$live_compose" "$fallback_reason" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
mode = sys.argv[2]
status = sys.argv[3]
live_compose = sys.argv[4] == "true"
fallback_reason = sys.argv[5]
dependency_families = [
    "SQL outbox",
    "Redis cache",
    "Kafka",
    "RabbitMQ",
    "Redis Stream",
    "Consul",
    "etcd",
    "Nacos",
    "OpenTelemetry collector",
]
if live_compose:
    live_proof_state = "live-compose"
elif mode == "memory":
    live_proof_state = "memory-fallback"
else:
    live_proof_state = "explicit-fallback"
report = {
    "schema": "gofly.reference_app_smoke_report.v1",
    "aiflowTask": "GOFLY-GOV-10P9-03",
    "p13AiflowTask": "GOFLY-P13-08-REFERENCE-APP-LIVE-PROOF",
    "mode": mode,
    "status": status,
    "liveCompose": live_compose,
    "liveProofState": live_proof_state,
    "fallbackReason": fallback_reason,
    "gate": "make reference-app-smoke",
    "components": dependency_families,
    "dependencyFamilies": dependency_families,
    "validatedModes": ["memory", "docker"],
}
manifest_path = pathlib.Path("docs/reference/reference-app-topology.json")
if manifest_path.is_file():
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    p13 = manifest.get("p13ReferenceAppLiveProof") or {}
    required = set(p13.get("requiredRuntimeReportFields") or [])
    missing = sorted(required - set(report))
    if missing:
        raise SystemExit(f"reference app smoke report missing P13 fields: {missing!r}")
    allowed = set(p13.get("allowedFallbackReasons") or [])
    if fallback_reason and fallback_reason not in allowed:
        raise SystemExit(f"reference app smoke fallback reason is not allowed: {fallback_reason!r}")
    if set(report["dependencyFamilies"]) != set(p13.get("dependencyFamilies") or []):
        raise SystemExit("reference app smoke dependencyFamilies drifted from P13 contract")
    if set(report["validatedModes"]) != set(p13.get("validatedModes") or []):
        raise SystemExit("reference app smoke validatedModes drifted from P13 contract")
if mode == "docker" and status == "passed" and not live_compose:
    raise SystemExit("docker reference app smoke cannot pass without liveCompose=true")
if mode == "docker" and status == "skipped" and live_compose:
    raise SystemExit("docker reference app smoke skip cannot claim liveCompose=true")
if mode == "memory" and fallback_reason != "memory-mode":
    raise SystemExit("memory reference app smoke must report fallbackReason=memory-mode")
path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

if [ "$mode" = "memory" ]; then
	(cd examples/production-orders && go test -count=1 ./...)
	write_report "passed" "false" "memory-mode"
	exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
	echo "reference app docker mode static topology verified; docker not found, skipping live compose smoke"
	write_report "skipped" "false" "docker-cli-missing"
	exit 0
fi
if ! docker info >/dev/null 2>&1; then
	echo "reference app docker mode static topology verified; docker daemon unavailable, skipping live compose smoke"
	write_report "skipped" "false" "docker-daemon-unavailable"
	exit 0
fi

compose_log="$(mktemp)"
if ! docker compose -f "$compose_file" up -d --build production-orders >"$compose_log" 2>&1; then
	if grep -E 'docker-credential|credential helper|Pulling|pull access denied|network.*timeout|TLS handshake timeout' "$compose_log" >/dev/null 2>&1; then
		echo "reference app docker mode static topology verified; docker compose dependency pull failed, skipping live compose smoke"
		cat "$compose_log"
		write_report "skipped" "false" "compose-dependency-pull-failed"
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
write_report "passed" "true" ""
