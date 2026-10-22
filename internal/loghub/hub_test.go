package loghub

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestHubHistoryFiltersAndBounds(t *testing.T) {
	hub := New(slog.NewTextHandler(io.Discard, nil), 100)
	baseTime := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	hidden := slog.NewRecord(baseTime, slog.LevelDebug, "hidden", 0)
	modemReady := slog.NewRecord(baseTime.Add(time.Nanosecond), slog.LevelInfo, "modem ready", 0)
	modemReady.Add("device", "ec20")
	retry := slog.NewRecord(baseTime.Add(2*time.Nanosecond), slog.LevelWarn, "retry", 0)
	retry.Add("device", "ec20")
	for _, record := range []slog.Record{hidden, modemReady, retry} {
		if err := hub.Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}

	history := hub.History(10, slog.LevelInfo, "ec20")
	if len(history) != 2 {
		t.Fatalf("History() length = %d, want 2", len(history))
	}
	if history[0].Message != "modem ready" || history[1].Message != "retry" {
		t.Fatalf("History() = %#v", history)
	}
	history[0].Fields["device"] = "changed"
	again := hub.History(10, slog.LevelInfo, "")
	if again[0].Fields["device"] != "ec20" {
		t.Fatal("History() exposed mutable internal fields")
	}
}

func TestHubRedactsDownstreamAndHistoryAndPreservesErrors(t *testing.T) {
	var output bytes.Buffer
	hub := New(slog.NewJSONHandler(&output, nil), 100)
	logger := slog.New(hub).With("imsi", "234159000000003")
	logger.Warn(
		"delivery to +447700900123 failed",
		"iccid", "8944101234567890123",
		"peer", "+447700900456",
		"error", errors.New("modem rejected MSISDN=447700900789 with +CMS ERROR: 305"),
	)

	entry := hub.History(1, slog.LevelDebug, "")[0]
	if strings.Contains(entry.Message, "447700900123") {
		t.Fatalf("message was not redacted: %q", entry.Message)
	}
	for _, key := range []string{"imsi", "iccid", "peer"} {
		if value := entry.Fields[key]; !strings.Contains(value.(string), "REDACTED") {
			t.Fatalf("%s = %#v, want redacted", key, value)
		}
	}
	errorText, ok := entry.Fields["error"].(string)
	if !ok || !strings.Contains(errorText, "+CMS ERROR: 305") || strings.Contains(errorText, "447700900789") {
		t.Fatalf("error = %#v, want original modem error with identity redacted", entry.Fields["error"])
	}
	if raw := output.String(); strings.Contains(raw, "234159000000003") || strings.Contains(raw, "447700900") {
		t.Fatalf("downstream output leaked an identity: %s", raw)
	}
}

func TestSanitizeEntryProtectsLegacyNestedFields(t *testing.T) {
	entry := SanitizeEntry(Entry{
		Message: "incoming SIP from sip:+447700900123@ims.example",
		Fields: map[string]any{
			"details": map[string]any{"associated_number": "+447700900456", "status": "registered"},
		},
	})
	if strings.Contains(entry.Message, "447700900123") {
		t.Fatalf("message = %q", entry.Message)
	}
	details := entry.Fields["details"].(map[string]any)
	if strings.Contains(details["associated_number"].(string), "447700900456") {
		t.Fatalf("nested field leaked: %#v", details)
	}
}

func TestHubSubscription(t *testing.T) {
	hub := New(slog.NewTextHandler(io.Discard, nil), 100)
	entries, cancel := hub.Subscribe(1)
	defer cancel()
	record := slog.NewRecord(time.Now(), slog.LevelError, "failure", 0)
	record.Add("stage", "ims")
	if err := hub.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	select {
	case entry := <-entries:
		if entry.Level != "error" || entry.Fields["stage"] != "ims" {
			t.Fatalf("entry = %#v", entry)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log entry")
	}
}

func TestHubClearDropsHistoryAndQueuedEntries(t *testing.T) {
	hub := New(slog.NewTextHandler(io.Discard, nil), 100)
	entries, cancel := hub.Subscribe(4)
	defer cancel()
	logger := slog.New(hub)
	logger.Info("before clear")
	hub.Clear()
	if history := hub.History(10, slog.LevelDebug, ""); len(history) != 0 {
		t.Fatalf("history after Clear = %#v", history)
	}
	select {
	case entry := <-entries:
		t.Fatalf("queued entry survived Clear: %#v", entry)
	default:
	}
	logger.Info("after clear")
	select {
	case entry := <-entries:
		if entry.Message != "after clear" {
			t.Fatalf("entry = %#v", entry)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not remain active after Clear")
	}
}
