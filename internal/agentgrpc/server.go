package agentgrpc

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"vonet/internal/agentproto"
	"vonet/internal/agentregistry"
)

const agentHeartbeatTimeout = 30 * time.Second

type Server struct {
	agentproto.UnimplementedAgentControlServer
	Logger      *slog.Logger
	grpc        *grpc.Server
	mu          sync.RWMutex
	devices     map[string][]json.RawMessage
	connections map[string]*connection
	state       StateStore
	statuses    map[string]Status
	completed   map[string]completedCommand
	inflight    map[string]*inflightCommand
	commands    CommandStore
}

type connection struct {
	stream       agentproto.AgentControl_ConnectServer
	mu           sync.Mutex
	pending      map[string]*eventQueue
	closed       bool
	capabilities map[string]bool
}

type eventQueue struct {
	mu     sync.Mutex
	items  []*agentproto.AgentEvent
	notify chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newEventQueue() *eventQueue {
	return &eventQueue{notify: make(chan struct{}, 1), done: make(chan struct{})}
}

func (queue *eventQueue) push(event *agentproto.AgentEvent) {
	queue.mu.Lock()
	queue.items = append(queue.items, event)
	queue.mu.Unlock()
	select {
	case queue.notify <- struct{}{}:
	default:
	}
}

func (queue *eventQueue) next(ctx context.Context) (*agentproto.AgentEvent, error) {
	for {
		queue.mu.Lock()
		if len(queue.items) > 0 {
			event := queue.items[0]
			queue.items[0] = nil
			queue.items = queue.items[1:]
			queue.mu.Unlock()
			return event, nil
		}
		queue.mu.Unlock()
		select {
		case <-queue.notify:
		case <-queue.done:
			return nil, status.Error(codes.Unavailable, "agent connection closed")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (queue *eventQueue) close() {
	queue.once.Do(func() { close(queue.done) })
}

type StateStore interface {
	AuthenticateCertificate(context.Context, agentregistry.ConnectionIdentity) error
	MarkConnected(context.Context, agentregistry.ConnectionIdentity, string, string, []string, uint64) error
	Heartbeat(context.Context, string, time.Time) error
	MarkDisconnected(context.Context, string, error) error
}

type CommandStore interface {
	LoadCommandResult(context.Context, string) (json.RawMessage, time.Time, bool, error)
	SaveCommandResult(context.Context, string, json.RawMessage, time.Time) error
}

type Status struct {
	AgentID         string    `json:"agent_id"`
	Online          bool      `json:"online"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	AgentVersion    string    `json:"agent_version,omitempty"`
	Capabilities    []string  `json:"capabilities,omitempty"`
	InstanceID      uint64    `json:"instance_id,omitempty"`
	ConnectedAt     time.Time `json:"connected_at,omitempty"`
	LastSeenAt      time.Time `json:"last_seen_at,omitempty"`
	SnapshotAt      time.Time `json:"snapshot_at,omitempty"`
}

type completedCommand struct {
	event     *agentproto.AgentEvent
	expiresAt time.Time
}

type inflightCommand struct {
	done  chan struct{}
	event *agentproto.AgentEvent
	err   error
}

func New(logger *slog.Logger, state ...StateStore) *Server {
	server := &Server{
		Logger: logger, grpc: grpc.NewServer(),
		devices: make(map[string][]json.RawMessage), connections: make(map[string]*connection),
		statuses: make(map[string]Status), completed: make(map[string]completedCommand),
		inflight: make(map[string]*inflightCommand),
	}
	if len(state) > 0 {
		server.state = state[0]
	}
	agentproto.RegisterAgentControlServer(server.grpc, server)
	return server
}

func (server *Server) Devices(agentID string) []json.RawMessage {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return append([]json.RawMessage(nil), server.devices[strings.TrimSpace(agentID)]...)
}

func (server *Server) Status(agentID string) (Status, bool) {
	server.mu.RLock()
	defer server.mu.RUnlock()
	value, ok := server.statuses[strings.TrimSpace(agentID)]
	if value.Online && !value.LastSeenAt.IsZero() && time.Since(value.LastSeenAt) > agentHeartbeatTimeout {
		value.Online = false
	}
	return value, ok
}

func (server *Server) SetCommandStore(store CommandStore) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.commands = store
}

func (server *Server) DisconnectAgent(agentID string) {
	agentID = strings.TrimSpace(agentID)
	server.mu.Lock()
	conn := server.connections[agentID]
	delete(server.connections, agentID)
	delete(server.devices, agentID)
	if value, ok := server.statuses[agentID]; ok {
		value.Online = false
		server.statuses[agentID] = value
	}
	server.mu.Unlock()
	if conn != nil {
		conn.close()
	}
}

func (connection *connection) close() {
	connection.mu.Lock()
	connection.closed = true
	for _, queue := range connection.pending {
		queue.close()
	}
	connection.mu.Unlock()
}

func (connection *connection) isClosed() bool {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.closed
}

func (connection *connection) hasCapability(capability string) bool {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.capabilities[strings.ToLower(strings.TrimSpace(capability))]
}

func (server *Server) Execute(ctx context.Context, agentID string, command *agentproto.AgentCommand) (*agentproto.AgentEvent, error) {
	return server.execute(ctx, agentID, command, nil)
}

func (server *Server) ExecuteWithProgress(
	ctx context.Context,
	agentID string,
	command *agentproto.AgentCommand,
	progress func(*agentproto.AgentEvent),
) (*agentproto.AgentEvent, error) {
	return server.execute(ctx, agentID, command, progress)
}

func (server *Server) execute(
	ctx context.Context,
	agentID string,
	command *agentproto.AgentCommand,
	progress func(*agentproto.AgentEvent),
) (result *agentproto.AgentEvent, retErr error) {
	agentID = strings.TrimSpace(agentID)
	requestedIdempotencyKey := strings.TrimSpace(command.GetExecuteHardware().GetCommandIdempotencyKey())
	if requestedIdempotencyKey != "" && !validIdempotencyKey(requestedIdempotencyKey) {
		return nil, status.Error(codes.InvalidArgument, "idempotency key must contain 16-256 letters, digits, hyphens, or underscores")
	}
	idempotencyKey := commandIdempotencyKey(agentID, command)
	var flight *inflightCommand
	if idempotencyKey != "" {
		server.mu.Lock()
		cached, ok := server.completed[idempotencyKey]
		if ok && time.Now().Before(cached.expiresAt) {
			server.mu.Unlock()
			return cached.event, nil
		}
		if existing := server.inflight[idempotencyKey]; existing != nil {
			server.mu.Unlock()
			select {
			case <-existing.done:
				return existing.event, existing.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		delete(server.completed, idempotencyKey)
		flight = &inflightCommand{done: make(chan struct{})}
		server.inflight[idempotencyKey] = flight
		commandStore := server.commands
		server.mu.Unlock()
		defer func() {
			server.mu.Lock()
			if server.inflight[idempotencyKey] == flight {
				delete(server.inflight, idempotencyKey)
			}
			server.mu.Unlock()
			flight.event = result
			flight.err = retErr
			close(flight.done)
		}()
		if commandStore != nil {
			raw, expiresAt, found, err := commandStore.LoadCommandResult(ctx, idempotencyKey)
			if err == nil && found {
				var event agentproto.AgentEvent
				if json.Unmarshal(raw, &event) == nil {
					server.mu.Lock()
					server.completed[idempotencyKey] = completedCommand{event: &event, expiresAt: expiresAt}
					server.mu.Unlock()
					return &event, nil
				}
			} else if err != nil && server.Logger != nil {
				server.Logger.Warn("load agent command ledger", "error", err)
			}
		}
	}
	server.mu.RLock()
	conn := server.connections[agentID]
	server.mu.RUnlock()
	if conn == nil || conn.isClosed() {
		return nil, status.Error(codes.Unavailable, "agent is offline")
	}
	commandID := commandID(command)
	if commandID == "" {
		return nil, status.Error(codes.InvalidArgument, "command ID is required")
	}
	if required := commandCapability(command); required != "" && !conn.hasCapability(required) {
		return nil, status.Errorf(codes.FailedPrecondition, "agent does not advertise %q", required)
	}
	queue := newEventQueue()
	conn.mu.Lock()
	if _, exists := conn.pending[commandID]; exists {
		conn.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "command ID is already in progress")
	}
	conn.pending[commandID] = queue
	conn.mu.Unlock()
	defer func() {
		conn.mu.Lock()
		delete(conn.pending, commandID)
		conn.mu.Unlock()
	}()
	conn.mu.Lock()
	err := conn.stream.Send(command)
	conn.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for {
		event, err := queue.next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				server.cancelRemoteCommand(conn, commandID)
			}
			return nil, err
		}
		if event.GetCommandProgress() != nil {
			if progress != nil {
				progress(event)
			}
			continue
		}
		if !terminalEvent(event) {
			continue
		}
		if idempotencyKey != "" {
			expiresAt := time.Now().Add(24 * time.Hour)
			server.mu.RLock()
			commandStore := server.commands
			server.mu.RUnlock()
			persisted := commandStore == nil
			if commandStore != nil {
				raw, err := json.Marshal(event)
				if err == nil {
					persistContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err = commandStore.SaveCommandResult(persistContext, idempotencyKey, raw, expiresAt)
					cancel()
					persisted = err == nil
					if err != nil && server.Logger != nil {
						server.Logger.Warn("save agent command ledger; idempotency is not durable", "error", err)
					}
				}
			}
			if persisted {
				server.mu.Lock()
				server.completed[idempotencyKey] = completedCommand{event: event, expiresAt: expiresAt}
				server.mu.Unlock()
			}
		}
		return event, nil
	}
}

func (server *Server) cancelRemoteCommand(conn *connection, commandID string) {
	cancelID := "cancel-" + commandID
	command := &agentproto.AgentCommand{Command: &agentproto.AgentCommand_Cancel{Cancel: &agentproto.CancelCommand{
		CommandId: cancelID, TargetCommandId: commandID,
	}}}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.closed {
		_ = conn.stream.Send(command)
	}
}

func (server *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.grpc.ServeHTTP(w, r)
	})
}

func (server *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server.grpc.ServeHTTP(w, r)
}

func (server *Server) Serve(listener net.Listener, tlsConfig *tls.Config) error {
	grpcTLS := tlsConfig.Clone()
	previousVerify := grpcTLS.VerifyConnection
	grpcTLS.VerifyConnection = func(state tls.ConnectionState) error {
		if previousVerify != nil {
			if err := previousVerify(state); err != nil {
				return err
			}
		}
		if len(state.PeerCertificates) == 0 {
			return nil
		}
		identity, err := certificateIdentity(state.PeerCertificates[0])
		if err != nil {
			return err
		}
		if server.state != nil {
			return server.state.AuthenticateCertificate(context.Background(), identity)
		}
		return nil
	}
	server.grpc = grpc.NewServer(grpc.Creds(credentials.NewTLS(grpcTLS)))
	agentproto.RegisterAgentControlServer(server.grpc, server)
	return server.grpc.Serve(listener)
}

func (server *Server) Stop() {
	server.grpc.Stop()
}

func (server *Server) Connect(stream agentproto.AgentControl_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || hello.GetAgentId() == "" {
		return status.Error(codes.PermissionDenied, "agent hello is required")
	}
	if hello.GetProtocolVersion() != "agent.v1" {
		return status.Error(codes.InvalidArgument, "unsupported agent protocol version")
	}
	identity, err := agentIDFromContext(stream.Context())
	if err != nil {
		return err
	}
	if identity.AgentID != hello.GetAgentId() {
		return status.Error(codes.PermissionDenied, "agent certificate does not match agent ID")
	}
	if server.state != nil {
		if err := server.state.AuthenticateCertificate(stream.Context(), identity); err != nil {
			return status.Error(codes.PermissionDenied, err.Error())
		}
	}
	capabilities := make(map[string]bool, len(hello.GetCapabilities()))
	for _, capability := range hello.GetCapabilities() {
		capabilities[strings.ToLower(strings.TrimSpace(capability))] = true
	}
	conn := &connection{stream: stream, pending: make(map[string]*eventQueue), capabilities: capabilities}
	server.mu.Lock()
	server.connections[hello.GetAgentId()] = conn
	server.mu.Unlock()
	defer func() {
		if conn.isClosed() {
			return
		}
		server.mu.Lock()
		if server.connections[hello.GetAgentId()] == conn {
			delete(server.connections, hello.GetAgentId())
		}
		delete(server.devices, hello.GetAgentId())
		if statusValue, ok := server.statuses[hello.GetAgentId()]; ok {
			statusValue.Online = false
			server.statuses[hello.GetAgentId()] = statusValue
		}
		server.mu.Unlock()
		if server.state != nil {
			disconnectContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.state.MarkDisconnected(disconnectContext, hello.GetAgentId(), nil)
			cancel()
		}
	}()
	if server.state != nil {
		if err := server.state.MarkConnected(
			stream.Context(), identity, hello.GetProtocolVersion(), hello.GetVersion(),
			hello.GetCapabilities(), hello.GetInstanceId(),
		); err != nil {
			return err
		}
	}
	server.mu.Lock()
	server.statuses[hello.GetAgentId()] = Status{
		AgentID: hello.GetAgentId(), Online: true,
		ProtocolVersion: hello.GetProtocolVersion(), AgentVersion: hello.GetVersion(),
		Capabilities: append([]string(nil), hello.GetCapabilities()...), InstanceID: hello.GetInstanceId(),
		ConnectedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
	}
	server.mu.Unlock()
	if server.Logger != nil {
		server.Logger.Info("VoNet-Agent connected", "agent_id", hello.GetAgentId(), "version", hello.GetVersion())
	}
	conn.mu.Lock()
	err = stream.Send(&agentproto.AgentCommand{Command: &agentproto.AgentCommand_GetHardwareSnapshot{
		GetHardwareSnapshot: &agentproto.GetHardwareSnapshotCommand{CommandId: "initial-snapshot"},
	}})
	conn.mu.Unlock()
	if err != nil {
		return err
	}
	for {
		event, err := stream.Recv()
		if err != nil {
			return err
		}
		if server.Logger != nil {
			server.Logger.Debug("VoNet-Agent event", "agent_id", hello.GetAgentId(), "event_type", fmt.Sprintf("%T", event.GetEvent()))
		}
		if heartbeat := event.GetHeartbeat(); heartbeat != nil {
			now := time.Now().UTC()
			if server.state != nil {
				_ = server.state.Heartbeat(stream.Context(), hello.GetAgentId(), now)
			}
			server.mu.Lock()
			if statusValue, ok := server.statuses[hello.GetAgentId()]; ok {
				statusValue.LastSeenAt = now
				server.statuses[hello.GetAgentId()] = statusValue
			}
			server.mu.Unlock()
		}
		if _, ok := event.GetEvent().(*agentproto.AgentEvent_HardwareSnapshot); ok {
			snapshot := event.GetHardwareSnapshot()
			values := make([]json.RawMessage, 0, len(snapshot.GetDevices()))
			for _, item := range snapshot.GetDevices() {
				if raw := json.RawMessage(item.GetPayloadJson()); len(raw) > 0 {
					values = append(values, raw)
				}
			}
			server.mu.Lock()
			server.devices[hello.GetAgentId()] = values
			if statusValue, ok := server.statuses[hello.GetAgentId()]; ok {
				statusValue.SnapshotAt = time.Now().UTC()
				server.statuses[hello.GetAgentId()] = statusValue
			}
			server.mu.Unlock()
			if server.Logger != nil {
				server.Logger.Info("VoNet-Agent hardware snapshot received", "agent_id", hello.GetAgentId(), "at", time.Now().UTC())
			}
		}
		if id := eventCommandID(event); id != "" {
			conn.mu.Lock()
			target := conn.pending[id]
			conn.mu.Unlock()
			if target != nil {
				target.push(event)
			}
		}
	}
}

func commandID(command *agentproto.AgentCommand) string {
	switch value := command.GetCommand().(type) {
	case *agentproto.AgentCommand_GetHardwareSnapshot:
		return value.GetHardwareSnapshot.GetCommandId()
	case *agentproto.AgentCommand_ExecuteHardware:
		return value.ExecuteHardware.GetCommandId()
	case *agentproto.AgentCommand_CaptureCardProfile:
		return value.CaptureCardProfile.GetCommandId()
	case *agentproto.AgentCommand_AcquireSimSession:
		return value.AcquireSimSession.GetCommandId()
	case *agentproto.AgentCommand_Authenticate:
		return value.Authenticate.GetCommandId()
	case *agentproto.AgentCommand_ReadSmsc:
		return value.ReadSmsc.GetCommandId()
	case *agentproto.AgentCommand_Cancel:
		return value.Cancel.GetCommandId()
	default:
		return ""
	}
}

func eventCommandID(event *agentproto.AgentEvent) string {
	switch value := event.GetEvent().(type) {
	case *agentproto.AgentEvent_HardwareSnapshot:
		return value.HardwareSnapshot.GetCommandId()
	case *agentproto.AgentEvent_CardProfile:
		return value.CardProfile.GetCommandId()
	case *agentproto.AgentEvent_SimSession:
		return value.SimSession.GetCommandId()
	case *agentproto.AgentEvent_AkaResult:
		return value.AkaResult.GetCommandId()
	case *agentproto.AgentEvent_CommandResult:
		return value.CommandResult.GetCommandId()
	case *agentproto.AgentEvent_PinStatus:
		return value.PinStatus.GetCommandId()
	case *agentproto.AgentEvent_CommandProgress:
		return value.CommandProgress.GetCommandId()
	default:
		return ""
	}
}

func agentIDFromContext(ctx context.Context) (agentregistry.ConnectionIdentity, error) {
	peerInfo, ok := peer.FromContext(ctx)
	if !ok {
		return agentregistry.ConnectionIdentity{}, status.Error(codes.PermissionDenied, "TLS peer information is unavailable")
	}
	tlsInfo, ok := peerInfo.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return agentregistry.ConnectionIdentity{}, status.Error(codes.PermissionDenied, "client certificate is required")
	}
	return certificateIdentity(tlsInfo.State.PeerCertificates[0])
}

func certificateIdentity(certificate *x509.Certificate) (agentregistry.ConnectionIdentity, error) {
	fingerprint := sha256.Sum256(certificate.Raw)
	identity := agentregistry.ConnectionIdentity{
		Fingerprint:  base64.RawURLEncoding.EncodeToString(fingerprint[:]),
		SerialNumber: certificate.SerialNumber.Text(16),
	}
	for _, uri := range certificate.URIs {
		if uri.Scheme == "spiffe" && uri.Host == "vonet" && strings.HasPrefix(uri.Path, "/agent/") {
			identity.AgentID = strings.TrimPrefix(uri.Path, "/agent/")
			return identity, nil
		}
	}
	return agentregistry.ConnectionIdentity{}, status.Error(codes.PermissionDenied, "client certificate has no agent identity")
}

func terminalEvent(event *agentproto.AgentEvent) bool {
	if event == nil {
		return false
	}
	switch value := event.GetEvent().(type) {
	case *agentproto.AgentEvent_HardwareSnapshot,
		*agentproto.AgentEvent_CardProfile,
		*agentproto.AgentEvent_SimSession,
		*agentproto.AgentEvent_AkaResult,
		*agentproto.AgentEvent_PinStatus:
		return true
	case *agentproto.AgentEvent_CommandResult:
		result := value.CommandResult
		return result.GetTerminal() || result.GetCompletedAt() != 0 || result.GetErrorMessage() != ""
	default:
		return false
	}
}

func commandIdempotencyKey(agentID string, command *agentproto.AgentCommand) string {
	key := strings.TrimSpace(command.GetExecuteHardware().GetCommandIdempotencyKey())
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{agentID, key, commandFingerprint(command)}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func validIdempotencyKey(key string) bool {
	if len(key) < 16 || len(key) > 256 {
		return false
	}
	for _, character := range key {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func commandFingerprint(command *agentproto.AgentCommand) string {
	clone := proto.Clone(command).(*agentproto.AgentCommand)
	if execute := clone.GetExecuteHardware(); execute != nil {
		execute.CommandId = ""
		execute.CommandIdempotencyKey = ""
	}
	raw, err := json.Marshal(clone)
	if err != nil {
		return "unavailable"
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func commandCapability(command *agentproto.AgentCommand) string {
	switch value := command.GetCommand().(type) {
	case *agentproto.AgentCommand_GetHardwareSnapshot:
		return "hardware_snapshot"
	case *agentproto.AgentCommand_ExecuteHardware:
		action := strings.ToLower(strings.TrimSpace(value.ExecuteHardware.GetAction()))
		switch {
		case action == "at" || action == "at_sensitive":
			return "at"
		case action == "network" || action == "network_status":
			return "network"
		case action == "sms_list" || action == "sms_send" || action == "sms_read" ||
			action == "sms_delete" || action == "sms_delete_storage" || action == "sms_delete_storage_match":
			return "sms"
		case action == "ussd" || action == "ussd_continue" || action == "ussd_cancel":
			return "ussd"
		case strings.HasPrefix(action, "esim_"):
			return "esim"
		case action == "operator_selection" || action == "operator_selection_set" ||
			action == "operator_reregister" || action == "operator_scan":
			return "operator"
		case action == "cells" || action == "cell_lock" || action == "cell_lock_set":
			return "cell_lock"
		default:
			return ""
		}
	case *agentproto.AgentCommand_CaptureCardProfile,
		*agentproto.AgentCommand_AcquireSimSession,
		*agentproto.AgentCommand_ReadSmsc:
		return "pcsc"
	case *agentproto.AgentCommand_Authenticate:
		return "sim_auth"
	case *agentproto.AgentCommand_Pin:
		return "sim_pin"
	default:
		return ""
	}
}
