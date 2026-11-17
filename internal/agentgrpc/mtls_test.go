package agentgrpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"vonet/internal/agentproto"
	"vonet/internal/agentregistry"
	"vonet/internal/store"
)

func TestAgentMTLSRegistersHeartbeatsAndDisconnectsOnKeyRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	registry, err := agentregistry.Open(ctx, database, filepath.Join(t.TempDir(), "agent.key"))
	if err != nil {
		t.Fatal(err)
	}
	agent, communicationKey, err := registry.Create(ctx, "integration-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	agentCertPEM, agentKeyPEM, csrPEM := generateAgentClientCertificate(t)
	enrollment, err := registry.Enroll(ctx, communicationKey, string(csrPEM), "agent.v1")
	if err != nil {
		t.Fatal(err)
	}
	serverCertPEM, serverKeyPEM := generateServerCertificate(t)
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := tls.X509KeyPair([]byte(enrollment.CertificatePEM), agentKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(enrollment.CAPEM)) {
		t.Fatal("invalid agent CA")
	}
	serverPool := x509.NewCertPool()
	if !serverPool.AppendCertsFromPEM(serverCertPEM) {
		t.Fatal("invalid server certificate")
	}
	server := New(testLogger(), registry)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"},
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener, serverTLS) }()
	t.Cleanup(func() {
		server.Stop()
		<-serveDone
	})

	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      serverPool,
		MinVersion:   tls.VersionTLS12,
	}
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	stream, err := agentproto.NewAgentControlClient(connection).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&agentproto.AgentEvent{Event: &agentproto.AgentEvent_Hello{Hello: &agentproto.AgentHello{
		AgentId: agent.ID, ProtocolVersion: "agent.v1", Version: "integration", InstanceId: 42,
		Capabilities: []string{"hardware_snapshot", "pcsc", "sim_auth"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&agentproto.AgentEvent{Event: &agentproto.AgentEvent_Heartbeat{Heartbeat: &agentproto.Heartbeat{
		UnixTime: time.Now().Unix(), ProtocolVersion: "agent.v1", Sequence: 1,
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receive initial agent command: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		agents, err := registry.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(agents) == 1 && agents[0].Status == "online" && agents[0].LastSeenAt != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not become online: %#v", agents)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, _, err := registry.RotateKey(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	server.DisconnectAgent(agent.ID)
	if _, err := server.Execute(ctx, agent.ID, &agentproto.AgentCommand{Command: &agentproto.AgentCommand_GetHardwareSnapshot{
		GetHardwareSnapshot: &agentproto.GetHardwareSnapshotCommand{CommandId: "after-rotation"},
	}}); err == nil {
		t.Fatal("rotated agent still accepted commands")
	}
	_ = agentCertPEM
}

func generateAgentClientCertificate(t *testing.T) (certPEM, keyPEM, csrPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "integration-agent"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return nil, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
}

func generateServerCertificate(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "VoNet integration server"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
