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

When `helm` is installed, `make cloud-native-render-check` renders the chart
with `helm template`. When `kustomize` is installed, the same gate runs
`kustomize build k8s/overlays/production`. When `kubeconform` or `kubeval` is
installed, the gate runs schema validation against the rendered Helm and
Kustomize output. Without those tools, the static fallback still checks the
required resource kinds.

Tool availability must be explicit in release evidence. Helm and Kustomize are
required when present, while `kubeconform` and `kubeval` remain optional CI
schema validators. If a tool is missing, the conformance manifest must record
the fallback status instead of silently accepting an unrendered profile.

## Policy Conformance

The conformance manifest keeps the release evidence explicit:

- render report: `.tmp-test/cloud-native-render/render-report.json`, schema
  `gofly.cloud_native_render_report.v1`;
- render modes: `helm-template`, `kustomize-build`, and
  `static-template-render`;
- tool availability policy: `helm`, `kustomize`, `kubeconform`, and `kubeval`;
- optional schema validation tools: `kubeconform` and `kubeval`;
- fallback reasons: each unavailable render or validation tool must add a
  `fallbackReasons` entry to the report;
- profiles: `helm-default`, `helm-production`, and `kustomize-production`;
- rendered goldens: `docs/reference/cloud-native-rendered-production.golden.yaml`
  with explicit fallback status;
- policy resources: `ServiceMonitor`, `HorizontalPodAutoscaler`,
  `PodDisruptionBudget`, and `NetworkPolicy`;
- rollout gates: `make helm-template-smoke`, `make cloud-native-render-check`,
  `make reference-app-smoke`, `make runtime-slo-check`, and
  `make p1-growth-check`.

P10 adds `p10CloudNativeAdoptionProof` as the production adoption closeout. The
proof chains connect rendered assets, the `production-orders` reference
topology, runtime SLO evidence, and rollback decisions. This keeps cloud-native
adoption claims tied to executable checks instead of treating rendered YAML as
production proof by itself.

P11 adds `p11HostedCloudNativeProof` as the hosted CI proof contract. It maps
Docker-backed `production-orders` topology, Helm rendering, Kustomize policy
rendering, kubeconform or kubeval schema validation, release evidence, Docker
digest, SBOM, provenance, Trivy scan evidence, fallback reasons, and operator
rollback actions to the same `make p1-growth-check` adoption gate. Hosted proof
must block release promotion when Docker, Helm, Trivy, release evidence, or
required-check drift evidence is missing; local fallback is valid only when
`fallbackReasons` records the missing tool explicitly.

Fallback status must be visible rather than implicit. If Helm is unavailable,
the static template render path remains valid evidence for local development,
but release CI should prefer real `helm template` output and may add
`kubeconform` or `kubeval` on top.

P12 adds `p12HostedLiveCIProof` as the live CI closure for cloud-native
rendering. The required `cloud-native live render` job installs Helm, Kustomize,
and kubeconform, runs `make cloud-native-render-check`, uploads
`cloud-native-live-render-evidence`, and the tagged release job downloads
`release-evidence/cloud-native/render-report.json` before uploading
`release-dist-evidence`. This makes hosted render evidence release-blocking
rather than only a local fallback contract.
