package grpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofly/gofly/core/security"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestServerClientMutualTLSRoundTrip(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := tlsTestCA(t, dir)
	caFile := filepath.Join(dir, "ca.crt")
	serverCert, serverKey := tlsTestLeaf(t, dir, "server", caCert, caKey)
	clientCert, clientKey := tlsTestLeaf(t, dir, "client", caCert, caKey)

	server := NewServer(
		WithAddress("127.0.0.1:0"),
		WithServerTLS(security.TLSConfig{
			CertFile:     serverCert,
			KeyFile:      serverKey,
			ClientCAFile: caFile,
		}),
	)
	started := make(chan error, 1)
	go func() { started <- server.Start() }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	addr := waitForAddr(t, server)

	// A client without a certificate must be rejected by mutual TLS.
	noCert, err := Dial(context.Background(), addr, WithClientTLS(security.TLSConfig{
		CAFile:     caFile,
		ServerName: "svc",
	}))
	if err != nil {
		t.Fatalf("dial without client cert: %v", err)
	}
	if err := healthCheck(noCert); err == nil {
		t.Fatal("expected mutual TLS to reject client without certificate")
	}
	_ = noCert.Close()

	// A client presenting a valid certificate succeeds.
	mtls, err := Dial(context.Background(), addr, WithClientTLS(security.TLSConfig{
		CAFile:     caFile,
		CertFile:   clientCert,
		KeyFile:    clientKey,
		ServerName: "svc",
	}))
	if err != nil {
		t.Fatalf("dial with client cert: %v", err)
	}
	defer mtls.Close()
	if err := healthCheck(mtls); err != nil {
		t.Fatalf("mutual TLS health check: %v", err)
	}
}

func TestServerTLSConfigErrorSurfacedAtStart(t *testing.T) {
	server := NewServer(
		WithAddress("127.0.0.1:0"),
		WithServerTLS(security.TLSConfig{CertFile: "missing.crt", KeyFile: "missing.key"}),
	)
	if err := server.Start(); err == nil {
		t.Fatal("expected start to fail with missing cert files")
		_ = server.Shutdown(context.Background())
	}
}

func healthCheck(conn *ClientConn) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := healthpb.NewHealthClient(conn.Conn())
	_, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func waitForAddr(t *testing.T, server *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := server.Address(); addr != "" && addr != "127.0.0.1:0" {
			return addr
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for grpc server address")
	return ""
}

func tlsTestCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "grpc-test-ca"},
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
	writeTestPEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", der)
	return cert, key
}

func tlsTestLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
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
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	writeTestPEM(t, certFile, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	writeTestPEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func writeTestPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
