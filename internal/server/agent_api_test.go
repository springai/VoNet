package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vonet/internal/agentregistry"
	"vonet/internal/store"
)

func TestAgentCanBeUpdatedAndDeletedWithItsDevices(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	registry, err := agentregistry.Open(ctx, database, filepath.Join(t.TempDir(), "secrets", "agent-registry.key"))
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := registry.Create(ctx, "Original Name", "Original notes")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "agent-device", Name: "Agent Device", AgentID: agent.ID, RemoteDeviceID: "hardware-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDevice(ctx, store.Device{ID: "other-device", Name: "Other Device"}); err != nil {
		t.Fatal(err)
	}
	controller := &fakeVoWiFiController{}
	server := &Server{
		store: database, agents: registry, vowifi: controller, maxRequestBodyBytes: 4096,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	update := httptest.NewRequest(http.MethodPatch, "/api/agents/"+agent.ID, strings.NewReader(`{"name":"Updated Name","notes":"Updated notes"}`))
	update.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleAgent(recorder, update, agent.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updatedAgents, err := registry.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedAgents) != 1 {
		t.Fatalf("agents after update=%#v", updatedAgents)
	}
	updated := updatedAgents[0]
	if updated.Name != "Updated Name" || updated.Notes != "Updated notes" {
		t.Fatalf("updated agent=%#v", updated)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/agents/"+agent.ID, nil)
	recorder = httptest.NewRecorder()
	server.handleAgent(recorder, remove, agent.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := database.Device(ctx, "agent-device"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("agent device still exists: %v", err)
	}
	if _, err := database.Device(ctx, "other-device"); err != nil {
		t.Fatalf("unrelated device was deleted: %v", err)
	}
	if len(controller.removed) != 1 || controller.removed[0] != "agent-device" {
		t.Fatalf("removed runtimes=%#v", controller.removed)
	}
	agents, err := registry.List(ctx)
	if err != nil || len(agents) != 0 {
		t.Fatalf("agents after delete=%#v err=%v", agents, err)
	}
}
