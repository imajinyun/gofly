// Package config provides layered configuration loading with support for JSON,
// YAML, environment variables, remote sources, and validation.
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	core "github.com/imajinyun/gofly/core"

	"go.yaml.in/yaml/v2"
)

type Validator[T any] func(T) error

type ManagerOption[T any] func(*Manager[T])

type LoadOption func(*loadOptions)

type loadOptions struct {
	expandEnv  bool
	strict     bool
	validators []func(any) error
}

// WithEnvExpansion expands ${VAR} and $VAR placeholders before decoding the
// configuration file. It is opt-in so literal dollar strings remain backwards
// compatible for existing callers.
func WithEnvExpansion() LoadOption {
	return func(o *loadOptions) {
		o.expandEnv = true
	}
}

// WithStrictFields rejects unknown fields for JSON configuration files. YAML
// decoding currently follows the yaml package behaviour and ignores unknown
// fields to keep YAML support dependency-light.
func WithStrictFields() LoadOption {
	return func(o *loadOptions) {
		o.strict = true
	}
}

func WithLoadValidator[T any](validator Validator[T]) LoadOption {
	return func(o *loadOptions) {
		if validator == nil {
			return
		}
		o.validators = append(o.validators, func(v any) error {
			value, ok := v.(*T)
			if !ok || value == nil {
				return fmt.Errorf("config validator expects *%T, got %T", *new(T), v)
			}
			return validator(*value)
		})
	}
}

// Snapshot is a point-in-time view of a loaded configuration.
type Snapshot[T any] struct {
	Path        string    `json:"path"`
	Version     int64     `json:"version"`
	LoadedAt    time.Time `json:"loadedAt"`
	LastError   string    `json:"lastError,omitempty"`
	Config      T         `json:"config"`
	Subscribers int       `json:"subscribers"`
}

// Manager loads and watches configuration from a provider.
type Manager[T any] struct {
	mu          sync.RWMutex
	path        string
	provider    Provider[T]
	value       T
	version     int64
	loadedAt    time.Time
	lastError   string
	validator   Validator[T]
	loadOptions []LoadOption
	subscribers map[chan T]struct{}
}

// WithValidator sets a validation function for the Manager.
func WithValidator[T any](validator Validator[T]) ManagerOption[T] {
	return func(m *Manager[T]) {
		m.validator = validator
	}
}

// WithLoadOptions appends load options to the Manager.
func WithLoadOptions[T any](opts ...LoadOption) ManagerOption[T] {
	return func(m *Manager[T]) {
		m.loadOptions = append(m.loadOptions, opts...)
	}
}

// NewManager creates a Manager that loads configuration from the given file path.
func NewManager[T any](path string, opts ...ManagerOption[T]) (*Manager[T], error) {
	m := &Manager[T]{path: path, subscribers: make(map[chan T]struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if err := m.Reload(context.Background()); err != nil {
		return nil, err
	}
	return m, nil
}

// NewManagerFromProvider creates a Manager from a custom Provider.
func NewManagerFromProvider[T any](provider Provider[T], opts ...ManagerOption[T]) (*Manager[T], error) {
	if provider == nil {
		return nil, fmt.Errorf("config provider is nil")
	}
	m := &Manager[T]{path: "provider", provider: provider, subscribers: make(map[chan T]struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if err := m.Reload(context.Background()); err != nil {
		return nil, err
	}
	return m, nil
}

// Load reads a JSON or YAML configuration file into v.
func Load(path string, v any, opts ...LoadOption) error {
	// #nosec G304 -- config files are explicit operator/caller-provided paths, not request-derived file names.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var options loadOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.expandEnv {
		data = []byte(os.ExpandEnv(string(data)))
	}
	if err := decodeConfig(path, data, v, options); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	for _, validator := range options.validators {
		if err := validator(v); err != nil {
			return fmt.Errorf("validate config: %w", err)
		}
	}
	return nil
}

func decodeConfig(path string, data []byte, v any, opts loadOptions) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", "":
		decoder := json.NewDecoder(bytes.NewReader(data))
		if opts.strict {
			decoder.DisallowUnknownFields()
		}
		return decoder.Decode(v)
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, v)
	case ".toml":
		normalized, err := parseSimpleTOML(data)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		if opts.strict {
			decoder.DisallowUnknownFields()
		}
		return decoder.Decode(v)
	default:
		return fmt.Errorf("unsupported config format %q", filepath.Ext(path))
	}
}

func parseSimpleTOML(data []byte) (map[string]any, error) {
	root := make(map[string]any)
	var section []string
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			if name == "" {
				return nil, fmt.Errorf("toml line %d: empty section", lineNo+1)
			}
			section = splitTOMLPath(name)
			continue
		}
		key, valueRaw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("toml line %d: expected key = value", lineNo+1)
		}
		keyPath := append(append([]string(nil), section...), splitTOMLPath(strings.TrimSpace(key))...)
		if len(keyPath) == 0 {
			return nil, fmt.Errorf("toml line %d: empty key", lineNo+1)
		}
		value, err := parseTOMLValue(strings.TrimSpace(valueRaw))
		if err != nil {
			return nil, fmt.Errorf("toml line %d: %w", lineNo+1, err)
		}
		if err := setTOMLValue(root, keyPath, value); err != nil {
			return nil, fmt.Errorf("toml line %d: %w", lineNo+1, err)
		}
	}
	return root, nil
}

func stripTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case r == '#' && !inString:
			return line[:i]
		}
	}
	return line
}

func splitTOMLPath(path string) []string {
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(strings.Trim(part, `"`)); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseTOMLValue(raw string) (any, error) {
	if raw == "" {
		return nil, errors.New("empty value")
	}
	if strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
		return strconv.Unquote(raw)
	}
	switch strings.ToLower(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return parseTOMLArray(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")))
	}
	if strings.ContainsAny(raw, ".eE") {
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			return value, nil
		}
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return value, nil
	}
	return nil, fmt.Errorf("unsupported value %q", raw)
}

func parseTOMLArray(raw string) ([]any, error) {
	if raw == "" {
		return []any{}, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		value, err := parseTOMLValue(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func setTOMLValue(root map[string]any, path []string, value any) error {
	current := root
	for _, part := range path[:len(path)-1] {
		next, ok := current[part]
		if !ok {
			child := make(map[string]any)
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("key %q conflicts with scalar value", part)
		}
		current = child
	}
	key := path[len(path)-1]
	if _, exists := current[key]; exists {
		return fmt.Errorf("key %q is duplicated", strings.Join(path, "."))
	}
	current[key] = value
	return nil
}

func Watch[T any](ctx context.Context, path string, interval time.Duration, onChange func(T), opts ...LoadOption) error {
	ctx = core.Context(ctx)
	if interval <= 0 {
		interval = time.Second
	}
	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	lastMod := stat.ModTime()
	lastSize := stat.Size()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			stat, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("stat config: %w", err)
			}
			if stat.ModTime().Equal(lastMod) && stat.Size() == lastSize {
				continue
			}
			var next T
			if err := Load(path, &next, opts...); err != nil {
				return err
			}
			lastMod = stat.ModTime()
			lastSize = stat.Size()
			if onChange != nil {
				onChange(next)
			}
		}
	}
}

func (m *Manager[T]) Current() T {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.value
}

func (m *Manager[T]) Snapshot() Snapshot[T] {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot[T]{
		Path:        m.path,
		Version:     m.version,
		LoadedAt:    m.loadedAt,
		LastError:   m.lastError,
		Config:      m.value,
		Subscribers: len(m.subscribers),
	}
}

func (m *Manager[T]) Subscribe(buffer int) (<-chan T, func()) {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan T, buffer)
	m.mu.Lock()
	m.subscribers[ch] = struct{}{}
	current := m.value
	m.mu.Unlock()
	select {
	case ch <- current:
	default:
	}
	return ch, func() {
		m.mu.Lock()
		if _, ok := m.subscribers[ch]; ok {
			delete(m.subscribers, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
}

func (m *Manager[T]) Reload(ctx context.Context) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	next, err := m.load(ctx)
	if err != nil {
		m.setLastError(err)
		return err
	}
	return m.apply(next)
}

func (m *Manager[T]) apply(next T) error {
	if m.validator != nil {
		if err := m.validator(next); err != nil {
			wrapped := fmt.Errorf("validate config: %w", err)
			m.setLastError(wrapped)
			return wrapped
		}
	}
	m.mu.Lock()
	m.value = next
	m.version++
	m.loadedAt = time.Now()
	m.lastError = ""
	for ch := range m.subscribers {
		select {
		case ch <- next:
		default:
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager[T]) load(ctx context.Context) (T, error) {
	if m.provider != nil {
		return m.provider.Load(ctx)
	}
	var next T
	err := Load(m.path, &next, m.loadOptions...)
	return next, err
}

func (m *Manager[T]) Watch(ctx context.Context, interval time.Duration) error {
	ctx = core.Context(ctx)
	if interval <= 0 {
		interval = time.Second
	}
	if m.provider != nil {
		if watcher, ok := m.provider.(IntervalWatchProvider[T]); ok {
			return watcher.WatchInterval(ctx, interval, func(next T) {
				if err := m.apply(next); err != nil {
					m.setLastError(err)
				}
			})
		}
		if watcher, ok := m.provider.(WatchProvider[T]); ok {
			return watcher.Watch(ctx, func(next T) {
				if err := m.apply(next); err != nil {
					m.setLastError(err)
				}
			})
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				_ = m.Reload(ctx)
			}
		}
	}
	stat, err := os.Stat(m.path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	lastMod := stat.ModTime()
	lastSize := stat.Size()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			stat, err := os.Stat(m.path)
			if err != nil {
				m.setLastError(err)
				return fmt.Errorf("stat config: %w", err)
			}
			if stat.ModTime().Equal(lastMod) && stat.Size() == lastSize {
				continue
			}
			if err := m.Reload(ctx); err == nil {
				lastMod = stat.ModTime()
				lastSize = stat.Size()
			}
		}
	}
}

func (m *Manager[T]) setLastError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.lastError = ""
		return
	}
	m.lastError = err.Error()
}
