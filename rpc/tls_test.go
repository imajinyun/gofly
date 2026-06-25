package rpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/security"
)

func TestHTTPClientMutualTLSRoundTrip(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := rpcTLSCA(t, dir)
	caFile := filepath.Join(dir, "ca.crt")
	serverCert, serverKey := rpcTLSLeaf(t, dir, "server", caCert, caKey)
	clientCert, clientKey := rpcTLSLeaf(t, dir, "client", caCert, caKey)

	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := (security.TLSConfig{CertFile: serverCert, KeyFile: serverKey, ClientCAFile: caFile}).ServerTLSConfig()
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	ts := httptest.NewUnstartedServer(s)
	ts.TLS = tlsCfg
	ts.StartTLS()
	t.Cleanup(ts.Close)

	noCert, err := NewClient(ts.URL, WithClientTLS(security.TLSConfig{CAFile: caFile, ServerName: "svc"}))
	if err != nil {
		t.Fatalf("client without cert: %v", err)
	}
	var resp helloResp
	if err := noCert.Call(context.Background(), "greeter/SayHello", helloReq{Name: "client"}, &resp); err == nil {
		t.Fatal("expected mutual TLS to reject client without certificate")
	}

	mtls, err := NewClient(ts.URL, WithClientTLS(security.TLSConfig{
		CAFile:     caFile,
		CertFile:   clientCert,
		KeyFile:    clientKey,
		ServerName: "svc",
	}))
	if err != nil {
		t.Fatalf("client with cert: %v", err)
	}
	if err := mtls.Call(context.Background(), "greeter/SayHello", helloReq{Name: "client"}, &resp); err != nil {
		t.Fatalf("mutual TLS rpc call: %v", err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
	}
}

func TestHTTPClientStreamMutualTLSRoundTrip(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := rpcTLSCA(t, dir)
	caFile := filepath.Join(dir, "ca.crt")
	serverCert, serverKey := rpcTLSLeaf(t, dir, "server", caCert, caKey)
	clientCert, clientKey := rpcTLSLeaf(t, dir, "client", caCert, caKey)

	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Echo",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			var req helloReq
			if err := stream.Recv(&req); err != nil {
				return err
			}
			return stream.Send(helloResp{Message: "hello " + req.Name})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := (security.TLSConfig{CertFile: serverCert, KeyFile: serverKey, ClientCAFile: caFile}).ServerTLSConfig()
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	ts := httptest.NewUnstartedServer(s)
	ts.TLS = tlsCfg
	ts.StartTLS()
	t.Cleanup(ts.Close)

	noCert, err := NewClient(ts.URL, WithClientTLS(security.TLSConfig{CAFile: caFile, ServerName: "svc"}))
	if err != nil {
		t.Fatalf("client without cert: %v", err)
	}
	if _, err := noCert.Stream(context.Background(), "chat/Echo"); err == nil {
		t.Fatal("expected mutual TLS stream to reject client without certificate")
	}

	mtls, err := NewClient(ts.URL, WithClientTLS(security.TLSConfig{
		CAFile:     caFile,
		CertFile:   clientCert,
		KeyFile:    clientKey,
		ServerName: "svc",
	}))
	if err != nil {
		t.Fatalf("client with cert: %v", err)
	}
	stream, err := mtls.Stream(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("mutual TLS stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "client"}); err != nil {
		t.Fatalf("stream send: %v", err)
	}
	var resp helloResp
	if err := stream.Recv(&resp); err != nil {
		t.Fatalf("stream recv: %v", err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
	}
}

func rpcTLSCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "rpc-test-ca"},
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
	writeRPCTestPEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", der)
	return cert, key
}

func rpcTLSLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
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
	writeRPCTestPEM(t, certFile, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	writeRPCTestPEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func writeRPCTestPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
