// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
)

const placeholderAdminToken = "change-me-admin-token"

// ValidateProductionConfig rejects unsafe REST settings for production.
func ValidateProductionConfig(conf Config) error {
	var errs []error
	if conf.Admin.Enabled {
		if tokenLooksUnsafe(conf.Admin.Token) {
			errs = append(errs, errors.New("production rest admin requires a non-placeholder token"))
		}
		if conf.Admin.Pprof && tokenLooksUnsafe(conf.Admin.Token) {
			errs = append(errs, errors.New("production rest admin pprof requires a token"))
		}
	}
	if conf.TLS.InsecureSkipVerify {
		errs = append(errs, errors.New("production rest tls cannot enable insecureSkipVerify"))
	}
	if conf.Middlewares.Health && conf.Middlewares.Metrics && bindsPublicInterface(conf.Host) {
		errs = append(errs, fmt.Errorf("production rest metrics cannot be exposed on public listener %q", conf.Host))
	}
	if conf.Middlewares.CSRF != nil {
		if err := ValidateProductionCSRFConfig(*conf.Middlewares.CSRF); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func ValidateProductionCSRFConfig(conf CSRFConfig) error {
	var errs []error
	secret := bytes.TrimSpace(conf.Secret)
	if len(secret) == 0 || bytes.Equal(secret, []byte(DefaultDevelopmentCSRFSecret)) {
		errs = append(errs, errors.New("production csrf requires a non-development secret"))
	}
	if len(secret) > 0 && len(secret) < 32 {
		errs = append(errs, errors.New("production csrf secret must be at least 32 bytes"))
	}
	if !conf.Secure {
		errs = append(errs, errors.New("production csrf cookie must be secure"))
	}
	if !conf.HTTPOnly {
		errs = append(errs, errors.New("production csrf cookie must be httpOnly"))
	}
	return errors.Join(errs...)
}

func tokenLooksUnsafe(token string) bool {
	token = strings.TrimSpace(strings.ToLower(token))
	return token == "" ||
		token == placeholderAdminToken ||
		strings.Contains(token, "change-me") ||
		strings.Contains(token, "changeme") ||
		strings.Contains(token, "placeholder")
}

func bindsPublicInterface(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return true
	}
	host = strings.Trim(host, "[]")
	if host == "0.0.0.0" || host == "::" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsLoopback()
}
