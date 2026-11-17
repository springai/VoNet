package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCommandLedgerPersistsEncryptedResultUntilExpiry(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	key := strings.Repeat("a", 32)
	event := json.RawMessage(`{"command_result":{"command_id":"cmd","success":true}}`)
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := database.SaveCommandResult(ctx, key, event, expiresAt); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := database.db.QueryRowContext(ctx,
		`SELECT value_json FROM app_settings WHERE key = ?`, commandLedgerKeyPrefix+key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "command_result") {
		t.Fatalf("command result was stored in plaintext: %s", stored)
	}
	loaded, loadedExpiresAt, ok, err := database.LoadCommandResult(ctx, key)
	if err != nil || !ok || string(loaded) != string(event) || !loadedExpiresAt.Equal(expiresAt) {
		t.Fatalf("loaded=%s expires=%s ok=%v err=%v", loaded, loadedExpiresAt, ok, err)
	}
	if err := database.SaveCommandResult(ctx, key, event, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := database.LoadCommandResult(ctx, key); err != nil || ok {
		t.Fatalf("expired ledger returned ok=%v err=%v", ok, err)
	}
}
