package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const commandLedgerKeyPrefix = "agent.command_result:"

type commandLedgerRecord struct {
	ExpiresAt int64           `json:"expires_at"`
	Event     json.RawMessage `json:"event"`
}

func (s *Store) SaveCommandResult(
	ctx context.Context,
	idempotencyKey string,
	event json.RawMessage,
	expiresAt time.Time,
) error {
	key, err := commandLedgerKey(idempotencyKey)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(commandLedgerRecord{ExpiresAt: expiresAt.UTC().Unix(), Event: event})
	if err != nil {
		return err
	}
	sealed, err := s.SealJSON("app_setting."+key, raw)
	if err != nil {
		return err
	}
	return s.UpsertAppSetting(ctx, AppSetting{Key: key, Value: sealed, Sensitive: true})
}

func (s *Store) LoadCommandResult(
	ctx context.Context,
	idempotencyKey string,
) (json.RawMessage, time.Time, bool, error) {
	key, err := commandLedgerKey(idempotencyKey)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	setting, err := s.AppSetting(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	raw, err := s.OpenJSON(key, setting.Value)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	var record commandLedgerRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, time.Time{}, false, err
	}
	expiresAt := time.Unix(record.ExpiresAt, 0).UTC()
	if !expiresAt.After(time.Now().UTC()) {
		_ = s.DeleteAppSetting(ctx, key)
		return nil, time.Time{}, false, nil
	}
	return record.Event, expiresAt, true, nil
}

func commandLedgerKey(idempotencyKey string) (string, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 256 {
		return "", errors.New("idempotency key must contain between 16 and 256 characters")
	}
	for _, character := range idempotencyKey {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return "", fmt.Errorf("idempotency key contains invalid character %q", character)
	}
	return commandLedgerKeyPrefix + idempotencyKey, nil
}
