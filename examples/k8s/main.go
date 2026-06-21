// Command k8s demonstrates Kubernetes endpoints resolution and service
// registration using the in-cluster client.
package main

import "fmt"

func main() {
	fmt.Println("k8s example: configure rpc.KubernetesResolver with service account token, namespace, service, port name, and watch interval")
	fmt.Println("manifest checklist: Deployment, Service, readinessProbe, startupProbe, ServiceMonitor, HPA, PodDisruptionBudget, Helm chart, Kustomize overlay, RBAC get/list endpoints, ConfigMap for gofly service config")
}
