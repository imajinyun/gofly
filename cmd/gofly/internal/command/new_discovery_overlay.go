package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type discoveryCLIOverlay struct {
	Provider    string
	Address     string
	Endpoints   string
	Prefix      string
	TTL         string
	DialTimeout string
	TokenEnv    string
	UsernameEnv string
	PasswordEnv string
}

type discoveryCLIFlagValues struct {
	Discovery            *string
	DiscoveryAddress     *string
	DiscoveryEndpoints   *string
	DiscoveryPrefix      *string
	DiscoveryTTL         *string
	DiscoveryDialTimeout *string
	DiscoveryTokenEnv    *string
	DiscoveryUsernameEnv *string
	DiscoveryPasswordEnv *string
}

func registerDiscoveryCLIFlags(fs *flag.FlagSet) discoveryCLIFlagValues {
	return discoveryCLIFlagValues{
		Discovery:            fs.String("discovery", "", "service discovery provider: memory, consul, or etcdv3"),
		DiscoveryAddress:     fs.String("discovery-address", "", "service discovery address, or comma-separated endpoints for etcdv3"),
		DiscoveryEndpoints:   fs.String("discovery-endpoints", "", "service discovery endpoints, comma-separated"),
		DiscoveryPrefix:      fs.String("discovery-prefix", "", "service discovery key prefix for etcdv3"),
		DiscoveryTTL:         fs.String("discovery-ttl", "", "service discovery registration TTL, e.g. 15s"),
		DiscoveryDialTimeout: fs.String("discovery-dial-timeout", "", "service discovery dial timeout, e.g. 5s"),
		DiscoveryTokenEnv:    fs.String("discovery-token-env", "", "environment variable containing the Consul ACL token"),
		DiscoveryUsernameEnv: fs.String("discovery-username-env", "", "environment variable containing the etcd username"),
		DiscoveryPasswordEnv: fs.String("discovery-password-env", "", "environment variable containing the etcd password"),
	}
}

func discoveryCLIOverlayFromFlags(flags discoveryCLIFlagValues) discoveryCLIOverlay {
	return discoveryCLIOverlay{
		Provider:    valueFromStringFlag(flags.Discovery),
		Address:     valueFromStringFlag(flags.DiscoveryAddress),
		Endpoints:   valueFromStringFlag(flags.DiscoveryEndpoints),
		Prefix:      valueFromStringFlag(flags.DiscoveryPrefix),
		TTL:         valueFromStringFlag(flags.DiscoveryTTL),
		DialTimeout: valueFromStringFlag(flags.DiscoveryDialTimeout),
		TokenEnv:    valueFromStringFlag(flags.DiscoveryTokenEnv),
		UsernameEnv: valueFromStringFlag(flags.DiscoveryUsernameEnv),
		PasswordEnv: valueFromStringFlag(flags.DiscoveryPasswordEnv),
	}
}

func valueFromStringFlag(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueFromBoolFlag(value *bool) bool {
	return value != nil && *value
}

func applyDiscoveryCLIOverlay(cfg *generator.Config, overlay discoveryCLIOverlay) {
	if cfg == nil || !overlay.hasValue() {
		return
	}
	if cfg.Discovery == nil {
		cfg.Discovery = &generator.DiscoveryConfig{}
	}
	if overlay.Provider != "" {
		cfg.Discovery.Provider = overlay.Provider
	}
	if overlay.Address != "" {
		cfg.Discovery.Address = overlay.Address
	}
	if overlay.Endpoints != "" {
		cfg.Discovery.Endpoints = splitCSV(overlay.Endpoints)
	}
	if overlay.Prefix != "" {
		cfg.Discovery.Prefix = overlay.Prefix
	}
	if overlay.TTL != "" {
		cfg.Discovery.TTL = overlay.TTL
	}
	if overlay.DialTimeout != "" {
		cfg.Discovery.DialTimeout = overlay.DialTimeout
	}
	if overlay.TokenEnv != "" {
		cfg.Discovery.TokenEnv = overlay.TokenEnv
	}
	if overlay.UsernameEnv != "" {
		cfg.Discovery.UsernameEnv = overlay.UsernameEnv
	}
	if overlay.PasswordEnv != "" {
		cfg.Discovery.PasswordEnv = overlay.PasswordEnv
	}
}

func (o discoveryCLIOverlay) hasValue() bool {
	return o.Provider != "" || o.Address != "" || o.Endpoints != "" || o.Prefix != "" || o.TTL != "" || o.DialTimeout != "" || o.TokenEnv != "" || o.UsernameEnv != "" || o.PasswordEnv != ""
}
