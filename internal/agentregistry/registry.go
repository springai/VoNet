package agentregistry

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vonet/internal/store"
)

const (
	stateKey     = "agents.registry.v1"
	stateAAD     = "vonet-agent-registry-v1"
	stateVersion = 2
)

type CertificateRecord struct {
	Fingerprint   string `json:"fingerprint"`
	SerialNumber  string `json:"serial_number"`
	KeyVersion    int    `json:"key_version"`
	IssuedAt      int64  `json:"issued_at"`
	ExpiresAt     int64  `json:"expires_at"`
	RevokedAt     int64  `json:"revoked_at,omitempty"`
	RevokedReason string `json:"revoked_reason,omitempty"`
}

type Agent struct {
	ID                     string              `json:"id"`
	Name                   string              `json:"name"`
	Notes                  string              `json:"notes"`
	KeyHash                string              `json:"key_hash"`
	KeyVersion             int                 `json:"key_version"`
	KeyConsumedAt          int64               `json:"key_consumed_at,omitempty"`
	Status                 string              `json:"status"`
	CertificateFingerprint string              `json:"certificate_fingerprint,omitempty"`
	CertificateSerial      string              `json:"certificate_serial,omitempty"`
	Certificates           []CertificateRecord `json:"certificates,omitempty"`
	ProtocolVersion        string              `json:"protocol_version,omitempty"`
	AgentVersion           string              `json:"agent_version,omitempty"`
	Capabilities           []string            `json:"capabilities,omitempty"`
	InstanceID             uint64              `json:"instance_id,omitempty"`
	CreatedAt              int64               `json:"created_at"`
	UpdatedAt              int64               `json:"updated_at"`
	LastSeenAt             int64               `json:"last_seen_at"`
	LastConnectedAt        int64               `json:"last_connected_at,omitempty"`
	LastDisconnectedAt     int64               `json:"last_disconnected_at,omitempty"`
	LastError              string              `json:"last_error,omitempty"`
	Disabled               bool                `json:"disabled,omitempty"`
}

type state struct {
	Version   int     `json:"version"`
	CACertPEM string  `json:"ca_cert_pem"`
	CAKeyPEM  string  `json:"ca_key_pem"`
	Agents    []Agent `json:"agents"`
}

type Registry struct {
	mu         sync.Mutex
	store      *store.Store
	masterKey  []byte
	revokeHook func(string)
}

type Enrollment struct {
	AgentID        string
	CertificatePEM string
	CAPEM          string
	GRPCTarget     string
}

func Open(ctx context.Context, database *store.Store, keyPath string) (*Registry, error) {
	key, err := loadOrCreateMasterKey(keyPath)
	if err != nil {
		return nil, err
	}
	registry := &Registry{store: database, masterKey: key}
	if err := registry.ensureState(ctx); err != nil {
		return nil, err
	}
	return registry, nil
}

func (registry *Registry) List(ctx context.Context) ([]Agent, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return nil, err
	}
	return append([]Agent(nil), state.Agents...), nil
}

func (registry *Registry) CACertificatePEM(ctx context.Context) (string, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return "", err
	}
	return state.CACertPEM, nil
}

func (registry *Registry) SetRevocationHook(hook func(string)) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.revokeHook = hook
}

func (registry *Registry) notifyRevoked(agentID string) {
	if registry.revokeHook != nil {
		registry.revokeHook(agentID)
	}
}

func (registry *Registry) Create(ctx context.Context, name, notes string) (Agent, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Agent{}, "", errors.New("agent name is required")
	}
	key, keyHash, err := newCommunicationKey()
	if err != nil {
		return Agent{}, "", err
	}
	now := time.Now().Unix()
	agent := Agent{
		ID:         randomID(),
		Name:       name,
		Notes:      strings.TrimSpace(notes),
		KeyHash:    keyHash,
		KeyVersion: 1,
		Status:     "waiting_for_key",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return Agent{}, "", err
	}
	state.Agents = append(state.Agents, agent)
	if err := registry.saveState(ctx, state); err != nil {
		return Agent{}, "", err
	}
	return agent, key, nil
}

func (registry *Registry) UpdateAgent(ctx context.Context, id, name, notes string) (Agent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Agent{}, errors.New("agent name is required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return Agent{}, err
	}
	for index := range state.Agents {
		if state.Agents[index].ID != strings.TrimSpace(id) {
			continue
		}
		state.Agents[index].Name = name
		state.Agents[index].Notes = strings.TrimSpace(notes)
		state.Agents[index].UpdatedAt = time.Now().Unix()
		if err := registry.saveState(ctx, state); err != nil {
			return Agent{}, err
		}
		return state.Agents[index], nil
	}
	return Agent{}, store.ErrNotFound
}

func (registry *Registry) RotateKey(ctx context.Context, id string) (Agent, string, error) {
	key, keyHash, err := newCommunicationKey()
	if err != nil {
		return Agent{}, "", err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return Agent{}, "", err
	}
	for index := range state.Agents {
		if state.Agents[index].ID != strings.TrimSpace(id) {
			continue
		}
		state.Agents[index].KeyHash = keyHash
		state.Agents[index].KeyVersion++
		state.Agents[index].KeyConsumedAt = 0
		state.Agents[index].Status = "waiting_for_key"
		state.Agents[index].CertificateFingerprint = ""
		state.Agents[index].CertificateSerial = ""
		revokeCertificates(state.Agents[index].Certificates, time.Now().Unix(), "communication_key_rotated")
		state.Agents[index].UpdatedAt = time.Now().Unix()
		if err := registry.saveState(ctx, state); err != nil {
			return Agent{}, "", err
		}
		registry.notifyRevoked(strings.TrimSpace(id))
		return state.Agents[index], key, nil
	}
	return Agent{}, "", store.ErrNotFound
}

func (registry *Registry) Enroll(ctx context.Context, communicationKey, csrPEM, protocolVersion string) (Enrollment, error) {
	if strings.TrimSpace(communicationKey) == "" || strings.TrimSpace(csrPEM) == "" {
		return Enrollment{}, errors.New("communication key and CSR are required")
	}
	keyHash := hashCommunicationKey(communicationKey)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	var target *Agent
	for index := range state.Agents {
		if subtle.ConstantTimeCompare([]byte(state.Agents[index].KeyHash), []byte(keyHash)) == 1 {
			target = &state.Agents[index]
			break
		}
	}
	if target == nil {
		return Enrollment{}, errors.New("communication key is invalid or has been replaced")
	}
	if target.KeyConsumedAt != 0 {
		return Enrollment{}, errors.New("communication key has already been used")
	}
	if target.Disabled {
		return Enrollment{}, errors.New("agent is disabled")
	}
	issued, err := signCertificate(state.CACertPEM, state.CAKeyPEM, csrPEM, target.ID)
	if err != nil {
		return Enrollment{}, err
	}
	now := time.Now().Unix()
	revokeCertificates(target.Certificates, now, "re_enrolled")
	target.Certificates = append(target.Certificates, CertificateRecord{
		Fingerprint:  issued.Fingerprint,
		SerialNumber: issued.SerialNumber,
		KeyVersion:   target.KeyVersion,
		IssuedAt:     now,
		ExpiresAt:    issued.NotAfter.Unix(),
	})
	target.Status = "ready"
	target.CertificateFingerprint = issued.Fingerprint
	target.CertificateSerial = issued.SerialNumber
	target.KeyConsumedAt = now
	target.ProtocolVersion = strings.TrimSpace(protocolVersion)
	target.UpdatedAt = now
	if err := registry.saveState(ctx, state); err != nil {
		return Enrollment{}, err
	}
	return Enrollment{AgentID: target.ID, CertificatePEM: issued.PEM, CAPEM: state.CACertPEM}, nil
}

type ConnectionIdentity struct {
	AgentID      string
	Fingerprint  string
	SerialNumber string
}

func (registry *Registry) AuthenticateCertificate(ctx context.Context, identity ConnectionIdentity) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return err
	}
	for _, agent := range state.Agents {
		if agent.ID != identity.AgentID || agent.Disabled {
			continue
		}
		for _, certificate := range agent.Certificates {
			if certificate.RevokedAt != 0 ||
				(identity.Fingerprint != "" && certificate.Fingerprint != identity.Fingerprint) ||
				(identity.SerialNumber != "" && certificate.SerialNumber != identity.SerialNumber) {
				continue
			}
			if certificate.ExpiresAt != 0 && certificate.ExpiresAt <= time.Now().Unix() {
				return errors.New("agent certificate is expired")
			}
			return nil
		}
	}
	return errors.New("agent certificate is revoked or unknown")
}

func (registry *Registry) MarkConnected(
	ctx context.Context,
	identity ConnectionIdentity,
	protocolVersion, agentVersion string,
	capabilities []string,
	instanceID uint64,
) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return err
	}
	for index := range state.Agents {
		if state.Agents[index].ID != identity.AgentID {
			continue
		}
		now := time.Now().Unix()
		state.Agents[index].Status = "online"
		state.Agents[index].ProtocolVersion = strings.TrimSpace(protocolVersion)
		state.Agents[index].AgentVersion = strings.TrimSpace(agentVersion)
		state.Agents[index].Capabilities = append([]string(nil), capabilities...)
		state.Agents[index].InstanceID = instanceID
		state.Agents[index].LastConnectedAt = now
		state.Agents[index].LastSeenAt = now
		state.Agents[index].LastError = ""
		state.Agents[index].UpdatedAt = now
		return registry.saveState(ctx, state)
	}
	return store.ErrNotFound
}

func (registry *Registry) Heartbeat(ctx context.Context, agentID string, at time.Time) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return err
	}
	for index := range state.Agents {
		if state.Agents[index].ID == agentID {
			now := at.UTC().Unix()
			state.Agents[index].Status = "online"
			state.Agents[index].LastSeenAt = now
			state.Agents[index].UpdatedAt = now
			return registry.saveState(ctx, state)
		}
	}
	return store.ErrNotFound
}

func (registry *Registry) MarkDisconnected(ctx context.Context, agentID string, cause error) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return err
	}
	for index := range state.Agents {
		if state.Agents[index].ID != agentID {
			continue
		}
		now := time.Now().Unix()
		if state.Agents[index].Status != "disabled" {
			state.Agents[index].Status = "offline"
		}
		state.Agents[index].LastDisconnectedAt = now
		state.Agents[index].UpdatedAt = now
		if cause != nil {
			state.Agents[index].LastError = cause.Error()
		}
		return registry.saveState(ctx, state)
	}
	return store.ErrNotFound
}

func (registry *Registry) RenewCertificate(
	ctx context.Context,
	identity ConnectionIdentity,
	csrPEM string,
) (Enrollment, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	for index := range state.Agents {
		agent := &state.Agents[index]
		if agent.ID != identity.AgentID || agent.Disabled {
			continue
		}
		active := false
		for _, certificate := range agent.Certificates {
			if certificate.RevokedAt == 0 &&
				(identity.Fingerprint == "" || certificate.Fingerprint == identity.Fingerprint) &&
				(identity.SerialNumber == "" || certificate.SerialNumber == identity.SerialNumber) {
				active = true
				break
			}
		}
		if !active {
			return Enrollment{}, errors.New("current agent certificate is revoked or unknown")
		}
		issued, err := signCertificate(state.CACertPEM, state.CAKeyPEM, csrPEM, agent.ID)
		if err != nil {
			return Enrollment{}, err
		}
		now := time.Now().Unix()
		revokeCertificates(agent.Certificates, now, "certificate_renewed")
		agent.Certificates = append(agent.Certificates, CertificateRecord{
			Fingerprint: issued.Fingerprint, SerialNumber: issued.SerialNumber,
			KeyVersion: agent.KeyVersion, IssuedAt: now, ExpiresAt: issued.NotAfter.Unix(),
		})
		agent.CertificateFingerprint = issued.Fingerprint
		agent.CertificateSerial = issued.SerialNumber
		agent.Status = "ready"
		agent.UpdatedAt = now
		if err := registry.saveState(ctx, state); err != nil {
			return Enrollment{}, err
		}
		return Enrollment{AgentID: agent.ID, CertificatePEM: issued.PEM, CAPEM: state.CACertPEM}, nil
	}
	return Enrollment{}, store.ErrNotFound
}

func (registry *Registry) RevokeAgent(ctx context.Context, id, reason string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return err
	}
	for index := range state.Agents {
		if state.Agents[index].ID != id {
			continue
		}
		now := time.Now().Unix()
		state.Agents[index].Disabled = true
		state.Agents[index].Status = "disabled"
		state.Agents[index].CertificateFingerprint = ""
		state.Agents[index].CertificateSerial = ""
		state.Agents[index].UpdatedAt = now
		revokeCertificates(state.Agents[index].Certificates, now, reason)
		if err := registry.saveState(ctx, state); err != nil {
			return err
		}
		registry.notifyRevoked(id)
		return nil
	}
	return store.ErrNotFound
}

func (registry *Registry) DeleteAgent(ctx context.Context, id string) error {
	if err := registry.RevokeAgent(ctx, id, "agent_deleted"); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, err := registry.loadState(ctx)
	if err != nil {
		return err
	}
	for index := range state.Agents {
		if state.Agents[index].ID == id {
			state.Agents = append(state.Agents[:index], state.Agents[index+1:]...)
			return registry.saveState(ctx, state)
		}
	}
	return store.ErrNotFound
}

func revokeCertificates(certificates []CertificateRecord, at int64, reason string) {
	for index := range certificates {
		if certificates[index].RevokedAt == 0 {
			certificates[index].RevokedAt = at
			certificates[index].RevokedReason = reason
		}
	}
}

func (registry *Registry) ensureState(ctx context.Context) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	current, err := registry.loadState(ctx)
	if err == nil {
		if current.CACertPEM != "" && current.CAKeyPEM != "" {
			return nil
		}
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	certPEM, keyPEM, err := createCA()
	if err != nil {
		return err
	}
	return registry.saveState(ctx, state{Version: stateVersion, CACertPEM: certPEM, CAKeyPEM: keyPEM})
}

func (registry *Registry) loadState(ctx context.Context) (state, error) {
	setting, err := registry.store.AppSetting(ctx, stateKey)
	if err != nil {
		return state{}, err
	}
	var encoded string
	if err := json.Unmarshal(setting.Value, &encoded); err != nil {
		return state{}, err
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return state{}, err
	}
	plaintext, err := open(registry.masterKey, ciphertext)
	if err != nil {
		return state{}, err
	}
	var result state
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return state{}, err
	}
	if result.Version == 1 {
		result.Version = stateVersion
		for index := range result.Agents {
			if result.Agents[index].Certificates == nil {
				result.Agents[index].Certificates = []CertificateRecord{}
			}
			if result.Agents[index].CertificateFingerprint != "" && len(result.Agents[index].Certificates) == 0 {
				now := time.Now().Unix()
				result.Agents[index].Certificates = append(result.Agents[index].Certificates, CertificateRecord{
					Fingerprint:  result.Agents[index].CertificateFingerprint,
					SerialNumber: result.Agents[index].CertificateSerial,
					KeyVersion:   result.Agents[index].KeyVersion,
					IssuedAt:     now,
					ExpiresAt:    time.Now().AddDate(0, 3, 0).Unix(),
				})
			}
		}
	} else if result.Version != stateVersion {
		return state{}, fmt.Errorf("unsupported agent registry version %d", result.Version)
	}
	return result, nil
}

func (registry *Registry) saveState(ctx context.Context, value state) error {
	value.Version = stateVersion
	plaintext, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encrypted, err := seal(registry.masterKey, plaintext)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(base64.RawStdEncoding.EncodeToString(encrypted))
	if err != nil {
		return err
	}
	return registry.store.UpsertAppSetting(ctx, store.AppSetting{Key: stateKey, Value: json.RawMessage(encoded)})
}

func newCommunicationKey() (string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", err
	}
	key := base64.RawURLEncoding.EncodeToString(value)
	return key, hashCommunicationKey(key), nil
}

func hashCommunicationKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return "agent-" + base64.RawURLEncoding.EncodeToString(value)
}

func createCA() (string, string, error) {
	key, err := generateECKey()
	if err != nil {
		return "", "", err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "VoNet Agent CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}

type issuedCertificate struct {
	PEM          string
	Fingerprint  string
	SerialNumber string
	NotAfter     time.Time
}

func signCertificate(caCertPEM, caKeyPEM, csrPEM, agentID string) (issuedCertificate, error) {
	caCertBlock, _ := pem.Decode([]byte(caCertPEM))
	caKeyBlock, _ := pem.Decode([]byte(caKeyPEM))
	csrBlock, _ := pem.Decode([]byte(csrPEM))
	if caCertBlock == nil || caKeyBlock == nil || csrBlock == nil {
		return issuedCertificate{}, errors.New("invalid certificate or CSR PEM")
	}
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return issuedCertificate{}, err
	}
	caKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return issuedCertificate{}, err
	}
	signer, ok := caKey.(crypto.Signer)
	if !ok {
		return issuedCertificate{}, errors.New("unsupported CA private key")
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return issuedCertificate{}, err
	}
	if err := csr.CheckSignature(); err != nil {
		return issuedCertificate{}, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return issuedCertificate{}, err
	}
	notAfter := time.Now().AddDate(0, 3, 0)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: strings.TrimSpace(agentID)},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     csr.DNSNames,
		IPAddresses:  csr.IPAddresses,
		URIs: []*url.URL{{
			Scheme: "spiffe",
			Host:   "vonet",
			Path:   "/agent/" + strings.TrimSpace(agentID),
		}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, signer)
	if err != nil {
		return issuedCertificate{}, err
	}
	fingerprint := sha256.Sum256(der)
	return issuedCertificate{
		PEM:          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Fingerprint:  base64.RawURLEncoding.EncodeToString(fingerprint[:]),
		SerialNumber: serial.Text(16),
		NotAfter:     notAfter,
	}, nil
}

func loadOrCreateMasterKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("agent registry key must contain 32 bytes")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func seal(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, []byte(stateAAD)), nil
}

func open(key, encrypted []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(encrypted) < gcm.NonceSize() {
		return nil, errors.New("agent registry ciphertext is truncated")
	}
	nonce, ciphertext := encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, []byte(stateAAD))
}

func generateECKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
