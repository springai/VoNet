package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"vonet/internal/pcscimei"
)

var (
	ErrImmutableReaderDeviceType = errors.New("immutable_reader_device_type")
	ErrPCSCIMEIExists            = errors.New("pcsc_imei_exists")
	ErrPCSCIMEIUnavailable       = errors.New("pcsc_imei_unavailable")
	ErrPCSCIMEIInUse             = errors.New("pcsc_imei_in_use")
	ErrPCSCIMEIPoolExhausted     = errors.New("pcsc_imei_pool_exhausted")
)

func (s *Store) ListPCSCIMEIPool(ctx context.Context) ([]PCSCIMEIPoolEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.imei, p.created_at, p.updated_at,
			COALESCE(l.device_id, ''), COALESCE(d.name, ''), COALESCE(l.mode, '')
		FROM pcsc_imei_pool p
		LEFT JOIN pcsc_imei_leases l ON l.imei = p.imei
		LEFT JOIN devices d ON d.id = l.device_id
		ORDER BY p.imei
	`)
	if err != nil {
		return nil, fmt.Errorf("list PCSC IMEI pool: %w", err)
	}
	defer rows.Close()
	values := make([]PCSCIMEIPoolEntry, 0)
	for rows.Next() {
		var value PCSCIMEIPoolEntry
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&value.IMEI, &createdAt, &updatedAt,
			&value.DeviceID, &value.DeviceName, &value.LeaseMode,
		); err != nil {
			return nil, fmt.Errorf("scan PCSC IMEI pool: %w", err)
		}
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PCSC IMEI pool: %w", err)
	}
	return values, nil
}

func (s *Store) AddPCSCIMEIPool(ctx context.Context, value string) (PCSCIMEIPoolEntry, error) {
	value = pcscimei.Normalize(value)
	if err := pcscimei.Validate(value); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("begin PCSC IMEI pool add: %w", err)
	}
	defer tx.Rollback()
	if err := ensurePCSCIMEINotUsedByModem(ctx, tx, value, ""); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO pcsc_imei_pool (imei, created_at, updated_at)
		VALUES (?, ?, ?)
	`, value, now.Unix(), now.Unix())
	if err != nil {
		if isUniqueConstraint(err) {
			return PCSCIMEIPoolEntry{}, ErrPCSCIMEIExists
		}
		return PCSCIMEIPoolEntry{}, fmt.Errorf("add PCSC IMEI pool value: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("commit PCSC IMEI pool add: %w", err)
	}
	return PCSCIMEIPoolEntry{IMEI: value, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdatePCSCIMEIPool(ctx context.Context, current, next string) (PCSCIMEIPoolEntry, error) {
	current = pcscimei.Normalize(current)
	next = pcscimei.Normalize(next)
	if err := pcscimei.Validate(current); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	if err := pcscimei.Validate(next); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	if current == next {
		entry, err := s.findPCSCIMEIPoolEntry(ctx, current)
		if err != nil {
			return PCSCIMEIPoolEntry{}, err
		}
		var boundDevice string
		err = s.db.QueryRowContext(ctx, `SELECT device_id FROM pcsc_imei_leases WHERE imei = ?`, current).Scan(&boundDevice)
		if err == nil {
			return PCSCIMEIPoolEntry{}, fmt.Errorf("%w: %s", ErrPCSCIMEIInUse, boundDevice)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return PCSCIMEIPoolEntry{}, fmt.Errorf("read PCSC IMEI lease before pool update: %w", err)
		}
		return entry, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("begin PCSC IMEI pool update: %w", err)
	}
	defer tx.Rollback()
	var boundDevice string
	err = tx.QueryRowContext(ctx, `
		SELECT device_id FROM pcsc_imei_leases WHERE imei = ?
	`, current).Scan(&boundDevice)
	if err == nil {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("%w: %s", ErrPCSCIMEIInUse, boundDevice)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("read PCSC IMEI lease before pool update: %w", err)
	}
	if err := ensurePCSCIMEINotUsedByModem(ctx, tx, next, ""); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE pcsc_imei_pool SET imei = ?, updated_at = ? WHERE imei = ?
	`, next, now.Unix(), current)
	if err != nil {
		if isUniqueConstraint(err) {
			return PCSCIMEIPoolEntry{}, ErrPCSCIMEIExists
		}
		return PCSCIMEIPoolEntry{}, fmt.Errorf("update PCSC IMEI pool value: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return PCSCIMEIPoolEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("commit PCSC IMEI pool update: %w", err)
	}
	return PCSCIMEIPoolEntry{IMEI: next, UpdatedAt: now}, nil
}

func (s *Store) DeletePCSCIMEIPool(ctx context.Context, value string) error {
	value = pcscimei.Normalize(value)
	if err := pcscimei.Validate(value); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PCSC IMEI pool delete: %w", err)
	}
	defer tx.Rollback()
	var boundDevice string
	err = tx.QueryRowContext(ctx, `
		SELECT device_id FROM pcsc_imei_leases WHERE imei = ?
	`, value).Scan(&boundDevice)
	if err == nil {
		return fmt.Errorf("%w: %s", ErrPCSCIMEIInUse, boundDevice)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read PCSC IMEI lease before pool delete: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM pcsc_imei_pool WHERE imei = ?`, value)
	if err != nil {
		return fmt.Errorf("delete PCSC IMEI pool value: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PCSC IMEI pool delete: %w", err)
	}
	return nil
}

func (s *Store) PCSCIMEILease(ctx context.Context, deviceID string) (PCSCIMEILease, error) {
	return scanPCSCIMEILease(s.db.QueryRowContext(ctx, `
		SELECT device_id, imei, mode, updated_at
		FROM pcsc_imei_leases WHERE device_id = ?
	`, strings.TrimSpace(deviceID)))
}

func ensurePCSCIMEILease(
	ctx context.Context,
	executor contextQueryExecer,
	deviceID string,
) (PCSCIMEILease, error) {
	deviceID = strings.TrimSpace(deviceID)
	lease, err := scanPCSCIMEILease(executor.QueryRowContext(ctx, `
		SELECT device_id, imei, mode, updated_at
		FROM pcsc_imei_leases WHERE device_id = ?
	`, deviceID))
	if err == nil {
		return lease, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return PCSCIMEILease{}, fmt.Errorf("read existing PCSC IMEI lease: %w", err)
	}
	return upsertPCSCIMEILease(ctx, executor, deviceID, PCSCIMEILeaseRequest{Mode: "random"})
}

// EnsurePCSCIMEILease repairs a reader configuration whose lease is missing.
// Existing leases are preserved so this never rotates a random IMEI.
func (s *Store) EnsurePCSCIMEILease(ctx context.Context, deviceID string) (PCSCIMEILease, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PCSCIMEILease{}, fmt.Errorf("begin PCSC IMEI lease repair: %w", err)
	}
	defer tx.Rollback()
	var deviceType string
	if err := tx.QueryRowContext(ctx, `SELECT device_type FROM devices WHERE id = ?`, strings.TrimSpace(deviceID)).Scan(&deviceType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PCSCIMEILease{}, ErrNotFound
		}
		return PCSCIMEILease{}, fmt.Errorf("read device before PCSC IMEI lease repair: %w", err)
	}
	if NormalizeDeviceType(deviceType) != DeviceTypeUSBSIMReader {
		return PCSCIMEILease{}, errors.New("PCSC IMEI lease requires a USB SIM reader")
	}
	lease, err := ensurePCSCIMEILease(ctx, tx, deviceID)
	if err != nil {
		return PCSCIMEILease{}, err
	}
	if err := tx.Commit(); err != nil {
		return PCSCIMEILease{}, fmt.Errorf("commit PCSC IMEI lease repair: %w", err)
	}
	return lease, nil
}

func (s *Store) CreateDeviceWithPCSCIMEILease(
	ctx context.Context,
	value Device,
	request PCSCIMEILeaseRequest,
) (Device, PCSCIMEILease, error) {
	if NormalizeDeviceType(value.DeviceType) != DeviceTypeUSBSIMReader {
		return Device{}, PCSCIMEILease{}, errors.New("PCSC IMEI lease requires a USB SIM reader")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, PCSCIMEILease{}, fmt.Errorf("begin PCSC device create: %w", err)
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM devices WHERE id = ?`, strings.TrimSpace(value.ID)).Scan(&existing)
	if err == nil {
		return Device{}, PCSCIMEILease{}, errors.New("device exists")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Device{}, PCSCIMEILease{}, fmt.Errorf("check PCSC device before create: %w", err)
	}
	if err := upsertDevice(ctx, tx, value); err != nil {
		return Device{}, PCSCIMEILease{}, err
	}
	lease, err := upsertPCSCIMEILease(ctx, tx, value.ID, request)
	if err != nil {
		return Device{}, PCSCIMEILease{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, PCSCIMEILease{}, fmt.Errorf("commit PCSC device create: %w", err)
	}
	return value, lease, nil
}

func (s *Store) UpdateDeviceWithPCSCIMEILease(
	ctx context.Context,
	value Device,
	request PCSCIMEILeaseRequest,
) (Device, PCSCIMEILease, error) {
	if NormalizeDeviceType(value.DeviceType) != DeviceTypeUSBSIMReader {
		return Device{}, PCSCIMEILease{}, errors.New("PCSC IMEI lease requires a USB SIM reader")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, PCSCIMEILease{}, fmt.Errorf("begin PCSC device update: %w", err)
	}
	defer tx.Rollback()
	if err := upsertDevice(ctx, tx, value); err != nil {
		return Device{}, PCSCIMEILease{}, err
	}
	lease, err := upsertPCSCIMEILease(ctx, tx, value.ID, request)
	if err != nil {
		return Device{}, PCSCIMEILease{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, PCSCIMEILease{}, fmt.Errorf("commit PCSC device update: %w", err)
	}
	return value, lease, nil
}

// RotatePCSCIMEILease advances a random-mode lease when another pool value is
// available. A fixed lease and an exhausted random pool keep their current IMEI.
func (s *Store) RotatePCSCIMEILease(ctx context.Context, deviceID string) (PCSCIMEILease, bool, error) {
	deviceID = strings.TrimSpace(deviceID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PCSCIMEILease{}, false, fmt.Errorf("begin PCSC IMEI rotation: %w", err)
	}
	defer tx.Rollback()
	current, err := scanPCSCIMEILease(tx.QueryRowContext(ctx, `
		SELECT device_id, imei, mode, updated_at
		FROM pcsc_imei_leases WHERE device_id = ?
	`, deviceID))
	if err != nil {
		return PCSCIMEILease{}, false, err
	}
	if current.Mode != "random" {
		return current, false, nil
	}
	var next string
	err = tx.QueryRowContext(ctx, `
		SELECT p.imei
		FROM pcsc_imei_pool p
		WHERE p.imei <> ?
			AND NOT EXISTS (SELECT 1 FROM pcsc_imei_leases l WHERE l.imei = p.imei)
			AND NOT EXISTS (SELECT 1 FROM devices d WHERE TRIM(d.modem_imei) = p.imei AND d.device_type <> 'usb_sim_reader')
		ORDER BY RANDOM()
		LIMIT 1
	`, current.IMEI).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return current, false, nil
	}
	if err != nil {
		return PCSCIMEILease{}, false, fmt.Errorf("select random PCSC IMEI: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE pcsc_imei_leases SET imei = ?, updated_at = ? WHERE device_id = ?
	`, next, now.Unix(), deviceID); err != nil {
		return PCSCIMEILease{}, false, fmt.Errorf("rotate PCSC IMEI lease: %w", err)
	}
	current.IMEI = next
	current.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return PCSCIMEILease{}, false, fmt.Errorf("commit PCSC IMEI rotation: %w", err)
	}
	return current, true, nil
}

func upsertPCSCIMEILease(
	ctx context.Context,
	executor contextQueryExecer,
	deviceID string,
	request PCSCIMEILeaseRequest,
) (PCSCIMEILease, error) {
	deviceID = strings.TrimSpace(deviceID)
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode != "random" && mode != "fixed" {
		return PCSCIMEILease{}, fmt.Errorf("unsupported PCSC IMEI lease mode %q", request.Mode)
	}
	value := pcscimei.Normalize(request.IMEI)
	var err error
	if mode == "fixed" {
		if err = pcscimei.Validate(value); err != nil {
			return PCSCIMEILease{}, err
		}
		var exists int
		err = executor.QueryRowContext(ctx, `
			SELECT 1 FROM pcsc_imei_pool p
			WHERE p.imei = ?
				AND NOT EXISTS (
					SELECT 1 FROM pcsc_imei_leases l
					WHERE l.imei = p.imei AND l.device_id <> ?
				)
				AND NOT EXISTS (
					SELECT 1 FROM devices d
					WHERE TRIM(d.modem_imei) = p.imei AND d.device_type <> 'usb_sim_reader'
				)
		`, value, deviceID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return PCSCIMEILease{}, ErrPCSCIMEIUnavailable
		}
	} else {
		err = executor.QueryRowContext(ctx, `
			SELECT imei FROM pcsc_imei_leases WHERE device_id = ?
		`, deviceID).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			err = executor.QueryRowContext(ctx, `
				SELECT p.imei
				FROM pcsc_imei_pool p
				WHERE NOT EXISTS (
					SELECT 1 FROM pcsc_imei_leases l WHERE l.imei = p.imei
				)
				AND NOT EXISTS (
					SELECT 1 FROM devices d
					WHERE TRIM(d.modem_imei) = p.imei AND d.device_type <> 'usb_sim_reader'
				)
				ORDER BY RANDOM()
				LIMIT 1
			`).Scan(&value)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return PCSCIMEILease{}, ErrPCSCIMEIPoolExhausted
		}
	}
	if err != nil {
		return PCSCIMEILease{}, fmt.Errorf("select PCSC IMEI lease value: %w", err)
	}
	now := time.Now().UTC()
	result, err := executor.ExecContext(ctx, `
		INSERT INTO pcsc_imei_leases (device_id, imei, mode, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			imei = excluded.imei,
			mode = excluded.mode,
			updated_at = excluded.updated_at
	`, deviceID, value, mode, now.Unix())
	if err != nil {
		if isUniqueConstraint(err) {
			return PCSCIMEILease{}, ErrPCSCIMEIUnavailable
		}
		return PCSCIMEILease{}, fmt.Errorf("upsert PCSC IMEI lease: %w", err)
	}
	if err := requireAffected(result); err != nil {
		return PCSCIMEILease{}, err
	}
	return PCSCIMEILease{DeviceID: deviceID, IMEI: value, Mode: mode, UpdatedAt: now}, nil
}

func ensurePCSCIMEINotUsedByModem(
	ctx context.Context,
	executor contextQueryExecer,
	imei string,
	exceptDeviceID string,
) error {
	var deviceID string
	err := executor.QueryRowContext(ctx, `
		SELECT id FROM devices
		WHERE TRIM(modem_imei) = ? AND device_type <> 'usb_sim_reader' AND id <> ?
	`, imei, strings.TrimSpace(exceptDeviceID)).Scan(&deviceID)
	if err == nil {
		return fmt.Errorf("%w: modem %s", ErrPCSCIMEIUnavailable, deviceID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("check modem IMEI conflict: %w", err)
}

func (s *Store) findPCSCIMEIPoolEntry(ctx context.Context, imei string) (PCSCIMEIPoolEntry, error) {
	entry := PCSCIMEIPoolEntry{IMEI: imei}
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT created_at, updated_at FROM pcsc_imei_pool WHERE imei = ?
	`, imei).Scan(&createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PCSCIMEIPoolEntry{}, ErrNotFound
	}
	if err != nil {
		return PCSCIMEIPoolEntry{}, fmt.Errorf("read PCSC IMEI pool value: %w", err)
	}
	entry.CreatedAt = time.Unix(createdAt, 0).UTC()
	entry.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return entry, nil
}

func scanPCSCIMEILease(row rowScanner) (PCSCIMEILease, error) {
	var value PCSCIMEILease
	var updatedAt int64
	err := row.Scan(&value.DeviceID, &value.IMEI, &value.Mode, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PCSCIMEILease{}, ErrNotFound
	}
	if err != nil {
		return PCSCIMEILease{}, err
	}
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
