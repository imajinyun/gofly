package command

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func readExplicitInputFile(path, label string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%s path is required", label)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s path: %w", label, err)
	}
	parent := filepath.Dir(abs)
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) {
		return nil, fmt.Errorf("%s path must name a file", label)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, fmt.Errorf("open %s parent: %w", label, err)
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s file: %w", label, err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read %s file: %w", label, err)
	}
	return data, nil
}
