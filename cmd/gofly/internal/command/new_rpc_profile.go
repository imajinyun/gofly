package command

import (
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func resolveNewRPCProfile(cfg *generator.Config, profile string) string {
	resolvedProfile := strings.TrimSpace(profile)
	if resolvedProfile == "" && cfg.RPC != nil {
		resolvedProfile = strings.TrimSpace(cfg.RPC.Profile)
	}
	if cfg.RPC == nil {
		cfg.RPC = &generator.RPCConfig{}
	}
	cfg.RPC.Profile = resolvedProfile
	return resolvedProfile
}
