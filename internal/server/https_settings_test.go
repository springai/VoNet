package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vonet/internal/httpsmode"
	"vonet/internal/store"
)

func newHTTPSSettingsTest(t *testing.T) (*Server, *httpsmode.Manager, string) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	certDir := filepath.Join(t.TempDir(), "data", "etc", "certs")
	manager, err := httpsmode.New(ctx, database, certDir, "127.0.0.1:17575")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		store: database, logger: regionTestLogger(), https: manager, maxRequestBodyBytes: 1 << 20,
	}
	return server, manager, certDir
}

func generateHTTPSTestPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "VoNet upload test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func TestHTTPSSettingsRejectsDisable(t *testing.T) {
	server, _, _ := newHTTPSSettingsTest(t)
	request := httptest.NewRequest(http.MethodPut, "/api/settings/https", bytes.NewBufferString(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleHTTPSSettings(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("disable status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPSCertificateUploadValidatesAndPersists(t *testing.T) {
	server, manager, certDir := newHTTPSSettingsTest(t)
	restarts := make(chan struct{}, 1)
	server.updateRestart = func(_ *slog.Logger) error {
		restarts <- struct{}{}
		return nil
	}
	certificate, privateKey := generateHTTPSTestPair(t)
	body, err := json.Marshal(map[string]string{
		"certificate": certificate,
		"private_key": privateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/settings/https/certificate", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleHTTPSCertificate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var uploaded struct {
		Data struct {
			RestartPending bool `json:"restart_pending"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if !uploaded.Data.RestartPending {
		t.Fatal("certificate upload response did not schedule a restart")
	}
	select {
	case <-restarts:
	case <-time.After(2 * time.Second):
		t.Fatal("certificate upload did not restart the service")
	}
	for _, name := range []string{"custom.crt", "custom.key"} {
		if _, err := os.Stat(filepath.Join(certDir, name)); err != nil {
			t.Fatalf("%s was not persisted: %v", name, err)
		}
	}
	if source := manager.State("").CertificateSource; source != "uploaded" {
		t.Fatalf("certificate source = %q, want uploaded", source)
	}

	invalid, _ := json.Marshal(map[string]string{
		"certificate": "not a certificate",
		"private_key": privateKey,
	})
	request = httptest.NewRequest(http.MethodPost, "/api/settings/https/certificate", bytes.NewReader(invalid))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.handleHTTPSCertificate(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid upload status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
	active, err := manager.CertificatePEM()
	if err != nil || !bytes.Equal(active, []byte(certificate)) {
		t.Fatalf("invalid upload replaced active certificate: %v", err)
	}
}
