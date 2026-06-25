// Package nacossource implements a config.RemoteSource backed by Nacos using
// the official nacos-sdk-go/v2 client. Updates are delivered through the SDK's
// event-driven ListenConfig callback rather than polling.
package nacossource

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/imajinyun/gofly/core/config"
)

// ServerConfig points to a single Nacos server.
type ServerConfig struct {
	IPAddr string
	Port   uint64
}

// Config configures the Nacos config source.
type Config struct {
	// Servers is the list of Nacos servers.
	Servers []ServerConfig
	// Namespace is the Nacos namespace (tenant) id. Optional.
	Namespace string
	// Group is the Nacos config group. Defaults to DEFAULT_GROUP.
	Group string
	// DataID is the Nacos dataId that stores the configuration payload.
	DataID string
	// Username/Password authenticate against Nacos when set.
	Username string
	Password string
}

func (c Config) group() string {
	if c.Group == "" {
		return "DEFAULT_GROUP"
	}
	return c.Group
}

// Source is a config.RemoteSource backed by Nacos.
type Source struct {
	client config_client.IConfigClient
	dataID string
	group  string
}

var _ config.RemoteSource = (*Source)(nil)

// New connects to Nacos and returns a Source.
func New(cfg Config) (*Source, error) {
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("nacossource: at least one server is required")
	}
	if cfg.DataID == "" {
		return nil, fmt.Errorf("nacossource: dataId is required")
	}
	servers := make([]constant.ServerConfig, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, *constant.NewServerConfig(s.IPAddr, s.Port))
	}
	clientCfg := constant.NewClientConfig(
		constant.WithNamespaceId(cfg.Namespace),
		constant.WithUsername(cfg.Username),
		constant.WithPassword(cfg.Password),
	)
	client, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig:  clientCfg,
		ServerConfigs: servers,
	})
	if err != nil {
		return nil, fmt.Errorf("nacossource: new client: %w", err)
	}
	return &Source{client: client, dataID: cfg.DataID, group: cfg.group()}, nil
}

// NewWithClient wraps an existing Nacos config client.
func NewWithClient(client config_client.IConfigClient, group, dataID string) (*Source, error) {
	if client == nil {
		return nil, fmt.Errorf("nacossource: client is nil")
	}
	if dataID == "" {
		return nil, fmt.Errorf("nacossource: dataId is required")
	}
	if group == "" {
		group = "DEFAULT_GROUP"
	}
	return &Source{client: client, dataID: dataID, group: group}, nil
}

// Get fetches the current configuration payload.
func (s *Source) Get(ctx context.Context) (config.RemoteValue, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return config.RemoteValue{}, err
		}
	}
	content, err := s.client.GetConfig(vo.ConfigParam{
		DataId: s.dataID,
		Group:  s.group,
	})
	if err != nil {
		return config.RemoteValue{}, fmt.Errorf("nacossource: get %q: %w", s.dataID, err)
	}
	return config.RemoteValue{Key: s.dataID, Data: []byte(content)}, nil
}

// Watch registers a Nacos listener and forwards updates until ctx ends. The
// listener is cancelled on return.
func (s *Source) Watch(ctx context.Context, onChange func(config.RemoteValue)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	err := s.client.ListenConfig(vo.ConfigParam{
		DataId: s.dataID,
		Group:  s.group,
		OnChange: func(namespace, group, dataId, data string) {
			if onChange != nil {
				onChange(config.RemoteValue{Key: dataId, Data: []byte(data)})
			}
		},
	})
	if err != nil {
		return fmt.Errorf("nacossource: listen %q: %w", s.dataID, err)
	}
	<-ctx.Done()
	if err := s.client.CancelListenConfig(vo.ConfigParam{DataId: s.dataID, Group: s.group}); err != nil {
		slog.Error("nacossource cancel listen config failed", "data_id", s.dataID, "group", s.group, "error", err)
	}
	return ctx.Err()
}

// Close is a no-op; the Nacos SDK manages its own connection lifecycle.
func (s *Source) Close() error { return nil }
