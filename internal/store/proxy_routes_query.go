package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) DeviceProxyBindingGroupByID(ctx context.Context, id string) (DeviceProxyBindingGroupWithRoutes, error) {
	group, err := scanProxyBindingGroup(s.db.QueryRowContext(ctx, `
		SELECT id, device_id, iccid, profile_name, created_at, updated_at
		FROM device_proxy_binding_groups WHERE id = ?
	`, strings.TrimSpace(id)))
	if err != nil {
		return DeviceProxyBindingGroupWithRoutes{}, err
	}
	routes, err := s.ListDeviceProxyBindingRoutes(ctx, group.ID)
	if err != nil {
		return DeviceProxyBindingGroupWithRoutes{}, err
	}
	return DeviceProxyBindingGroupWithRoutes{DeviceProxyBindingGroup: group, Routes: routes}, nil
}

func (s *Store) DeleteDeviceProxyBindingGroupByID(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM device_proxy_binding_groups WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("delete proxy binding group by ID: %w", err)
	}
	return requireAffected(result)
}

func (s *Store) ReplaceDeviceProxyBindingRoutes(ctx context.Context, groupID string, proxyIDs []string) error {
	group, err := s.DeviceProxyBindingGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	return s.SaveDeviceProxyBindingGroup(ctx, group.DeviceProxyBindingGroup, proxyIDs)
}

func (s *Store) DeviceProxyBindingGroupExistsByICCID(ctx context.Context, iccid string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM device_proxy_binding_groups WHERE iccid = ? LIMIT 1
	`, strings.TrimSpace(iccid)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check proxy binding group: %w", err)
	}
	return one == 1, nil
}

func (s *Store) DeviceProxyBindingGroupExists(ctx context.Context, _ string, iccid string) (bool, error) {
	return s.DeviceProxyBindingGroupExistsByICCID(ctx, iccid)
}
func (s *Store) ListDeviceProxyBindingGroups(ctx context.Context, deviceID string) ([]DeviceProxyBindingGroupWithRoutes, error) {
	query := `SELECT id, device_id, iccid, profile_name, created_at, updated_at FROM device_proxy_binding_groups`
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
	groups := make([]DeviceProxyBindingGroup, 0)
	for rows.Next() {
		group, err := scanProxyBindingGroup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	values := make([]DeviceProxyBindingGroupWithRoutes, 0, len(groups))
	for _, group := range groups {
		routes, err := s.ListDeviceProxyBindingRoutes(ctx, group.ID)
		if err != nil {
			return nil, err
		}
		values = append(values, DeviceProxyBindingGroupWithRoutes{DeviceProxyBindingGroup: group, Routes: routes})
	}
	return values, nil
}
