// Package migration implements the CLI's versioned SQL migration workflow.
package migration

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxSQLBytes = 8 << 20
const maxCatalogBytes = 64 << 20

var filePattern = regexp.MustCompile(`^([0-9]+)_([a-z0-9_]+)\.(up|down)\.sql$`)
var namePattern = regexp.MustCompile(`[^a-z0-9_]+`)

// CreateOptions describes a new SQL pair. DDLFile is optional for empty templates.
type CreateOptions struct {
	Name, Dir, Version, DDLFile, Dialect string
	Time                                 time.Time
}

// Entry describes an immutable migration file pair and its execution status.
type Entry struct {
	Version        int    `json:"version"`
	Name           string `json:"name"`
	Up             string `json:"up"`
	Down           string `json:"down"`
	UpSHA256       string `json:"upSHA256"`
	DownSHA256     string `json:"downSHA256"`
	Status         string `json:"status,omitempty"`
	upSQL, downSQL []byte
}

// Report is the migration command's machine-readable result.
type Report struct {
	Schema         string   `json:"schema"`
	Action         string   `json:"action"`
	Files          []string `json:"files"`
	Migrations     []Entry  `json:"migrations"`
	CurrentVersion int      `json:"currentVersion"`
	Dirty          bool     `json:"dirty"`
	Changed        int      `json:"changed"`
}

func newReport(action string) Report {
	return Report{Schema: "gofly.migration.v1", Action: action, Files: []string{}, Migrations: []Entry{}, CurrentVersion: -1}
}

// ParseVersion validates positive versions that fit the execution engine's int.
func ParseVersion(value string) (int, error) {
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, errors.New("version must contain positive decimal digits")
	}
	n, err := strconv.ParseInt(value, 10, strconv.IntSize)
	if err != nil || n <= 0 {
		return 0, errors.New("version must be a positive integer within platform range")
	}
	return int(n), nil
}

// Create preserves input SQL and refuses to replace any existing migration version.
func Create(opts CreateOptions) (report Report, err error) {
	report = newReport("create")
	if strings.TrimSpace(opts.Name) == "" {
		return report, errors.New("migration name is required")
	}
	name := strings.Trim(namePattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(opts.Name)), "_"), "_")
	if name == "" {
		name = "migration"
	}
	if len(name) > 100 {
		return report, errors.New("migration name exceeds 100 characters")
	}
	version := opts.Version
	if version == "" {
		now := opts.Time
		if now.IsZero() {
			now = time.Now().UTC()
		}
		version = now.Format("20060102150405")
	}
	n, err := ParseVersion(version)
	if err != nil {
		return report, err
	}
	up := []byte("-- write forward migration SQL here\n")
	down := []byte("-- write rollback migration SQL here\n")
	if opts.DDLFile != "" {
		report.Action = "gen"
		if opts.Dialect != "mysql" && opts.Dialect != "postgres" {
			return report, errors.New("dialect must be mysql or postgres")
		}
		inputRoot, err := openRoot(filepath.Dir(opts.DDLFile), false)
		if err != nil {
			return report, err
		}
		up, err = readSQL(inputRoot, filepath.Base(opts.DDLFile))
		closeErr := inputRoot.Close()
		if err = errors.Join(err, closeErr); err != nil {
			return report, err
		}
		down, err = initialDown(up, opts.Dialect)
		if err != nil {
			return report, err
		}
	}
	dir := opts.Dir
	if dir == "" {
		dir = "migrations"
	}
	root, err := openRoot(dir, true)
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, err := root.OpenFile(".gofly-migration.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return report, errors.New("migration directory is locked; ensure no writer is running before removing .gofly-migration.lock")
	}
	defer func() { err = errors.Join(err, lock.Close(), root.Remove(".gofly-migration.lock")) }()
	existing, err := loadRoot(root)
	if err != nil {
		return report, err
	}
	for _, entry := range existing {
		if entry.Version == n {
			return report, fmt.Errorf("migration version %d already exists", n)
		}
	}
	base := version + "_" + name
	written := []string{}
	defer func() {
		if err != nil {
			for _, path := range written {
				err = errors.Join(err, root.Remove(path))
			}
		}
	}()
	for _, file := range []struct {
		name string
		data []byte
	}{{base + ".up.sql", up}, {base + ".down.sql", down}} {
		f, openErr := root.OpenFile(file.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if openErr != nil {
			return report, fmt.Errorf("create migration file: %w", openErr)
		}
		written = append(written, file.name)
		_, writeErr := f.Write(file.data)
		syncErr := f.Sync()
		if err = errors.Join(writeErr, syncErr, f.Close()); err != nil {
			return report, fmt.Errorf("write migration file: %w", err)
		}
		report.Files = append(report.Files, filepath.Join(dir, file.name))
	}
	return report, nil
}

// Validate checks names, versions, pairs, symlinks and file sizes without connecting to a database.
func Validate(dir string) (report Report, err error) {
	report = newReport("validate")
	root, err := openRoot(dir, false)
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, err := root.OpenFile(".gofly-migration.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return report, errors.New("migration snapshot is locked or directory is not writable")
	}
	defer func() { err = errors.Join(err, lock.Close(), root.Remove(".gofly-migration.lock")) }()
	report.Migrations, err = loadRoot(root)
	return report, err
}

func loadRoot(root *os.Root) ([]Entry, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	files, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return nil, err
	}
	entries := map[int]*Entry{}
	total := 0
	for _, file := range files {
		name := file.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		match := filePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q; expected <version>_<name>.up.sql or .down.sql", name)
		}
		version, err := ParseVersion(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid migration filename %q: %w", name, err)
		}
		data, err := readSQL(root, name)
		if err != nil {
			return nil, err
		}
		total += len(data)
		if total > maxCatalogBytes {
			return nil, errors.New("migration directory exceeds 64 MiB")
		}
		entry := entries[version]
		if entry == nil {
			entry = &Entry{Version: version, Name: match[2]}
			entries[version] = entry
		}
		if entry.Name != match[2] {
			return nil, fmt.Errorf("duplicate or mismatched migration version %d", version)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		if match[3] == "up" {
			if entry.Up != "" {
				return nil, fmt.Errorf("duplicate up migration %d", version)
			}
			entry.Up = name
			entry.UpSHA256 = hash
			entry.upSQL = data
		} else {
			if entry.Down != "" {
				return nil, fmt.Errorf("duplicate down migration %d", version)
			}
			entry.Down = name
			entry.DownSHA256 = hash
			entry.downSQL = data
		}
	}
	result := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Up == "" || entry.Down == "" {
			return nil, fmt.Errorf("migration %d requires matching up and down files", entry.Version)
		}
		result = append(result, *entry)
	}
	slices.SortFunc(result, func(a, b Entry) int {
		if a.Version < b.Version {
			return -1
		}
		if a.Version > b.Version {
			return 1
		}
		return 0
	})
	return result, nil
}

func readSQL(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SQL input %q must be a regular file, not a symlink", name)
	}
	f, err := openSQLFile(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("SQL file changed while opening snapshot")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSQLBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSQLBytes {
		return nil, fmt.Errorf("SQL input %q exceeds 8 MiB", name)
	}
	return data, nil
}

// openRoot walks from the filesystem root, rejecting symlinks at each user-selected
// directory component. Darwin's OS-owned temporary-directory alias is normalized first.
func openRoot(path string, create bool) (*os.Root, error) {
	if path == "" {
		path = "migrations"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "darwin" {
		tmp := filepath.Clean(os.TempDir())
		rel, e := filepath.Rel(tmp, abs)
		if e == nil && (rel == "." || filepath.IsLocal(rel)) {
			canonical, e := filepath.EvalSymlinks(tmp)
			if e != nil {
				return nil, e
			}
			abs = filepath.Join(canonical, rel)
		}
	}
	volume := filepath.VolumeName(abs)
	root, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, volume+string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		info, e := root.Lstat(part)
		if errors.Is(e, os.ErrNotExist) && create {
			e = root.Mkdir(part, 0o755)
			if e == nil || errors.Is(e, os.ErrExist) {
				info, e = root.Lstat(part)
			}
		}
		if e != nil {
			return nil, errors.Join(e, root.Close())
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.Join(fmt.Errorf("migration directory component %q must not be a symlink", part), root.Close())
		}
		next, e := root.OpenRoot(part)
		if e == nil {
			opened, statErr := next.Stat(".")
			after, pathErr := root.Lstat(part)
			e = errors.Join(statErr, pathErr)
			if e == nil && (!after.IsDir() || !os.SameFile(info, opened) || !os.SameFile(info, after)) {
				e = errors.New("migration directory changed while opening")
			}
		}
		closeErr := root.Close()
		if e = errors.Join(e, closeErr); e != nil {
			if next != nil {
				e = errors.Join(e, next.Close())
			}
			return nil, e
		}
		root = next
	}
	return root, nil
}
