// Package config provides layered configuration loading with file, environment
// and remote backends, plus validation hooks.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RawSource yields raw JSON for one layer of a layered configuration. Returning
// (nil, nil) signals an absent layer that should be skipped during the merge.
type RawSource interface {
	Raw(ctx context.Context) ([]byte, error)
}

// RawSourceFunc adapts a function to a RawSource.
type RawSourceFunc func(ctx context.Context) ([]byte, error)

func (f RawSourceFunc) Raw(ctx context.Context) ([]byte, error) { return f(ctx) }

// FileSource reads JSON from a file. When Optional is true a missing file is
// skipped instead of returning an error, which is what enables per-profile
// overlays that may not exist in every environment.
type FileSource struct {
	Path     string
	Optional bool
}

func (s FileSource) Raw(ctx context.Context) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if s.Optional && os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config %q: %w", s.Path, err)
	}
	return data, nil
}

// EnvSource reads a JSON document from an environment variable. An empty
// variable is skipped when Optional is true.
type EnvSource struct {
	Name     string
	Optional bool
}

func (s EnvSource) Raw(ctx context.Context) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	data := os.Getenv(s.Name)
	if data == "" {
		if s.Optional {
			return nil, nil
		}
		return nil, fmt.Errorf("environment variable %q is empty", s.Name)
	}
	return []byte(data), nil
}

// BytesSource serves a fixed JSON document, useful for defaults baked into the
// binary.
type BytesSource struct {
	Data []byte
}

func (s BytesSource) Raw(ctx context.Context) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	return append([]byte(nil), s.Data...), nil
}

// LayeredProvider deep-merges JSON from an ordered list of sources and decodes
// the result into T. Later sources override earlier ones; nested objects are
// merged recursively while scalars and arrays are replaced wholesale.
type LayeredProvider[T any] struct {
	Sources   []RawSource
	Validator Validator[T]
}

// NewLayeredProvider builds a LayeredProvider from the given sources, ordered
// from lowest to highest precedence.
func NewLayeredProvider[T any](sources ...RawSource) LayeredProvider[T] {
	out := make([]RawSource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			out = append(out, s)
		}
	}
	return LayeredProvider[T]{Sources: out}
}

// WithValidator returns a copy of the provider that validates the merged value.
func (p LayeredProvider[T]) WithValidator(v Validator[T]) LayeredProvider[T] {
	p.Validator = v
	return p
}

func (p LayeredProvider[T]) Load(ctx context.Context) (T, error) {
	var zero T
	if err := ctxErr(ctx); err != nil {
		return zero, err
	}
	merged := map[string]any{}
	any_ := false
	for _, source := range p.Sources {
		data, err := source.Raw(ctx)
		if err != nil {
			return zero, err
		}
		if len(data) == 0 {
			continue
		}
		var layer map[string]any
		if err := json.Unmarshal(data, &layer); err != nil {
			return zero, fmt.Errorf("decode config layer: %w", err)
		}
		merged = mergeMaps(merged, layer)
		any_ = true
	}
	if !any_ {
		return zero, fmt.Errorf("no config layers produced data")
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return zero, fmt.Errorf("encode merged config: %w", err)
	}
	var value T
	if err := json.Unmarshal(encoded, &value); err != nil {
		return zero, fmt.Errorf("decode merged config: %w", err)
	}
	if p.Validator != nil {
		if err := p.Validator(value); err != nil {
			return value, fmt.Errorf("validate config: %w", err)
		}
	}
	return value, nil
}

// mergeMaps returns a deep merge of dst and src, with src taking precedence.
// Nested map[string]any values are merged recursively; all other types replace.
func mergeMaps(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if existing, ok := out[k]; ok {
			existingMap, em := existing.(map[string]any)
			vMap, vm := v.(map[string]any)
			if em && vm {
				out[k] = mergeMaps(existingMap, vMap)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// ProfileOptions configures profile-based file layering.
type ProfileOptions struct {
	// Dir is the directory holding the config files.
	Dir string
	// Name is the base file name without extension (e.g. "config").
	Name string
	// Ext is the file extension including the dot (default ".json").
	Ext string
	// Profile is the active environment (e.g. "prod"). When empty only the base
	// file is loaded.
	Profile string
	// EnvVar, when set, supplies a final JSON overlay from an environment
	// variable (highest precedence).
	EnvVar string
}

// ActiveProfile resolves the profile from the explicit value, falling back to
// the GOFLY_PROFILE then APP_ENV environment variables.
func ActiveProfile(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, key := range []string{"GOFLY_PROFILE", "APP_ENV"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// NewProfileProvider builds a LayeredProvider that loads a base config file and
// overlays an optional per-profile file (e.g. config.json then
// config.prod.json) and an optional environment-variable overlay.
func NewProfileProvider[T any](opts ProfileOptions) LayeredProvider[T] {
	ext := opts.Ext
	if ext == "" {
		ext = ".json"
	}
	profile := ActiveProfile(opts.Profile)
	sources := []RawSource{
		FileSource{Path: filepath.Join(opts.Dir, opts.Name+ext)},
	}
	if profile != "" {
		sources = append(sources, FileSource{
			Path:     filepath.Join(opts.Dir, opts.Name+"."+profile+ext),
			Optional: true,
		})
	}
	if opts.EnvVar != "" {
		sources = append(sources, EnvSource{Name: opts.EnvVar, Optional: true})
	}
	return NewLayeredProvider[T](sources...)
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
