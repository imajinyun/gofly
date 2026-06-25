# Cloud-Native Rendering

Schema: `gofly.cloud_native_rendering.v1`

Cloud-native assets are release evidence, not decorative manifests. Helm schema,
values profiles, Kustomize overlays, NetworkPolicy, HPA, PDB, and
ServiceMonitor resources must render or pass static fallback checks before a
release.

## Assets

| Asset | Path | Purpose |
| --- | --- | --- |
| Helm schema | `charts/gofly/values.schema.json` | Validate chart values shape. |
| production values profiles | `charts/gofly/values-production.yaml` | Enable production HPA, PDB, ServiceMonitor, and NetworkPolicy. |
| Kustomize overlays | `k8s/overlays/production/kustomization.yaml` | Direct YAML production profile. |
| render golden | `make cloud-native-render-check` | Run `helm template` when available and static fallback otherwise. |

When `helm` is installed, `make cloud-native-render-check` renders the chart.
When `kubeconform` or `kubeval` is installed, CI may add schema validation on
top of the rendered output. Without those tools, the static fallback still
checks the required resource kinds.
