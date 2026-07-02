#!/usr/bin/env sh
set -eu

chart_dir="deploy/helm/gofly"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

if command -v helm >/dev/null 2>&1; then
	helm template gofly "$chart_dir" >"$workdir/rendered.yaml"
else
	cat "$chart_dir"/templates/*.yaml >"$workdir/rendered.yaml"
fi

python3 - "$workdir/rendered.yaml" "$chart_dir" <<'PY'
import pathlib
import sys

rendered = pathlib.Path(sys.argv[1]).read_text(encoding='utf-8')
chart = pathlib.Path(sys.argv[2])
values = (chart / 'values.yaml').read_text(encoding='utf-8')

required_rendered = [
    'kind: Service',
    'kind: Deployment',
    'kind: ServiceMonitor',
    'kind: HorizontalPodAutoscaler',
    'kind: PodDisruptionBudget',
    'kind: NetworkPolicy',
    'livenessProbe:',
    'readinessProbe:',
    'startupProbe:',
    'prometheus.io/scrape',
]
missing = [term for term in required_rendered if term not in rendered]

required_values = [
    'serviceMonitor:',
    'autoscaling:',
    'podDisruptionBudget:',
    'networkPolicy:',
    'allowDNS:',
]
missing += [f'values:{term}' for term in required_values if term not in values]

if missing:
    print('helm template smoke failed:', file=sys.stderr)
    for term in missing:
        print(f'  missing {term}', file=sys.stderr)
    sys.exit(1)

print('helm template smoke ok')
PY
