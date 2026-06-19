package app

import (
	"strings"
	"testing"
)

func TestValidateProductionProfileConfigRejectsUnsafeExposure(t *testing.T) {
	tests := []struct {
		name string
		conf ProfileConfig
		want string
	}{
		{name: "empty token", conf: ProfileConfig{Enabled: true}, want: "requires a non-placeholder token"},
		{name: "placeholder token", conf: ProfileConfig{Enabled: true, Token: "change-me-admin-token"}, want: "requires a non-placeholder token"},
		{name: "remote without token", conf: ProfileConfig{Enabled: true, AllowRemote: true}, want: "cannot allow remote access without a token"},
		{name: "public listener without token", conf: ProfileConfig{Enabled: true, Addr: "0.0.0.0:6060"}, want: "requires a token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProductionProfileConfig(tt.conf)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateProductionProfileConfig() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateProductionProfileConfigAllowsTokenProtectedProfile(t *testing.T) {
	if err := ValidateProductionProfileConfig(ProfileConfig{Enabled: true, AllowRemote: true, Addr: "0.0.0.0:6060", Token: "prod-secret-token"}); err != nil {
		t.Fatalf("ValidateProductionProfileConfig() = %v, want nil", err)
	}
}

func TestValidateProductionConfigDelegatesProfileValidation(t *testing.T) {
	if err := ValidateProductionConfig(Config{Profile: ProfileConfig{Enabled: false}}); err != nil {
		t.Fatalf("ValidateProductionConfig(disabled profile) = %v, want nil", err)
	}

	err := ValidateProductionConfig(Config{Profile: ProfileConfig{Enabled: true, AllowRemote: true}})
	if err == nil || !strings.Contains(err.Error(), "production profile server") {
		t.Fatalf("ValidateProductionConfig(unsafe profile) = %v, want production profile error", err)
	}
}
