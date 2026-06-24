#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import pathlib
import sys

root = pathlib.Path('.').resolve()

checks = {
    pathlib.Path('docs/reference/p1-growth-roadmap.md'): [
        'HTTP middleware ecosystem',
        'Binding and validation experience',
        'CLI DX',
        'RPC IDL ecosystem',
        'Cloud-native deployment assets',
        'Plugin ecosystem',
        'JWT',
        'CORS',
        'CSRF',
        'sessions',
        'OpenTelemetry',
        'Prometheus',
        'SSE',
        'WebSocket',
        'validator adapter',
        'OpenAPI schema',
        'proto and thrift',
        'streaming',
        'interceptor',
        'resolver',
        'load-balancing',
        'Helm chart',
        'Kustomize',
        'ServiceMonitor',
        'HPA',
        'PodDisruptionBudget',
        'SPI registry',
        'compatible versions',
    ],
    pathlib.Path('examples/README.md'): [
        '`middlewares`',
        '`middleware-demo`',
        '`http-middleware`',
        '`rpc-idl-matrix`',
        'JWT, CORS, CSRF, sessions, OpenTelemetry, Prometheus, SSE, WebSocket and validation',
        'Proto/thrift fixtures, unary/server-streaming/client-streaming/bidirectional streaming',
        'go test -C examples/middlewares ./...',
        'go test -C examples/middleware-demo ./...',
        'go run -C examples/http-middleware .',
        'go run -C examples/rpc-idl-matrix .',
    ],
    pathlib.Path('examples/rpc-idl-matrix/contracts/greeter.proto'): [
        'service MatrixGreeter',
        'rpc SayHello(HelloRequest) returns (HelloResponse);',
        'rpc WatchHello(HelloRequest) returns (stream HelloResponse);',
        'rpc CollectHello(stream HelloRequest) returns (HelloResponse);',
        'rpc Chat(stream ChatMessage) returns (stream ChatMessage);',
    ],
    pathlib.Path('examples/rpc-idl-matrix/contracts/greeter.thrift'): [
        'service MatrixGreeter',
        'HelloResponse SayHello',
        'HelloResponse CollectHello',
    ],
    pathlib.Path('examples/rpc-idl-matrix/main.go'): [
        'gofly.rpc_idl_matrix.v1',
        'contracts/greeter.proto',
        'contracts/greeter.thrift',
        'StreamModeServerStream',
        'StreamModeClientStream',
        'StreamModeBidiStream',
        'RecoverMiddleware',
        'TraceMiddleware',
        'LoggingMiddleware',
        'TimeoutMiddleware',
        'WithRetryPolicy',
        'WithBreaker',
        'validationMiddleware',
        'NewWeightedRoundRobinBalancer',
        'NewP2CBalancer',
        'NewConsistentHashBalancer',
        'NewHealthBalancer',
    ],
    pathlib.Path('examples/rpc-idl-matrix/main_test.go'): [
        'TestRPCIDLMatrixReport_BitsUT',
        'server_stream',
        'client_stream',
        'bidi_stream',
        'weighted_round_robin',
        'consistent_hash',
        'health_aware',
    ],
    pathlib.Path('examples/middlewares/catalog.go'): [
        'MiddlewareCatalog',
        'JWT',
        'CORS',
        'CSRF',
        'Prometheus metrics',
        'OpenTelemetry trace',
        'OpenAPIExpose',
        '/middleware/catalog',
    ],
    pathlib.Path('examples/middlewares/matrix_test.go'): [
        'TestMiddlewareCatalogProductization_BitsUT',
        'catalog item is not fully productized',
        'JWT',
        'OpenTelemetry trace',
        'adaptive limit',
    ],
    pathlib.Path('examples/middleware-demo/main_test.go'): [
        'TestMiddlewareDemoCatalogAndOpenAPI_BitsUT',
        '/middleware/catalog',
        '/openapi.json',
    ],
    pathlib.Path('examples/http-middleware/main_test.go'): [
        'TestHTTPMiddlewareServerContracts_BitsUT',
        'JWT',
        'CSRF',
        'text/event-stream',
        'gofly_requests_total',
        '/openapi.json',
    ],
    pathlib.Path('k8s/deployment.yaml'): [
        'kind: Service',
        'kind: Deployment',
        'livenessProbe:',
        'readinessProbe:',
        'startupProbe:',
    ],
    pathlib.Path('k8s/kustomization.yaml'): [
        'deployment.yaml',
        'servicemonitor.yaml',
        'hpa.yaml',
        'pdb.yaml',
    ],
    pathlib.Path('k8s/servicemonitor.yaml'): ['kind: ServiceMonitor', 'port: metrics'],
    pathlib.Path('k8s/hpa.yaml'): ['kind: HorizontalPodAutoscaler', 'averageUtilization'],
    pathlib.Path('k8s/pdb.yaml'): ['kind: PodDisruptionBudget', 'minAvailable'],
    pathlib.Path('charts/gofly/Chart.yaml'): ['apiVersion: v2', 'name: gofly'],
    pathlib.Path('charts/gofly/values.yaml'): ['serviceMonitor:', 'autoscaling:', 'podDisruptionBudget:'],
    pathlib.Path('charts/gofly/templates/deployment.yaml'): ['livenessProbe:', 'readinessProbe:', 'startupProbe:'],
    pathlib.Path('charts/gofly/templates/service.yaml'): ['kind: Service', 'name: metrics'],
    pathlib.Path('charts/gofly/templates/servicemonitor.yaml'): ['kind: ServiceMonitor', '.Values.serviceMonitor.enabled'],
    pathlib.Path('charts/gofly/templates/hpa.yaml'): ['kind: HorizontalPodAutoscaler', '.Values.autoscaling.enabled'],
    pathlib.Path('charts/gofly/templates/pdb.yaml'): ['kind: PodDisruptionBudget', '.Values.podDisruptionBudget.enabled'],
    pathlib.Path('cmd/gofly/internal/generator/templates.go'): [
        'deploy/observability/logs-correlation.yaml',
        'Logs by trace_id',
        'log trace correlation is missing',
        'replace placeholder admin token before production',
        'ServiceMonitor',
    ],
    pathlib.Path('cmd/gofly/internal/generator/service_test.go'): [
        'assertGeneratedProductionCheckBehavior',
        'logs-correlation.yaml',
        'replace placeholder admin token',
        'hello production checklist passed',
    ],
    pathlib.Path('bin/scripts/examples-smoke.sh'): [
        'rpc-idl-matrix.json',
        'gofly.rpc_idl_matrix.v1',
        'server_stream',
        'client_stream',
        'bidi_stream',
        'weighted_round_robin',
        'consistent_hash',
        'health_aware',
    ],
}

missing = []
for rel, terms in checks.items():
    path = root / rel
    if not path.is_file():
        missing.append(f'missing P1 asset: {rel}')
        continue
    text = path.read_text(encoding='utf-8')
    for term in terms:
        if term not in text:
            missing.append(f'{rel} missing required term: {term}')

if missing:
    print('P1 growth asset check failed:', file=sys.stderr)
    for item in missing:
        print(f'  {item}', file=sys.stderr)
    sys.exit(1)

print('P1 growth assets ok')
PY
