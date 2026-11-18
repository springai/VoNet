package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	CardKeepAliveCycleCustomDays = "custom_days"
	CardKeepAliveCycleMonthly    = "monthly"
	CardKeepAliveCycleQuarterly  = "quarterly"
	CardKeepAliveCycleHalfYearly = "half_yearly"
	CardKeepAliveCycleYearly     = "yearly"

	CardKeepAliveChargeSuccess      = "success"
	CardKeepAliveChargeInsufficient = "insufficient"
	CardKeepAliveChargeFailed       = "failed"
)

func NormalizeCardKeepAliveCurrency(value string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(value))
	switch currency {
	case "CNY", "USD", "GBP", "EUR", "NZD":
		return currency, nil
	default:
		return "", fmt.Errorf("unsupported currency %q", value)
	}
}

func NormalizeCardKeepAliveCycleRule(value string) (string, error) {
	rule := strings.ToLower(strings.TrimSpace(value))
	switch rule {
	case CardKeepAliveCycleCustomDays, CardKeepAliveCycleMonthly, CardKeepAliveCycleQuarterly, CardKeepAliveCycleHalfYearly, CardKeepAliveCycleYearly:
		return rule, nil
	default:
		return "", fmt.Errorf("unsupported keep-alive cycle rule %q", value)
	}
}

func NextCardKeepAliveExpiry(start time.Time, rule string, cycleDays int) (time.Time, error) {
	if start.IsZero() {
		return time.Time{}, errors.New("keep-alive plan start time is required")
	}
	normalized, err := NormalizeCardKeepAliveCycleRule(rule)
	if err != nil {
		return time.Time{}, err
	}
	switch normalized {
	case CardKeepAliveCycleCustomDays:
		if cycleDays <= 0 || cycleDays > 3650 {
			return time.Time{}, errors.New("custom keep-alive cycle must be between 1 and 3650 days")
		}
		return start.AddDate(0, 0, cycleDays), nil
	case CardKeepAliveCycleMonthly:
		return addCardKeepAliveMonths(start, 1), nil
	case CardKeepAliveCycleQuarterly:
		return addCardKeepAliveMonths(start, 3), nil
	case CardKeepAliveCycleHalfYearly:
		return addCardKeepAliveMonths(start, 6), nil
	case CardKeepAliveCycleYearly:
		return addCardKeepAliveMonths(start, 12), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported keep-alive cycle rule %q", rule)
	}
}

func addCardKeepAliveMonths(start time.Time, months int) time.Time {
	year, month, day := start.Date()
	targetYear := year + (int(month)-1+months)/12
	targetMonth := time.Month((int(month)-1+months)%12 + 1)
	lastDay := time.Date(targetYear, targetMonth+1, 0, 0, 0, 0, 0, start.Location()).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(targetYear, targetMonth, day, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
}

func startOfCardKeepAliveDay(value time.Time) time.Time {
	local := value.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

func NormalizeCardKeepAlive(value CardKeepAlive) (CardKeepAlive, error) {
	iccid, err := NormalizeSimICCID(value.ICCID)
	if err != nil {
		return CardKeepAlive{}, err
	}
	value.ICCID = iccid
	value.PlanInfo = strings.TrimSpace(value.PlanInfo)
	if value.BalanceCents < 0 {
		return CardKeepAlive{}, errors.New("keep-alive balance cannot be negative")
	}
	if value.DeductionCents < 0 {
		return CardKeepAlive{}, errors.New("keep-alive deduction cannot be negative")
	}
	if value.NotifyDaysBefore < 0 || value.NotifyDaysBefore > 365 {
		return CardKeepAlive{}, errors.New("keep-alive notification days must be between 0 and 365")
	}
	currency, err := NormalizeCardKeepAliveCurrency(value.Currency)
	if err != nil {
		return CardKeepAlive{}, err
	}
	value.Currency = currency
	rule, err := NormalizeCardKeepAliveCycleRule(value.CycleRule)
	if err != nil {
		return CardKeepAlive{}, err
	}
	value.CycleRule = rule
	if value.CycleRule == CardKeepAliveCycleCustomDays {
		if value.CycleDays <= 0 || value.CycleDays > 3650 {
			return CardKeepAlive{}, errors.New("custom keep-alive cycle must be between 1 and 3650 days")
		}
	} else {
		value.CycleDays = 0
	}
	if value.Enabled {
		if value.PlanStartedAt.IsZero() {
			return CardKeepAlive{}, errors.New("keep-alive plan start time is required")
		}
		if value.ExpiresAt.IsZero() {
			baselineAt := value.CurrentCycleStartAt
			if baselineAt.IsZero() {
				baselineAt = value.PlanStartedAt
			}
			baselineAt = baselineAt.In(time.Local)
			expiresAt, err := NextCardKeepAliveExpiry(baselineAt, value.CycleRule, value.CycleDays)
			if err != nil {
				return CardKeepAlive{}, err
			}
			value.CurrentCycleStartAt = baselineAt
			value.ExpiresAt = expiresAt
		} else {
			value.ExpiresAt = value.ExpiresAt.In(time.Local)
			if value.CurrentCycleStartAt.IsZero() {
				value.CurrentCycleStartAt = value.PlanStartedAt.In(time.Local)
			} else {
				value.CurrentCycleStartAt = value.CurrentCycleStartAt.In(time.Local)
			}
		}
	}
	return value, nil
}

func (s *Store) UpsertCardKeepAlive(ctx context.Context, value CardKeepAlive) (CardKeepAlive, error) {
	value, err := NormalizeCardKeepAlive(value)
	if err != nil {
		return CardKeepAlive{}, err
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO card_keepalive (
			iccid, enabled, plan_info, currency, balance_cents, plan_start_at,
			cycle_rule, cycle_days, deduction_cents, auto_renew,
			notification_enabled, notify_days_before, current_cycle_start_at,
			expires_at, last_renewed_at, last_charge_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid) DO UPDATE SET
			enabled = excluded.enabled,
			plan_info = excluded.plan_info,
			currency = excluded.currency,
			balance_cents = excluded.balance_cents,
			plan_start_at = excluded.plan_start_at,
			cycle_rule = excluded.cycle_rule,
			cycle_days = excluded.cycle_days,
			deduction_cents = excluded.deduction_cents,
			auto_renew = excluded.auto_renew,
			notification_enabled = excluded.notification_enabled,
			notify_days_before = excluded.notify_days_before,
			current_cycle_start_at = excluded.current_cycle_start_at,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at
	`, value.ICCID, boolInt(value.Enabled), value.PlanInfo, value.Currency, value.BalanceCents,
		unixOrZero(value.PlanStartedAt), value.CycleRule, value.CycleDays, value.DeductionCents,
		boolInt(value.AutoRenew), boolInt(value.NotificationEnabled), value.NotifyDaysBefore,
		unixOrZero(value.CurrentCycleStartAt), unixOrZero(value.ExpiresAt),
		unixOrZero(value.LastRenewedAt), unixOrZero(value.LastChargeAt),
		value.CreatedAt.Unix(), value.UpdatedAt.Unix(),
	)
	if err != nil {
		return CardKeepAlive{}, fmt.Errorf("upsert card keep-alive %q: %w", value.ICCID, err)
	}
	return s.CardKeepAlive(ctx, value.ICCID)
}

const cardKeepAliveSelect = `
	SELECT iccid, enabled, plan_info, currency, balance_cents, plan_start_at,
		cycle_rule, cycle_days, deduction_cents, auto_renew, notification_enabled,
		notify_days_before, current_cycle_start_at, expires_at, last_renewed_at,
		last_charge_at, created_at, updated_at
	FROM card_keepalive`

func scanCardKeepAlive(row rowScanner) (CardKeepAlive, error) {
	var value CardKeepAlive
	var enabled, autoRenew, notificationEnabled int
	var planStart, currentCycleStart, expiresAt, lastRenewedAt, lastChargeAt, createdAt, updatedAt int64
	err := row.Scan(
		&value.ICCID, &enabled, &value.PlanInfo, &value.Currency, &value.BalanceCents,
		&planStart, &value.CycleRule, &value.CycleDays, &value.DeductionCents, &autoRenew,
		&notificationEnabled, &value.NotifyDaysBefore, &currentCycleStart, &expiresAt,
		&lastRenewedAt, &lastChargeAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CardKeepAlive{}, ErrNotFound
	}
	if err != nil {
		return CardKeepAlive{}, err
	}
	value.Enabled = enabled != 0
	value.AutoRenew = autoRenew != 0
	value.NotificationEnabled = notificationEnabled != 0
	value.PlanStartedAt = timeFromUnix(planStart)
	value.CurrentCycleStartAt = timeFromUnix(currentCycleStart)
	value.ExpiresAt = timeFromUnix(expiresAt)
	value.LastRenewedAt = timeFromUnix(lastRenewedAt)
	value.LastChargeAt = timeFromUnix(lastChargeAt)
	value.CreatedAt = timeFromUnix(createdAt)
	value.UpdatedAt = timeFromUnix(updatedAt)
	return value, nil
}

func (s *Store) CardKeepAlive(ctx context.Context, iccid string) (CardKeepAlive, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return CardKeepAlive{}, err
	}
	return scanCardKeepAlive(s.db.QueryRowContext(ctx, cardKeepAliveSelect+` WHERE iccid = ?`, iccid))
}

func (s *Store) ListCardKeepAlive(ctx context.Context) ([]CardKeepAlive, error) {
	rows, err := s.db.QueryContext(ctx, cardKeepAliveSelect+` ORDER BY iccid`)
	if err != nil {
		return nil, fmt.Errorf("list card keep-alive: %w", err)
	}
	defer rows.Close()
	values := make([]CardKeepAlive, 0)
	for rows.Next() {
		value, err := scanCardKeepAlive(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate card keep-alive: %w", err)
	}
	return values, nil
}

func (s *Store) ApplyCardKeepAliveRenewal(
	ctx context.Context,
	iccid string,
	expectedExpiresAt time.Time,
	now time.Time,
) (CardKeepAliveRenewal, error) {
	return s.applyCardKeepAliveRenewal(ctx, iccid, expectedExpiresAt, now, "automatic")
}

func (s *Store) ApplyManualCardKeepAliveRenewal(
	ctx context.Context,
	iccid string,
	now time.Time,
) (CardKeepAliveRenewal, error) {
	current, err := s.CardKeepAlive(ctx, iccid)
	if err != nil {
		return CardKeepAliveRenewal{}, err
	}
	return s.applyCardKeepAliveRenewal(ctx, iccid, current.ExpiresAt, now, "manual")
}

func (s *Store) applyCardKeepAliveRenewal(
	ctx context.Context,
	iccid string,
	expectedExpiresAt time.Time,
	now time.Time,
	source string,
) (CardKeepAliveRenewal, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return CardKeepAliveRenewal{}, err
	}
	if source != "manual" && source != "automatic" {
		return CardKeepAliveRenewal{}, fmt.Errorf("invalid card keep-alive renewal source %q", source)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CardKeepAliveRenewal{}, fmt.Errorf("begin card keep-alive renewal: %w", err)
	}
	defer tx.Rollback()
	value, err := scanCardKeepAlive(tx.QueryRowContext(ctx, cardKeepAliveSelect+` WHERE iccid = ?`, iccid))
	if err != nil {
		return CardKeepAliveRenewal{}, err
	}
	if unixOrZero(value.ExpiresAt) != unixOrZero(expectedExpiresAt) {
		return CardKeepAliveRenewal{}, errors.New("card keep-alive expiry changed before renewal")
	}
	if !value.Enabled {
		return CardKeepAliveRenewal{}, errors.New("card keep-alive is disabled")
	}
	if source == "automatic" && !value.AutoRenew {
		return CardKeepAliveRenewal{}, errors.New("card keep-alive auto renewal is disabled")
	}
	renewal := CardKeepAliveRenewal{
		ICCID:              value.ICCID,
		Source:             source,
		AmountCents:        value.DeductionCents,
		BalanceBeforeCents: value.BalanceCents,
		BalanceAfterCents:  value.BalanceCents,
		Currency:           value.Currency,
		CycleStartAt:       now.UTC(),
		CycleEndAt:         value.ExpiresAt,
		NewExpiresAt:       value.ExpiresAt,
		CreatedAt:          now.UTC(),
	}
	if value.BalanceCents < value.DeductionCents {
		renewal.Status = CardKeepAliveChargeInsufficient
		renewal.Note = fmt.Sprintf("%s renewal skipped because the balance was insufficient", source)
	} else {
		renewalBase := startOfCardKeepAliveDay(now)
		nextExpiry, nextErr := NextCardKeepAliveExpiry(renewalBase, value.CycleRule, value.CycleDays)
		if nextErr != nil {
			return CardKeepAliveRenewal{}, nextErr
		}
		renewal.Status = CardKeepAliveChargeSuccess
		renewal.BalanceAfterCents = value.BalanceCents - value.DeductionCents
		renewal.CycleStartAt = renewalBase
		renewal.CycleEndAt = nextExpiry
		renewal.NewExpiresAt = nextExpiry
		renewal.Note = fmt.Sprintf("%s keep-alive renewal", source)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO card_keepalive_charges (
			iccid, status, source, amount_cents, balance_before_cents, balance_after_cents,
			currency, cycle_start_at, cycle_end_at, note, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, renewal.ICCID, renewal.Status, renewal.Source, renewal.AmountCents, renewal.BalanceBeforeCents,
		renewal.BalanceAfterCents, renewal.Currency, unixOrZero(renewal.CycleStartAt),
		unixOrZero(renewal.CycleEndAt), renewal.Note, renewal.CreatedAt.Unix()); err != nil {
		return CardKeepAliveRenewal{}, fmt.Errorf("insert card keep-alive charge: %w", err)
	}
	if renewal.Status == CardKeepAliveChargeSuccess {
		if _, err := tx.ExecContext(ctx, `
			UPDATE card_keepalive SET
				balance_cents = ?, current_cycle_start_at = ?, expires_at = ?,
				last_renewed_at = ?, last_charge_at = ?, updated_at = ?
			WHERE iccid = ? AND expires_at = ?
		`, renewal.BalanceAfterCents, unixOrZero(renewal.CycleStartAt), unixOrZero(renewal.NewExpiresAt),
			now.Unix(), now.Unix(), now.Unix(), iccid, unixOrZero(expectedExpiresAt)); err != nil {
			return CardKeepAliveRenewal{}, fmt.Errorf("advance card keep-alive cycle: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE card_keepalive SET last_charge_at = ?, updated_at = ? WHERE iccid = ?`,
			now.Unix(), now.Unix(), iccid); err != nil {
			return CardKeepAliveRenewal{}, fmt.Errorf("record failed card keep-alive renewal: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CardKeepAliveRenewal{}, fmt.Errorf("commit card keep-alive renewal: %w", err)
	}
	return renewal, nil
}

func (s *Store) ListCardKeepAliveCharges(ctx context.Context, iccid string, limit int) ([]CardKeepAliveCharge, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, iccid, status, source, amount_cents, balance_before_cents, balance_after_cents,
			currency, cycle_start_at, cycle_end_at, note, created_at
		FROM card_keepalive_charges
		WHERE iccid = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, iccid, limit)
	if err != nil {
		return nil, fmt.Errorf("list card keep-alive charges: %w", err)
	}
	defer rows.Close()
	values := make([]CardKeepAliveCharge, 0)
	for rows.Next() {
		var value CardKeepAliveCharge
		var cycleStartAt, cycleEndAt, createdAt int64
		if err := rows.Scan(
			&value.ID, &value.ICCID, &value.Status, &value.Source, &value.AmountCents,
			&value.BalanceBeforeCents, &value.BalanceAfterCents, &value.Currency,
			&cycleStartAt, &cycleEndAt, &value.Note, &createdAt,
		); err != nil {
			return nil, err
		}
		value.CycleStartAt = timeFromUnix(cycleStartAt)
		value.CycleEndAt = timeFromUnix(cycleEndAt)
		value.CreatedAt = timeFromUnix(createdAt)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate card keep-alive charges: %w", err)
	}
	return values, nil
}

func (s *Store) DeleteCardKeepAliveCharge(ctx context.Context, iccid string, id int64) error {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return err
	}
	if id <= 0 {
		return errors.New("keep-alive charge ID must be positive")
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM card_keepalive_charges WHERE iccid = ? AND id = ?`,
		iccid, id,
	)
	if err != nil {
		return fmt.Errorf("delete card keep-alive charge: %w", err)
	}
	return requireAffected(result)
}

func normalizeCardKeepAliveCharge(value CardKeepAliveCharge) (CardKeepAliveCharge, error) {
	iccid, err := NormalizeSimICCID(value.ICCID)
	if err != nil {
		return CardKeepAliveCharge{}, err
	}
	switch value.Status {
	case CardKeepAliveChargeSuccess, CardKeepAliveChargeInsufficient, CardKeepAliveChargeFailed:
	default:
		return CardKeepAliveCharge{}, fmt.Errorf("invalid keep-alive charge status %q", value.Status)
	}
	if value.Source != "automatic" && value.Source != "manual" {
		return CardKeepAliveCharge{}, fmt.Errorf("invalid keep-alive charge source %q", value.Source)
	}
	currency, err := NormalizeCardKeepAliveCurrency(value.Currency)
	if err != nil {
		return CardKeepAliveCharge{}, err
	}
	if value.AmountCents < 0 || value.BalanceBeforeCents < 0 || value.BalanceAfterCents < 0 {
		return CardKeepAliveCharge{}, errors.New("keep-alive charge amounts cannot be negative")
	}
	value.ICCID = iccid
	value.Currency = currency
	value.Note = strings.TrimSpace(value.Note)
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	return value, nil
}

func (s *Store) ImportCardKeepAliveCharges(ctx context.Context, values []CardKeepAliveCharge) (int, error) {
	if len(values) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin card keep-alive charge import: %w", err)
	}
	defer tx.Rollback()
	created := 0
	for _, raw := range values {
		value, err := normalizeCardKeepAliveCharge(raw)
		if err != nil {
			return created, err
		}
		if value.ID > 0 {
			result, err := tx.ExecContext(ctx, `
				INSERT INTO card_keepalive_charges (
					id, iccid, status, source, amount_cents, balance_before_cents, balance_after_cents,
					currency, cycle_start_at, cycle_end_at, note, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					status = excluded.status,
					source = excluded.source,
					amount_cents = excluded.amount_cents,
					balance_before_cents = excluded.balance_before_cents,
					balance_after_cents = excluded.balance_after_cents,
					currency = excluded.currency,
					cycle_start_at = excluded.cycle_start_at,
					cycle_end_at = excluded.cycle_end_at,
					note = excluded.note,
					created_at = excluded.created_at
				WHERE card_keepalive_charges.iccid = excluded.iccid
			`, value.ID, value.ICCID, value.Status, value.Source, value.AmountCents, value.BalanceBeforeCents,
				value.BalanceAfterCents, value.Currency, unixOrZero(value.CycleStartAt), unixOrZero(value.CycleEndAt),
				value.Note, value.CreatedAt.Unix())
			if err != nil {
				return created, fmt.Errorf("import card keep-alive charge %d: %w", value.ID, err)
			}
			if affected, err := result.RowsAffected(); err == nil && affected > 0 {
				created++
			}
			continue
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO card_keepalive_charges (
				iccid, status, source, amount_cents, balance_before_cents, balance_after_cents,
				currency, cycle_start_at, cycle_end_at, note, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, value.ICCID, value.Status, value.Source, value.AmountCents, value.BalanceBeforeCents,
			value.BalanceAfterCents, value.Currency, unixOrZero(value.CycleStartAt), unixOrZero(value.CycleEndAt),
			value.Note, value.CreatedAt.Unix())
		if err != nil {
			return created, fmt.Errorf("import card keep-alive charge: %w", err)
		}
		if err := requireAffected(result); err != nil {
			return created, err
		}
		created++
	}
	if err := tx.Commit(); err != nil {
		return created, fmt.Errorf("commit card keep-alive charge import: %w", err)
	}
	return created, nil
}

func (s *Store) ClaimCardKeepAliveReminder(
	ctx context.Context,
	iccid string,
	eventType string,
	cycleEndAt time.Time,
	now time.Time,
) (bool, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return false, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO card_keepalive_reminders (
			iccid, event_type, cycle_end_at, created_at
		) VALUES (?, ?, ?, ?)
	`, iccid, strings.TrimSpace(eventType), unixOrZero(cycleEndAt), now.Unix())
	if err != nil {
		return false, fmt.Errorf("claim card keep-alive reminder: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read card keep-alive reminder result: %w", err)
	}
	return affected > 0, nil
}
