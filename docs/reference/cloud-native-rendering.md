# Cloud-Native Rendering

Schema: `gofly.cloud_native_rendering.v1`

Cloud-native assets are release evidence, not decorative manifests. Helm schema,
values profiles, Kustomize overlays, NetworkPolicy, HPA, PDB, and
ServiceMonitor resources must render or pass static fallback checks before a
release.

Machine-readable policy conformance evidence lives in
[`cloud-native-policy-conformance.json`](cloud-native-policy-conformance.json)
and is validated by the same render gate.

## Assets

| Asset | Path | Purpose |
| --- | --- | --- |
| Helm schema | `charts/gofly/values.schema.json` | Validate chart values shape. |
| production values profiles | `charts/gofly/values-production.yaml` | Enable production HPA, PDB, ServiceMonitor, and NetworkPolicy. |
| Kustomize overlays | `k8s/overlays/production/kustomization.yaml` | Direct YAML production profile. |
| production rendered golden | `docs/reference/cloud-native-rendered-production.golden.yaml` | Static production render evidence when Helm or Kustomize is unavailable. |
| render golden | `make cloud-native-render-check` | Run `helm template` when available and static fallback otherwise. |

When `helm` is installed, `make cloud-native-render-check` renders the chart.
When `kubeconform` or `kubeval` is installed, CI may add schema validation on
top of the rendered output. Without those tools, the static fallback still
checks the required resource kinds.

Tool availability must be explicit in release evidence. Helm and Kustomize are
required when present, while `kubeconform` and `kubeval` remain optional CI
schema validators. If a tool is missing, the conformance manifest must record
the fallback status instead of silently accepting an unrendered profile.

## Policy Conformance

The conformance manifest keeps the release evidence explicit:

- render modes: `helm-template` and `static-template-render`;
- tool availability policy: `helm`, `kustomize`, `kubeconform`, and `kubeval`;
- optional schema validation tools: `kubeconform` and `kubeval`;
- profiles: `helm-default`, `helm-production`, and `kustomize-production`;
- rendered goldens: `docs/reference/cloud-native-rendered-production.golden.yaml`
  with explicit fallback status;
- policy resources: `ServiceMonitor`, `HorizontalPodAutoscaler`,
  `PodDisruptionBudget`, and `NetworkPolicy`;
- rollout gates: `make helm-template-smoke`, `make cloud-native-render-check`,
  and `make p1-growth-check`.

Fallback status must be visible rather than implicit. If Helm is unavailable,
the static template render path remains valid evidence for local development,
but release CI should prefer real `helm template` output and may add
`kubeconform` or `kubeval` on top.
