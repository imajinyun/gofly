package command

import (
	"flag"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func kubeCommand(args []string) error {
	if printCommandHelp("kube", args) {
		return nil
	}
	kind := "deploy"
	if len(args) > 0 && isKubeKind(args[0]) {
		kind = args[0]
		args = args[1:]
	}
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("kube", flag.ContinueOnError)
	name := fs.String("name", "", "service name")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output yaml path")
	namespace := fs.String("namespace", "default", "kubernetes namespace")
	image := fs.String("image", "", "container image")
	secret := fs.String("secret", "", "image pull secret name")
	port := fs.String("port", "8080", "http container port")
	targetPort := fs.String("targetPort", "", "target container port")
	nodePort := fs.String("nodePort", "", "Kubernetes node port")
	rpcPort := fs.String("rpc-port", "8081", "rpc container port")
	replicas := fs.String("replicas", "2", "deployment replicas")
	revisions := fs.String("revisions", "", "revision history limit")
	minReplicas := fs.String("minReplicas", "", "minimum autoscale replicas")
	maxReplicas := fs.String("maxReplicas", "", "maximum autoscale replicas")
	requestCPU := fs.String("requestCpu", "", "requested CPU resource")
	requestMem := fs.String("requestMem", "", "requested memory resource")
	limitCPU := fs.String("limitCpu", "", "CPU resource limit")
	limitMem := fs.String("limitMem", "", "memory resource limit")
	imagePullPolicy := fs.String("imagePullPolicy", "", "image pull policy")
	serviceAccount := fs.String("serviceAccount", "", "Kubernetes service account")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	host := fs.String("host", "", "ingress host")
	path := fs.String("path", "/", "ingress path")
	data := fs.String("data", "", "configmap data as comma-separated key=value pairs")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	if *targetPort != "" {
		*port = *targetPort
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateKube(generator.KubeOptions{
		Name:            *name,
		Dir:             *dir,
		Output:          output.resolve(),
		Kind:            kind,
		Namespace:       *namespace,
		Image:           *image,
		Port:            *port,
		RPCPort:         *rpcPort,
		Replicas:        *replicas,
		Host:            *host,
		Path:            *path,
		Config:          parseKeyValueCSV(*data),
		Secret:          *secret,
		NodePort:        *nodePort,
		Revisions:       *revisions,
		MinReplicas:     *minReplicas,
		MaxReplicas:     *maxReplicas,
		RequestCPU:      *requestCPU,
		RequestMem:      *requestMem,
		LimitCPU:        *limitCPU,
		LimitMem:        *limitMem,
		ImagePullPolicy: *imagePullPolicy,
		ServiceAccount:  *serviceAccount,
		TemplateDir:     *home,
		Remote:          *remote,
		Branch:          *branch,
	})
}

func isKubeKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deploy", "deployment", "service", "svc", "ingress", "ing", "configmap", "cm", "job":
		return true
	default:
		return false
	}
}
