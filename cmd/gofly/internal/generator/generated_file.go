package generator

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	generatedDirMode         os.FileMode = 0o755
	generatedPrivateDirMode  os.FileMode = 0o750
	generatedPublicFileMode  os.FileMode = 0o644
	generatedPrivateFileMode os.FileMode = 0o600
)

func ensureGeneratedDir(path string) error {
	// #nosec G301 -- generated project source trees are intentionally traversable by editors, build tools, and version control.
	if err := os.MkdirAll(path, generatedDirMode); err != nil {
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
	// #nosec G306 G703 -- caller-provided generated file paths are validated by generator entrypoints; generated source/configuration files are intentionally readable within the generated project; shell scripts need execute bits for generated operational checks.
	if err := os.WriteFile(path, data, generatedFileMode(path)); err != nil {
		return fmt.Errorf("write generated file %s: %w", path, err)
	}
	return nil
}

func writeGeneratedFileUnder(root string, name string, data []byte) error {
	target, err := safeRelativeTarget(root, name, "generated file")
	if err != nil {
		return err
	}
	return WriteFileUnderRoot(root, target, data, generatedFileMode(name), generatedDirMode, "generated file")
}

// SafeTarget resolves target under root and rejects root escapes plus parent symlink traversal.
// target may be relative to root or absolute, but the resolved location must stay under root.
func SafeTarget(root string, target string, label string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%s root is required", safetyLabel(label))
	}
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("%s target path is required", safetyLabel(label))
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s root %s: %w", safetyLabel(label), root, err)
	}
	absRoot = filepath.Clean(absRoot)
	if info, err := os.Lstat(absRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s root %s must not be a symlink", safetyLabel(label), absRoot)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s root %s: %w", safetyLabel(label), absRoot, err)
	}

	absTarget, err := absoluteTarget(absRoot, target, label)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", fmt.Errorf("rel %s path %q: %w", safetyLabel(label), target, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes root %q", safetyLabel(label), absTarget, absRoot)
	}
	if err := rejectSymlinkParent(absRoot, rel, label); err != nil {
		return "", err
	}
	return absTarget, nil
}

func safeRelativeTarget(root, name string, label string) (string, error) {
	if strings.TrimSpace(name) == "" || filepath.IsAbs(name) || strings.Contains(name, ":") {
		return "", fmt.Errorf("%s path %q must be relative", safetyLabel(label), name)
	}
	cleanName := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(name, `\`, "/")))
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes output directory", safetyLabel(label), name)
	}
	return SafeTarget(root, cleanName, label)
}

func absoluteTarget(absRoot string, target string, label string) (string, error) {
	if filepath.IsAbs(target) {
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return "", fmt.Errorf("resolve %s path %q: %w", safetyLabel(label), target, err)
		}
		return filepath.Clean(absTarget), nil
	}
	cleanTarget := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(target, `\`, "/")))
	if cleanTarget == "." || cleanTarget == ".." || strings.HasPrefix(cleanTarget, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q escapes output directory", safetyLabel(label), target)
	}
	return filepath.Abs(filepath.Join(absRoot, cleanTarget))
}

func rejectSymlinkParent(root, cleanName string, label string) error {
	current := root
	parts := strings.Split(filepath.Clean(cleanName), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("inspect %s path %q: %w", safetyLabel(label), current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s path %q traverses symlink %q", safetyLabel(label), cleanName, part)
		}
	}
	return nil
}

func rejectExistingSymlinkTarget(target, label string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect %s target %q: %w", safetyLabel(label), target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s target %q is a symlink", safetyLabel(label), target)
	}
	return nil
}

// EnsureDirectoryUnderRoot creates a directory constrained to root, rejecting symlink parents/leaves.
func EnsureDirectoryUnderRoot(root string, target string, mode os.FileMode, label string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve %s root %s: %w", safetyLabel(label), root, err)
	}
	if strings.TrimSpace(target) == "." {
		if err := rejectExistingSymlinkTarget(absRoot, label+" root"); err != nil {
			return err
		}
		// #nosec G301 -- generated and template directories are intentionally traversable by editors, build tools, and version control.
		return os.MkdirAll(absRoot, mode)
	}
	absTarget, err := absoluteTarget(absRoot, target, label)
	if err != nil {
		return err
	}
	if samePath(absRoot, absTarget) {
		if err := rejectExistingSymlinkTarget(absRoot, label+" root"); err != nil {
			return err
		}
		// #nosec G301 -- generated and template directories are intentionally traversable by editors, build tools, and version control.
		return os.MkdirAll(absRoot, mode)
	}
	absTarget, err = SafeTarget(root, target, label)
	if err != nil {
		return err
	}
	if err := rejectExistingSymlinkTarget(absTarget, label); err != nil {
		return err
	}
	// #nosec G301 -- generated and template directories are intentionally traversable by editors, build tools, and version control.
	return os.MkdirAll(absTarget, mode)
}

// WriteFileUnderRoot writes a file constrained to root, rejecting symlink parents and leaf targets.
func WriteFileUnderRoot(root string, target string, data []byte, fileMode os.FileMode, dirMode os.FileMode, label string) error {
	if err := EnsureDirectoryUnderRoot(root, ".", dirMode, label); err != nil {
		return err
	}
	absTarget, err := SafeTarget(root, target, label)
	if err != nil {
		return err
	}
	if err := EnsureDirectoryUnderRoot(root, filepath.Dir(absTarget), dirMode, label); err != nil {
		return err
	}
	if err := rejectExistingSymlinkTarget(absTarget, label); err != nil {
		return err
	}
	// #nosec G306 G703 -- target is constrained to a verified root with symlink parents and leaf targets rejected; caller selects public/private generated-file mode by policy.
	if err := os.WriteFile(absTarget, data, fileMode); err != nil {
		return fmt.Errorf("write %s file %s: %w", safetyLabel(label), absTarget, err)
	}
	return nil
}

// ReadFileUnderRoot reads a non-symlink file constrained to root.
func ReadFileUnderRoot(root string, target string, label string) ([]byte, error) {
	absTarget, err := SafeTarget(root, target, label)
	if err != nil {
		return nil, err
	}
	if err := rejectExistingSymlinkTarget(absTarget, label); err != nil {
		return nil, err
	}
	// #nosec G304 -- target is constrained to a verified root with symlink parents and leaf targets rejected.
	data, err := os.ReadFile(absTarget)
	if err != nil {
		return nil, fmt.Errorf("read %s file %s: %w", safetyLabel(label), absTarget, err)
	}
	return data, nil
}

// CopyFileUnderRoot copies a non-symlink source constrained to srcRoot into a non-symlink target constrained to dstRoot.
func CopyFileUnderRoot(srcRoot string, src string, dstRoot string, dst string, fileMode os.FileMode, dirMode os.FileMode, label string) error {
	absSrc, err := SafeTarget(srcRoot, src, label+" source")
	if err != nil {
		return err
	}
	absDst, err := SafeTarget(dstRoot, dst, label+" target")
	if err != nil {
		return err
	}
	if samePath(absSrc, absDst) {
		return nil
	}
	data, err := ReadFileUnderRoot(srcRoot, src, label+" source")
	if err != nil {
		return err
	}
	return WriteFileUnderRoot(dstRoot, dst, data, fileMode, dirMode, label+" target")
}

// CopyFileToRoot copies an explicit operator-selected source file into a root-constrained target.
func CopyFileToRoot(src string, dstRoot string, dst string, fileMode os.FileMode, dirMode os.FileMode, label string) error {
	// #nosec G304 -- source is an explicit caller/operator-selected file; destination is constrained to a verified root.
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	absTarget, err := SafeTarget(dstRoot, dst, label)
	if err != nil {
		return err
	}
	if samePath(src, absTarget) {
		return nil
	}
	if err := EnsureDirectoryUnderRoot(dstRoot, filepath.Dir(absTarget), dirMode, label); err != nil {
		return err
	}
	if err := rejectExistingSymlinkTarget(absTarget, label); err != nil {
		return err
	}
	// #nosec G304 -- destination is constrained to a verified root with symlink parents and leaf targets rejected.
	out, err := os.OpenFile(absTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func generatedFileMode(path string) os.FileMode {
	if filepath.Ext(path) == ".sh" {
		return generatedDirMode
	}
	return generatedPublicFileMode
}

func safetyLabel(label string) string {
	if strings.TrimSpace(label) == "" {
		return "generated file"
	}
	return label
}
