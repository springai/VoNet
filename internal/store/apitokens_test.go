package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func newTokenStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestAPITokenUniqueConstraints(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)

	digest := sha256.Sum256([]byte("vonet_first"))
	if _, err := database.CreateAPIToken(ctx, "same-name", digest[:], false, time.Time{}); err != nil {
		t.Fatalf("CreateAPIToken() error = %v", err)
	}
	otherDigest := sha256.Sum256([]byte("vonet_second"))
	if _, err := database.CreateAPIToken(ctx, "same-name", otherDigest[:], false, time.Time{}); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if _, err := database.CreateAPIToken(ctx, "other-name", digest[:], false, time.Time{}); err == nil {
		t.Fatal("duplicate token hash accepted")
	}
}

func TestAPITokenTouchThreshold(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)

	digest := sha256.Sum256([]byte("vonet_touch"))
	if _, err := database.CreateAPIToken(ctx, "touched", digest[:], true, time.Time{}); err != nil {
		t.Fatalf("CreateAPIToken() error = %v", err)
	}
	if err := database.TouchAPIToken(ctx, digest[:], 5*time.Minute); err != nil {
		t.Fatalf("TouchAPIToken() error = %v", err)
	}
	first, err := database.APITokenByName(ctx, "touched")
	if err != nil {
		t.Fatalf("APITokenByName() error = %v", err)
	}
	if first.LastUsedAt.IsZero() {
		t.Fatal("touch did not record last_used_at")
	}
	if first.ReadOnly != true {
		t.Fatal("read_only not persisted")
	}

	// A second touch inside the threshold must not move last_used_at.
	time.Sleep(1100 * time.Millisecond)
	if err := database.TouchAPIToken(ctx, digest[:], 5*time.Minute); err != nil {
		t.Fatalf("TouchAPIToken() second error = %v", err)
	}
	second, err := database.APITokenByName(ctx, "touched")
	if err != nil {
		t.Fatalf("APITokenByName() second error = %v", err)
	}
	if !second.LastUsedAt.Equal(first.LastUsedAt) {
		t.Fatal("last_used_at moved inside the threshold")
	}
}

func TestDeleteAPITokenAndExpired(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)

	digest := sha256.Sum256([]byte("vonet_keep"))
	if _, err := database.CreateAPIToken(ctx, "keeper", digest[:], false, time.Time{}); err != nil {
		t.Fatalf("CreateAPIToken() error = %v", err)
	}
	expiredDigest := sha256.Sum256([]byte("vonet_expired"))
	if _, err := database.CreateAPIToken(
		ctx,
		"expired",
		expiredDigest[:],
		false,
		time.Now().UTC().Add(-time.Hour),
	); err != nil {
		t.Fatalf("CreateAPIToken() error = %v", err)
	}

	if err := database.DeleteAPIToken(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteAPIToken(unknown) error = %v, want ErrNotFound", err)
	}
	if err := database.DeleteExpiredAPITokens(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("DeleteExpiredAPITokens() error = %v", err)
	}
	tokens, err := database.APITokens(ctx)
	if err != nil {
		t.Fatalf("APITokens() error = %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "keeper" {
		t.Fatalf("APITokens() = %+v, want only keeper", tokens)
	}
	if err := database.DeleteAPIToken(ctx, "keeper"); err != nil {
		t.Fatalf("DeleteAPIToken() error = %v", err)
	}
	if _, err := database.APITokenByHash(ctx, digest[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("APITokenByHash(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestDeleteAllAuthCredentialsRevokesSessionsAndAPITokens(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAdmin(ctx, "admin", []byte("rotated-hash")); err != nil {
		t.Fatal(err)
	}
	sessionHash := []byte("active-session")
	if err := database.CreateSession(
		ctx, 1, sessionHash, []byte("csrf"), time.Now().Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	tokenHash := []byte("active-api-token")
	if _, err := database.CreateAPIToken(ctx, "active", tokenHash, true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO sso_flows (
			id, state_hash, browser_token_hash, purpose, provider_instance_id,
			config_revision, pkce_verifier, redirect_uri, return_path,
			created_at, expires_at
		) VALUES (
			'restored-flow', randomblob(32), randomblob(32), 'login', 'restored-provider',
			1, '', 'https://vonet.example/api/auth/sso/callback', '/',
			1, 4102444800
		)
	`); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteAllAuthCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SessionByTokenHash(ctx, sessionHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived auth revocation: %v", err)
	}
	if _, err := database.APITokenByHash(ctx, tokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("API token survived auth revocation: %v", err)
	}
	var flowCount int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sso_flows").Scan(&flowCount); err != nil {
		t.Fatal(err)
	}
	if flowCount != 0 {
		t.Fatalf("pending SSO flows survived auth revocation: %d", flowCount)
	}
}

func TestCredentialEpochRejectsLateSessionAndTokenWrites(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAdmin(ctx, "admin", []byte("rotated-hash")); err != nil {
		t.Fatal(err)
	}

	// Simulate a request that read epoch 0 before SetAdmin committed epoch 1.
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO api_tokens (name, token_hash, read_only, expires_at, created_at, last_used_at, credential_epoch)
		VALUES ('late-token', X'11', 0, 0, 1, 0, 0)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, admin_id, csrf_hash, expires_at, created_at, credential_epoch)
		VALUES (X'22', 1, X'33', 4102444800, 1, 0)
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := database.APITokenByHash(ctx, []byte{0x11}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late API token authenticated: %v", err)
	}
	if _, err := database.SessionByTokenHash(ctx, []byte{0x22}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late session authenticated: %v", err)
	}
}
