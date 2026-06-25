package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
)

func TestTLSConfigEnabled(t *testing.T) {
	if (TLSConfig{}).Enabled() {
		t.Fatal("empty tls config should be disabled")
	}
	if !(TLSConfig{CertFile: "server.crt", KeyFile: "server.key"}).Enabled() {
		t.Fatal("complete tls config should be enabled")
	}
}

func TestTLSConfigMutualEnabled(t *testing.T) {
	if (TLSConfig{CertFile: "a", KeyFile: "b"}).MutualEnabled() {
		t.Fatal("config without client CA should not be mutual")
	}
	if !(TLSConfig{ClientCAFile: "ca.pem"}).MutualEnabled() {
		t.Fatal("config with client CA should be mutual")
	}
}

func TestServerTLSConfig(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := writeCA(t, dir, "ca")
	certFile, keyFile := writeLeaf(t, dir, "server", caCert, caKey)

	// No cert/key -> nil config, no error.
	cfg, err := (TLSConfig{}).ServerTLSConfig()
	if err != nil || cfg != nil {
		t.Fatalf("empty server tls config = %v, %v", cfg, err)
	}

	// TLS only.
	cfg, err = (TLSConfig{CertFile: certFile, KeyFile: keyFile}).ServerTLSConfig()
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatal("expected one certificate")
	}
	if cfg.ClientAuth != tls.NoClientCert {
		t.Fatal("plain TLS should not require client certs")
	}

	// Mutual TLS.
	caFile := filepath.Join(dir, "ca.crt")
	cfg, err = (TLSConfig{CertFile: certFile, KeyFile: keyFile, ClientCAFile: caFile}).ServerTLSConfig()
	if err != nil {
		t.Fatalf("mutual server tls config: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatal("mutual TLS should require and verify client certs")
	}
	if cfg.ClientCAs == nil {
		t.Fatal("mutual TLS should set client CA pool")
	}
}

func TestClientTLSConfig(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := writeCA(t, dir, "ca")
	certFile, keyFile := writeLeaf(t, dir, "client", caCert, caKey)
	caFile := filepath.Join(dir, "ca.crt")

	// Nothing configured -> nil.
	cfg, err := (TLSConfig{}).ClientTLSConfig()
	if err != nil || cfg != nil {
		t.Fatalf("empty client tls config = %v, %v", cfg, err)
	}

	// CA + client identity (mTLS).
	cfg, err = (TLSConfig{CAFile: caFile, CertFile: certFile, KeyFile: keyFile, ServerName: "svc"}).ClientTLSConfig()
	if err != nil {
		t.Fatalf("client tls config: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("client should set root CA pool")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatal("client should present its certificate")
	}
	if cfg.ServerName != "svc" {
		t.Fatalf("server name = %q", cfg.ServerName)
	}

	// Skip verify only.
	cfg, err = (TLSConfig{InsecureSkipVerify: true}).ClientTLSConfig()
	if err != nil {
		t.Fatalf("skip-verify client tls config: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("skip verify should be set")
	}
}

func TestLoadCertPoolMissingFile(t *testing.T) {
	if _, err := (TLSConfig{CAFile: filepath.Join(t.TempDir(), "missing.pem")}).ClientTLSConfig(); err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func writeCA(t *testing.T, dir, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	cert, _ := x509.ParseCertificate(der)
	writePEM(t, filepath.Join(dir, name+".crt"), "CERTIFICATE", der)
	return cert, key
}

func writeLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"svc"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	writePEM(t, certFile, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	writePEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestAuthorizeBearerOrLocal(t *testing.T) {
	local := httptest.NewRequest("GET", "/admin", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	if !AuthorizeBearerOrLocal(local, "") {
		t.Fatal("local request without token should be allowed")
	}
	remote := httptest.NewRequest("GET", "/admin", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	if AuthorizeBearerOrLocal(remote, "") {
		t.Fatal("remote request without token should be denied")
	}
	remote.Header.Set(auth.AuthorizationHeader, auth.BearerValue("secret"))
	if !AuthorizeBearerOrLocal(remote, "secret") {
		t.Fatal("remote request with matching token should be allowed")
	}
	if AuthorizeBearerOrLocal(remote, "other") {
		t.Fatal("remote request with wrong token should be denied")
	}
}

func TestIsLocalRemote(t *testing.T) {
	if !IsLocalRemote("[::1]:6060") {
		t.Fatal("loopback ipv6 should be local")
	}
	if IsLocalRemote("203.0.113.10:6060") {
		t.Fatal("public address should not be local")
	}
}
