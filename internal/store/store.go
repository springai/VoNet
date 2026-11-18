package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 42

var ErrNotFound = errors.New("store: not found")
var ErrAlreadyExists = errors.New("store: already exists")

// Store owns the SQLite connection used by the process.
type Store struct {
	db           *sql.DB
	sensitive    *fieldCipher
	sensitiveMu  sync.Mutex
	logMu        sync.Mutex
	logClearedAt time.Time
	logMinLevel  string

	startupSMSID  int64 // see StartupSMSMessageID
	smsInsertedMu sync.Mutex
	smsInserted   chan struct{} // see SMSInserted
}

type Admin struct {
	ID              int64
	Username        string
	PasswordHash    []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CredentialEpoch int64
}

type Session struct {
	TokenHash       []byte
	CSRFHash        []byte
	ExpiresAt       time.Time
	CreatedAt       time.Time
	LoginMethod     string
	SSOBindingID    string
	CredentialEpoch int64
	SecureCookies   bool
	Admin           Admin
}

// Open creates the parent directory, opens SQLite, applies safety pragmas and
// runs the built-in schema migration.
func Open(ctx context.Context, path string) (*Store, error) {
	return open(ctx, path, true)
}

// OpenWithoutMigration opens an existing database and applies connection
// safety settings without changing its schema. It is intended for maintenance
// operations such as credential cleanup after restoring an older snapshot.
func OpenWithoutMigration(ctx context.Context, path string) (*Store, error) {
	return open(ctx, path, false)
}

func open(ctx context.Context, path string, migrateSchema bool) (*Store, error) {
	if err := prepareDatabasePath(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	closeOnError := func(err error) (*Store, error) {
		_ = db.Close()
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping sqlite: %w", err))
	}
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return closeOnError(fmt.Errorf("%s: %w", pragma, err))
		}
	}
	if migrateSchema {
		if err := migrate(ctx, db); err != nil {
			return closeOnError(err)
		}
	}
	if isFilesystemPath(path) {
		if err := os.Chmod(path, 0o600); err != nil {
			return closeOnError(fmt.Errorf("secure sqlite file: %w", err))
		}
	}
	sensitive, err := newFieldCipher(path)
	if err != nil {
		return closeOnError(fmt.Errorf("open store data key: %w", err))
	}
	s := &Store{db: db, sensitive: sensitive}
	if !migrateSchema {
		return s, nil
	}
	if err := s.encryptLegacySensitiveFields(ctx); err != nil {
		return closeOnError(err)
	}
	if err := s.DeleteExpiredSSOFlows(ctx, time.Now()); err != nil {
		return closeOnError(err)
	}
	// Captured before this process can save an SMS; see StartupSMSMessageID.
	if s.startupSMSID, err = s.LatestSMSMessageID(ctx); err != nil {
		return closeOnError(err)
	}
	return s, nil
}

func prepareDatabasePath(path string) error {
	if !isFilesystemPath(path) {
		return nil
	}
	parent := filepath.Dir(path)
	if parent == "." {
		return nil
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create sqlite directory: %w", err)
	}
	return nil
}

func isFilesystemPath(path string) bool {
	return path != ":memory:" && !strings.HasPrefix(path, "file:")
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("sqlite schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return ensureProxyRouteSchema(ctx, db)
	}

	for nextVersion := version + 1; nextVersion <= schemaVersion; nextVersion++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin sqlite migration %d: %w", nextVersion, err)
		}
		for _, statement := range migrationStatements(nextVersion) {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				// A database whose user_version was repaired or rolled back may
				// already contain an additive column. Remaining statements in the
				// migration are still safe and must be applied.
				duplicateAdditiveColumn := (nextVersion == 7 && strings.Contains(statement, "ADD COLUMN modem_imei")) ||
					(nextVersion == 8 && strings.Contains(statement, "ADD COLUMN device_type")) ||
					(nextVersion == 14 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion == 16 && strings.Contains(statement, "ADD COLUMN sim_pin")) ||
					(nextVersion == 19 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion == 23 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion == 25 && strings.Contains(statement, "ADD COLUMN mbn_profile")) ||
					(nextVersion == 31 && strings.Contains(statement, "ADD COLUMN revert_profile")) ||
					(nextVersion == 34 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion == 40 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion == 42 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion == 35 && strings.Contains(statement, "ADD COLUMN")) ||
					(nextVersion >= 36 && strings.Contains(statement, "ADD COLUMN"))
				if duplicateAdditiveColumn && strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
					continue
				}
				// A rolled-back or repaired database may already contain a table
				// that a migration attempts to create. Skip the statement when the
				// table (or index) already exists so remaining migrations apply.
				duplicateTable := nextVersion == 24 && strings.Contains(statement, "CREATE TABLE") || strings.Contains(statement, "CREATE INDEX")
				if duplicateTable && strings.Contains(strings.ToLower(err.Error()), "already exists") {
					continue
				}
				_ = tx.Rollback()
				return fmt.Errorf("apply sqlite migration %d: %w", nextVersion, err)
			}
		}
		if _, err := tx.ExecContext(
			ctx,
			fmt.Sprintf("PRAGMA user_version = %d", nextVersion),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record sqlite migration %d: %w", nextVersion, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit sqlite migration %d: %w", nextVersion, err)
		}
	}
	return ensureProxyRouteSchema(ctx, db)
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return errors.New("sqlite readiness query returned an unexpected value")
	}
	return nil
}

func (s *Store) CurrentAdmin(ctx context.Context) (Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at, credential_epoch
		FROM admins
		WHERE id = 1
	`))
}

func (s *Store) AdminByUsername(ctx context.Context, username string) (Admin, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at, credential_epoch
		FROM admins
		WHERE username = ?
	`, username))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAdmin(row rowScanner) (Admin, error) {
	var admin Admin
	var createdAt int64
	var updatedAt int64
	err := row.Scan(
		&admin.ID,
		&admin.Username,
		&admin.PasswordHash,
		&createdAt,
		&updatedAt,
		&admin.CredentialEpoch,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrNotFound
	}
	if err != nil {
		return Admin{}, err
	}
	admin.CreatedAt = time.Unix(createdAt, 0).UTC()
	admin.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return admin, nil
}

// SetAdmin inserts or replaces the single configured administrator and
// atomically revokes all existing sessions and API tokens.
func (s *Store) SetAdmin(ctx context.Context, username string, passwordHash []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin admin update: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Unix()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO admins (id, username, password_hash, created_at, updated_at, credential_epoch)
		VALUES (1, ?, ?, ?, ?, 0)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			password_hash = excluded.password_hash,
			updated_at = excluded.updated_at,
			credential_epoch = admins.credential_epoch + 1
	`, username, passwordHash, now, now)
	if err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("revoke sessions after admin update: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM api_tokens"); err != nil {
		return fmt.Errorf("revoke api tokens after admin update: %w", err)
	}
	if err := invalidateSSOAfterAdminUpdate(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit admin update: %w", err)
	}
	return nil
}

func (s *Store) DeleteAllSessions(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("delete all sessions: %w", err)
	}
	return nil
}

// DeleteAllAuthCredentials atomically revokes browser sessions and durable API
// tokens after a trusted binary update.
func (s *Store) DeleteAllAuthCredentials(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin auth credential revocation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("delete all sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM api_tokens"); err != nil {
		return fmt.Errorf("delete all api tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE admins SET credential_epoch = credential_epoch + 1 WHERE id = 1"); err != nil {
		return fmt.Errorf("advance credential epoch: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sso_flows"); err != nil {
		return fmt.Errorf("delete all sso flows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit auth credential revocation: %w", err)
	}
	return nil
}

func (s *Store) CreateSession(
	ctx context.Context,
	adminID int64,
	tokenHash []byte,
	csrfHash []byte,
	expiresAt time.Time,
	secureCookies bool,
) error {
	for attempt := 0; attempt < 3; attempt++ {
		retry, err := s.createSession(ctx, adminID, tokenHash, csrfHash, expiresAt, secureCookies)
		if err == nil {
			return nil
		}
		if !retry {
			return err
		}
	}
	return fmt.Errorf("create session: %w", ErrNotFound)
}

func (s *Store) createSession(
	ctx context.Context,
	adminID int64,
	tokenHash []byte,
	csrfHash []byte,
	expiresAt time.Time,
	secureCookies bool,
) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin create session: %w", err)
	}
	defer tx.Rollback()

	var credentialEpoch int64
	if err := tx.QueryRowContext(ctx, "SELECT credential_epoch FROM admins WHERE id = ?", adminID).Scan(&credentialEpoch); err != nil {
		return false, fmt.Errorf("read credential epoch: %w", err)
	}
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (
			token_hash, admin_id, csrf_hash, expires_at, created_at, secure_cookies, credential_epoch
		)
		SELECT ?, ?, ?, ?, ?, ?, a.credential_epoch
		FROM admins a
		WHERE a.id = ? AND a.credential_epoch = ?
	`, tokenHash, adminID, csrfHash, expiresAt.UTC().Unix(), now, secureCookies, adminID, credentialEpoch)
	if err != nil {
		return false, fmt.Errorf("create session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read session insert result: %w", err)
	}
	if affected != 1 {
		return true, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit create session: %w", err)
	}
	return false, nil
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, error) {
	var session Session
	var expiresAt int64
	var createdAt int64
	var adminCreatedAt int64
	var adminUpdatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT
			s.token_hash,
			s.csrf_hash,
			s.expires_at,
			s.created_at,
			s.login_method,
			COALESCE(s.sso_binding_id, ''),
			s.credential_epoch,
			s.secure_cookies,
			a.id,
			a.username,
			a.created_at,
			a.updated_at,
			a.credential_epoch
		FROM sessions s
		JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = ? AND s.credential_epoch = a.credential_epoch
	`, tokenHash).Scan(
		&session.TokenHash,
		&session.CSRFHash,
		&expiresAt,
		&createdAt,
		&session.LoginMethod,
		&session.SSOBindingID,
		&session.CredentialEpoch,
		&session.SecureCookies,
		&session.Admin.ID,
		&session.Admin.Username,
		&adminCreatedAt,
		&adminUpdatedAt,
		&session.Admin.CredentialEpoch,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	session.CreatedAt = time.Unix(createdAt, 0).UTC()
	session.Admin.CreatedAt = time.Unix(adminCreatedAt, 0).UTC()
	session.Admin.UpdatedAt = time.Unix(adminUpdatedAt, 0).UTC()
	return session, nil
}

// RequireSessionSecureCookies permanently upgrades a live session's cookie
// policy. Missing request metadata on later requests can never undo it.
func (s *Store) RequireSessionSecureCookies(ctx context.Context, tokenHash []byte) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET secure_cookies = 1
		WHERE token_hash = ?
		  AND expires_at > ?
		  AND credential_epoch = (
			SELECT credential_epoch FROM admins WHERE admins.id = sessions.admin_id
		  )
	`, tokenHash, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("secure session cookies: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read secure session update result: %w", err)
	}
	if affected != 0 {
		return nil
	}
	var active int
	var alreadySecure int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(secure_cookies), 0)
		FROM sessions
		WHERE token_hash = ?
		  AND expires_at > ?
		  AND credential_epoch = (
			SELECT credential_epoch FROM admins WHERE admins.id = sessions.admin_id
		  )
	`, tokenHash, time.Now().UTC().Unix()).Scan(&active, &alreadySecure); err != nil {
		return fmt.Errorf("read secure session update result: %w", err)
	}
	if active == 0 || alreadySecure != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateSessionCSRF(ctx context.Context, tokenHash []byte, csrfHash []byte) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET csrf_hash = ?
		WHERE token_hash = ?
	`, csrfHash, tokenHash)
	if err != nil {
		return fmt.Errorf("update session csrf: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read session update result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateSessionCSRFIfUnchanged atomically replaces exactly the CSRF digest the
// caller authenticated, preventing concurrent refreshes from silently
// invalidating one another's returned credential.
func (s *Store) UpdateSessionCSRFIfUnchanged(
	ctx context.Context,
	tokenHash []byte,
	csrfHash []byte,
	expectedCSRFHash []byte,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET csrf_hash = ?
		WHERE token_hash = ?
		  AND csrf_hash = ?
		  AND expires_at > ?
		  AND credential_epoch = (
			SELECT credential_epoch FROM admins WHERE admins.id = sessions.admin_id
		  )
	`, csrfHash, tokenHash, expectedCSRFHash, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("replace session csrf: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read session csrf update result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", now.UTC().Unix()); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
