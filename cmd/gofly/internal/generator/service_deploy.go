package generator

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type DockerOptions struct {
	Name        string
	Dir         string
	Output      string
	GoFile      string
	Exe         string
	GoVersion   string
	BaseImage   string
	Port        string
	Timezone    string
	TemplateDir string
	Remote      string
	Branch      string
}

type KubeOptions struct {
	Name            string
	Dir             string
	Output          string
	Kind            string
	Namespace       string
	Image           string
	Port            string
	RPCPort         string
	Replicas        string
	Host            string
	Path            string
	Config          map[string]string
	Secret          string
	NodePort        string
	Revisions       string
	MinReplicas     string
	MaxReplicas     string
	RequestCPU      string
	RequestMem      string
	LimitCPU        string
	LimitMem        string
	ImagePullPolicy string
	ServiceAccount  string
	TemplateDir     string
	Remote          string
	Branch          string
}

func GenerateDockerfile(opts DockerOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	if opts.GoFile == "" {
		opts.GoFile = "./cmd/" + opts.Name
	}
	if opts.Exe == "" {
		opts.Exe = opts.Name
	}
	if opts.GoVersion == "" {
		opts.GoVersion = "1.26"
	}
	if opts.BaseImage == "" {
		opts.BaseImage = "gcr.io/distroless/static-debian12"
	}
	if opts.Port == "" {
		opts.Port = "8080"
	}
	if opts.Timezone == "" {
		opts.Timezone = "UTC"
	}
	output := opts.Output
	if output == "" {
		output = filepath.Join(opts.Dir, "Dockerfile")
	}
	tmpl, err := resolveNamedTemplate(opts.TemplateDir, opts.Remote, opts.Branch, "docker.tpl", dockerfileTemplate)
	if err != nil {
		return err
	}
	return writeRenderedFile(
		output,
		tmpl,
		map[string]string{
			"Name":      opts.Name,
			"GoFile":    opts.GoFile,
			"Exe":       opts.Exe,
			"GoVersion": opts.GoVersion,
			"BaseImage": opts.BaseImage,
			"Port":      opts.Port,
			"Timezone":  opts.Timezone,
		},
	)
}

func GenerateKube(opts KubeOptions) error {
	if opts.Name == "" {
		return errors.New("name is required")
	}
	requestedKind := strings.TrimSpace(opts.Kind)
	if opts.Dir == "" {
		opts.Dir = "."
	}
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.Image == "" {
		opts.Image = opts.Name + ":latest"
	}
	if opts.Port == "" {
		opts.Port = "8080"
	}
	if opts.RPCPort == "" {
		opts.RPCPort = "8081"
	}
	if opts.Replicas == "" {
		opts.Replicas = "2"
	}
	if opts.Kind == "" {
		opts.Kind = "deploy"
	}
	if opts.Host == "" {
		opts.Host = opts.Name + ".local"
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	data := map[string]string{
		"Name":             opts.Name,
		"Namespace":        opts.Namespace,
		"Image":            opts.Image,
		"Port":             opts.Port,
		"RPCPort":          opts.RPCPort,
		"Replicas":         opts.Replicas,
		"Host":             opts.Host,
		"Path":             opts.Path,
		"Data":             kubeConfigData(opts.Config),
		"RevisionHistory":  kubeRevisionHistory(opts.Revisions),
		"ImagePullSecrets": kubeImagePullSecrets(opts.Secret),
		"ServiceAccount":   kubeServiceAccount(opts.ServiceAccount),
		"ImagePullPolicy":  kubeImagePullPolicy(opts.ImagePullPolicy),
		"Resources":        kubeResources(opts.RequestCPU, opts.RequestMem, opts.LimitCPU, opts.LimitMem),
		"ServiceType":      kubeServiceType(opts.NodePort),
		"NodePort":         kubeNodePort(opts.NodePort),
		"Autoscale":        kubeAutoscale(opts.Name, opts.Namespace, opts.MinReplicas, opts.MaxReplicas),
	}
	output := opts.Output
	if output == "" {
		if requestedKind == "" {
			output = filepath.Join(opts.Dir, opts.Name+".yaml")
		} else {
			output = filepath.Join(opts.Dir, kubeOutputName(opts.Name, opts.Kind))
		}
	}
	tmpl, err := resolveKubeTemplate(opts.TemplateDir, opts.Remote, opts.Branch, opts.Kind)
	if err != nil {
		return err
	}
	return writeRenderedFile(output, tmpl, data)
}

func resolveKubeTemplate(dir, remote, branch, kind string) (string, error) {
	fallback, err := kubeTemplateForKind(kind)
	if err != nil {
		return "", err
	}
	names := []string{"kube.tpl"}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "deploy", "deployment":
		names = append([]string{"kube-deployment.tpl", "deployment.tpl"}, names...)
	case "service", "svc":
		names = append([]string{"kube-service.tpl", "service.tpl"}, names...)
	case "ingress", "ing":
		names = append([]string{"kube-ingress.tpl", "ingress.tpl"}, names...)
	case "configmap", "cm":
		names = append([]string{"kube-configmap.tpl", "configmap.tpl"}, names...)
	case "job":
		names = append([]string{"kube-job.tpl", "job.tpl"}, names...)
	}
	return resolveNamedTemplates(dir, remote, branch, names, fallback)
}

func kubeTemplateForKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "deploy", "deployment":
		return kubeTemplate, nil
	case "service", "svc":
		return kubeServiceTemplate, nil
	case "ingress", "ing":
		return kubeIngressTemplate, nil
	case "configmap", "cm":
		return kubeConfigMapTemplate, nil
	case "job":
		return kubeJobTemplate, nil
	default:
		return "", fmt.Errorf("unsupported kube resource kind %q", kind)
	}
}

func kubeOutputName(name, kind string) string {
	suffix := strings.ToLower(strings.TrimSpace(kind))
	if suffix == "" || suffix == "deploy" || suffix == "deployment" {
		return name + ".yaml"
	}
	switch suffix {
	case "svc":
		suffix = "service"
	case "ing":
		suffix = "ingress"
	case "cm":
		suffix = "configmap"
	}
	return name + "-" + suffix + ".yaml"
}

func kubeConfigData(config map[string]string) string {
	if len(config) == 0 {
		return "  app.json: |\n    {}"
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		value := config[key]
		if strings.Contains(value, "\n") {
			fmt.Fprintf(&b, "  %s: |\n", key)
			for _, line := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
			continue
		}
		fmt.Fprintf(&b, "  %s: %q\n", key, value)
	}
	return strings.TrimRight(b.String(), "\n")
}

func kubeRevisionHistory(revisions string) string {
	revisions = strings.TrimSpace(revisions)
	if revisions == "" {
		return ""
	}
	return fmt.Sprintf("  revisionHistoryLimit: %s\n", revisions)
}

func kubeImagePullSecrets(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	return fmt.Sprintf("      imagePullSecrets:\n        - name: %s\n", secret)
}

func kubeServiceAccount(serviceAccount string) string {
	serviceAccount = strings.TrimSpace(serviceAccount)
	if serviceAccount == "" {
		return ""
	}
	return fmt.Sprintf("      serviceAccountName: %s\n", serviceAccount)
}

func kubeImagePullPolicy(policy string) string {
	policy = strings.TrimSpace(policy)
	if policy == "" {
		return ""
	}
	return fmt.Sprintf("          imagePullPolicy: %s\n", policy)
}

func kubeResources(requestCPU, requestMem, limitCPU, limitMem string) string {
	requestCPU = strings.TrimSpace(requestCPU)
	requestMem = strings.TrimSpace(requestMem)
	limitCPU = strings.TrimSpace(limitCPU)
	limitMem = strings.TrimSpace(limitMem)
	if requestCPU == "" && requestMem == "" && limitCPU == "" && limitMem == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("          resources:\n")
	if requestCPU != "" || requestMem != "" {
		b.WriteString("            requests:\n")
		if requestCPU != "" {
			fmt.Fprintf(&b, "              cpu: %s\n", requestCPU)
		}
		if requestMem != "" {
			fmt.Fprintf(&b, "              memory: %s\n", requestMem)
		}
	}
	if limitCPU != "" || limitMem != "" {
		b.WriteString("            limits:\n")
		if limitCPU != "" {
			fmt.Fprintf(&b, "              cpu: %s\n", limitCPU)
		}
		if limitMem != "" {
			fmt.Fprintf(&b, "              memory: %s\n", limitMem)
		}
	}
	return b.String()
}

func kubeServiceType(nodePort string) string {
	if strings.TrimSpace(nodePort) == "" {
		return ""
	}
	return "  type: NodePort\n"
}

func kubeNodePort(nodePort string) string {
	nodePort = strings.TrimSpace(nodePort)
	if nodePort == "" {
		return ""
	}
	return fmt.Sprintf("      nodePort: %s\n", nodePort)
}

func kubeAutoscale(name, namespace, minReplicas, maxReplicas string) string {
	minReplicas = strings.TrimSpace(minReplicas)
	maxReplicas = strings.TrimSpace(maxReplicas)
	if minReplicas == "" && maxReplicas == "" {
		return ""
	}
	if minReplicas == "" {
		minReplicas = "1"
	}
	if maxReplicas == "" {
		maxReplicas = minReplicas
	}
	return fmt.Sprintf(`---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %[1]s
  minReplicas: %[3]s
  maxReplicas: %[4]s
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 80
`, name, namespace, minReplicas, maxReplicas)
}
