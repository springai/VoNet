package httpsmode

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vonet/internal/store"
)

const SettingKey = "transport.self_signed_https"

type State struct {
	Enabled            bool      `json:"enabled"`
	HTTPURL            string    `json:"http_url"`
	HTTPSURL           string    `json:"https_url"`
	CertificateSource  string    `json:"certificate_source"`
	CertificateSubject string    `json:"certificate_subject,omitempty"`
	Fingerprint        string    `json:"fingerprint,omitempty"`
	NotAfter           time.Time `json:"not_after,omitempty"`
	RestartPending     bool      `json:"restart_pending,omitempty"`
}

type Manager struct {
	dir     string
	address string
	mu      sync.RWMutex
	cert    *tls.Certificate
	source  string
}

func New(ctx context.Context, _ *store.Store, dir, address string) (*Manager, error) {
	manager := &Manager{dir: dir, address: address}
	if err := manager.ensureCertificate(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) Enabled() bool { return manager != nil }

func (manager *Manager) SetEnabled(ctx context.Context, enabled bool) (State, error) {
	if !enabled {
		return State{}, errors.New("HTTPS is required and cannot be disabled")
	}
	if err := manager.ensureCertificate(); err != nil {
		return State{}, err
	}
	return manager.State(""), nil
}

func (manager *Manager) State(host string) State {
	host = strings.TrimSpace(host)
	if host == "" {
		host = manager.address
	}
	state := State{
		Enabled:  true,
		HTTPURL:  "http://" + host,
		HTTPSURL: "https://" + host,
	}
	manager.mu.RLock()
	if manager.cert != nil && manager.cert.Leaf != nil {
		digest := sha256.Sum256(manager.cert.Leaf.Raw)
		encoded := strings.ToUpper(hex.EncodeToString(digest[:]))
		parts := make([]string, 0, len(encoded)/2)
		for len(encoded) >= 2 {
			parts = append(parts, encoded[:2])
			encoded = encoded[2:]
		}
		state.Fingerprint = strings.Join(parts, ":")
		state.NotAfter = manager.cert.Leaf.NotAfter
		state.CertificateSource = manager.source
		state.CertificateSubject = manager.cert.Leaf.Subject.String()
	}
	manager.mu.RUnlock()
	if state.CertificateSource == "" {
		state.CertificateSource = "self_signed"
	}
	return state
}

func (manager *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			manager.mu.RLock()
			defer manager.mu.RUnlock()
			if manager.cert == nil {
				return nil, errors.New("self-signed certificate is unavailable")
			}
			return manager.cert, nil
		},
	}
}

func (manager *Manager) CertificatePEM() ([]byte, error) {
	if err := manager.ensureCertificate(); err != nil {
		return nil, err
	}
	manager.mu.RLock()
	source := manager.source
	manager.mu.RUnlock()
	name := "selfsigned.crt"
	if source == "uploaded" {
		name = "custom.crt"
	}
	return os.ReadFile(filepath.Join(manager.dir, name))
}

func (manager *Manager) ensureCertificate() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.cert != nil && manager.cert.Leaf != nil && time.Now().Before(manager.cert.Leaf.NotAfter) {
		return nil
	}
	if err := os.MkdirAll(manager.dir, 0o750); err != nil {
		return fmt.Errorf("create TLS directory: %w", err)
	}
	customCertPath := filepath.Join(manager.dir, "custom.crt")
	customKeyPath := filepath.Join(manager.dir, "custom.key")
	if cert, err := loadCertificate(customCertPath, customKeyPath); err == nil && time.Now().Before(cert.Leaf.NotAfter) {
		manager.cert = cert
		manager.source = "uploaded"
		return nil
	}
	certPath := filepath.Join(manager.dir, "selfsigned.crt")
	keyPath := filepath.Join(manager.dir, "selfsigned.key")
	if cert, err := loadCertificate(certPath, keyPath); err == nil && time.Until(cert.Leaf.NotAfter) > 30*24*time.Hour {
		manager.cert = cert
		manager.source = "self_signed"
		return nil
	}
	certPEM, keyPEM, err := generateCertificate(manager.address)
	if err != nil {
		return err
	}
	if err := writePrivateFile(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := writePrivateFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	cert, err := loadCertificate(certPath, keyPath)
	if err != nil {
		return err
	}
	manager.cert = cert
	manager.source = "self_signed"
	return nil
}

// InstallCertificate validates a PEM certificate and matching private key,
// persists the pair under the configured certificate directory, and switches
// the live TLS certificate without restarting the HTTP server.
func (manager *Manager) InstallCertificate(ctx context.Context, certPEM, keyPEM []byte) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	cert, err := parseCertificatePair(certPEM, keyPEM)
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(manager.dir, 0o750); err != nil {
		return State{}, fmt.Errorf("create TLS directory: %w", err)
	}
	certPath := filepath.Join(manager.dir, "custom.crt")
	keyPath := filepath.Join(manager.dir, "custom.key")
	if err := writePrivateFile(keyPath, keyPEM, 0o600); err != nil {
		return State{}, fmt.Errorf("write TLS private key: %w", err)
	}
	if err := writePrivateFile(certPath, certPEM, 0o644); err != nil {
		return State{}, fmt.Errorf("write TLS certificate: %w", err)
	}
	manager.mu.Lock()
	manager.cert = cert
	manager.source = "uploaded"
	manager.mu.Unlock()
	return manager.State(""), nil
}

func parseCertificatePair(certPEM, keyPEM []byte) (*tls.Certificate, error) {
	if len(bytes.TrimSpace(certPEM)) == 0 {
		return nil, errors.New("certificate PEM is empty")
	}
	if len(bytes.TrimSpace(keyPEM)) == 0 {
		return nil, errors.New("private key PEM is empty")
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("certificate and private key do not form a valid pair: %w", err)
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	now := time.Now()
	if now.Before(cert.Leaf.NotBefore) {
		return nil, errors.New("certificate is not valid yet")
	}
	if !now.Before(cert.Leaf.NotAfter) {
		return nil, errors.New("certificate has expired")
	}
	return &cert, nil
}

func loadCertificate(certPath, keyPath string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("certificate chain is empty")
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

func generateCertificate(address string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "VoNet self-signed local certificate", Organization: []string{"VoNet"}},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if hostname, hostnameErr := os.Hostname(); hostnameErr == nil && strings.TrimSpace(hostname) != "" {
		template.DNSNames = append(template.DNSNames, strings.TrimSpace(hostname))
	}
	if host, _, splitErr := net.SplitHostPort(address); splitErr == nil {
		if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else if host != "" && host != "0.0.0.0" && host != "::" {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	if interfaces, interfaceErr := net.InterfaceAddrs(); interfaceErr == nil {
		for _, item := range interfaces {
			text := item.String()
			if slash := strings.IndexByte(text, '/'); slash >= 0 {
				text = text[:slash]
			}
			if ip := net.ParseIP(strings.TrimSpace(text)); ip != nil && !ip.IsUnspecified() {
				template.IPAddresses = append(template.IPAddresses, ip)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func writePrivateFile(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
