package store

import (
	"strings"
	"time"
)

type CallRecord struct {
	ID              int64  `json:"id"`
	DeviceID        string `json:"device_id"`
	ICCID           string `json:"iccid"`
	Number          string `json:"number"`
	Direction       string `json:"direction"`
	Status          string `json:"status"`
	Transport       string `json:"transport"`
	DurationSeconds int    `json:"duration_seconds"`
	StartedAt       int64  `json:"started_at"`
	EndedAt         int64  `json:"ended_at"`
	CreatedAt       int64  `json:"created_at"`
}

func (s *Store) InsertCallRecord(record CallRecord) error {
	record.DeviceID = strings.TrimSpace(record.DeviceID)
	record.ICCID = strings.TrimSpace(record.ICCID)
	if record.CreatedAt == 0 {
		record.CreatedAt = time.Now().Unix()
	}
	if record.StartedAt == 0 {
		record.StartedAt = time.Now().Unix()
	}
	storedNumber, err := s.sealField("call_history.number", record.Number)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO call_history (device_id, iccid, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.DeviceID, record.ICCID, storedNumber, record.Direction, record.Status, record.Transport, record.DurationSeconds, record.StartedAt, record.EndedAt, record.CreatedAt)
	return err
}

func (s *Store) scanCallRecord(row rowScanner) (CallRecord, error) {
	var record CallRecord
	var storedNumber string
	if err := row.Scan(
		&record.ID, &record.DeviceID, &record.ICCID, &storedNumber,
		&record.Direction, &record.Status, &record.Transport,
		&record.DurationSeconds, &record.StartedAt, &record.EndedAt, &record.CreatedAt,
	); err != nil {
		return CallRecord{}, err
	}
	number, err := s.openField("call_history.number", storedNumber)
	if err != nil {
		return CallRecord{}, err
	}
	record.Number = number
	return record, nil
}

func (s *Store) ListCallHistory(deviceID string, limit int, offset int) ([]CallRecord, int, error) {
	if limit <= 0 {
		limit = 50
	}
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM call_history WHERE device_id = ?", deviceID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
		SELECT id, device_id, iccid, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at
		FROM call_history WHERE device_id = ? ORDER BY started_at DESC LIMIT ? OFFSET ?
	`, deviceID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var records []CallRecord
	for rows.Next() {
		var r CallRecord
		r, err := s.scanCallRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, r)
	}
	return records, total, rows.Err()
}

func (s *Store) SearchCallHistory(deviceID, query string, limit int) ([]CallRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, device_id, iccid, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at
		FROM call_history WHERE device_id = ? ORDER BY started_at DESC
	`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CallRecord
	for rows.Next() {
		r, err := s.scanCallRecord(rows)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToLower(r.Number), strings.ToLower(strings.TrimSpace(query))) {
			records = append(records, r)
			if len(records) >= limit {
				break
			}
		}
	}
	return records, rows.Err()
}

func (s *Store) ListCallHistoryForCard(deviceID, iccid string, limit int, offset int) ([]CallRecord, int, error) {
	deviceID = strings.TrimSpace(deviceID)
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return nil, 0, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	var total int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM call_history WHERE device_id = ? AND iccid = ?",
		deviceID, iccid,
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
		SELECT id, device_id, iccid, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at
		FROM call_history WHERE device_id = ? AND iccid = ?
		ORDER BY started_at DESC LIMIT ? OFFSET ?
	`, deviceID, iccid, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var records []CallRecord
	for rows.Next() {
		var record CallRecord
		record, err := s.scanCallRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, record)
	}
	return records, total, rows.Err()
}

func (s *Store) SearchCallHistoryForCard(deviceID, iccid, query string, limit int) ([]CallRecord, error) {
	deviceID = strings.TrimSpace(deviceID)
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, device_id, iccid, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at
		FROM call_history WHERE device_id = ? AND iccid = ?
		ORDER BY started_at DESC
	`, deviceID, iccid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CallRecord
	for rows.Next() {
		record, err := s.scanCallRecord(rows)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToLower(record.Number), strings.ToLower(strings.TrimSpace(query))) {
			records = append(records, record)
			if len(records) >= limit {
				break
			}
		}
	}
	return records, rows.Err()
}

func (s *Store) DeleteCallRecord(deviceID string, id int64) error {
	_, err := s.db.Exec("DELETE FROM call_history WHERE device_id = ? AND id = ?", strings.TrimSpace(deviceID), id)
	return err
}

func (s *Store) DeleteCallRecordForCard(deviceID, iccid string, id int64) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return ErrNotFound
	}
	_, err := s.db.Exec(
		"DELETE FROM call_history WHERE device_id = ? AND iccid = ? AND id = ?",
		strings.TrimSpace(deviceID), iccid, id,
	)
	return err
}

func (s *Store) DeleteAllCallHistory(deviceID string) error {
	_, err := s.db.Exec("DELETE FROM call_history WHERE device_id = ?", strings.TrimSpace(deviceID))
	return err
}

func (s *Store) DeleteAllCallHistoryForCard(deviceID, iccid string) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return ErrNotFound
	}
	_, err := s.db.Exec(
		"DELETE FROM call_history WHERE device_id = ? AND iccid = ?",
		strings.TrimSpace(deviceID), iccid,
	)
	return err
}
