package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type layeredConfig struct {
	Name     string `json:"name"`
	Replicas int    `json:"replicas"`
	DB       struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"db"`
	Tags []string `json:"tags"`
}

func TestLayeredProviderDeepMerge(t *testing.T) {
	base := BytesSource{Data: []byte(`{"name":"svc","replicas":1,"db":{"host":"localhost","port":5432},"tags":["a"]}`)}
	override := BytesSource{Data: []byte(`{"replicas":3,"db":{"host":"prod-db"},"tags":["b","c"]}`)}

	p := NewLayeredProvider[layeredConfig](base, override)
	cfg, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "svc" {
		t.Fatalf("name = %q, want svc", cfg.Name)
	}
	if cfg.Replicas != 3 {
		t.Fatalf("replicas = %d, want 3", cfg.Replicas)
	}
	if cfg.DB.Host != "prod-db" {
		t.Fatalf("db.host = %q, want prod-db", cfg.DB.Host)
	}
	if cfg.DB.Port != 5432 {
		t.Fatalf("db.port = %d, want 5432 (preserved from base)", cfg.DB.Port)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[0] != "b" {
		t.Fatalf("tags = %v, want [b c] (array replaced)", cfg.Tags)
	}
}

func TestBytesSourceRawReturnsDefensiveCopy(t *testing.T) {
	source := BytesSource{Data: []byte(`{"name":"svc"}`)}
	data, err := source.Raw(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data[9] = 'X'

	again, err := source.Raw(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != `{"name":"svc"}` {
		t.Fatalf("BytesSource data mutated through Raw result: %q", again)
	}
}

func TestLayeredProviderOptionalSkipped(t *testing.T) {
	p := NewLayeredProvider[layeredConfig](
		BytesSource{Data: []byte(`{"name":"svc"}`)},
		FileSource{Path: "/nonexistent/override.json", Optional: true},
		EnvSource{Name: "LAYERED_MISSING_ENV", Optional: true},
	)
	cfg, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "svc" {
		t.Fatalf("name = %q, want svc", cfg.Name)
	}
}

func TestLayeredProviderRequiredMissing(t *testing.T) {
	p := NewLayeredProvider[layeredConfig](FileSource{Path: "/nonexistent/base.json"})
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("expected error for missing required file")
	}
}

func TestProfileProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"name":"svc","replicas":1,"db":{"host":"localhost","port":5432}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.prod.json"), []byte(`{"replicas":5,"db":{"host":"prod"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAYERED_OVERLAY", `{"name":"svc-prod"}`)

	p := NewProfileProvider[layeredConfig](ProfileOptions{
		Dir:     dir,
		Name:    "config",
		Profile: "prod",
		EnvVar:  "LAYERED_OVERLAY",
	})
	cfg, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "svc-prod" {
		t.Fatalf("name = %q, want svc-prod (env overlay)", cfg.Name)
	}
	if cfg.Replicas != 5 {
		t.Fatalf("replicas = %d, want 5 (profile)", cfg.Replicas)
	}
	if cfg.DB.Host != "prod" || cfg.DB.Port != 5432 {
		t.Fatalf("db = %+v, want host=prod port=5432", cfg.DB)
	}
}

func TestProfileProviderMissingProfileFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"name":"svc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewProfileProvider[layeredConfig](ProfileOptions{Dir: dir, Name: "config", Profile: "staging"})
	cfg, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "svc" {
		t.Fatalf("name = %q, want svc", cfg.Name)
	}
}

func TestActiveProfile(t *testing.T) {
	if got := ActiveProfile("explicit"); got != "explicit" {
		t.Fatalf("explicit = %q, want explicit", got)
	}
	t.Setenv("GOFLY_PROFILE", "dev")
	if got := ActiveProfile(""); got != "dev" {
		t.Fatalf("GOFLY_PROFILE = %q, want dev", got)
	}
}

func TestLayeredProviderWithValidator(t *testing.T) {
	p := NewLayeredProvider[layeredConfig](BytesSource{Data: []byte(`{"name":""}`)}).
		WithValidator(func(c layeredConfig) error {
			if c.Name == "" {
				return os.ErrInvalid
			}
			return nil
		})
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRawSourceFunc(t *testing.T) {
	fn := RawSourceFunc(func(ctx context.Context) ([]byte, error) {
		return []byte(`{"name":"func"}`), nil
	})
	data, err := fn.Raw(context.Background())
	if err != nil {
		t.Fatalf("Raw error: %v", err)
	}
	if string(data) != `{"name":"func"}` {
		t.Fatalf("Raw = %q, want func source", data)
	}
}

func TestLayeredProviderNilSourceSkipped(t *testing.T) {
	p := NewLayeredProvider[layeredConfig](nil, BytesSource{Data: []byte(`{"name":"svc"}`)})
	cfg, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "svc" {
		t.Fatalf("name = %q, want svc", cfg.Name)
	}
}

func TestLayeredProviderNoDataLayers(t *testing.T) {
	p := NewLayeredProvider[layeredConfig](
		FileSource{Path: "/nonexistent/optional.json", Optional: true},
	)
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("expected error when no layers produce data")
	}
}
