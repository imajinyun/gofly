package config

import (
	"testing"
	"time"
)

func TestIdentityStoreConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    IdentityStoreConfig
		setDSN    bool
		wantError bool
	}{
		{name: "disabled store", config: IdentityStoreConfig{}},
		{name: "enabled requires mysql driver", config: IdentityStoreConfig{Enabled: true, Driver: "postgres", DSNEnv: "GOSKY_MYSQL_DSN"}, wantError: true},
		{name: "enabled requires DSN environment", config: IdentityStoreConfig{Enabled: true, Driver: "mysql"}, wantError: true},
		{name: "enabled requires DSN value", config: IdentityStoreConfig{Enabled: true, Driver: "mysql", DSNEnv: "GOSKY_MYSQL_DSN"}, wantError: true},
		{name: "refresh must exceed access lifetime", config: IdentityStoreConfig{Enabled: true, Driver: "mysql", DSNEnv: "GOSKY_MYSQL_DSN", AccessTTL: time.Hour, RefreshTTL: time.Hour}, setDSN: true, wantError: true},
		{name: "enabled MySQL store", config: IdentityStoreConfig{Enabled: true, Driver: "mysql", DSNEnv: "GOSKY_MYSQL_DSN", AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour}, setDSN: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOSKY_MYSQL_DSN", "")
			if tt.setDSN {
				t.Setenv("GOSKY_MYSQL_DSN", "gosky:secret@tcp(127.0.0.1:3306)/gosky?parseTime=true")
			}
			err := ValidateIdentityStoreConfig(tt.config)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateIdentityStoreConfig(%#v) error = %v, wantError=%t", tt.config, err, tt.wantError)
			}
		})
	}
}
