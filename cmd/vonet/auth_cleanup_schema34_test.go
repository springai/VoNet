package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"vonet/internal/store"
)

func TestAuthCleanupSchema34MigrationModes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		targetVersion int
		secureColumn  bool
	}{
		{name: "no migrate", args: []string{"--no-migrate"}, targetVersion: 34, secureColumn: false},
		{name: "migrate", args: nil, targetVersion: 35, secureColumn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "schema34.db")
			createSchema34AuthFixture(t, path)

			args := append([]string{"--database", path}, tc.args...)
			if err := runAuthCleanup(args, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}

			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var version int
			var secureColumns int
			var sessions int
			var tokens int
			var flows int
			var epoch int64
			for _, query := range []struct {
				sql string
				dst any
			}{
				{`PRAGMA user_version`, &version},
				{`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'secure_cookies'`, &secureColumns},
				{`SELECT COUNT(*) FROM sessions`, &sessions},
				{`SELECT COUNT(*) FROM api_tokens`, &tokens},
				{`SELECT COUNT(*) FROM sso_flows`, &flows},
				{`SELECT credential_epoch FROM admins WHERE id = 1`, &epoch},
			} {
				if err := db.QueryRowContext(ctx, query.sql).Scan(query.dst); err != nil {
					t.Fatalf("%s: %v", query.sql, err)
				}
			}
			if version != tc.targetVersion {
				t.Fatalf("user_version = %d, want %d", version, tc.targetVersion)
			}
			if (secureColumns == 1) != tc.secureColumn {
				t.Fatalf("secure_cookies columns = %d, want present=%v", secureColumns, tc.secureColumn)
			}
			if sessions != 0 || tokens != 0 || flows != 0 {
				t.Fatalf("credentials survived cleanup: sessions=%d tokens=%d flows=%d", sessions, tokens, flows)
			}
			if epoch != 1 {
				t.Fatalf("credential epoch = %d, want 1", epoch)
			}

			database, err := store.OpenWithoutMigration(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			snapshot, err := database.SSOSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !snapshot.HasConfiguration || snapshot.Config.ProviderInstanceID != "schema34-provider" ||
				snapshot.ClientSecret != "schema34-secret" {
				t.Fatalf("SSO configuration changed: %+v secret=%q", snapshot.Config, snapshot.ClientSecret)
			}
			if snapshot.Binding == nil || snapshot.Binding.ID != "schema34-binding" ||
				snapshot.Binding.Subject != "schema34-subject" {
				t.Fatalf("SSO binding changed: %+v", snapshot.Binding)
			}
		})
	}
}

func createSchema34AuthFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE devices (id TEXT PRIMARY KEY)`,
		`CREATE TABLE upstream_proxies (id TEXT PRIMARY KEY)`,
		`CREATE TABLE sms_messages (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE admins (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			credential_epoch INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE sso_bindings (
			id TEXT PRIMARY KEY,
			admin_id INTEGER NOT NULL UNIQUE REFERENCES admins(id) ON DELETE CASCADE,
			provider_instance_id TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('oauth2', 'oidc')),
			authority TEXT NOT NULL,
			subject TEXT NOT NULL CHECK (length(subject) > 0),
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_login_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE(provider_instance_id, authority, subject)
		)`,
		`CREATE TABLE sessions (
			token_hash BLOB PRIMARY KEY,
			admin_id INTEGER NOT NULL,
			csrf_hash BLOB NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			login_method TEXT NOT NULL DEFAULT 'password'
				CHECK (login_method IN ('password', 'sso')),
			sso_binding_id TEXT REFERENCES sso_bindings(id) ON DELETE CASCADE,
			credential_epoch INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			token_hash BLOB NOT NULL UNIQUE,
			read_only INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
			expires_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			last_used_at INTEGER NOT NULL DEFAULT 0,
			credential_epoch INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE sso_flows (
			id TEXT PRIMARY KEY,
			state_hash BLOB NOT NULL UNIQUE CHECK (length(state_hash) = 32),
			browser_token_hash BLOB NOT NULL CHECK (length(browser_token_hash) = 32),
			purpose TEXT NOT NULL CHECK (purpose IN ('login', 'bind')),
			provider_instance_id TEXT NOT NULL,
			config_revision INTEGER NOT NULL,
			initiating_session_hash BLOB REFERENCES sessions(token_hash) ON DELETE CASCADE,
			admin_id INTEGER REFERENCES admins(id) ON DELETE CASCADE,
			nonce_hash BLOB,
			pkce_verifier TEXT NOT NULL DEFAULT '',
			redirect_uri TEXT NOT NULL,
			return_path TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			claimed_at INTEGER NOT NULL DEFAULT 0,
			CHECK (expires_at > created_at),
			CHECK ((purpose = 'login' AND initiating_session_hash IS NULL AND admin_id IS NULL)
				OR (purpose = 'bind' AND initiating_session_hash IS NOT NULL AND admin_id IS NOT NULL))
		)`,
		`CREATE TABLE app_settings (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL,
			sensitive INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0, 1)),
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE device_proxy_bindings (
			iccid TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			profile_name TEXT NOT NULL DEFAULT '',
			upstream_proxy_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE,
			FOREIGN KEY (upstream_proxy_id) REFERENCES upstream_proxies(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	config, err := json.Marshal(store.SSOConfig{
		Revision:           1,
		ProviderInstanceID: "schema34-provider",
		Enabled:            true,
		Kind:               "pocketid",
		ClientID:           "vonet",
		IssuerURL:          "https://id.example.com",
		PublicOrigin:       "https://vonet.example.com",
		Scopes:             []string{"openid", "profile"},
		TokenAuthMethod:    "client_secret_basic",
		PKCEMode:           "s256",
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := json.Marshal("schema34-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{
			`INSERT INTO admins
				(id, username, password_hash, created_at, updated_at, credential_epoch)
				VALUES (1, 'admin', X'1234', 1, 1, 0)`,
			nil,
		},
		{
			`INSERT INTO sso_bindings
				(id, admin_id, provider_instance_id, protocol, authority, subject, created_at, updated_at)
				VALUES ('schema34-binding', 1, 'schema34-provider', 'oidc',
					'https://id.example.com', 'schema34-subject', 1, 1)`,
			nil,
		},
		{
			`INSERT INTO sessions
				(token_hash, admin_id, csrf_hash, expires_at, created_at, credential_epoch)
				VALUES (X'11', 1, X'22', 4102444800, 1, 0)`,
			nil,
		},
		{
			`INSERT INTO api_tokens
				(name, token_hash, read_only, expires_at, created_at, last_used_at, credential_epoch)
				VALUES ('restored', X'33', 0, 0, 1, 0, 0)`,
			nil,
		},
		{
			`INSERT INTO sso_flows
				(id, state_hash, browser_token_hash, purpose, provider_instance_id,
					config_revision, pkce_verifier, redirect_uri, return_path, created_at, expires_at)
				VALUES ('restored-flow', randomblob(32), randomblob(32), 'login',
					'schema34-provider', 1, '', 'https://vonet.example.com/api/auth/sso/callback',
					'/', 1, 2)`,
			nil,
		},
		{
			`INSERT INTO app_settings (key, value_json, sensitive, updated_at)
				VALUES (?, ?, 0, 1)`,
			[]any{store.SSOConfigKey, string(config)},
		},
		{
			`INSERT INTO app_settings (key, value_json, sensitive, updated_at)
				VALUES (?, ?, 1, 1)`,
			[]any{store.SSOClientSecretKey, string(secret)},
		},
		{`PRAGMA user_version = 34`, nil},
	} {
		if _, err := db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}
