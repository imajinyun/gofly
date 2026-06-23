// Package runtime captures low-cardinality runtime diagnostics for gofly
// transports and governance components.
package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Snapshot captures the current runtime state of registered components.
type Snapshot struct {
	GeneratedAt time.Time           `json:"generatedAt"`
	Components  []ComponentSnapshot `json:"components,omitempty"`
}

// ComponentSnapshot captures one runtime component's diagnostic state.
type ComponentSnapshot struct {
	Name       string              `json:"name"`
	Kind       string              `json:"kind"`
	Owner      string              `json:"owner,omitempty"`
	Target     string              `json:"target,omitempty"`
	Status     string              `json:"status,omitempty"`
	Error      string              `json:"error,omitempty"`
	Middleware *MiddlewareSnapshot `json:"middleware,omitempty"`
	Governance any                 `json:"governance,omitempty"`
	Resolver   any                 `json:"resolver,omitempty"`
	Balancer   any                 `json:"balancer,omitempty"`
	ConnPool   any                 `json:"connPool,omitempty"`
	Retry      any                 `json:"retry,omitempty"`
	Breaker    any                 `json:"breaker,omitempty"`
	Details    any                 `json:"details,omitempty"`
}

// MiddlewareSnapshot captures the final middleware or interceptor chain.
type MiddlewareSnapshot struct {
	Unary  []MiddlewareLayer `json:"unary,omitempty"`
	Stream []MiddlewareLayer `json:"stream,omitempty"`
}

// MiddlewareLayer identifies one resolved middleware layer.
type MiddlewareLayer struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
	Order  int    `json:"order"`
	Reason string `json:"reason,omitempty"`
}

// DumpFunc returns a component runtime snapshot.
type DumpFunc func(context.Context) ComponentSnapshot

// Option customises a registered runtime component.
type Option func(*component)

type component struct {
	name   string
	kind   string
	owner  string
	target string
	dump   DumpFunc
}

// Registry collects runtime component snapshots.
type Registry struct {
	mu         sync.RWMutex
	components []component
}

// NewRegistry creates an empty runtime registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// WithOwner records the owning subsystem, such as rest, rpc, or governance.
func WithOwner(owner string) Option {
	return func(c *component) {
		c.owner = strings.TrimSpace(owner)
	}
}

// WithTarget records the component target, such as a route, service, or endpoint.
func WithTarget(target string) Option {
	return func(c *component) {
		c.target = strings.TrimSpace(target)
	}
}

// Register adds a component dump function to the registry.
func (r *Registry) Register(name, kind string, dump DumpFunc, opts ...Option) {
	if r == nil || strings.TrimSpace(name) == "" || strings.TrimSpace(kind) == "" || dump == nil {
		return
	}
	c := component{
		name: strings.TrimSpace(name),
		kind: strings.TrimSpace(kind),
		dump: dump,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = append(r.components, c)
}

// Snapshot returns a deterministic snapshot of all registered components.
func (r *Registry) Snapshot(ctx context.Context) Snapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return Snapshot{GeneratedAt: time.Now()}
	}
	r.mu.RLock()
	components := append([]component(nil), r.components...)
	r.mu.RUnlock()
	out := Snapshot{GeneratedAt: time.Now(), Components: make([]ComponentSnapshot, 0, len(components))}
	for _, c := range components {
		snapshot := safeDump(ctx, c)
		out.Components = append(out.Components, snapshot)
	}
	sort.Slice(out.Components, func(i, j int) bool {
		left := out.Components[i]
		right := out.Components[j]
		if left.Owner != right.Owner {
			return left.Owner < right.Owner
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.Name < right.Name
	})
	return out
}

func safeDump(ctx context.Context, c component) (snapshot ComponentSnapshot) {
	snapshot = ComponentSnapshot{Name: c.name, Kind: c.kind, Owner: c.owner, Target: c.target}
	defer func() {
		if v := recover(); v != nil {
			snapshot.Status = "error"
			snapshot.Error = fmt.Sprint(v)
		}
		if snapshot.Name == "" {
			snapshot.Name = c.name
		}
		if snapshot.Kind == "" {
			snapshot.Kind = c.kind
		}
		if snapshot.Owner == "" {
			snapshot.Owner = c.owner
		}
		if snapshot.Target == "" {
			snapshot.Target = c.target
		}
	}()
	snapshot = c.dump(ctx)
	return snapshot
}
