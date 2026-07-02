#!/usr/bin/env sh
set -eu

python3 - <<'PY'
import json
import pathlib
import sys

root = pathlib.Path('.').resolve()
middleware_manifest_path = root / 'docs' / 'reference' / 'http-middleware-ecosystem.json'
cloud_native_manifest_path = root / 'docs' / 'reference' / 'cloud-native-policy-conformance.json'

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
        'NetworkPolicy',
        'SPI registry',
        'compatible versions',
        'checksum',
        'source',
    ],
    pathlib.Path('examples/README.md'): [
        '`middlewares`',
        '`middleware-demo`',
        '`http-middleware`',
        '`migration-proof`',
        '`rpc-idl-matrix`',
        '`plugin-ecosystem`',
        'JWT, CORS, CSRF, sessions, OpenTelemetry, Prometheus, SSE, WebSocket and validation',
        'Gin, go-zero, Kratos and Kitex',
        'Proto/thrift fixtures, unary/server-streaming/client-streaming/bidirectional streaming',
        'SPI registry, code-generation plugin, post-generation patching and third-party template directory contract',
        'go -C examples/middlewares test ./...',
        'go -C examples/middleware-demo test ./...',
        'go -C examples/http-middleware run .',
        'go run -C examples/migration-proof .',
        'go run -C examples/rpc-idl-matrix .',
        'go run -C examples/plugin-ecosystem .',
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
        'TestRPCIDLMatrixReport',
        'server_stream',
        'client_stream',
        'bidi_stream',
        'weighted_round_robin',
        'consistent_hash',
        'health_aware',
    ],
    pathlib.Path('examples/plugin-ecosystem/registry/plugins.json'): [
        'audit-trail-generator',
        'company-template-pack',
        '"protocol": "1"',
        '"checksum": "sha256:',
        '"source": "https://github.com/example/',
        '"compatibleVersions": ["1"]',
        '"capabilities": ["generate:file", "generate:patch"]',
        '"permissions": ["filesystem:write-relative"]',
    ],
    pathlib.Path('examples/plugin-ecosystem/templates/service/gofly.template.json'): [
        'gofly.third_party_template_directory.v1',
        '"protocol": "1"',
        '"permissions": ["filesystem:write-relative"]',
        '"checksum": "sha256:',
        '"source": "https://github.com/example/gofly-company-template-pack"',
    ],
    pathlib.Path('examples/plugin-ecosystem/main.go'): [
        'gofly.plugin_ecosystem.v1',
        'registry/plugins.json',
        'templates/service/gofly.template.json',
        'generate:file',
        'generate:patch',
        'old-protocol',
        'future-plus-current',
        'filesystem:write-relative',
    ],
    pathlib.Path('examples/plugin-ecosystem/main_test.go'): [
        'TestPluginEcosystemReport',
        'checksum',
        'source',
        'example-file-generator',
        'example-patch-generator',
        'third-party-template-directory',
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
        'TestMiddlewareCatalogProductization',
        'catalog item is not fully productized',
        'JWT',
        'OpenTelemetry trace',
        'adaptive limit',
    ],
    pathlib.Path('examples/middleware-demo/main_test.go'): [
        'TestMiddlewareDemoCatalogAndOpenAPI',
        '/middleware/catalog',
        '/openapi.json',
    ],
    pathlib.Path('examples/http-middleware/main_test.go'): [
        'TestHTTPMiddlewareServerContracts',
        'JWT',
        'CSRF',
        'TestJWTAndSessionHelpers',
        'text/event-stream',
        'gofly_requests_total',
        '/openapi.json',
    ],
    pathlib.Path('examples/migration-proof/main.go'): [
        'gofly.migration_proof.v1',
        'examples/restserver',
        'examples/production-orders',
        'examples/microshop',
        'examples/rpc-idl-matrix',
        'rollback',
    ],
    pathlib.Path('examples/migration-proof/main_test.go'): [
        'TestMigrationProofReport',
        'gin',
        'go-zero',
        'kratos',
        'kitex',
    ],
    pathlib.Path('deploy/k8s/deployment.yaml'): [
        'kind: Service',
        'kind: Deployment',
        'livenessProbe:',
        'readinessProbe:',
        'startupProbe:',
    ],
    pathlib.Path('deploy/k8s/kustomization.yaml'): [
        'deployment.yaml',
        'servicemonitor.yaml',
        'hpa.yaml',
        'pdb.yaml',
        'networkpolicy.yaml',
    ],
    pathlib.Path('deploy/k8s/servicemonitor.yaml'): ['kind: ServiceMonitor', 'port: metrics'],
    pathlib.Path('deploy/k8s/hpa.yaml'): ['kind: HorizontalPodAutoscaler', 'averageUtilization'],
    pathlib.Path('deploy/k8s/pdb.yaml'): ['kind: PodDisruptionBudget', 'minAvailable'],
    pathlib.Path('deploy/k8s/networkpolicy.yaml'): ['kind: NetworkPolicy', 'policyTypes:', 'Ingress', 'Egress'],
    pathlib.Path('deploy/helm/gofly/Chart.yaml'): ['apiVersion: v2', 'name: gofly'],
    pathlib.Path('deploy/helm/gofly/values.yaml'): ['serviceMonitor:', 'autoscaling:', 'podDisruptionBudget:', 'networkPolicy:'],
    pathlib.Path('deploy/helm/gofly/templates/deployment.yaml'): ['livenessProbe:', 'readinessProbe:', 'startupProbe:'],
    pathlib.Path('deploy/helm/gofly/templates/service.yaml'): ['kind: Service', 'name: metrics'],
    pathlib.Path('deploy/helm/gofly/templates/servicemonitor.yaml'): ['kind: ServiceMonitor', '.Values.serviceMonitor.enabled'],
    pathlib.Path('deploy/helm/gofly/templates/hpa.yaml'): ['kind: HorizontalPodAutoscaler', '.Values.autoscaling.enabled'],
    pathlib.Path('deploy/helm/gofly/templates/pdb.yaml'): ['kind: PodDisruptionBudget', '.Values.podDisruptionBudget.enabled'],
    pathlib.Path('deploy/helm/gofly/templates/networkpolicy.yaml'): ['kind: NetworkPolicy', '.Values.networkPolicy.enabled', 'allowDNS'],
    pathlib.Path('bin/scripts/helm-template-smoke.sh'): [
        'helm template',
        'kind: NetworkPolicy',
        'helm template smoke ok',
    ],
    pathlib.Path('cmd/gofly/internal/generator/templates.go'): [
        'deploy/observability/logs-correlation.yaml',
        'Logs by trace_id',
        'log trace correlation is missing',
        'replace placeholder admin token before production',
        'ServiceMonitor',
    ],
    pathlib.Path('cmd/gofly/internal/generator/plugin.go'): [
        'Protocol    string',
        'Checksum    string',
        'Source      string',
        'validatePluginRegistryChecksum',
        'must use sha256:<hex> format',
    ],
    pathlib.Path('cmd/gofly/internal/generator/plugin_test.go'): [
        'TestPluginRegistryIndexValidationAndFiltering',
        'missing protocol',
        'invalid checksum',
        'missing source',
        'future',
    ],
    pathlib.Path('cmd/gofly/internal/generator/service_test.go'): [
        'assertGeneratedProductionCheckBehavior',
        'logs-correlation.yaml',
        'replace placeholder admin token',
        'hello production checklist passed',
    ],
    pathlib.Path('bin/scripts/examples-smoke.sh'): [
        'http-middleware.json',
        'gofly.http_middleware_matrix.v1',
        'invalidRequestStatus',
        'schemaOutput',
        'migration-proof.json',
        'gofly.migration_proof.v1',
        'examples/production-orders',
        'examples/rpc-idl-matrix',
        'rpc-idl-matrix.json',
        'gofly.rpc_idl_matrix.v1',
        'server_stream',
        'client_stream',
        'bidi_stream',
        'weighted_round_robin',
        'consistent_hash',
        'health_aware',
        'plugin-ecosystem.json',
        'gofly.plugin_ecosystem.v1',
        'audit-trail-generator',
        'third-party-template-directory',
    ],
    pathlib.Path('docs/guides/extensions.md'): [
        'checksum',
        'source',
        'Compatibility matrix',
        'examples/plugin-ecosystem',
        'third-party template directory',
    ],
}

missing = []


def require(condition, message):
    if not condition:
        missing.append(message)


if middleware_manifest_path.is_file():
    middleware_manifest = json.loads(middleware_manifest_path.read_text(encoding='utf-8'))
else:
    middleware_manifest = {}
    missing.append('docs/reference/http-middleware-ecosystem.json: file is missing')
if cloud_native_manifest_path.is_file():
    cloud_native_manifest = json.loads(cloud_native_manifest_path.read_text(encoding='utf-8'))
else:
    cloud_native_manifest = {}
    missing.append('docs/reference/cloud-native-policy-conformance.json: file is missing')

require(middleware_manifest.get('schema') == 'gofly.http_middleware_ecosystem.v1', 'HTTP middleware ecosystem schema mismatch')
require(middleware_manifest.get('status') == 'blocking', 'HTTP middleware ecosystem status must be blocking')
require(middleware_manifest.get('blockingGate') == 'make p1-growth-check', 'HTTP middleware ecosystem blocking gate must be make p1-growth-check')
require(middleware_manifest.get('smokeGate') == 'make examples-smoke', 'HTTP middleware ecosystem smoke gate must be make examples-smoke')
middleware_policy = middleware_manifest.get('migrationPolicy') or {}
require(set(middleware_policy.get('referenceFrameworks') or []) == {'Gin', 'go-zero'}, 'HTTP middleware migration policy must reference Gin and go-zero')
require(middleware_policy.get('openAPIVisibilityRequired') is True, 'HTTP middleware migration policy must require OpenAPI visibility')
require(middleware_policy.get('controlPlaneVisibilityRequired') is True, 'HTTP middleware migration policy must require control-plane visibility')
for field in ('adopterAction', 'rollbackOrEscalation'):
    require(len(str(middleware_policy.get(field) or '').split()) >= 10, f'HTTP middleware migration policy {field} must be actionable')

middleware_dx = middleware_manifest.get('migrationDX') or {}
ordering = middleware_dx.get('ordering') or []
for step in ('recover', 'request-id', 'trace', 'metrics', 'cors', 'session', 'csrf', 'jwt', 'validation', 'handler', 'sse-websocket-bounds'):
    require(step in ordering, f'HTTP middleware migrationDX.ordering missing {step}')
require(ordering.index('recover') < ordering.index('handler'), 'HTTP middleware migrationDX.ordering must run recover before handler')
require(ordering.index('cors') < ordering.index('csrf') < ordering.index('jwt') < ordering.index('validation') < ordering.index('handler'), 'HTTP middleware migrationDX.ordering must preserve browser/auth/validation order')
framework_mapping = middleware_dx.get('frameworkMapping') or {}
for framework in ('Gin', 'go-zero'):
    mapping = framework_mapping.get(framework) or {}
    for topic in ('auth', 'cors', 'csrf', 'session', 'observability', 'realtime'):
        require(topic in mapping and len(str(mapping.get(topic) or '').split()) >= 8, f'HTTP middleware migrationDX.frameworkMapping.{framework}.{topic} must be actionable')
for field, needles in {
    'failureModes': ('JWT', 'CORS', 'CSRF', 'Session', 'Prometheus', 'OpenTelemetry', 'SSE', 'WebSocket'),
    'productionDefaults': ('secret-manager', 'CORS', 'Secure', 'metrics', 'SSE', 'Gin or go-zero'),
    'smokeReferences': ('make p1-growth-check', 'make examples-smoke', 'make api-example-consistency-check', 'go -C examples/http-middleware test ./...', 'go -C examples/middlewares test ./...', 'go -C examples/http-middleware run . --describe'),
}.items():
    values = middleware_dx.get(field) or []
    joined = '\n'.join(values)
    for needle in needles:
        require(needle in joined, f'HTTP middleware migrationDX.{field} missing {needle!r}')

modules = set(middleware_manifest.get('exampleModules') or [])
for module in ('examples/middlewares', 'examples/middleware-demo', 'examples/http-middleware'):
    require(module in modules, f'HTTP middleware ecosystem missing example module {module}')

capabilities = middleware_manifest.get('capabilities') or []
required_capabilities = {'jwt', 'cors', 'csrf', 'session', 'prometheus', 'otel', 'sse', 'websocket'}
actual_capabilities = {item.get('id') for item in capabilities if isinstance(item, dict)}
require(required_capabilities <= actual_capabilities, f'HTTP middleware ecosystem missing capabilities: {sorted(required_capabilities - actual_capabilities)!r}')

for item in capabilities:
    if not isinstance(item, dict):
        missing.append(f'HTTP middleware capability must be an object: {item!r}')
        continue
    item_id = item.get('id', '<missing>')
    for field in ('id', 'name', 'category', 'ownerDocs', 'examples', 'smokeGates', 'evidenceRefs'):
        require(item.get(field) not in ('', None, []), f'HTTP middleware capability {item_id}: {field} is required')
    migration = item.get('migration') or {}
    require(set(migration.get('from') or []) & {'Gin auth middleware', 'Gin CORS middleware', 'Gin CSRF middleware', 'Gin session middleware', 'Gin Prometheus middleware', 'Gin OpenTelemetry middleware', 'Gin SSE handlers', 'Gin WebSocket handlers'}, f'HTTP middleware capability {item_id}: migration.from must include a Gin source')
    require(any('go-zero' in source for source in migration.get('from') or []), f'HTTP middleware capability {item_id}: migration.from must include a go-zero source')
    for field in ('adopterAction', 'rollbackOrEscalation'):
        require(len(str(migration.get(field) or '').split()) >= 8, f'HTTP middleware capability {item_id}: migration.{field} must be actionable')
    for doc in item.get('ownerDocs') or []:
        require((root / doc).is_file(), f'HTTP middleware capability {item_id}: owner doc missing: {doc}')
    for example in item.get('examples') or []:
        require((root / example).exists(), f'HTTP middleware capability {item_id}: example missing: {example}')
    for gate in item.get('smokeGates') or []:
        require('examples/' in gate, f'HTTP middleware capability {item_id}: smoke gate must reference examples: {gate}')
    for ref in item.get('evidenceRefs') or []:
        ref_path = ref.get('path', '')
        needles = ref.get('contains') or []
        require(bool(ref_path), f'HTTP middleware capability {item_id}: ref path is required')
        require(bool(needles), f'HTTP middleware capability {item_id}: ref contains list is required for {ref_path}')
        if not ref_path:
            continue
        path = root / ref_path
        if not path.is_file():
            missing.append(f'HTTP middleware capability {item_id}: ref file missing: {ref_path}')
            continue
        text = path.read_text(encoding='utf-8')
        for needle in needles:
            if needle not in text:
                missing.append(f'HTTP middleware capability {item_id}: {ref_path} missing {needle!r}')

p10_closeout = middleware_manifest.get('p10MiddlewareEcosystemCloseout') or {}
require(p10_closeout.get('schema') == 'gofly.http_middleware_p10_closeout.v1', 'HTTP middleware P10 closeout schema mismatch')
require(p10_closeout.get('aiflowTask') == 'GOFLY-P10-4-REST-MIDDLEWARE-ECOSYSTEM-MATRIX', 'HTTP middleware P10 closeout aiflowTask mismatch')
require(p10_closeout.get('status') == 'blocking-contract', 'HTTP middleware P10 closeout status must be blocking-contract')
require(p10_closeout.get('acceptanceGate') == 'make p1-growth-check', 'HTTP middleware P10 closeout acceptanceGate mismatch')
require(set(p10_closeout.get('requiredCapabilities') or []) == required_capabilities, 'HTTP middleware P10 requiredCapabilities mismatch')
require(set(p10_closeout.get('migrationSources') or []) == {'Gin', 'go-zero'}, 'HTTP middleware P10 migrationSources mismatch')
p10_rows = {
    item.get('id'): item
    for item in p10_closeout.get('closeoutRows') or []
    if isinstance(item, dict) and item.get('id')
}
expected_p10_rows = {
    'auth-browser-safety': {'jwt', 'cors', 'csrf', 'session'},
    'observability': {'prometheus', 'otel'},
    'realtime': {'sse', 'websocket'},
    'openapi-control-plane': required_capabilities,
}
require(set(p10_rows) == set(expected_p10_rows), f'HTTP middleware P10 rows mismatch: {sorted(p10_rows)!r}')
for row_id, expected_caps in expected_p10_rows.items():
    row = p10_rows.get(row_id) or {}
    for field in ('id', 'capabilities', 'smokeGate', 'migrationBoundary', 'rollbackOrEscalation'):
        require(row.get(field), f'HTTP middleware P10 row {row_id}: {field} is required')
    caps = set(row.get('capabilities') or [])
    require(caps == expected_caps, f'HTTP middleware P10 row {row_id}: capabilities mismatch')
    require(set(row.get('capabilities') or []) <= actual_capabilities, f'HTTP middleware P10 row {row_id}: unknown capability')
    require(len(str(row.get('migrationBoundary') or '').split()) >= 10, f'HTTP middleware P10 row {row_id}: migrationBoundary must be actionable')
    require(len(str(row.get('rollbackOrEscalation') or '').split()) >= 10, f'HTTP middleware P10 row {row_id}: rollbackOrEscalation must be actionable')
    gate = str(row.get('smokeGate') or '')
    require(gate.startswith('go -C examples/') or gate.startswith('make '), f'HTTP middleware P10 row {row_id}: smokeGate must be runnable')
policy_text = str(p10_closeout.get('promotionPolicy') or '')
for needle in ('auth', 'browser safety', 'observability', 'realtime', 'OpenAPI', 'control-plane', 'Gin', 'go-zero'):
    require(needle in policy_text, f'HTTP middleware P10 promotionPolicy missing {needle!r}')
runtime_policy = str(p10_closeout.get('runtimeArtifactPolicy') or '')
for needle in ('runtime evidence', 'durable evidence'):
    require(needle in runtime_policy, f'HTTP middleware P10 runtimeArtifactPolicy missing {needle!r}')

p10_cloud = cloud_native_manifest.get('p10CloudNativeAdoptionProof') or {}
require(p10_cloud.get('schema') == 'gofly.cloud_native_p10_adoption_proof.v1', 'cloud-native P10 adoption proof schema mismatch')
require(p10_cloud.get('aiflowTask') == 'GOFLY-P10-8-CLOUD-NATIVE-ADOPTION-PROOF', 'cloud-native P10 adoption proof aiflowTask mismatch')
require(p10_cloud.get('status') == 'blocking-contract', 'cloud-native P10 adoption proof status must be blocking-contract')
require(p10_cloud.get('acceptanceGate') == 'make p1-growth-check', 'cloud-native P10 adoption proof acceptanceGate mismatch')
require(p10_cloud.get('dashboardReportField') == 'cloudNativeAdoption.p10Proof', 'cloud-native P10 adoption proof dashboardReportField mismatch')
require(len(str(p10_cloud.get('policy') or '').split()) >= 20, 'cloud-native P10 adoption proof policy must be actionable')
required_cloud_gates = {
    'make helm-template-smoke',
    'make cloud-native-render-check',
    'make reference-app-smoke',
    'make runtime-slo-check',
    'make p1-growth-check',
}
require(set(p10_cloud.get('requiredGates') or []) == required_cloud_gates, 'cloud-native P10 adoption proof requiredGates mismatch')
cloud_chains = {
    item.get('id'): item
    for item in p10_cloud.get('proofChains') or []
    if isinstance(item, dict) and item.get('id')
}
expected_cloud_chains = {
    'render-proof': 'make cloud-native-render-check',
    'reference-topology-proof': 'make reference-app-smoke',
    'runtime-slo-proof': 'make runtime-slo-check',
    'rollback-proof': 'make governance-report-check',
}
require(set(cloud_chains) == set(expected_cloud_chains), f'cloud-native P10 proof chains mismatch: {sorted(cloud_chains)!r}')
for chain_id, gate in expected_cloud_chains.items():
    row = cloud_chains.get(chain_id) or {}
    for field in ('id', 'surface', 'gate', 'evidence', 'adopterAction', 'rollbackOrEscalation'):
        require(row.get(field), f'cloud-native P10 proof {chain_id}: {field} is required')
    require(row.get('gate') == gate, f'cloud-native P10 proof {chain_id}: gate mismatch')
    for evidence in row.get('evidence') or []:
        require((root / evidence).exists(), f'cloud-native P10 proof {chain_id}: evidence missing: {evidence}')
    for field in ('adopterAction', 'rollbackOrEscalation'):
        require(len(str(row.get(field) or '').split()) >= 10, f'cloud-native P10 proof {chain_id}: {field} must be actionable')
for needle in ('render proof', 'reference topology proof', 'runtime SLO proof', 'rollback proof', 'P1 growth gates'):
    require(needle in str(p10_cloud.get('promotionPolicy') or ''), f'cloud-native P10 proof promotionPolicy missing {needle!r}')
for needle in ('.tmp-test', '.aiflow', 'durable adoption proof', 'docs/reference'):
    require(needle in str(p10_cloud.get('runtimeEvidencePolicy') or ''), f'cloud-native P10 proof runtimeEvidencePolicy missing {needle!r}')

p11_cloud = cloud_native_manifest.get('p11HostedCloudNativeProof') or {}
require(p11_cloud.get('schema') == 'gofly.cloud_native_p11_hosted_proof.v1', 'cloud-native P11 hosted proof schema mismatch')
require(p11_cloud.get('aiflowTask') == 'GOFLY-P11-3-CLOUD-NATIVE-HOSTED-PROOF', 'cloud-native P11 hosted proof aiflowTask mismatch')
require(p11_cloud.get('status') == 'blocking-contract', 'cloud-native P11 hosted proof status must be blocking-contract')
require(p11_cloud.get('acceptanceGate') == 'make p1-growth-check', 'cloud-native P11 hosted proof acceptanceGate mismatch')
require(p11_cloud.get('dashboardReportField') == 'cloudNativeAdoption.p11HostedProof', 'cloud-native P11 hosted proof dashboardReportField mismatch')
require(len(str(p11_cloud.get('policy') or '').split()) >= 20, 'cloud-native P11 hosted proof policy must be actionable')
p11_env = p11_cloud.get('hostedEnvironment') or {}
for tool in ('Docker', 'Helm', 'Kustomize', 'kubeconform', 'kubeval', 'Trivy'):
    require(tool in set(p11_env.get('requiredWhenAvailable') or []), f'cloud-native P11 hosted proof requiredWhenAvailable missing {tool!r}')
for needle in ('fallbackReasons', 'release promotion', 'hosted Docker', 'Helm', 'Trivy'):
    require(needle in str(p11_env.get('fallbackPolicy') or ''), f'cloud-native P11 hosted proof fallbackPolicy missing {needle!r}')
for needle in ('.aiflow', '.tmp-test', 'must not be committed'):
    require(needle in str(p11_env.get('runtimeStatePolicy') or ''), f'cloud-native P11 hosted proof runtimeStatePolicy missing {needle!r}')
p11_rows = {
    item.get('id'): item
    for item in p11_cloud.get('proofRows') or []
    if isinstance(item, dict) and item.get('id')
}
expected_p11_rows = {
    'docker-reference-app': 'REFERENCE_APP_MODE=docker make reference-app-smoke',
    'helm-render': 'make helm-template-smoke && make cloud-native-render-check',
    'kustomize-policy': 'make cloud-native-render-check',
    'kube-schema-validation': 'make cloud-native-render-check',
    'release-security-evidence': 'make governance-report-check && make required-checks-drift-check',
    'operator-rollback': 'make governance-report-check',
}
require(set(p11_rows) == set(expected_p11_rows), f'cloud-native P11 hosted proof rows mismatch: {sorted(p11_rows)!r}')
for row_id, gate in expected_p11_rows.items():
    row = p11_rows.get(row_id) or {}
    for field in ('id', 'surface', 'hostedEvidence', 'localGate', 'sourceEvidence', 'fallbackPolicy', 'rollbackAction'):
        require(row.get(field), f'cloud-native P11 hosted proof {row_id}: {field} is required')
    require(row.get('localGate') == gate, f'cloud-native P11 hosted proof {row_id}: localGate mismatch')
    for evidence in row.get('sourceEvidence') or []:
        require((root / evidence).exists(), f'cloud-native P11 hosted proof {row_id}: evidence missing: {evidence}')
    for field in ('fallbackPolicy', 'rollbackAction'):
        require(len(str(row.get(field) or '').split()) >= 12, f'cloud-native P11 hosted proof {row_id}: {field} must be actionable')
for gate in (
    'make helm-template-smoke',
    'make cloud-native-render-check',
    'make reference-app-smoke',
    'make runtime-slo-check',
    'make governance-report-check',
    'make required-checks-drift-check',
    'make p1-growth-check',
):
    require(gate in set(p11_cloud.get('requiredGates') or []), f'cloud-native P11 hosted proof requiredGates missing {gate!r}')
p11_fallback = p11_cloud.get('fallbackReasonContract') or {}
require(p11_fallback.get('renderReport') == '.tmp-test/cloud-native-render/render-report.json', 'cloud-native P11 hosted proof fallbackReasonContract renderReport mismatch')
for field in ('fallbackReasons', 'helm.fallbackStatus', 'kustomize.fallbackStatus', 'kubeconform.schemaValidationStatus', 'kubeval.schemaValidationStatus'):
    require(field in set(p11_fallback.get('requiredFields') or []), f'cloud-native P11 hosted proof fallbackReasonContract requiredFields missing {field!r}')
require(len(str(p11_fallback.get('policy') or '').split()) >= 15, 'cloud-native P11 hosted proof fallbackReasonContract policy must be actionable')
for needle in ('Docker-backed reference topology', 'Helm rendering', 'Kustomize rendering', 'release security evidence', 'fallback reasons', 'operator rollback'):
    require(needle in str(p11_cloud.get('promotionPolicy') or ''), f'cloud-native P11 hosted proof promotionPolicy missing {needle!r}')

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
