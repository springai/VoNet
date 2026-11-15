package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DeviceProxyBindingGroup is one manual proxy policy for an ICCID. DeviceID is
// optional so a route can be prepared before the card is bound to a device.
type DeviceProxyBindingGroup struct {
	ID          string
	DeviceID    string
	ICCID       string
	ProfileName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DeviceProxyBindingRoute is one ordered upstream candidate in a binding group.
type DeviceProxyBindingRoute struct {
	ID              string
	BindingGroupID  string
	UpstreamProxyID string
	Priority        int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DeviceProxyRouteState preserves the selected route across restarts.
type DeviceProxyRouteState struct {
	BindingGroupID string
	CurrentRouteID string
	StickyRouteID  string
	RouteFailures  uint
	LastSuccessAt  time.Time
	UpdatedAt      time.Time
}

type DeviceProxyBindingGroupWithRoutes struct {
	DeviceProxyBindingGroup
	Routes []DeviceProxyBindingRoute
}

func ensureProxyRouteSchema(ctx context.Context, db *sql.DB) error {
	createStatements := []string{
		`CREATE TABLE IF NOT EXISTS device_proxy_binding_groups (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL DEFAULT '',
			iccid TEXT NOT NULL UNIQUE,
			profile_name TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS device_proxy_binding_routes (
			id TEXT PRIMARY KEY,
			binding_group_id TEXT NOT NULL,
			upstream_proxy_id TEXT NOT NULL,
			priority INTEGER NOT NULL CHECK (priority >= 0),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(binding_group_id, upstream_proxy_id),
			UNIQUE(binding_group_id, priority),
			FOREIGN KEY (binding_group_id) REFERENCES device_proxy_binding_groups(id) ON DELETE CASCADE,
			FOREIGN KEY (upstream_proxy_id) REFERENCES upstream_proxies(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS device_proxy_binding_routes_proxy_idx
			ON device_proxy_binding_routes(upstream_proxy_id)`,
		`CREATE TABLE IF NOT EXISTS device_proxy_route_state (
			binding_group_id TEXT PRIMARY KEY,
			current_route_id TEXT,
			sticky_route_id TEXT,
			route_failures INTEGER NOT NULL DEFAULT 0 CHECK (route_failures >= 0),
			last_success_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (binding_group_id) REFERENCES device_proxy_binding_groups(id) ON DELETE CASCADE,
			FOREIGN KEY (current_route_id) REFERENCES device_proxy_binding_routes(id) ON DELETE SET NULL,
			FOREIGN KEY (sticky_route_id) REFERENCES device_proxy_binding_routes(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS device_proxy_binding_routes_proxy_idx
			ON device_proxy_binding_routes(upstream_proxy_id)`,
	}
	for _, statement := range createStatements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply proxy route schema: %w", err)
		}
	}
	legacyNeedsRebuild, err := legacyProxyBindingSchemaNeedsRebuild(ctx, db)
	if err != nil {
		return err
	}
	if legacyNeedsRebuild {
		if err := rebuildLegacyProxyBindingSchema(ctx, db); err != nil {
			return err
		}
	}
	needsRebuild, err := proxyBindingGroupSchemaNeedsRebuild(ctx, db)
	if err != nil {
		return err
	}
	if needsRebuild {
		if err := rebuildProxyBindingGroupSchema(ctx, db); err != nil {
			return err
		}
	}
	migrationStatements := []string{
		`INSERT OR IGNORE INTO device_proxy_binding_groups (
			id, device_id, iccid, profile_name, created_at, updated_at
		)
		SELECT 'binding:' || iccid, COALESCE(device_id, ''), iccid, profile_name, created_at, updated_at
		FROM device_proxy_bindings`,
		`INSERT OR IGNORE INTO device_proxy_binding_routes (
			id, binding_group_id, upstream_proxy_id, priority, created_at, updated_at
		)
		SELECT 'route:binding:' || iccid || ':' || upstream_proxy_id,
			'binding:' || iccid, upstream_proxy_id, 0, created_at, updated_at
		FROM device_proxy_bindings`,
	}
	for _, statement := range migrationStatements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply proxy route schema: %w", err)
		}
	}
	return nil
}

func proxyBindingGroupSchemaNeedsRebuild(ctx context.Context, db *sql.DB) (bool, error) {
	var ddl string
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(sql, '') FROM sqlite_master
		WHERE type = 'table' AND name = 'device_proxy_binding_groups'
	`).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read proxy binding group schema: %w", err)
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(ddl)), " ")
	return strings.Contains(normalized, "unique(device_id, iccid)") ||
		strings.Contains(normalized, "foreign key (device_id) references devices"), nil
}

func legacyProxyBindingSchemaNeedsRebuild(ctx context.Context, db *sql.DB) (bool, error) {
	var ddl string
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(sql, '') FROM sqlite_master
		WHERE type = 'table' AND name = 'device_proxy_bindings'
	`).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read legacy proxy binding schema: %w", err)
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(ddl)), " ")
	return strings.Contains(normalized, "foreign key (device_id) references devices"), nil
}

func rebuildLegacyProxyBindingSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for legacy proxy binding rebuild: %w", err)
	}
	defer func() {
		_, _ = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy proxy binding rebuild: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE device_proxy_bindings_v2 (
			iccid TEXT PRIMARY KEY,
			device_id TEXT NOT NULL DEFAULT '',
			profile_name TEXT NOT NULL DEFAULT '',
			upstream_proxy_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (upstream_proxy_id)
				REFERENCES upstream_proxies(id) ON DELETE CASCADE
		)`,
		`INSERT INTO device_proxy_bindings_v2 (
			iccid, device_id, profile_name, upstream_proxy_id, created_at, updated_at
		)
		SELECT iccid, COALESCE(device_id, ''), profile_name, upstream_proxy_id,
			created_at, updated_at
		FROM device_proxy_bindings`,
		`DROP TABLE device_proxy_bindings`,
		`ALTER TABLE device_proxy_bindings_v2 RENAME TO device_proxy_bindings`,
		`CREATE INDEX IF NOT EXISTS device_proxy_bindings_proxy_idx
			ON device_proxy_bindings(upstream_proxy_id)`,
		`CREATE INDEX IF NOT EXISTS device_proxy_bindings_device_idx
			ON device_proxy_bindings(device_id, iccid)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild legacy proxy binding schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy proxy binding rebuild: %w", err)
	}
	return nil
}

func rebuildProxyBindingGroupSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for proxy binding rebuild: %w", err)
	}
	defer func() {
		_, _ = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin proxy binding group rebuild: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE device_proxy_binding_groups_v2 (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL DEFAULT '',
			iccid TEXT NOT NULL UNIQUE,
			profile_name TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO device_proxy_binding_groups_v2 (
			id, device_id, iccid, profile_name, created_at, updated_at
		)
		SELECT g.id, COALESCE(g.device_id, ''), g.iccid, g.profile_name,
			g.created_at, g.updated_at
		FROM device_proxy_binding_groups g
		WHERE g.id = (
			SELECT candidate.id
			FROM device_proxy_binding_groups candidate
			WHERE candidate.iccid = g.iccid
			ORDER BY candidate.updated_at DESC, candidate.id DESC
			LIMIT 1
		)`,
		`CREATE TABLE device_proxy_binding_routes_v2 (
			id TEXT PRIMARY KEY,
			binding_group_id TEXT NOT NULL,
			upstream_proxy_id TEXT NOT NULL,
			priority INTEGER NOT NULL CHECK (priority >= 0),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(binding_group_id, upstream_proxy_id),
			UNIQUE(binding_group_id, priority),
			FOREIGN KEY (binding_group_id)
				REFERENCES device_proxy_binding_groups(id) ON DELETE CASCADE,
			FOREIGN KEY (upstream_proxy_id)
				REFERENCES upstream_proxies(id) ON DELETE CASCADE
		)`,
		`INSERT INTO device_proxy_binding_routes_v2 (
			id, binding_group_id, upstream_proxy_id, priority, created_at, updated_at
		)
		SELECT r.id, r.binding_group_id, r.upstream_proxy_id, r.priority,
			r.created_at, r.updated_at
		FROM device_proxy_binding_routes r
		JOIN device_proxy_binding_groups_v2 g ON g.id = r.binding_group_id`,
		`CREATE TABLE device_proxy_route_state_v2 (
			binding_group_id TEXT PRIMARY KEY,
			current_route_id TEXT,
			sticky_route_id TEXT,
			route_failures INTEGER NOT NULL DEFAULT 0 CHECK (route_failures >= 0),
			last_success_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (binding_group_id)
				REFERENCES device_proxy_binding_groups(id) ON DELETE CASCADE,
			FOREIGN KEY (current_route_id)
				REFERENCES device_proxy_binding_routes(id) ON DELETE SET NULL,
			FOREIGN KEY (sticky_route_id)
				REFERENCES device_proxy_binding_routes(id) ON DELETE SET NULL
		)`,
		`INSERT INTO device_proxy_route_state_v2 (
			binding_group_id, current_route_id, sticky_route_id,
			route_failures, last_success_at, updated_at
		)
		SELECT s.binding_group_id, s.current_route_id, s.sticky_route_id,
			s.route_failures, s.last_success_at, s.updated_at
		FROM device_proxy_route_state s
		JOIN device_proxy_binding_groups_v2 g ON g.id = s.binding_group_id`,
		`DROP TABLE device_proxy_route_state`,
		`DROP TABLE device_proxy_binding_routes`,
		`DROP TABLE device_proxy_binding_groups`,
		`ALTER TABLE device_proxy_binding_groups_v2 RENAME TO device_proxy_binding_groups`,
		`ALTER TABLE device_proxy_binding_routes_v2 RENAME TO device_proxy_binding_routes`,
		`ALTER TABLE device_proxy_route_state_v2 RENAME TO device_proxy_route_state`,
		`CREATE INDEX IF NOT EXISTS device_proxy_binding_routes_proxy_idx
			ON device_proxy_binding_routes(upstream_proxy_id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild proxy binding group schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proxy binding group rebuild: %w", err)
	}
	return nil
}

func proxyBindingGroupID(iccid string) string {
	return "binding:" + strings.TrimSpace(iccid)
}

func proxyBindingRouteID(groupID, upstreamID string) string {
	return "route:" + strings.TrimSpace(groupID) + ":" + strings.TrimSpace(upstreamID)
}

func normalizeProxyBindingGroup(value DeviceProxyBindingGroup) (DeviceProxyBindingGroup, error) {
	value.DeviceID = strings.TrimSpace(value.DeviceID)
	value.ICCID = strings.TrimSpace(value.ICCID)
	value.ProfileName = strings.TrimSpace(value.ProfileName)
	if value.ICCID == "" {
		return DeviceProxyBindingGroup{}, errors.New("proxy binding group requires ICCID")
	}
	value.ID = proxyBindingGroupID(value.ICCID)
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	}
	return value, nil
}

func validateProxyRouteIDs(proxyIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(proxyIDs))
	values := make([]string, 0, len(proxyIDs))
	for _, raw := range proxyIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, errors.New("proxy route ID cannot be empty")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate proxy route %q", id)
		}
		seen[id] = struct{}{}
		values = append(values, id)
	}
	if len(values) == 0 {
		return nil, errors.New("at least one proxy route is required")
	}
	return values, nil
}

// SaveDeviceProxyBindingGroup atomically creates or replaces a binding group
// and its ordered routes.
func (s *Store) SaveDeviceProxyBindingGroup(ctx context.Context, value DeviceProxyBindingGroup, proxyIDs []string) error {
	value, err := normalizeProxyBindingGroup(value)
	if err != nil {
		return err
	}
	proxyIDs, err = validateProxyRouteIDs(proxyIDs)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin proxy binding group save: %w", err)
	}
	defer tx.Rollback()
	var existingID string
	var existingCreatedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, created_at FROM device_proxy_binding_groups WHERE iccid = ?
	`, value.ICCID).Scan(&existingID, &existingCreatedAt)
	switch {
	case err == nil:
		value.ID = existingID
		value.CreatedAt = time.Unix(existingCreatedAt, 0).UTC()
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("read existing proxy binding group %q: %w", value.ICCID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_proxy_binding_groups (
			id, device_id, iccid, profile_name, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid) DO UPDATE SET
			device_id = excluded.device_id,
			profile_name = excluded.profile_name,
			created_at = device_proxy_binding_groups.created_at,
			updated_at = excluded.updated_at
	`, value.ID, value.DeviceID, value.ICCID, value.ProfileName, value.CreatedAt.Unix(), value.UpdatedAt.Unix()); err != nil {
		return fmt.Errorf("save proxy binding group: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_proxy_binding_routes WHERE binding_group_id = ?`, value.ID); err != nil {
		return fmt.Errorf("clear proxy binding routes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_proxy_route_state SET current_route_id = NULL, sticky_route_id = NULL, route_failures = 0, updated_at = ? WHERE binding_group_id = ?`, value.UpdatedAt.Unix(), value.ID); err != nil {
		return fmt.Errorf("reset proxy route state: %w", err)
	}
	now := value.UpdatedAt
	for priority, proxyID := range proxyIDs {
		routeID := proxyBindingRouteID(value.ID, proxyID)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO device_proxy_binding_routes (
				id, binding_group_id, upstream_proxy_id, priority, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, routeID, value.ID, proxyID, priority, now.Unix(), now.Unix()); err != nil {
			return fmt.Errorf("insert proxy binding route %q: %w", proxyID, err)
		}
	}
	return tx.Commit()
}

func (s *Store) DeviceProxyBindingGroupByICCID(ctx context.Context, iccid string) (DeviceProxyBindingGroupWithRoutes, error) {
	group, err := scanProxyBindingGroup(s.db.QueryRowContext(ctx, `
		SELECT id, device_id, iccid, profile_name, created_at, updated_at
		FROM device_proxy_binding_groups WHERE iccid = ?
	`, strings.TrimSpace(iccid)))
	if err != nil {
		return DeviceProxyBindingGroupWithRoutes{}, err
	}
	routes, err := s.ListDeviceProxyBindingRoutes(ctx, group.ID)
	if err != nil {
		return DeviceProxyBindingGroupWithRoutes{}, err
	}
	return DeviceProxyBindingGroupWithRoutes{DeviceProxyBindingGroup: group, Routes: routes}, nil
}

// DeviceProxyBindingGroup keeps the historical deviceID+ICCID signature while
// resolving ownership by ICCID alone.
func (s *Store) DeviceProxyBindingGroup(ctx context.Context, _ string, iccid string) (DeviceProxyBindingGroupWithRoutes, error) {
	return s.DeviceProxyBindingGroupByICCID(ctx, iccid)
}

func (s *Store) listDeviceProxyBindingGroupsUnsafe(ctx context.Context, deviceID string) ([]DeviceProxyBindingGroupWithRoutes, error) {
	query := `
		SELECT id, device_id, iccid, profile_name, created_at, updated_at
		FROM device_proxy_binding_groups
	`
	args := []any{}
	if strings.TrimSpace(deviceID) != "" {
		query += ` WHERE device_id = ?`
		args = append(args, strings.TrimSpace(deviceID))
	}
	query += ` ORDER BY device_id, iccid`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list proxy binding groups: %w", err)
	}
	defer rows.Close()
	values := make([]DeviceProxyBindingGroupWithRoutes, 0)
	for rows.Next() {
		group, err := scanProxyBindingGroup(rows)
		if err != nil {
			return nil, err
		}
		routes, err := s.ListDeviceProxyBindingRoutes(ctx, group.ID)
		if err != nil {
			return nil, err
		}
		values = append(values, DeviceProxyBindingGroupWithRoutes{DeviceProxyBindingGroup: group, Routes: routes})
	}
	return values, rows.Err()
}

func (s *Store) ListDeviceProxyBindingRoutes(ctx context.Context, groupID string) ([]DeviceProxyBindingRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, binding_group_id, upstream_proxy_id, priority, created_at, updated_at
		FROM device_proxy_binding_routes
		WHERE binding_group_id = ?
		ORDER BY priority, id
	`, strings.TrimSpace(groupID))
	if err != nil {
		return nil, fmt.Errorf("list proxy binding routes: %w", err)
	}
	defer rows.Close()
	values := make([]DeviceProxyBindingRoute, 0)
	for rows.Next() {
		var value DeviceProxyBindingRoute
		var createdAt, updatedAt int64
		if err := rows.Scan(&value.ID, &value.BindingGroupID, &value.UpstreamProxyID, &value.Priority, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan proxy binding route: %w", err)
		}
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) DeleteDeviceProxyBindingGroupByICCID(ctx context.Context, iccid string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM device_proxy_binding_groups WHERE iccid = ?
	`, strings.TrimSpace(iccid))
	if err != nil {
		return fmt.Errorf("delete proxy binding group: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) DeleteDeviceProxyBindingGroup(ctx context.Context, _ string, iccid string) error {
	return s.DeleteDeviceProxyBindingGroupByICCID(ctx, iccid)
}

func (s *Store) SaveDeviceProxyRouteState(ctx context.Context, value DeviceProxyRouteState) error {
	value.BindingGroupID = strings.TrimSpace(value.BindingGroupID)
	if value.BindingGroupID == "" {
		return errors.New("proxy route state requires binding group ID")
	}
	now := value.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lastSuccess := value.LastSuccessAt
	if lastSuccess.IsZero() {
		lastSuccess = time.Unix(0, 0).UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO device_proxy_route_state (
			binding_group_id, current_route_id, sticky_route_id,
			route_failures, last_success_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(binding_group_id) DO UPDATE SET
			current_route_id = excluded.current_route_id,
			sticky_route_id = excluded.sticky_route_id,
			route_failures = excluded.route_failures,
			last_success_at = excluded.last_success_at,
			updated_at = excluded.updated_at
	`, value.BindingGroupID, value.CurrentRouteID, value.StickyRouteID,
		value.RouteFailures, lastSuccess.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("save proxy route state: %w", err)
	}
	return nil
}

func (s *Store) DeviceProxyRouteState(ctx context.Context, groupID string) (DeviceProxyRouteState, error) {
	var value DeviceProxyRouteState
	var currentRoute, stickyRoute sql.NullString
	var lastSuccess, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT binding_group_id, current_route_id, sticky_route_id,
			route_failures, last_success_at, updated_at
		FROM device_proxy_route_state WHERE binding_group_id = ?
	`, strings.TrimSpace(groupID)).Scan(
		&value.BindingGroupID, &currentRoute, &stickyRoute,
		&value.RouteFailures, &lastSuccess, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceProxyRouteState{}, ErrNotFound
	}
	if err != nil {
		return DeviceProxyRouteState{}, fmt.Errorf("read proxy route state: %w", err)
	}
	value.CurrentRouteID = currentRoute.String
	value.StickyRouteID = stickyRoute.String
	value.LastSuccessAt = time.Unix(lastSuccess, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func scanProxyBindingGroup(row rowScanner) (DeviceProxyBindingGroup, error) {
	var value DeviceProxyBindingGroup
	var createdAt, updatedAt int64
	err := row.Scan(&value.ID, &value.DeviceID, &value.ICCID, &value.ProfileName, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceProxyBindingGroup{}, ErrNotFound
	}
	if err != nil {
		return DeviceProxyBindingGroup{}, fmt.Errorf("scan proxy binding group: %w", err)
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}
