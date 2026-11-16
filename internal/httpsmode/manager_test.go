package httpsmode

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"vonet/internal/store"
)

func TestManagerAlwaysEnforcesHTTPSAndCertificate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dir, "vonet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	certDir := filepath.Join(dir, "data", "etc", "certs")
	manager, err := New(ctx, database, certDir, "0.0.0.0:17575")
	if err != nil {
		t.Fatal(err)
	}
	state := manager.State("")
	if !state.Enabled || state.CertificateSource != "self_signed" || state.Fingerprint == "" || state.NotAfter.IsZero() {
		t.Fatalf("initial state = %#v", state)
	}
	certificate, err := manager.CertificatePEM()
	if err != nil || len(certificate) == 0 {
		t.Fatalf("certificate = %d bytes, %v", len(certificate), err)
	}
	reloaded, err := New(ctx, database, certDir, "0.0.0.0:17575")
	if err != nil || !reloaded.Enabled() {
		t.Fatalf("reloaded manager enabled=%v error=%v", reloaded.Enabled(), err)
	}
	if _, err := reloaded.SetEnabled(ctx, false); err == nil {
		t.Fatal("HTTPS disable was accepted")
	}
}

func TestInstallCertificateValidatesAndPersistsPair(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dir, "vonet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	certDir := filepath.Join(dir, "data", "etc", "certs")
	manager, err := New(ctx, database, certDir, "127.0.0.1:17575")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := generateCertificate("127.0.0.1:17575")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.InstallCertificate(ctx, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled || state.CertificateSource != "uploaded" {
		t.Fatalf("uploaded state = %#v", state)
	}
	for _, name := range []string{"custom.crt", "custom.key"} {
		if _, err := os.Stat(filepath.Join(certDir, name)); err != nil {
			t.Fatalf("%s was not persisted: %v", name, err)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(certDir, "custom.key"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("custom.key permissions = %o, want owner-only", info.Mode().Perm())
		}
	}
	reloaded, err := New(ctx, database, certDir, "127.0.0.1:17575")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.State("").CertificateSource; got != "uploaded" {
		t.Fatalf("reloaded certificate source = %q, want uploaded", got)
	}
	if _, err := manager.InstallCertificate(ctx, []byte("not a certificate"), keyPEM); err == nil {
		t.Fatal("invalid certificate was accepted")
	}
	active, err := manager.CertificatePEM()
	if err != nil || !bytes.Equal(active, certPEM) {
		t.Fatalf("failed upload replaced active certificate: %v", err)
	}
}

func TestMultiplexerRoutesPlainAndTLS(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(dir, "vonet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager, err := New(ctx, database, filepath.Join(dir, "tls"), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMultiplexer(base, manager)
	defer mux.Close()

	plainClient, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer plainClient.Close()
	if _, err := plainClient.Write([]byte("GET / HTTP/1.1\r\nHost: local\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	plainServer, err := mux.Plain().Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer plainServer.Close()

	tlsResult := make(chan error, 1)
	go func() {
		serverConn, acceptErr := mux.TLS().Accept()
		if acceptErr != nil {
			tlsResult <- acceptErr
			return
		}
		defer serverConn.Close()
		tlsResult <- tls.Server(serverConn, manager.TLSConfig()).Handshake()
	}()
	tlsClient, err := tls.Dial("tcp", base.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // test-only local certificate
	if err != nil {
		t.Fatal(err)
	}
	_ = tlsClient.Close()
	if err := <-tlsResult; err != nil {
		t.Fatal(err)
	}
}
