package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentEnrollmentRequestAcceptsAgentMetadata(t *testing.T) {
	body := `{
		"communication_key":"key",
		"csr_pem":"csr",
		"protocol_version":"agent.v1",
		"hostname":"agent-host",
		"capabilities":["hardware_snapshot","pcsc","sim_auth"],
		"agent_version":"0.1.0"
	}`
	request := httptest.NewRequest("POST", "/agent/enroll", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	server := &Server{maxRequestBodyBytes: 4096}
	var decoded agentEnrollmentRequest
	if err := server.decodeJSON(httptest.NewRecorder(), request, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CommunicationKey != "key" || decoded.Hostname != "agent-host" ||
		decoded.AgentVersion != "0.1.0" || len(decoded.Capabilities) != 3 {
		t.Fatalf("decoded request = %#v", decoded)
	}
}
