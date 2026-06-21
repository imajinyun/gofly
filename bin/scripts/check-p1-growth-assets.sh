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
