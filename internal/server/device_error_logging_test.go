package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vonet/internal/device"
	"vonet/internal/loghub"
)

func TestWriteDeviceErrorDoesNotLogExpectedUnsupportedCapability(t *testing.T) {
	hub := loghub.New(slog.NewTextHandler(io.Discard, nil), 10)
	server := &Server{logger: slog.New(hub)}
	recorder := httptest.NewRecorder()

	server.writeDeviceError(recorder, device.ErrUnsupportedCapability)

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "unsupported_capability") ||
		strings.Contains(body, "AT port") || strings.Contains(body, "ATPort") {
		t.Fatalf("unsupported response exposed modem AT details: %s", body)
	}
	if entries := hub.History(10, slog.LevelWarn, ""); len(entries) != 0 {
		t.Fatalf("expected unsupported capability logged %d warnings: %#v", len(entries), entries)
	}
}
