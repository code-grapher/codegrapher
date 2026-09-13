package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api#ac:insecure-remote-listeners-are-refused
func TestOpenAPIListenerRejectsInsecureRemoteExposure(t *testing.T) {
	for _, address := range []string{"0.0.0.0:7331", "[::]:7331", "127.0.0.2:7331", "192.0.2.10:7331", "daemon.example:7331"} {
		t.Run(address, func(t *testing.T) {
			listener, err := openAPIListener(apiListenerSettings{listenAddress: address})
			if listener != nil {
				_ = listener.Close()
				t.Fatal("insecure remote listener was created")
			}
			if err == nil || !strings.Contains(err.Error(), "require direct TLS") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBrowserCompatibleLoopbackAuthorities(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "127.0.0.1", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false", host)
		}
	}
	for _, host := range []string{"127.0.0.2", "0.0.0.0", "::", "daemon.example"} {
		if isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = true", host)
		}
	}
}

func TestOpenAPIListenerRejectsPartialTLSAndMismatchedPorts(t *testing.T) {
	certFile, keyFile, _ := writeTestCertificate(t, "localhost")
	for _, settings := range []apiListenerSettings{
		{listenAddress: "127.0.0.1:0", certificate: certFile},
		{listenAddress: "127.0.0.1:0", privateKey: keyFile},
		{listenAddress: "127.0.0.1:7443", certificate: certFile, privateKey: keyFile, publicAuthority: "localhost:8443"},
		{listenAddress: "127.0.0.1:0", certificate: certFile, privateKey: keyFile, publicAuthority: "localhost:8443"},
		{listenAddress: "127.0.0.1:0", publicAuthority: "localhost"},
	} {
		listener, err := openAPIListener(settings)
		if listener != nil {
			_ = listener.Close()
			t.Fatalf("invalid settings opened listener: %+v", settings)
		}
		if err == nil {
			t.Fatalf("invalid settings succeeded: %+v", settings)
		}
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api#ac:insecure-remote-listeners-are-refused
func TestOpenAPIListenerExternalTLSStaysOnLoopback(t *testing.T) {
	listener, err := openAPIListener(apiListenerSettings{
		listenAddress: "127.0.0.1:0", publicAuthority: "DAEMON.EXAMPLE:8443", externalTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if listener.scheme != "https" || listener.authority != "daemon.example:8443" {
		t.Fatalf("external TLS listener = %s://%s", listener.scheme, listener.authority)
	}
	if host, _, err := splitListenAddress(listener.Addr().String()); err != nil || !isLoopbackHost(host) {
		t.Fatalf("external TLS bind = %s, %v", listener.Addr(), err)
	}
	if insecure, err := openAPIListener(apiListenerSettings{
		listenAddress: "0.0.0.0:0", publicAuthority: "daemon.example", externalTLS: true,
	}); err == nil || insecure != nil {
		if insecure != nil {
			_ = insecure.Close()
		}
		t.Fatalf("non-loopback external termination = %+v, %v", insecure, err)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/secure-remote-browser-api#ac:direct-tls-authority-is-verified
func TestOpenAPIListenerDirectTLSMatchesAuthorityAndServesHTTPS(t *testing.T) {
	certFile, keyFile, cert := writeTestCertificate(t, "localhost")
	listener, err := openAPIListener(apiListenerSettings{
		listenAddress: "127.0.0.1:0", certificate: certFile, privateKey: keyFile, publicAuthority: "localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if listener.scheme != "https" || !strings.HasPrefix(listener.authority, "localhost:") {
		t.Fatalf("direct TLS listener = %s://%s", listener.scheme, listener.authority)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: testTLSConfig("localhost", roots)}}
	response, err := client.Get("https://" + listener.authority)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS status = %d", response.StatusCode)
	}
	_ = server.Shutdown(context.Background())
	<-served

	mismatch, err := openAPIListener(apiListenerSettings{
		listenAddress: "127.0.0.1:0", certificate: certFile, privateKey: keyFile, publicAuthority: "other.example",
	})
	if mismatch != nil {
		_ = mismatch.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("mismatched authority error = %v", err)
	}
}

func TestParsePublicAuthorityRejectsUnsafeForms(t *testing.T) {
	for _, authority := range []string{"https://daemon.example", "user@daemon.example", "daemon.example/path", "2001:db8::1", "daemon.example:0", " daemon.example", "daemon.example ", "daemon.example.", "127.0.0.01"} {
		if _, _, _, err := parsePublicAuthority(authority); err == nil {
			t.Errorf("parsePublicAuthority(%q) unexpectedly succeeded", authority)
		}
	}
}

func TestParsePublicAuthorityCanonicalizesBrowserRouteAuthority(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "DAEMON.EXAMPLE", want: "daemon.example"},
		{input: "DAEMON.EXAMPLE:8443", want: "daemon.example:8443"},
		{input: "daemon.example:443", want: "daemon.example"},
		{input: "127.0.0.1:8443", want: "127.0.0.1:8443"},
		{input: "[0:0:0:0:0:0:0:1]:8443", want: "[::1]:8443"},
	} {
		host, port, hasPort, err := parsePublicAuthority(test.input)
		if err != nil {
			t.Fatalf("parsePublicAuthority(%q): %v", test.input, err)
		}
		if got := canonicalPublicAuthority(host, port, hasPort); got != test.want {
			t.Errorf("canonicalPublicAuthority(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func testTLSConfig(serverName string, roots *x509.CertPool) *tls.Config {
	return &tls.Config{ServerName: serverName, RootCAs: roots, MinVersion: tls.VersionTLS12}
}

func writeTestCertificate(t *testing.T, host string) (string, string, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host},
		DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, cert
}
