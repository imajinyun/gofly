package command

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func modelTypesMapFromConfig(configPath, dir string) (map[string]string, error) {
	path := strings.TrimSpace(configPath)
	explicitPath := path != ""
	if path == "" {
		path = filepath.Join(dir, ".gofly", "config.json")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicitPath {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := generator.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	if cfg.Model == nil || len(cfg.Model.TypesMap) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(cfg.Model.TypesMap))
	for key, value := range cfg.Model.TypesMap {
		out[key] = value
	}
	return out, nil
}

func registerGoctlModelTemplateFlags(fs *flag.FlagSet) {
	registerTemplateSourceFlags(fs, "", "", "")
	fs.Bool("idea", false, "open generated project in IDE")
}
