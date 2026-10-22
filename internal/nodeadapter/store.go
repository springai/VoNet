package nodeadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrNotFound = errors.New("node adapter: not found")

type NodeAdapter struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Protocol        string          `json:"protocol"`
	NodeConfigJSON  json.RawMessage `json:"node_config_json"`
	ListenHost      string          `json:"listen_host"`
	ListenPort      int             `json:"listen_port"`
	UDPIP           string          `json:"udp_ip"`
	HTTPListenHost  string          `json:"http_listen_host"`
	HTTPListenPort  int             `json:"http_listen_port"`
	AuthEnabled     bool            `json:"auth_enabled"`
	Username        string          `json:"username"`
	Password        string          `json:"password"`
	Enabled         bool            `json:"enabled"`
	HTTPEnabled     bool            `json:"http_enabled"`
	Socks5Configured bool           `json:"socks5_configured"`
	HTTPConfigured  bool            `json:"http_configured"`
	Status          string          `json:"status"`
	LastError       string          `json:"last_error"`
	HTTPStatus      string          `json:"http_status"`
	HTTPLastError   string          `json:"http_last_error"`
	LastCheckAt     int64           `json:"last_check_at"`
	CheckResultJSON json.RawMessage `json:"check_result_json"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

const secretMask = "********"

func (v NodeAdapter) Redacted() NodeAdapter {
	if v.Password != "" {
		v.Password = secretMask
	}
	return v
}

// DB owns the node-adapter SQLite connection.
type DB struct {
	db *sql.DB
}

// Open creates the directory, opens SQLite, and runs the schema migration.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := preparePath(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open node adapter sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	closeOnError := func(err error) (*DB, error) {
		_ = db.Close()
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping node adapter sqlite: %w", err))
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
	if err := migrate(ctx, db); err != nil {
		return closeOnError(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return closeOnError(fmt.Errorf("secure node adapter sqlite: %w", err))
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read node adapter schema version: %w", err)
	}
	if version < 1 {
		for _, stmt := range schemaV1 {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply node adapter migration 1: %w", err)
			}
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("record node adapter migration 1: %w", err)
		}
		version = 1
	}
	if version < 2 {
		for _, stmt := range schemaV2 {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply node adapter migration 2: %w", err)
			}
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
			return fmt.Errorf("record node adapter migration 2: %w", err)
		}
		version = 2
	}
	if version < 3 {
		for _, stmt := range schemaV3 {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply node adapter migration 3: %w", err)
			}
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
			return fmt.Errorf("record node adapter migration 3: %w", err)
		}
		version = 3
	}
	if version < 4 {
		for _, stmt := range schemaV4 {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply node adapter migration 4: %w", err)
			}
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
			return fmt.Errorf("record node adapter migration 4: %w", err)
		}
		version = 4
	}
	if version < 5 {
		for _, stmt := range schemaV5 {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply node adapter migration 5: %w", err)
			}
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
			return fmt.Errorf("record node adapter migration 5: %w", err)
		}
	}
	return nil
}

var schemaV1 = []string{
	`CREATE TABLE node_adapters (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		protocol TEXT NOT NULL,
		node_config_json TEXT NOT NULL DEFAULT '{}',
		listen_host TEXT NOT NULL DEFAULT '',
		listen_port INTEGER NOT NULL,
		udp_ip TEXT NOT NULL DEFAULT '',
		auth_enabled INTEGER NOT NULL DEFAULT 0 CHECK (auth_enabled IN (0, 1)),
		username TEXT NOT NULL DEFAULT '',
		password TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
		status TEXT NOT NULL DEFAULT 'stopped',
		last_error TEXT NOT NULL DEFAULT '',
		last_check_at INTEGER NOT NULL DEFAULT 0,
		check_result_json TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE INDEX node_adapters_port_idx ON node_adapters(listen_port)`,
}

var schemaV2 = []string{
	`CREATE TABLE IF NOT EXISTS kv (
		key TEXT PRIMARY KEY,
		value_json TEXT NOT NULL DEFAULT '{}'
	)`,
}

var schemaV3 = []string{
	"ALTER TABLE node_adapters ADD COLUMN http_listen_host TEXT NOT NULL DEFAULT '0.0.0.0';",
	"ALTER TABLE node_adapters ADD COLUMN http_listen_port INTEGER NOT NULL DEFAULT 0;",
	"ALTER TABLE node_adapters ADD COLUMN http_enabled INTEGER NOT NULL DEFAULT 0;",
}

var schemaV4 = []string{
	"ALTER TABLE node_adapters ADD COLUMN http_status TEXT NOT NULL DEFAULT '';",
	"ALTER TABLE node_adapters ADD COLUMN http_last_error TEXT NOT NULL DEFAULT '';",
}

var schemaV5 = []string{
	"ALTER TABLE node_adapters ADD COLUMN socks5_configured INTEGER NOT NULL DEFAULT 1;",
	"ALTER TABLE node_adapters ADD COLUMN http_configured INTEGER NOT NULL DEFAULT 0;",
}


func preparePath(path string) error {
	parent := filepath.Dir(path)
	if parent == "." {
		return nil
	}
	return os.MkdirAll(parent, 0o750)
}

// --- CRUD ---

const adapterSelect = `SELECT
	id, name, protocol, node_config_json,
	listen_host, listen_port, udp_ip, http_listen_host, http_listen_port,
	auth_enabled, username, password,
	enabled, http_enabled, socks5_configured, http_configured, status, last_error, http_status, http_last_error,
	last_check_at, check_result_json,
	created_at, updated_at
FROM node_adapters`

func scanAdapter(scanner interface{ Scan(...any) error }) (NodeAdapter, error) {
	var v NodeAdapter
	var configRaw, checkRaw []byte
	var createdAt, updatedAt int64
	var authEnabled, enabled int
	err := scanner.Scan(
		&v.ID, &v.Name, &v.Protocol, &configRaw,
		&v.ListenHost, &v.ListenPort, &v.UDPIP, &v.HTTPListenHost, &v.HTTPListenPort,
		&authEnabled, &v.Username, &v.Password,
		&enabled, &v.HTTPEnabled, &v.Socks5Configured, &v.HTTPConfigured, &v.Status, &v.LastError, &v.HTTPStatus, &v.HTTPLastError,
		&v.LastCheckAt, &checkRaw,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodeAdapter{}, ErrNotFound
		}
		return NodeAdapter{}, fmt.Errorf("scan node adapter: %w", err)
	}
	v.NodeConfigJSON = json.RawMessage(configRaw)
	v.CheckResultJSON = json.RawMessage(checkRaw)
	v.AuthEnabled = authEnabled != 0
	v.Enabled = enabled != 0
	v.CreatedAt = time.Unix(createdAt, 0).UTC()
	v.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return v, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (d *DB) Upsert(ctx context.Context, v NodeAdapter) error {
	v.ID = strings.TrimSpace(v.ID)
	v.Name = strings.TrimSpace(v.Name)
	v.Protocol = strings.ToLower(strings.TrimSpace(v.Protocol))
	v.ListenHost = strings.TrimSpace(v.ListenHost)
	v.UDPIP = strings.TrimSpace(v.UDPIP)
	v.Username = strings.TrimSpace(v.Username)

	if v.ID == "" {
		return errors.New("node adapter id is required")
	}
	if v.Name == "" {
		return errors.New("node adapter name is required")
	}
	if v.Protocol == "" {
		return errors.New("node adapter protocol is required")
	}
	if v.Socks5Configured && (v.ListenPort < 1 || v.ListenPort > 65535) {
		return errors.New("listen port must be between 1 and 65535")
	}
	if len(v.NodeConfigJSON) == 0 {
		v.NodeConfigJSON = json.RawMessage("{}")
	}
	if len(v.CheckResultJSON) == 0 {
		v.CheckResultJSON = json.RawMessage("{}")
	}
	if !v.AuthEnabled {
		v.Username = ""
		v.Password = ""
	} else {
		existing, existingErr := d.Get(ctx, v.ID)
		if existingErr == nil && (v.Password == "" || v.Password == secretMask) {
			v.Password = existing.Password
		}
		if strings.TrimSpace(v.Username) == "" {
			return errors.New("authentication requires username")
		}
	}
	if v.Status == "" {
		v.Status = "stopped"
	}

	now := time.Now().UTC()
	createdAt := v.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := v.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}

	_, err := d.db.ExecContext(ctx, `
		INSERT INTO node_adapters (
			id, name, protocol, node_config_json,
			listen_host, listen_port, udp_ip,
			http_listen_host, http_listen_port,
			auth_enabled, username, password,
			enabled, http_enabled, socks5_configured, http_configured, status, last_error, http_status, http_last_error,
			last_check_at, check_result_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			protocol = excluded.protocol,
			node_config_json = excluded.node_config_json,
			listen_host = excluded.listen_host,
			listen_port = excluded.listen_port,
			udp_ip = excluded.udp_ip,
			http_listen_host = excluded.http_listen_host,
			http_listen_port = excluded.http_listen_port,
			auth_enabled = excluded.auth_enabled,
			username = excluded.username,
			password = excluded.password,
			enabled = excluded.enabled,
			http_enabled = excluded.http_enabled,
			socks5_configured = excluded.socks5_configured,
			http_configured = excluded.http_configured,
			status = excluded.status,
			last_error = excluded.last_error,
			http_status = excluded.http_status,
			http_last_error = excluded.http_last_error,
			last_check_at = excluded.last_check_at,
			check_result_json = excluded.check_result_json,
			updated_at = excluded.updated_at
	`,
		v.ID, v.Name, v.Protocol, string(v.NodeConfigJSON),
		v.ListenHost, v.ListenPort, v.UDPIP,
		v.HTTPListenHost, v.HTTPListenPort,
		boolInt(v.AuthEnabled), v.Username, v.Password,
		boolInt(v.Enabled), boolInt(v.HTTPEnabled), boolInt(v.Socks5Configured), boolInt(v.HTTPConfigured), v.Status, v.LastError, v.HTTPStatus, v.HTTPLastError,
		v.LastCheckAt, string(v.CheckResultJSON),
		createdAt.Unix(), updatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert node adapter: %w", err)
	}
	return nil
}

func (d *DB) Get(ctx context.Context, id string) (NodeAdapter, error) {
	row := d.db.QueryRowContext(ctx, adapterSelect+` WHERE id = ?`, strings.TrimSpace(id))
	return scanAdapter(row)
}

func (d *DB) List(ctx context.Context) ([]NodeAdapter, error) {
	rows, err := d.db.QueryContext(ctx, adapterSelect+` ORDER BY listen_port ASC`)
	if err != nil {
		return nil, fmt.Errorf("list node adapters: %w", err)
	}
	defer rows.Close()
	var result []NodeAdapter
	for rows.Next() {
		v, err := scanAdapter(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (d *DB) Delete(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, `DELETE FROM node_adapters WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("delete node adapter: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) UpdateStatus(ctx context.Context, id string, status string, lastError string) error {
	now := time.Now().UTC().Unix()
	_, err := d.db.ExecContext(ctx, `
		UPDATE node_adapters SET status = ?, last_error = ?, updated_at = ? WHERE id = ?
	`, status, lastError, now, strings.TrimSpace(id))
	return err
}

func (d *DB) UpdateHTTPStatus(ctx context.Context, id string, status string, lastError string) error {
	_, err := d.db.ExecContext(ctx,
		"UPDATE node_adapters SET http_status = ?, http_last_error = ?, updated_at = ? WHERE id = ?",
		status, lastError, time.Now().UTC().Unix(), id,
	)
	return err
}

func (d *DB) UpdateCheckResult(ctx context.Context, id string, result json.RawMessage) error {
	now := time.Now().UTC().Unix()
	_, err := d.db.ExecContext(ctx, `
		UPDATE node_adapters SET check_result_json = ?, last_check_at = ?, updated_at = ? WHERE id = ?
	`, string(result), now, now, strings.TrimSpace(id))
	return err
}

func (d *DB) MaxPort(ctx context.Context) (int, error) {
	var maxPort sql.NullInt64
	err := d.db.QueryRowContext(ctx, `SELECT MAX(listen_port) FROM node_adapters`).Scan(&maxPort)
	if err != nil {
		return 0, err
	}
	if !maxPort.Valid {
		return 0, nil
	}
	return int(maxPort.Int64), nil
}
