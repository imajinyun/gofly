package command

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func modelTypeConfigFromConfig(configPath, dir string) (map[string]string, map[string]generator.ModelTypeOverride, error) {
	path := strings.TrimSpace(configPath)
	explicitPath := path != ""
	if path == "" {
		path = filepath.Join(dir, ".gofly", "config.json")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicitPath {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	cfg, err := generator.LoadConfig(path)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Model == nil {
		return nil, nil, nil
	}
	out := make(map[string]string, len(cfg.Model.TypesMap))
	for key, value := range cfg.Model.TypesMap {
		out[key] = value
	}
	overrides := make(map[string]generator.ModelTypeOverride, len(cfg.Model.TypeOverrides))
	for key, value := range cfg.Model.TypeOverrides {
		overrides[key] = value
	}
	return out, overrides, nil
}

func modelTypesMapFromConfig(configPath, dir string) (map[string]string, error) {
	typesMap, _, err := modelTypeConfigFromConfig(configPath, dir)
	return typesMap, err
}

type modelTemplateSourceFlags struct {
	templateSourceFlags
	SHA256 *string
}

func registerGoctlModelTemplateFlags(fs *flag.FlagSet) modelTemplateSourceFlags {
	flags := registerTemplateSourceFlags(fs, "local model template directory containing model-entity.tpl", "remote model template repository (requires --template-sha256)", "remote model template branch")
	fs.Bool("idea", false, "open generated project in IDE")
	return modelTemplateSourceFlags{
		templateSourceFlags: flags,
		SHA256:              fs.String("template-sha256", "", "SHA-256 digest of remote model-entity.tpl"),
	}
}
