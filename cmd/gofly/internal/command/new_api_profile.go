package command

import (
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func resolveNewAPIProfile(cfg *generator.Config, profile string) (string, error) {
	resolvedProfile := strings.TrimSpace(profile)
	if resolvedProfile == "" && cfg.API != nil {
		resolvedProfile = strings.TrimSpace(cfg.API.Profile)
	}
	if _, err := generator.NormalizeGenerationProfile(resolvedProfile); err != nil {
		return "", err
	}
	if cfg.API == nil {
		cfg.API = &generator.APIConfig{}
	}
	cfg.API.Profile = resolvedProfile
	return resolvedProfile, nil
}
