package rest

import (
	"strings"
	"testing"

	"github.com/imajinyun/gofly/core/security"
)

func TestValidateProductionConfigRejectsDangerousDefaults(t *testing.T) {
	tests := []struct {
		name string
		conf Config
		want string
	}{
		{name: "admin without token", conf: Config{Admin: AdminConfig{Enabled: true}}, want: "admin requires"},
		{name: "admin placeholder token", conf: Config{Admin: AdminConfig{Enabled: true, Token: "change-me-admin-token"}}, want: "admin requires"},
		{name: "pprof without token", conf: Config{Admin: AdminConfig{Enabled: true, Pprof: true}}, want: "pprof requires"},
		{name: "tls insecure", conf: Config{TLS: securityTLSConfigInsecure()}, want: "insecureSkipVerify"},
		{name: "public metrics", conf: Config{Host: "0.0.0.0", Middlewares: MiddlewaresConfig{Health: true, Metrics: true}}, want: "metrics cannot be exposed"},
		{name: "csrf default secret", conf: Config{Middlewares: MiddlewaresConfig{CSRF: &CSRFConfig{Secret: []byte(DefaultDevelopmentCSRFSecret), Secure: true, HTTPOnly: true}}}, want: "csrf requires"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProductionConfig(tt.conf)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateProductionConfig() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateProductionConfigAllowsHardenedConfig(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	err := ValidateProductionConfig(Config{
		Host:        "127.0.0.1",
		Admin:       AdminConfig{Enabled: true, Pprof: true, Token: "prod-secret-token"},
		Middlewares: MiddlewaresConfig{Health: true, Metrics: true, CSRF: &CSRFConfig{Secret: secret, Secure: true, HTTPOnly: true}},
	})
	if err != nil {
		t.Fatalf("ValidateProductionConfig() = %v, want nil", err)
	}
}

func securityTLSConfigInsecure() security.TLSConfig {
	return security.TLSConfig{InsecureSkipVerify: true}
}
