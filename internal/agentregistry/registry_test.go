package agentregistry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"path/filepath"
	"testing"
	"time"

	"vonet/internal/store"
)

func TestAgentLifecycleRotatesAndEnrollsCommunicationKeys(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	registry, err := Open(ctx, database, filepath.Join(t.TempDir(), "secrets", "agent-registry.key"))
	if err != nil {
		t.Fatal(err)
	}
	agent, firstKey, err := registry.Create(ctx, "Reader Agent", "USB reader")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "Reader Agent" || firstKey == "" || agent.KeyVersion != 1 {
		t.Fatalf("created agent = %#v, key = %q", agent, firstKey)
	}
	rotated, secondKey, err := registry.RotateKey(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.KeyVersion != 2 || rotated.Status != "waiting_for_key" || secondKey == "" {
		t.Fatalf("rotated agent = %#v", rotated)
	}
	if _, err := registry.Enroll(ctx, firstKey, testCSR(t), "agent.v1"); err == nil {
		t.Fatal("replaced communication key was accepted")
	}
	result, err := registry.Enroll(ctx, secondKey, testCSR(t), "agent.v1")
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentID != agent.ID || result.CertificatePEM == "" || result.CAPEM == "" {
		t.Fatalf("enrollment result = %#v", result)
	}
	if _, err := registry.Enroll(ctx, secondKey, testCSR(t), "agent.v1"); err == nil {
		t.Fatal("consumed communication key was accepted twice")
	}
	agents, err := registry.List(ctx)
	if err != nil || len(agents) != 1 || agents[0].Status != "ready" || agents[0].CertificateFingerprint == "" {
		t.Fatalf("agents after enrollment = %#v, %v", agents, err)
	}
	current := agents[0].Certificates[len(agents[0].Certificates)-1]
	identity := ConnectionIdentity{
		AgentID: agents[0].ID, Fingerprint: current.Fingerprint, SerialNumber: current.SerialNumber,
	}
	if err := registry.AuthenticateCertificate(ctx, identity); err != nil {
		t.Fatalf("current certificate rejected: %v", err)
	}
	if err := registry.MarkConnected(ctx, identity, "agent.v1", "test", []string{"pcsc"}, 7); err != nil {
		t.Fatal(err)
	}
	if err := registry.Heartbeat(ctx, agents[0].ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	connected, err := registry.List(ctx)
	if err != nil || connected[0].Status != "online" || connected[0].LastSeenAt == 0 || connected[0].InstanceID != 7 {
		t.Fatalf("connected agent = %#v, %v", connected, err)
	}
	if _, _, err := registry.RotateKey(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.AuthenticateCertificate(ctx, identity); err == nil {
		t.Fatal("revoked certificate remained valid after key rotation")
	}
}

func testCSR(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent-test"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}
