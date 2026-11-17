package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const encryptedFieldPrefix = "enc:v1:"
const encryptedLegacyLookupPrefix = "enc:v1d:"
const encryptedLookupPrefix = "enc:v2d:"

type fieldCipher struct {
	key []byte
}

func (s *Store) sealField(field, plaintext string) (string, error) {
	return s.ensureSensitive().seal(field, plaintext)
}

func (s *Store) openField(field, stored string) (string, error) {
	return s.ensureSensitive().open(field, stored)
}

func (s *Store) SealJSON(field string, value json.RawMessage) (json.RawMessage, error) {
	sealed, err := s.sealField(field, string(value))
	if err != nil {
		return nil, err
	}
	return json.Marshal(sealed)
}

func (s *Store) OpenJSON(field string, value json.RawMessage) (json.RawMessage, error) {
	var sealed string
	if err := json.Unmarshal(value, &sealed); err == nil && strings.HasPrefix(sealed, encryptedFieldPrefix) {
		plain, err := s.openField(field, sealed)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(plain), nil
	}
	return value, nil
}

func sealedJSONValue(value json.RawMessage) bool {
	var encoded string
	if err := json.Unmarshal(value, &encoded); err != nil {
		return false
	}
	return strings.HasPrefix(encoded, encryptedFieldPrefix) ||
		strings.HasPrefix(encoded, encryptedLookupPrefix) ||
		strings.HasPrefix(encoded, encryptedLegacyLookupPrefix)
}

func (s *Store) ensureSensitive() *fieldCipher {
	s.sensitiveMu.Lock()
	defer s.sensitiveMu.Unlock()
	if s.sensitive == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic(fmt.Sprintf("store: generate ephemeral data key: %v", err))
		}
		s.sensitive = &fieldCipher{key: key}
	}
	return s.sensitive
}

func (s *Store) encryptLegacySensitiveFields(ctx context.Context) error {
	targets := []struct {
		table  string
		fields []string
	}{
		{"sim_cards", []string{"imsi", "msisdn", "eid", "profile_aid", "gid1", "gid2", "smsc", "derived_json"}},
		{"sim_card_candidates", []string{"imsi"}},
		{"sim_card_observations", []string{"eid", "profile_aid"}},
		{"sim_card_bindings", []string{"eid", "profile_aid"}},
		{"device_runtime", []string{"imsi", "phone_number"}},
		{"vowifi_runtime", []string{"imsi", "local_phone", "tunnel_json", "imscore_json", "smsip_json", "extra_json"}},
		{"card_apn_profiles", []string{"username", "password", "proxy"}},
		{"call_history", []string{"number"}},
		{"sms_messages", []string{"imsi", "local_phone", "peer", "body", "extra_json"}},
		{"sms_raw_records", []string{"imsi", "raw_pdu", "raw_user_data", "decode_error"}},
		{"devices", []string{"extra_json"}},
	}
	for _, target := range targets {
		if err := s.encryptLegacyTableFields(ctx, target.table, target.fields...); err != nil {
			return err
		}
	}
	if err := s.encryptLegacySensitiveAppSettings(ctx); err != nil {
		return err
	}
	return s.clearLegacyDevicePIN(ctx)
}

func (s *Store) encryptLegacySensitiveAppSettings(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value_json FROM app_settings WHERE sensitive = 1`)
	if err != nil {
		return err
	}
	type setting struct {
		key   string
		value string
	}
	var updates []setting
	for rows.Next() {
		var item setting
		if err := rows.Scan(&item.key, &item.value); err != nil {
			rows.Close()
			return err
		}
		var encoded string
		if json.Unmarshal([]byte(item.value), &encoded) == nil &&
			(strings.HasPrefix(encoded, encryptedFieldPrefix) || strings.HasPrefix(encoded, encryptedLookupPrefix)) {
			continue
		}
		updates = append(updates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range updates {
		raw := json.RawMessage(item.value)
		sealed, err := s.SealJSON("app_setting."+item.key, raw)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE app_settings SET value_json = ? WHERE key = ?`, string(sealed), item.key,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) clearLegacyDevicePIN(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(devices)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "sim_pin" {
			found = true
			break
		}
	}
	rows.Close()
	if !found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "UPDATE devices SET sim_pin = ''")
	return err
}

func (s *Store) encryptLegacyTableFields(ctx context.Context, table string, fields ...string) error {
	existing, err := s.tableColumns(ctx, table)
	if err != nil || len(existing) == 0 {
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return err
		}
		return nil
	}
	available := make([]string, 0, len(fields))
	for _, field := range fields {
		if existing[field] {
			available = append(available, field)
		}
	}
	fields = available
	if len(fields) == 0 {
		return nil
	}
	columns := strings.Join(fields, ", ")
	rows, err := s.db.QueryContext(ctx, "SELECT rowid, "+columns+" FROM "+table)
	if err != nil {
		return fmt.Errorf("read legacy sensitive fields from %s: %w", table, err)
	}
	type pending struct {
		rowID  int64
		values []string
	}
	var updates []pending
	for rows.Next() {
		raw := make([]any, len(fields)+1)
		rowID := new(int64)
		raw[0] = rowID
		values := make([]sql.RawBytes, len(fields))
		for index := range values {
			raw[index+1] = &values[index]
		}
		if err := rows.Scan(raw...); err != nil {
			rows.Close()
			return err
		}
		encrypted := make([]string, len(fields))
		for index, value := range values {
			stored := string(value)
			if strings.HasPrefix(stored, encryptedFieldPrefix) || strings.HasPrefix(stored, encryptedLookupPrefix) {
				encrypted[index] = string(value)
				continue
			}
			plaintext := stored
			if strings.HasPrefix(stored, encryptedLegacyLookupPrefix) {
				plaintext, err = s.openField(table+"."+fields[index], stored)
				if err != nil {
					rows.Close()
					return fmt.Errorf("decrypt legacy lookup field %s.%s: %w", table, fields[index], err)
				}
			}
			result, err := s.sealStoredField(table, fields[index], plaintext)
			if err != nil {
				rows.Close()
				return err
			}
			encrypted[index] = result
		}
		updates = append(updates, pending{rowID: *rowID, values: encrypted})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, update := range updates {
		assignments := make([]string, 0, len(fields))
		args := make([]any, 0, len(fields)+1)
		for index, field := range fields {
			assignments = append(assignments, field+" = ?")
			args = append(args, update.values[index])
		}
		args = append(args, update.rowID)
		if _, err := s.db.ExecContext(ctx, "UPDATE "+table+" SET "+strings.Join(assignments, ", ")+" WHERE rowid = ?", args...); err != nil {
			return fmt.Errorf("encrypt legacy sensitive fields in %s: %w", table, err)
		}
	}
	return nil
}

func (s *Store) sealStoredField(table, field, plaintext string) (string, error) {
	if ((field == "eid" || field == "profile_aid") && table != "sim_cards") ||
		((field == "imsi" || field == "peer") && table == "sms_messages") {
		return s.ensureSensitive().sealLookup(table+"."+field, plaintext)
	}
	return s.sealField(table+"."+field, plaintext)
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func newFieldCipher(databasePath string) (*fieldCipher, error) {
	if !isFilesystemPath(databasePath) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		return &fieldCipher{key: key}, nil
	}
	path := filepath.Join(filepath.Dir(databasePath), "secrets", "data.key")
	if key, err := os.ReadFile(path); err == nil {
		if len(key) != 32 {
			return nil, errors.New("store data key must contain exactly 32 bytes")
		}
		return &fieldCipher{key: append([]byte(nil), key...)}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read store data key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create store key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".data-key-*")
	if err != nil {
		return nil, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return nil, err
	}
	if _, err := temp.Write(key); err != nil {
		_ = temp.Close()
		return nil, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tempName, path); err != nil {
		return nil, err
	}
	return &fieldCipher{key: key}, nil
}

func (cipherValue *fieldCipher) seal(field, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(cipherValue.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	aad := []byte("vonet-sensitive-field-v1:" + field)
	return encryptedFieldPrefix + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), aad)), nil
}

func (cipherValue *fieldCipher) sealLookup(field, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(cipherValue.key)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, cipherValue.key)
	mac.Write([]byte("vonet-sensitive-lookup-v1:" + field))
	mac.Write([]byte(plaintext))
	tag := mac.Sum(nil)[:16]
	stream := cipher.NewCTR(block, tag)
	ciphertext := make([]byte, len(plaintext))
	stream.XORKeyStream(ciphertext, []byte(plaintext))
	payload := append(append([]byte(nil), tag...), ciphertext...)
	return encryptedLookupPrefix + base64.RawStdEncoding.EncodeToString(payload), nil
}

func (cipherValue *fieldCipher) open(field, stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, encryptedFieldPrefix) &&
		!strings.HasPrefix(stored, encryptedLookupPrefix) &&
		!strings.HasPrefix(stored, encryptedLegacyLookupPrefix) {
		return "", errors.New("sensitive field is not encrypted")
	}
	lookup := strings.HasPrefix(stored, encryptedLookupPrefix)
	legacyLookup := strings.HasPrefix(stored, encryptedLegacyLookupPrefix)
	encoded := strings.TrimPrefix(stored, encryptedFieldPrefix)
	encoded = strings.TrimPrefix(encoded, encryptedLookupPrefix)
	encoded = strings.TrimPrefix(encoded, encryptedLegacyLookupPrefix)
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(cipherValue.key)
	if err != nil {
		return "", err
	}
	if lookup {
		if len(raw) < 17 {
			return "", errors.New("encrypted lookup field is truncated")
		}
		tag, ciphertext := raw[:16], raw[16:]
		plaintext := make([]byte, len(ciphertext))
		stream := cipher.NewCTR(block, tag)
		stream.XORKeyStream(plaintext, ciphertext)
		mac := hmac.New(sha256.New, cipherValue.key)
		mac.Write([]byte("vonet-sensitive-lookup-v1:" + field))
		mac.Write(plaintext)
		if subtle.ConstantTimeCompare(mac.Sum(nil)[:16], tag) != 1 {
			return "", errors.New("encrypted lookup authentication failed")
		}
		return string(plaintext), nil
	}
	if legacyLookup {
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return "", err
		}
		if len(raw) < gcm.NonceSize() {
			return "", errors.New("encrypted legacy lookup field is truncated")
		}
		nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
		plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte("vonet-sensitive-field-v1:"+field))
		if err != nil {
			return "", err
		}
		return string(plaintext), nil
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("encrypted field is truncated")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte("vonet-sensitive-field-v1:"+field))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
