package agentgrpc

import (
	"context"
	"testing"
	"time"

	"vonet/internal/agentproto"
)

func TestEventQueuePreservesProgressBeforeTerminalResult(t *testing.T) {
	queue := newEventQueue()
	queue.push(&agentproto.AgentEvent{Event: &agentproto.AgentEvent_CommandProgress{
		CommandProgress: &agentproto.CommandProgress{CommandId: "cmd", Stage: "download"},
	}})
	queue.push(&agentproto.AgentEvent{Event: &agentproto.AgentEvent_CommandResult{
		CommandResult: &agentproto.CommandResult{CommandId: "cmd", Success: true, Terminal: true},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	progress, err := queue.next(ctx)
	if err != nil || progress.GetCommandProgress() == nil {
		t.Fatalf("progress=%#v err=%v", progress, err)
	}
	terminal, err := queue.next(ctx)
	if err != nil || terminal.GetCommandResult() == nil {
		t.Fatalf("terminal=%#v err=%v", terminal, err)
	}
}

func TestEventQueueCloseWakesWaitingCaller(t *testing.T) {
	queue := newEventQueue()
	queue.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := queue.next(ctx); err == nil {
		t.Fatal("closed queue returned without error")
	}
}

func TestCommandCapabilityRejectsUnsupportedHardwareAction(t *testing.T) {
	command := &agentproto.AgentCommand{Command: &agentproto.AgentCommand_ExecuteHardware{
		ExecuteHardware: &agentproto.ExecuteHardwareCommand{Action: "esim_download"},
	}}
	if capability := commandCapability(command); capability != "esim" {
		t.Fatalf("capability=%q", capability)
	}
}

func TestStatusMarksStaleHeartbeatOffline(t *testing.T) {
	server := &Server{statuses: map[string]Status{
		"agent": {AgentID: "agent", Online: true, LastSeenAt: time.Now().Add(-time.Minute)},
	}}
	status, ok := server.Status("agent")
	if !ok || status.Online {
		t.Fatalf("stale status=%#v ok=%v", status, ok)
	}
}
