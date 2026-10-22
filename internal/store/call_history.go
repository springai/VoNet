package store

import "time"

type CallRecord struct {
	ID              int64  `json:"id"`
	DeviceID        string `json:"device_id"`
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
	if record.CreatedAt == 0 {
		record.CreatedAt = time.Now().Unix()
	}
	if record.StartedAt == 0 {
		record.StartedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(`
		INSERT INTO call_history (device_id, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.DeviceID, record.Number, record.Direction, record.Status, record.Transport, record.DurationSeconds, record.StartedAt, record.EndedAt, record.CreatedAt)
	return err
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
		SELECT id, device_id, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at
		FROM call_history WHERE device_id = ? ORDER BY started_at DESC LIMIT ? OFFSET ?
	`, deviceID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var records []CallRecord
	for rows.Next() {
		var r CallRecord
		if err := rows.Scan(&r.ID, &r.DeviceID, &r.Number, &r.Direction, &r.Status, &r.Transport, &r.DurationSeconds, &r.StartedAt, &r.EndedAt, &r.CreatedAt); err != nil {
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
		SELECT id, device_id, number, direction, status, transport, duration_seconds, started_at, ended_at, created_at
		FROM call_history WHERE device_id = ? AND number LIKE ? ORDER BY started_at DESC LIMIT ?
	`, deviceID, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CallRecord
	for rows.Next() {
		var r CallRecord
		if err := rows.Scan(&r.ID, &r.DeviceID, &r.Number, &r.Direction, &r.Status, &r.Transport, &r.DurationSeconds, &r.StartedAt, &r.EndedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}
func (s *Store) DeleteCallRecord(deviceID string, id int64) error {
	_, err := s.db.Exec("DELETE FROM call_history WHERE device_id = ? AND id = ?", deviceID, id)
	return err
}

func (s *Store) DeleteAllCallHistory(deviceID string) error {
	_, err := s.db.Exec("DELETE FROM call_history WHERE device_id = ?", deviceID)
	return err
}
