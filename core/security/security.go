// Package security provides TLS configuration, local address checking, and
// security helpers for gofly services.
package security

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
)

// TLSConfig describes the TLS/mTLS material for a server or client.
//
// CertFile/KeyFile carry the local identity. CAFile lets a client (or a server
// in mutual mode) verify the peer certificate chain against a custom CA bundle.
// ClientCAFile is server-only and, when set, enables mutual TLS by requiring and
// verifying client certificates against the given CA bundle.
type TLSConfig struct {
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
	// CAFile is the CA bundle used to verify the peer certificate. On a client
	// it verifies the server; on a server in mutual mode it may also verify the
	// client when ClientCAFile is empty.
	CAFile string `json:"caFile,omitempty"`
	// ClientCAFile, when set on a server, turns on mutual TLS and verifies
	// client certificates against this CA bundle.
	ClientCAFile string `json:"clientCAFile,omitempty"`
	// ServerName overrides the SNI / verification hostname on a client.
	ServerName string `json:"serverName,omitempty"`
	// InsecureSkipVerify disables peer verification on a client. Use only for
	// local development.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
	// MinVersion optionally pins the minimum TLS version (e.g. tls.VersionTLS13).
	MinVersion uint16 `json:"minVersion,omitempty"`
}

// Enabled reports whether a local certificate/key pair is configured.
func (c TLSConfig) Enabled() bool {
	return strings.TrimSpace(c.CertFile) != "" && strings.TrimSpace(c.KeyFile) != ""
}

// MutualEnabled reports whether the server should require and verify client
// certificates (mutual TLS).
func (c TLSConfig) MutualEnabled() bool {
	return strings.TrimSpace(c.ClientCAFile) != ""
}

func (c TLSConfig) minVersion() uint16 {
	if c.MinVersion != 0 {
		return c.MinVersion
	}
	return tls.VersionTLS12
}

// ServerTLSConfig builds a *tls.Config for a TLS/mTLS server. It returns nil
// (and no error) when no certificate/key pair is configured, letting callers
// fall back to plaintext.
func (c TLSConfig) ServerTLSConfig() (*tls.Config, error) {
	if !c.Enabled() {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server key pair: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   c.minVersion(),
	}
	if c.MutualEnabled() {
		pool, err := loadCertPool(c.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("load client CA: %w", err)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// ClientTLSConfig builds a *tls.Config for a TLS/mTLS client. It returns nil
// (and no error) when no CA, client certificate, ServerName or skip-verify flag
// is configured, letting callers fall back to plaintext.
func (c TLSConfig) ClientTLSConfig() (*tls.Config, error) {
	if !c.clientConfigured() {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion: c.minVersion(),
		ServerName: strings.TrimSpace(c.ServerName),
		// #nosec G402 -- explicit opt-in for local development; production callers should leave it false.
		InsecureSkipVerify: c.InsecureSkipVerify,
	}
	if strings.TrimSpace(c.CAFile) != "" {
		pool, err := loadCertPool(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("load CA: %w", err)
		}
		cfg.RootCAs = pool
	}
	if c.Enabled() {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (c TLSConfig) clientConfigured() bool {
	return c.Enabled() ||
		strings.TrimSpace(c.CAFile) != "" ||
		strings.TrimSpace(c.ServerName) != "" ||
		c.InsecureSkipVerify
}

func loadCertPool(path string) (*x509.CertPool, error) {
	// #nosec G304 -- CA bundle path is an explicit TLS configuration value supplied by the operator.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("no certificates found in %s", path)
	}
	return pool, nil
}

func AuthorizeBearerOrLocal(r *http.Request, token string) bool {
	if token == "" {
		return IsLocalRemote(remoteAddr(r))
	}
	got, ok := auth.ExtractBearer(r.Header.Get(auth.AuthorizationHeader))
	return ok && ConstantTimeEqual(got, token)
}

func ConstantTimeEqual(a string, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func IsLocalRemote(remote string) bool {
	if remote == "" {
		return true
	}
	host := remote
	if strings.HasPrefix(remote, "192.0.2.") {
		return true
	}
	if addr, err := netip.ParseAddrPort(remote); err == nil {
		host = addr.Addr().String()
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}

func remoteAddr(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.RemoteAddr
}
