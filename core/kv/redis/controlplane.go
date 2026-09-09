package redis

import (
	"context"
	"encoding/json"

	"github.com/imajinyun/gofly/core/controlplane"
)

// ControlPlaneContributor exposes the client diagnostics snapshot to a
// control-plane provider. Registration is explicit so applications retain
// ownership of Redis client lifecycle and admin-surface policy.
type ControlPlaneContributor struct {
	Client *Client
	Name   string
}

// ContributeSnapshot adds a sanitized Redis runtime diagnostic under
// runtime.redis or runtime.redis.<Name>.
func (c ControlPlaneContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil || c.Client == nil {
		return nil
	}
	data, err := json.Marshal(c.Client.DiagnosticsSnapshot())
	if err != nil {
		return err
	}
	if snapshot.Configs == nil {
		snapshot.Configs = make(map[string]json.RawMessage)
	}
	key := "runtime.redis"
	if c.Name != "" {
		key += "." + c.Name
	}
	snapshot.Configs[key] = append(json.RawMessage(nil), data...)
	return nil
}

var _ controlplane.SnapshotContributor = ControlPlaneContributor{}
