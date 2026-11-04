package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCellularDataRuntimeAllowsLongATSetup(t *testing.T) {
	if got := cellularDataOperationTimeout(true, "at"); got != 6*time.Minute {
		t.Fatalf("AT timeout = %s", got)
	}
	if got := cellularDataOperationTimeout(true, "qmi"); got != 75*time.Second {
		t.Fatalf("QMI timeout = %s", got)
	}
	if got := cellularDataOperationTimeout(false, "at"); got != 30*time.Second {
		t.Fatalf("disable timeout = %s", got)
	}
}

func TestCellularDataRuntimeKeepsQMISetupBounded(t *testing.T) {
	if cellularDataOperationTimeout(true, "qmi") >= cellularDataATSetupTimeout {
		t.Fatal("QMI budget inherited AT timeout")
	}
}

func TestCellularDataRuntimeCancelsLongATSetup(t *testing.T) {
	runtime := newCellularDataRuntime(nil, nil)
	runtime.entries["dev1"] = &cellularDataRuntimeEntry{
		status:  cellularDataRuntimeStatus{Phase: "starting"},
		changed: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.wait(ctx, "dev1", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("long AT setup did not honor cancellation: %v", err)
	}
}
