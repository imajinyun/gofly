// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

const placeholderAdminToken = "change-me-admin-token"

// ValidateProductionConfig rejects runtime settings that are acceptable for
// local development but unsafe for production deployments.
func ValidateProductionConfig(conf Config) error {
	var errs []error
	if err := ValidateProductionProfileConfig(conf.Profile); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func ValidateProductionProfileConfig(conf ProfileConfig) error {
	if !conf.Enabled {
		return nil
	}
	var errs []error
	token := strings.TrimSpace(conf.Token)
	if tokenLooksUnsafe(token) {
		errs = append(errs, errors.New("production profile server requires a non-placeholder token"))
	}
	if conf.AllowRemote && tokenLooksUnsafe(token) {
		errs = append(errs, errors.New("production profile server cannot allow remote access without a token"))
	}
	if bindsAllInterfaces(conf.Addr) && tokenLooksUnsafe(token) {
		errs = append(errs, fmt.Errorf("production profile server on %q requires a token", conf.Addr))
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

func bindsAllInterfaces(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	return host == "" || host == "0.0.0.0" || host == "::" || host == "[::]"
}
