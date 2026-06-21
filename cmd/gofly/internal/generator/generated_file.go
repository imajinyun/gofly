package generator

import (
	"fmt"
	"os"
	"path/filepath"
)

func ensureGeneratedDir(path string) error {
	// #nosec G301 -- generated project source trees are intentionally traversable by editors, build tools, and version control.
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create generated directory %s: %w", path, err)
	}
	return nil
}

func ensureGeneratedFileDir(path string) error {
	return ensureGeneratedDir(filepath.Dir(path))
}

func writeGeneratedFile(path string, data []byte) error {
	if err := ensureGeneratedFileDir(path); err != nil {
		return err
	}
	if err := rejectExistingSymlinkTarget(path, "generated file"); err != nil {
		return err
	}
	// #nosec G306 G703 -- caller-provided generated file paths are validated by generator entrypoints; generated source/configuration files are intentionally readable within the generated project.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write generated file %s: %w", path, err)
	}
	return nil
}

func writeGeneratedFileUnder(root string, name string, data []byte) error {
	if err := ensureGeneratedDir(root); err != nil {
		return err
	}
	target, err := safeRelativeTarget(root, name, "generated file")
	if err != nil {
		return err
	}
	if err := ensureGeneratedFileDir(target); err != nil {
		return err
	}
	if err := rejectExistingSymlinkTarget(target, "generated file"); err != nil {
		return err
	}
	// #nosec G306 -- target is constrained to the generated project root; generated source/configuration files are intentionally user-readable.
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write generated file %s: %w", target, err)
	}
	return nil
}
