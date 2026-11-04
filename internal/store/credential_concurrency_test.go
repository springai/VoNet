package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenWithoutMigrationCleansSchema34WithoutUpgrading(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "schema34.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 34; version++ {
		for _, statement := range migrationStatements(version) {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				db.Close()
				t.Fatalf("schema migration %d: %v", version, err)
			}
		}
	}
	for _, statement := range []string{
		`PRAGMA user_version = 34`,
		`INSERT INTO admins
			(id, username, password_hash, created_at, updated_at, credential_epoch)
			VALUES (1, 'admin', X'1234', 1, 1, 0)`,
		`INSERT INTO sso_bindings
			(id, admin_id, provider_instance_id, protocol, authority, subject, created_at, updated_at)
			VALUES ('binding', 1, 'provider', 'oidc', 'https://id.example', 'subject', 1, 1)`,
		`INSERT INTO sessions
			(token_hash, admin_id, csrf_hash, expires_at, created_at, credential_epoch)
			VALUES (X'11', 1, X'22', 4102444800, 1, 0)`,
		`INSERT INTO api_tokens
			(name, token_hash, read_only, expires_at, created_at, last_used_at, credential_epoch)
			VALUES ('restored', X'33', 0, 0, 1, 0, 0)`,
		`INSERT INTO sso_flows
			(id, state_hash, browser_token_hash, purpose, provider_instance_id,
				config_revision, pkce_verifier, redirect_uri, return_path, created_at, expires_at)
			VALUES ('flow', randomblob(32), randomblob(32), 'login', 'provider', 1,
				'', 'https://vonet.example/api/auth/sso/callback', '/', 1, 2)`,
		`INSERT INTO app_settings (key, value_json, sensitive, updated_at)
			VALUES ('auth.sso.config',
				'{"revision":1,"provider_instance_id":"provider","enabled":true,"kind":"pocketid","client_id":"vonet","issuer_url":"https://id.example","public_origin":"https://vonet.example","scopes":["openid"],"token_auth_method":"client_secret_basic","pkce_mode":"s256"}',
				0, 1)`,
		`INSERT INTO app_settings (key, value_json, sensitive, updated_at)
			VALUES ('auth.sso.client_secret', '"secret"', 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := OpenWithoutMigration(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertSchema34AuthState(t, database, true)
	if err := database.DeleteAllAuthCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	assertSchema34AuthState(t, database, false)

	snapshot, err := database.SSOSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasConfiguration || snapshot.Config.ProviderInstanceID != "provider" ||
		snapshot.ClientSecret != "secret" || snapshot.Binding == nil ||
		snapshot.Binding.ID != "binding" {
		t.Fatalf("schema 34 SSO state changed: %+v secret=%q binding=%+v",
			snapshot.Config, snapshot.ClientSecret, snapshot.Binding)
	}
}

func assertSchema34AuthState(t *testing.T, database *Store, credentialsPresent bool) {
	t.Helper()
	ctx := context.Background()
	var version int
	var secureCookieColumns int
	var credentialRows int
	for _, query := range []struct {
		sql string
		dst any
	}{
		{`PRAGMA user_version`, &version},
		{`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'secure_cookies'`, &secureCookieColumns},
		{`SELECT
			(SELECT COUNT(*) FROM sessions) +
			(SELECT COUNT(*) FROM api_tokens) +
			(SELECT COUNT(*) FROM sso_flows)`, &credentialRows},
	} {
		if err := database.db.QueryRowContext(ctx, query.sql).Scan(query.dst); err != nil {
			t.Fatal(err)
		}
	}
	if version != 34 || secureCookieColumns != 0 {
		t.Fatalf("schema changed: version=%d secure_cookie_columns=%d", version, secureCookieColumns)
	}
	if credentialsPresent && credentialRows != 3 {
		t.Fatalf("credential rows = %d, want 3", credentialRows)
	}
	if !credentialsPresent && credentialRows != 0 {
		t.Fatalf("credential rows after cleanup = %d", credentialRows)
	}
}

func TestCreateSessionConcurrentWithDeleteAllAuthCredentials(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}

	const writers = 24
	start := make(chan struct{})
	hashes := make(chan []byte, writers)
	errs := make(chan error, writers+1)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		tokenHash := []byte(fmt.Sprintf("session-%02d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := database.CreateSession(
				ctx,
				1,
				tokenHash,
				[]byte(fmt.Sprintf("csrf-%02d", i)),
				time.Now().Add(time.Hour),
				false,
			)
			if err == nil {
				hashes <- tokenHash
			}
			errs <- err
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		errs <- database.DeleteAllAuthCredentials(ctx)
	}()
	close(start)
	wg.Wait()
	close(errs)
	close(hashes)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent session operation: %v", err)
		}
	}

	admin, err := database.CurrentAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for tokenHash := range hashes {
		assertStoredCredentialEpoch(
			t, database.db, "SELECT credential_epoch FROM sessions WHERE token_hash = ?", tokenHash, admin.CredentialEpoch,
		)
	}
	assertAllCredentialEpochs(
		t, database.db, "SELECT credential_epoch FROM sessions", admin.CredentialEpoch,
	)
}

func TestCreateAPITokenConcurrentWithDeleteAllAuthCredentials(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}

	const writers = 24
	start := make(chan struct{})
	hashes := make(chan []byte, writers)
	errs := make(chan error, writers+1)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		tokenHash := []byte(fmt.Sprintf("api-token-%02d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := database.CreateAPIToken(
				ctx,
				fmt.Sprintf("token-%02d", i),
				tokenHash,
				false,
				time.Time{},
			)
			if err == nil {
				hashes <- tokenHash
			}
			errs <- err
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		errs <- database.DeleteAllAuthCredentials(ctx)
	}()
	close(start)
	wg.Wait()
	close(errs)
	close(hashes)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent API token operation: %v", err)
		}
	}

	admin, err := database.CurrentAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for tokenHash := range hashes {
		assertStoredCredentialEpoch(
			t, database.db, "SELECT credential_epoch FROM api_tokens WHERE token_hash = ?", tokenHash, admin.CredentialEpoch,
		)
	}
	assertAllCredentialEpochs(
		t, database.db, "SELECT credential_epoch FROM api_tokens", admin.CredentialEpoch,
	)
}

func TestDeleteAllAuthCredentialsConcurrent(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := database.CreateSession(
			ctx,
			1,
			[]byte(fmt.Sprintf("session-%d", i)),
			[]byte(fmt.Sprintf("csrf-%d", i)),
			time.Now().Add(time.Hour),
			false,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := database.CreateAPIToken(
			ctx,
			fmt.Sprintf("token-%d", i),
			[]byte(fmt.Sprintf("api-token-%d", i)),
			false,
			time.Time{},
		); err != nil {
			t.Fatal(err)
		}
	}

	const cleaners = 12
	start := make(chan struct{})
	errs := make(chan error, cleaners)
	var wg sync.WaitGroup
	for i := 0; i < cleaners; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- database.DeleteAllAuthCredentials(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent credential cleanup: %v", err)
		}
	}

	for _, table := range []string{"sessions", "api_tokens", "sso_flows"} {
		var count int
		if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows after concurrent cleanup = %d", table, count)
		}
	}
	admin, err := database.CurrentAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if admin.CredentialEpoch != cleaners {
		t.Fatalf("credential epoch = %d, want %d", admin.CredentialEpoch, cleaners)
	}
}

func TestDeleteAllAuthCredentialsRollsBackAtomically(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	sessionHash := []byte("session")
	tokenHash := []byte("api-token")
	if err := database.CreateSession(
		ctx, 1, sessionHash, []byte("csrf"), time.Now().Add(time.Hour), false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAPIToken(ctx, "token", tokenHash, false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER reject_auth_cleanup BEFORE DELETE ON sessions
		BEGIN SELECT RAISE(ABORT, 'simulated cleanup failure'); END
	`); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteAllAuthCredentials(ctx); err == nil {
		t.Fatal("credential cleanup succeeded despite simulated failure")
	}
	admin, err := database.CurrentAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if admin.CredentialEpoch != 0 {
		t.Fatalf("credential epoch changed after rollback: %d", admin.CredentialEpoch)
	}
	assertStoredCredentialEpoch(
		t, database.db, "SELECT credential_epoch FROM sessions WHERE token_hash = ?", sessionHash, 0,
	)
	assertStoredCredentialEpoch(
		t, database.db, "SELECT credential_epoch FROM api_tokens WHERE token_hash = ?", tokenHash, 0,
	)
}

func TestUpdateSessionCSRFIfUnchangedConcurrentSingleWinner(t *testing.T) {
	ctx := context.Background()
	database := newTokenStore(t)
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	tokenHash := []byte("session")
	expected := []byte("csrf-before")
	if err := database.CreateSession(
		ctx, 1, tokenHash, expected, time.Now().Add(time.Hour), false,
	); err != nil {
		t.Fatal(err)
	}

	const refreshers = 32
	start := make(chan struct{})
	type result struct {
		csrf []byte
		err  error
	}
	results := make(chan result, refreshers)
	var wg sync.WaitGroup
	for i := 0; i < refreshers; i++ {
		replacement := []byte(fmt.Sprintf("csrf-after-%02d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- result{
				csrf: replacement,
				err: database.UpdateSessionCSRFIfUnchanged(
					ctx, tokenHash, replacement, expected,
				),
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner []byte
	var successes int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.csrf
		case errors.Is(result.err, ErrNotFound):
		default:
			t.Fatalf("concurrent CSRF refresh: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful CSRF refreshes = %d, want 1", successes)
	}
	session, err := database.SessionByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(session.CSRFHash, winner) {
		t.Fatalf("session CSRF = %q, want winning %q", session.CSRFHash, winner)
	}
}

func assertStoredCredentialEpoch(
	t *testing.T,
	db *sql.DB,
	query string,
	arg any,
	want int64,
) {
	t.Helper()
	var epoch int64
	err := db.QueryRowContext(context.Background(), query, arg).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if epoch != want {
		t.Fatalf("stored credential epoch = %d, want %d", epoch, want)
	}
}

func assertAllCredentialEpochs(t *testing.T, db *sql.DB, query string, want int64) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var epoch int64
		if err := rows.Scan(&epoch); err != nil {
			t.Fatal(err)
		}
		if epoch != want {
			t.Fatalf("stored credential epoch = %d, want %d", epoch, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
