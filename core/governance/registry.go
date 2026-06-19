// Package governance provides request routing rules, rate limiting, circuit
// breaking, concurrency limiting and canary release policies for gofly services.
package governance

import (
	"sort"
	"sync"
)

// SnapshotFunc returns a component's current state.
type SnapshotFunc func() any

// ComponentSnapshot captures a single registered component's state.
type ComponentSnapshot struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Target   string `json:"target,omitempty"`
	Snapshot any    `json:"snapshot,omitempty"`
}

// Registry collects component snapshots for the governance admin endpoint.
type Registry struct {
	mu         sync.RWMutex
	components []component
}

type component struct {
	name     string
	kind     string
	target   string
	snapshot SnapshotFunc
}

// NewRegistry creates an empty snapshot registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a component to the registry.
func (r *Registry) Register(name, kind, target string, snapshot SnapshotFunc) {
	if r == nil || name == "" || kind == "" || snapshot == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = append(r.components, component{name: name, kind: kind, target: target, snapshot: snapshot})
}

func (r *Registry) Snapshots() []ComponentSnapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	components := append([]component(nil), r.components...)
	r.mu.RUnlock()
	out := make([]ComponentSnapshot, 0, len(components))
	for _, component := range components {
		out = append(out, ComponentSnapshot{
			Name:     component.name,
			Kind:     component.kind,
			Target:   component.target,
			Snapshot: component.snapshot(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Name < out[j].Name
	})
	return out
}
