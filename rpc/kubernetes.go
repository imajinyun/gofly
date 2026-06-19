// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	core "github.com/gofly/gofly/core"
)

type KubernetesResolver struct {
	client          *http.Client
	baseURL         string
	namespace       string
	service         string
	scheme          string
	port            int
	portName        string
	token           string
	includeNotReady bool
	watchInterval   time.Duration
}

type KubernetesResolverConfig struct {
	BaseURL   string
	Namespace string
	Service   string
	Scheme    string
	Port      int
	// PortName selects a named endpoint port when set, taking precedence over
	// Port. Useful when a service exposes multiple ports (e.g. "grpc", "http").
	PortName string
	Token    string
	Client   *http.Client
	// IncludeNotReady includes addresses from the endpoints' notReadyAddresses
	// list. By default only ready addresses are returned.
	IncludeNotReady bool
	// WatchInterval is the polling interval used by Watch. Defaults to 5s.
	WatchInterval time.Duration
}

const (
	// #nosec G101 -- this is the standard in-cluster Kubernetes serviceaccount token path, not a credential literal.
	kubernetesTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubernetesNSFile    = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

func NewKubernetesResolver(conf KubernetesResolverConfig) (*KubernetesResolver, error) {
	if conf.BaseURL == "" {
		return nil, fmt.Errorf("kubernetes base url is required")
	}
	if conf.Namespace == "" {
		conf.Namespace = inClusterNamespace()
	}
	if conf.Namespace == "" {
		conf.Namespace = "default"
	}
	if conf.Service == "" {
		return nil, fmt.Errorf("kubernetes service is required")
	}
	if conf.Scheme == "" {
		conf.Scheme = "http"
	}
	if conf.Client == nil {
		conf.Client = core.DefaultHTTPClient()
	}
	if conf.Token == "" {
		conf.Token = inClusterToken()
	}
	if conf.WatchInterval <= 0 {
		conf.WatchInterval = 5 * time.Second
	}
	return &KubernetesResolver{
		client:          conf.Client,
		baseURL:         strings.TrimRight(conf.BaseURL, "/"),
		namespace:       conf.Namespace,
		service:         conf.Service,
		scheme:          conf.Scheme,
		port:            conf.Port,
		portName:        strings.TrimSpace(conf.PortName),
		token:           conf.Token,
		includeNotReady: conf.IncludeNotReady,
		watchInterval:   conf.WatchInterval,
	}, nil
}

func (r *KubernetesResolver) Resolve(ctx context.Context) ([]string, error) {
	instances, err := r.ResolveInstances(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(instances))
	for _, instance := range instances {
		out = append(out, instance.Endpoint)
	}
	return out, nil
}

func (r *KubernetesResolver) ResolveInstances(ctx context.Context) ([]ServiceInstance, error) {
	if r == nil {
		return nil, fmt.Errorf("kubernetes resolver is nil")
	}
	path := fmt.Sprintf("%s/api/v1/namespaces/%s/endpoints/%s", r.baseURL, url.PathEscape(r.namespace), url.PathEscape(r.service))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes endpoints request: %w", err)
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query kubernetes endpoints: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("query kubernetes endpoints: status %d", resp.StatusCode)
	}
	var endpoints kubernetesEndpoints
	if err := json.NewDecoder(resp.Body).Decode(&endpoints); err != nil {
		return nil, fmt.Errorf("decode kubernetes endpoints: %w", err)
	}
	instances := make([]ServiceInstance, 0)
	for _, subset := range endpoints.Subsets {
		port := r.selectPort(subset.Ports)
		if port == 0 {
			continue
		}
		instances = append(instances, r.addressInstances(subset.Addresses, port, "healthy")...)
		if r.includeNotReady {
			instances = append(instances, r.addressInstances(subset.NotReadyAddresses, port, "unhealthy")...)
		}
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("no rpc endpoints resolved")
	}
	return instances, nil
}

// Watch polls the Kubernetes API at WatchInterval and emits the current endpoint
// set whenever it changes. It satisfies WatchResolver so it can back a
// CachedResolver.
func (r *KubernetesResolver) Watch(ctx context.Context) (<-chan []string, error) {
	if r == nil {
		return nil, fmt.Errorf("kubernetes resolver is nil")
	}
	out := make(chan []string, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(r.watchInterval)
		defer ticker.Stop()
		var last []string
		emit := func() {
			endpoints, err := r.Resolve(ctx)
			if err != nil {
				return
			}
			if sameEndpoints(last, endpoints) {
				return
			}
			last = endpoints
			select {
			case out <- endpoints:
			case <-ctx.Done():
			}
		}
		emit()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				emit()
			}
		}
	}()
	return out, nil
}

func (r *KubernetesResolver) selectPort(ports []kubernetesEndpointPort) int {
	if r.portName != "" {
		for _, p := range ports {
			if p.Name == r.portName {
				return p.Port
			}
		}
		return 0
	}
	if r.port != 0 {
		return r.port
	}
	if len(ports) > 0 {
		return ports[0].Port
	}
	return 0
}

func (r *KubernetesResolver) addressInstances(addresses []kubernetesEndpointAddress, port int, status string) []ServiceInstance {
	out := make([]ServiceInstance, 0, len(addresses))
	for _, addr := range addresses {
		if addr.IP == "" {
			continue
		}
		out = append(out, ServiceInstance{
			Endpoint: r.scheme + "://" + addr.IP + ":" + strconv.Itoa(port),
			Status:   status,
			Tags:     addr.TargetRef.Labels,
		})
	}
	return out
}

func sameEndpoints(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func inClusterToken() string {
	data, err := os.ReadFile(kubernetesTokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func inClusterNamespace() string {
	data, err := os.ReadFile(kubernetesNSFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

type kubernetesEndpointAddress struct {
	IP        string `json:"ip"`
	TargetRef struct {
		Labels map[string]string `json:"labels"`
	} `json:"targetRef"`
}

type kubernetesEndpointPort struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type kubernetesEndpoints struct {
	Subsets []struct {
		Addresses         []kubernetesEndpointAddress `json:"addresses"`
		NotReadyAddresses []kubernetesEndpointAddress `json:"notReadyAddresses"`
		Ports             []kubernetesEndpointPort    `json:"ports"`
	} `json:"subsets"`
}
