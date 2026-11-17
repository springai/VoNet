package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"vonet/internal/agentproto"
	"vonet/internal/device"
	"vonet/internal/store"
)

type routerAgentStub struct {
	http.Handler
	command  *agentproto.AgentCommand
	event    *agentproto.AgentEvent
	progress []*agentproto.AgentEvent
	err      error
}

func (stub *routerAgentStub) Devices(string) []json.RawMessage { return nil }

func (stub *routerAgentStub) Execute(_ context.Context, _ string, command *agentproto.AgentCommand) (*agentproto.AgentEvent, error) {
	stub.command = command
	return stub.event, stub.err
}

func (stub *routerAgentStub) ExecuteWithProgress(
	ctx context.Context,
	agentID string,
	command *agentproto.AgentCommand,
	progress func(*agentproto.AgentEvent),
) (*agentproto.AgentEvent, error) {
	for _, event := range stub.progress {
		progress(event)
	}
	return stub.Execute(ctx, agentID, command)
}

func TestDeviceRouterRoutesRemoteDeviceToAgent(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "remote-1", Name: "Remote", DeviceType: store.DeviceTypePCIeEC20EC25,
		AgentID: "agent-1", RemoteDeviceID: "hardware-1",
	}); err != nil {
		t.Fatal(err)
	}
	stub := &routerAgentStub{event: &agentproto.AgentEvent{Event: &agentproto.AgentEvent_CommandResult{
		CommandResult: &agentproto.CommandResult{
			CommandId: "cmd", Success: true, Terminal: true,
			PayloadJson: `{"flightMode":true,"radioOff":true}`,
		},
	}}}
	router := &deviceRouter{store: database, agents: stub}
	result, err := router.SetFlight(ctx, "remote-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FlightMode || !result.RadioOff {
		t.Fatalf("flight result=%#v", result)
	}
	execute := stub.command.GetExecuteHardware()
	if execute == nil || execute.GetDeviceId() != "hardware-1" || execute.GetAction() != "flight" {
		t.Fatalf("remote command=%#v", stub.command)
	}
	if execute.GetCommandIdempotencyKey() != "" {
		t.Fatal("mutating command unexpectedly used a reusable idempotency key")
	}
}

func TestDeviceRouterResolvesRemoteHardwareIDBackToConfiguration(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "configured-reader", Name: "Reader", DeviceType: store.DeviceTypeUSBSIMReader,
		AgentID: "agent-reader", RemoteDeviceID: "Reader-48EF",
	}); err != nil {
		t.Fatal(err)
	}
	stub := &routerAgentStub{event: &agentproto.AgentEvent{Event: &agentproto.AgentEvent_CommandResult{
		CommandResult: &agentproto.CommandResult{
			CommandId: "cmd", Success: true, Terminal: true,
			PayloadJson: `{"flightMode":true,"radioOff":true}`,
		},
	}}}
	router := &deviceRouter{store: database, agents: stub}
	result, err := router.SetFlight(ctx, "Reader-48EF", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FlightMode || !result.RadioOff {
		t.Fatalf("flight result=%#v", result)
	}
	execute := stub.command.GetExecuteHardware()
	if execute == nil || execute.GetDeviceId() != "Reader-48EF" || execute.GetAction() != "flight" {
		t.Fatalf("remote command=%#v", stub.command)
	}
}

func TestDeviceRouterRoutesPINOperation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "remote-pin", Name: "Remote", DeviceType: store.DeviceTypeUSBSIMReader,
		AgentID: "agent-pin", RemoteDeviceID: "Reader-1",
	}); err != nil {
		t.Fatal(err)
	}
	stub := &routerAgentStub{event: &agentproto.AgentEvent{Event: &agentproto.AgentEvent_PinStatus{
		PinStatus: &agentproto.PINStatus{ReaderId: "Reader-1", Required: false, Tries: -1, CommandId: "pin"},
	}}}
	router := &deviceRouter{store: database, agents: stub}
	state, err := router.VerifyPIN(ctx, "remote-pin", "1234")
	if err != nil || state.Required {
		t.Fatalf("PIN state=%#v err=%v", state, err)
	}
	if stub.command.GetPin().GetOperation() != "verify" || stub.command.GetPin().GetReaderId() != "Reader-1" {
		t.Fatalf("PIN command=%#v", stub.command)
	}
}

func TestDeviceRouterRoutesRemoteDeviceSetupBeforeDatabaseBinding(t *testing.T) {
	stub := &routerAgentStub{event: &agentproto.AgentEvent{Event: &agentproto.AgentEvent_CommandResult{
		CommandResult: &agentproto.CommandResult{CommandId: "cmd", Success: true, Terminal: true},
	}}}
	router := &deviceRouter{agents: stub}

	if err := router.SetRemoteSIMPin("agent-new", "hardware-new", "1234"); err != nil {
		t.Fatal(err)
	}
	execute := stub.command.GetExecuteHardware()
	if execute == nil || execute.GetDeviceId() != "hardware-new" || execute.GetAction() != "set_sim_pin" {
		t.Fatalf("PIN setup command=%#v", stub.command)
	}
	var pinPayload hardwarePayload
	if err := json.Unmarshal([]byte(execute.GetPayloadJson()), &pinPayload); err != nil || pinPayload.Pin != "1234" {
		t.Fatalf("PIN setup payload=%q err=%v", execute.GetPayloadJson(), err)
	}

	if err := router.SetRemoteBackend("agent-new", "hardware-new", "pcsc"); err != nil {
		t.Fatal(err)
	}
	execute = stub.command.GetExecuteHardware()
	var backendPayload hardwarePayload
	if execute == nil || execute.GetAction() != "set_backend" ||
		json.Unmarshal([]byte(execute.GetPayloadJson()), &backendPayload) != nil || backendPayload.Backend != "pcsc" {
		t.Fatalf("backend setup command=%#v", stub.command)
	}

	if err := router.SetRemoteESIMTransport("agent-new", "hardware-new", "pcsc"); err != nil {
		t.Fatal(err)
	}
	execute = stub.command.GetExecuteHardware()
	var transportPayload hardwarePayload
	if execute == nil || execute.GetAction() != "set_esim_transport" ||
		json.Unmarshal([]byte(execute.GetPayloadJson()), &transportPayload) != nil || transportPayload.ESIMTransport != "pcsc" {
		t.Fatalf("eSIM transport setup command=%#v", stub.command)
	}
}

func TestDeviceRouterForwardsRemoteESIMProgress(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "remote-esim", Name: "Remote", DeviceType: store.DeviceTypePCIeEC20EC25,
		AgentID: "agent-esim", RemoteDeviceID: "hardware-esim",
	}); err != nil {
		t.Fatal(err)
	}
	stub := &routerAgentStub{
		progress: []*agentproto.AgentEvent{{Event: &agentproto.AgentEvent_CommandProgress{CommandProgress: &agentproto.CommandProgress{
			CommandId: "cmd", Stage: "download", Message: "downloading", Percent: 50,
		}}}},
		event: &agentproto.AgentEvent{Event: &agentproto.AgentEvent_CommandResult{CommandResult: &agentproto.CommandResult{
			CommandId: "cmd", Success: true, Terminal: true, PayloadJson: `{"ICCID":"8944100000000000001","SpaceDelta":12}`,
		}}},
	}
	router := &deviceRouter{store: database, agents: stub}
	var steps []device.EsimProgress
	result, err := router.ESIMDownloadProfile(ctx, "remote-esim", device.EsimDownloadParams{}, func(progress device.EsimProgress) {
		steps = append(steps, progress)
	})
	if err != nil || result == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(steps) != 1 || steps[0].Step != "download" || steps[0].Pct != 50 {
		t.Fatalf("progress=%#v", steps)
	}
}
