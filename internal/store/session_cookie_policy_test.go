package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestMigration35PreservesAuthDataAndAddsSecureCookiePolicy upgrades a schema34
// database without guessing transport policy for existing credentials.
func TestMigration35PreservesAuthDataAndAddsSecureCookiePolicy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migration35.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 34; version++ {
		for _, statement := range migrationStatements(version) {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				db.Close()
				t.Fatalf("baseline migration %d: %v", version, err)
			}
		}
	}
	for _, statement := range []string{
		`PRAGMA user_version = 34`,
		`INSERT INTO admins
			(id, username, password_hash, created_at, updated_at, credential_epoch)
			VALUES (1, 'admin', X'1234', 1, 1, 7)`,
		`INSERT INTO sessions
			(token_hash, admin_id, csrf_hash, expires_at, created_at, credential_epoch)
			VALUES (X'1234', 1, X'5678', 4102444800, 1, 7)`,
		`INSERT INTO sso_bindings
			(id, admin_id, provider_instance_id, protocol, authority, subject, created_at, updated_at)
			VALUES ('binding', 1, 'provider', 'oidc', 'https://id.example', 'subject', 1, 1)`,
		`INSERT INTO sessions
			(token_hash, admin_id, csrf_hash, expires_at, created_at, login_method, sso_binding_id, credential_epoch)
			VALUES (X'1122', 1, X'3344', 4102444800, 1, 'sso', 'binding', 7)`,
		`INSERT INTO api_tokens
			(name, token_hash, read_only, expires_at, created_at, last_used_at, credential_epoch)
			VALUES ('existing', X'4321', 1, 0, 1, 2, 7)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, tc := range []struct {
		token, csrf []byte
		binding     string
	}{
		{[]byte{0x12, 0x34}, []byte{0x56, 0x78}, ""},
		{[]byte{0x11, 0x22}, []byte{0x33, 0x44}, "binding"},
	} {
		session, err := database.SessionByTokenHash(ctx, tc.token)
		if err != nil || session.SecureCookies ||
			session.SSOBindingID != tc.binding ||
			session.CredentialEpoch != 7 ||
			!bytes.Equal(session.CSRFHash, tc.csrf) {
			t.Fatalf("legacy session changed: session=%+v err=%v", session, err)
		}
	}
	token, err := database.APITokenByName(ctx, "existing")
	if err != nil || !token.ReadOnly || token.LastUsedAt.Unix() != 2 {
		t.Fatalf("legacy API token changed: token=%+v err=%v", token, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE sessions SET secure_cookies = 2`); err == nil {
		t.Fatal("invalid cookie policy accepted")
	}

	if err := database.RequireSessionSecureCookies(ctx, []byte{0x12, 0x34}); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireSessionSecureCookies(ctx, []byte{0x12, 0x34}); err != nil {
		t.Fatalf("repeat secure policy upgrade failed: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO sessions
			(token_hash, admin_id, csrf_hash, expires_at, created_at, credential_epoch)
			VALUES (X'5544', 1, X'3322', 4102444800, 1, 7)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_cookie_policy BEFORE UPDATE OF secure_cookies ON sessions
		BEGIN SELECT RAISE(IGNORE); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireSessionSecureCookies(ctx, []byte{0x55, 0x44}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no-op policy update was accepted: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_cookie_policy`); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireSessionSecureCookies(ctx, []byte{0x55, 0x44}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO sessions
			(token_hash, admin_id, csrf_hash, expires_at, created_at, credential_epoch)
			VALUES (X'9988', 1, X'7766', 4102444800, 1, 6)
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireSessionSecureCookies(ctx, []byte{0x99, 0x88}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale credential epoch was upgraded: %v", err)
	}

	upgraded, err := database.SessionByTokenHash(ctx, []byte{0x12, 0x34})
	if err != nil || !upgraded.SecureCookies {
		t.Fatalf("secure cookie policy did not persist: session=%+v err=%v", upgraded, err)
	}
	unchanged, err := database.SessionByTokenHash(ctx, []byte{0x11, 0x22})
	if err != nil || unchanged.SecureCookies {
		t.Fatalf("another session changed policy: session=%+v err=%v", unchanged, err)
	}
}

// TestMigration35CreateSessionCarriesPolicyWithCredentialEpoch verifies new
// sessions persist the transport decision and the epoch captured at insertion.
func TestMigration35CreateSessionCarriesPolicyWithCredentialEpoch(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := database.SetAdmin(ctx, "admin", []byte("rotated-hash")); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(
		ctx,
		1,
		bytes.Repeat([]byte{1}, 32),
		bytes.Repeat([]byte{2}, 32),
		time.Now().Add(time.Hour),
		true,
	); err != nil {
		t.Fatal(err)
	}
	session, err := database.SessionByTokenHash(ctx, bytes.Repeat([]byte{1}, 32))
	if err != nil || !session.SecureCookies || session.CredentialEpoch != 1 {
		t.Fatalf("session policy/epoch mismatch: session=%+v err=%v", session, err)
	}
}
