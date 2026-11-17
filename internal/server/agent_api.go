package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"vonet/internal/agentproto"
	"vonet/internal/agentregistry"
	"vonet/internal/store"
)

func (s *Server) routeAgentAPI(w http.ResponseWriter, r *http.Request, cleanPath string) bool {
	segments := splitAPIPath(cleanPath)
	if len(segments) == 0 || segments[0] != "agents" {
		return false
	}
	switch {
	case len(segments) == 1:
		s.handleAgents(w, r)
	case len(segments) == 2:
		s.handleAgent(w, r, segments[1])
	case len(segments) == 3 && segments[2] == "rotate-key":
		s.handleAgentRotateKey(w, r, segments[1])
	case len(segments) == 3 && segments[2] == "revoke":
		s.handleAgentRevoke(w, r, segments[1])
	case len(segments) == 3 && segments[2] == "devices":
		s.handleAgentDevices(w, r, segments[1])
	case len(segments) == 5 && segments[2] == "devices" && segments[4] == "commands":
		s.handleAgentDeviceCommand(w, r, segments[1], segments[3])
	default:
		writeError(w, http.StatusNotFound, "not_found", "agent endpoint not found")
	}
	return true
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPatch:
		var request struct {
			Name  string `json:"name"`
			Notes string `json:"notes"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		agent, err := s.agents.UpdateAgent(r.Context(), id, request.Name, request.Notes)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.recordAudit(r.Context(), "admin", "agent.update", "agent", id, "success", "agent updated")
		writeJSON(w, http.StatusOK, map[string]any{"data": agentResponse(agent)})
	case http.MethodDelete:
		if err := s.deleteAgentDevices(r.Context(), id); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if disconnecter, ok := s.agentGRPC.(AgentGRPCDisconnector); ok {
			disconnecter.DisconnectAgent(id)
		}
		if err := s.agents.DeleteAgent(r.Context(), id); err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.recordAudit(r.Context(), "admin", "agent.delete", "agent", id, "success", "agent and device configurations deleted")
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"deleted": true}})
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) deleteAgentDevices(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	agents, err := s.agents.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, agent := range agents {
		if agent.ID == agentID {
			found = true
			break
		}
	}
	if !found {
		return store.ErrNotFound
	}
	configs, err := s.store.ListDevices(ctx)
	if err != nil {
		return err
	}
	for _, config := range configs {
		if strings.TrimSpace(config.AgentID) != agentID {
			continue
		}
		if err := s.deleteDeviceConfig(ctx, config); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleAgentRevoke(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if disconnecter, ok := s.agentGRPC.(AgentGRPCDisconnector); ok {
		disconnecter.DisconnectAgent(id)
	}
	if err := s.agents.RevokeAgent(r.Context(), id, "revoked_by_admin"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.recordAudit(r.Context(), "admin", "agent.revoke", "agent", id, "success", "agent revoked")
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"revoked": true}})
}

func (s *Server) handleAgentDeviceCommand(w http.ResponseWriter, r *http.Request, agentID, deviceID string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.agentGRPC == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_grpc_unavailable", "Agent gRPC service is unavailable")
		return
	}
	var request struct {
		Action         string          `json:"action"`
		PayloadJSON    json.RawMessage `json:"payload_json"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(request.Action) == "" {
		writeError(w, http.StatusBadRequest, "invalid_command", "action is required")
		return
	}
	if !remoteReadOnlyAction(request.Action) {
		writeError(w, http.StatusConflict, "typed_device_api_required",
			"mutating Agent commands must use the policy-checked device API")
		return
	}
	config, configErr := s.store.DeviceByRemoteBinding(r.Context(), agentID, deviceID)
	if configErr != nil {
		s.writeStoreError(w, configErr)
		return
	}
	if remoteSensitiveReadAction(request.Action) {
		if _, ok := s.RequireRegisteredActiveCard(w, r, config.ID, ""); !ok {
			return
		}
	}
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	command := &agentproto.AgentCommand{Command: &agentproto.AgentCommand_ExecuteHardware{
		ExecuteHardware: &agentproto.ExecuteHardwareCommand{
			CommandId:             "cmd-" + hex.EncodeToString(raw),
			DeviceId:              deviceID,
			Action:                request.Action,
			PayloadJson:           string(request.PayloadJSON),
			CommandIdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
		},
	}}
	event, err := s.agentGRPC.Execute(r.Context(), agentID, command)
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent_command_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": event})
	s.audit(r, "agent.device_command.read", "device", config.ID, "success")
}

func remoteReadOnlyAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "refresh", "network_status", "supports_clcc", "usb_net_mode",
		"operator_selection", "operator_scan", "cells", "cell_lock",
		"cellular_ims", "sms_list", "esim_inventory", "esim_list", "esim_chip_info":
		return true
	default:
		return false
	}
}

func remoteSensitiveReadAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "sms_list", "esim_inventory", "esim_list", "esim_chip_info":
		return true
	default:
		return false
	}
}

func (s *Server) handleAgentDevices(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if s.agentGRPC == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_grpc_unavailable", "Agent gRPC service is unavailable")
		return
	}
	configs, err := s.store.ListDevices(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(configs))
	for _, config := range configs {
		if strings.TrimSpace(config.AgentID) != strings.TrimSpace(id) {
			continue
		}
		entry, _, present := s.physicalForConfig(config)
		var summary map[string]any
		if present {
			summary = s.configuredDeviceSummary(config, &entry)
		} else {
			summary = s.configuredDeviceSummary(config, nil)
		}
		summary["agent_id"] = id
		remoteID := strings.TrimSpace(config.RemoteDeviceID)
		if remoteID == "" {
			remoteID = entry.ID
		}
		summary["remote_device_id"] = remoteID
		summary["remote_hardware_generation"] = config.RemoteHardwareGeneration
		result = append(result, summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"devices": result}})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if s.agents == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_registry_unavailable", "agent registry is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		values, err := s.agents.List(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		result := make([]map[string]any, 0, len(values))
		for _, value := range values {
			response := agentResponse(value)
			if statusService, ok := s.agentGRPC.(AgentGRPCStatusService); ok {
				if live, ok := statusService.Status(value.ID); ok {
					response["status"] = map[bool]string{true: "online", false: "offline"}[live.Online]
					if !live.LastSeenAt.IsZero() {
						response["last_seen_at"] = live.LastSeenAt.Unix()
					}
					response["agent_version"] = live.AgentVersion
					response["capabilities"] = live.Capabilities
				} else if response["status"] == "ready" || response["status"] == "online" {
					response["status"] = "offline"
				}
			}
			result = append(result, response)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"agents": result}})
	case http.MethodPost:
		var request struct {
			Name  string `json:"name"`
			Notes string `json:"notes"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		agent, communicationKey, err := s.agents.Create(r.Context(), request.Name, request.Notes)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_agent", err.Error())
			return
		}
		s.recordAudit(r.Context(), "admin", "agent.create", "agent", agent.ID, "success", "agent created")
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{
			"agent":             agentResponse(agent),
			"communication_key": communicationKey,
		}})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleAgentRotateKey(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if disconnecter, ok := s.agentGRPC.(AgentGRPCDisconnector); ok {
		disconnecter.DisconnectAgent(id)
	}
	agent, communicationKey, err := s.agents.RotateKey(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.recordAudit(r.Context(), "admin", "agent.rotate_key", "agent", agent.ID, "success", "communication key rotated")
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"agent":             agentResponse(agent),
		"communication_key": communicationKey,
	}})
}

type agentEnrollmentRequest struct {
	CommunicationKey string   `json:"communication_key"`
	CSRPEM           string   `json:"csr_pem"`
	ProtocolVersion  string   `json:"protocol_version"`
	Hostname         string   `json:"hostname"`
	Capabilities     []string `json:"capabilities"`
	AgentVersion     string   `json:"agent_version"`
}

func (s *Server) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if r.TLS == nil {
		writeError(w, http.StatusBadRequest, "https_required", "agent enrollment requires HTTPS")
		return
	}
	if s.agents == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_registry_unavailable", "agent registry is unavailable")
		return
	}
	var request agentEnrollmentRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.agents.Enroll(r.Context(), request.CommunicationKey, request.CSRPEM, request.ProtocolVersion)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_agent_key", err.Error())
		return
	}
	s.writeEnrollment(w, r, result)
}

func (s *Server) handleAgentRenew(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		writeError(w, http.StatusUnauthorized, "client_certificate_required", "agent certificate renewal requires mTLS")
		return
	}
	var request struct {
		CSRPEM string `json:"csr_pem"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.agents.RenewCertificate(r.Context(), peerCertificateIdentity(r), request.CSRPEM)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "certificate_renewal_failed", err.Error())
		return
	}
	s.recordAudit(r.Context(), result.AgentID, "agent.renew_certificate", "agent", result.AgentID, "success", "client certificate renewed")
	s.writeEnrollment(w, r, result)
}

func peerCertificateIdentity(r *http.Request) agentregistry.ConnectionIdentity {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return agentregistry.ConnectionIdentity{}
	}
	certificate := r.TLS.PeerCertificates[0]
	fingerprint := sha256.Sum256(certificate.Raw)
	identity := agentregistry.ConnectionIdentity{
		Fingerprint:  base64.RawURLEncoding.EncodeToString(fingerprint[:]),
		SerialNumber: certificate.SerialNumber.Text(16),
	}
	for _, uri := range certificate.URIs {
		if uri.Scheme == "spiffe" && uri.Host == "vonet" && strings.HasPrefix(uri.Path, "/agent/") {
			identity.AgentID = strings.TrimPrefix(uri.Path, "/agent/")
			break
		}
	}
	return identity
}

func (s *Server) writeEnrollment(w http.ResponseWriter, r *http.Request, result agentregistry.Enrollment) {
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"agent_id":               result.AgentID,
		"certificate_pem":        result.CertificatePEM,
		"ca_pem":                 result.CAPEM,
		"server_certificate_pem": serverCertificatePEM(s),
		"grpc_target":            agentGRPCTarget(r.Host),
		"protocol_version":       "agent.v1",
	}})
}

func serverCertificatePEM(s *Server) string {
	if s.https == nil {
		return ""
	}
	certificate, err := s.https.CertificatePEM()
	if err != nil {
		return ""
	}
	return string(certificate)
}

func agentGRPCTarget(host string) string {
	hostname, port, err := net.SplitHostPort(strings.TrimSpace(host))
	if err != nil {
		return net.JoinHostPort(strings.TrimSpace(host), "17577")
	}
	_ = port
	return net.JoinHostPort(hostname, "17577")
}

func (s *Server) handleAgentGRPC(w http.ResponseWriter, r *http.Request) {
	if s.agentGRPC == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_grpc_unavailable", "Agent gRPC service is unavailable")
		return
	}
	s.agentGRPC.ServeHTTP(w, r)
}

func agentResponse(value agentregistry.Agent) map[string]any {
	return map[string]any{
		"id":                      value.ID,
		"name":                    value.Name,
		"notes":                   value.Notes,
		"key_version":             value.KeyVersion,
		"status":                  value.Status,
		"certificate_fingerprint": value.CertificateFingerprint,
		"protocol_version":        value.ProtocolVersion,
		"created_at":              value.CreatedAt,
		"updated_at":              value.UpdatedAt,
		"last_seen_at":            value.LastSeenAt,
	}
}

func (s *Server) routeAgentAPIPath(path string) bool {
	return strings.HasPrefix(path, "agents/")
}
