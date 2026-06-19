package rest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/security"
)

func TestRESTClientMutualTLSRoundTrip(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := restTLSCA(t, dir)
	caFile := filepath.Join(dir, "ca.crt")
	serverCert, serverKey := restTLSLeaf(t, dir, "server", caCert, caKey)
	clientCert, clientKey := restTLSLeaf(t, dir, "client", caCert, caKey)

	tlsCfg, err := (security.TLSConfig{CertFile: serverCert, KeyFile: serverKey, ClientCAFile: caFile}).ServerTLSConfig()
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = tlsCfg
	server.StartTLS()
	t.Cleanup(server.Close)

	noCert := MustNewClient(server.URL, WithClientTLS(security.TLSConfig{CAFile: caFile, ServerName: "svc"}))
	if resp, err := noCert.Get(context.Background(), "/"); err == nil {
		closeResponseBody(resp)
		t.Fatal("expected mutual TLS to reject client without certificate")
	}

	mtls := MustNewClient(server.URL, WithClientTLS(security.TLSConfig{
		CAFile:     caFile,
		CertFile:   clientCert,
		KeyFile:    clientKey,
		ServerName: "svc",
	}))
	resp, err := mtls.Get(context.Background(), "/")
	if err != nil {
		t.Fatalf("mutual TLS request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestRESTClientTLSConfigError(t *testing.T) {
	_, err := NewClient("https://example.com", WithClientTLS(security.TLSConfig{CAFile: filepath.Join(t.TempDir(), "missing.pem")}))
	if err == nil || !strings.Contains(err.Error(), "configure rest tls") {
		t.Fatalf("error = %v, want configure rest tls", err)
	}
}

func restTLSCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "rest-test-ca"},
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
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	writeRESTTestPEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", der)
	return cert, key
}

func restTLSLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
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
	writeRESTTestPEM(t, certFile, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	writeRESTTestPEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func writeRESTTestPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
