package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vonet/internal/store"
)

type cardKeepAlivePayload struct {
	Enabled             bool   `json:"enabled"`
	PlanInfo            string `json:"plan_info"`
	Currency            string `json:"currency"`
	BalanceCents        int64  `json:"balance_cents"`
	PlanStartAt         int64  `json:"plan_start_at"`
	CurrentCycleStartAt int64  `json:"current_cycle_start_at"`
	ExpiresAt           int64  `json:"expires_at"`
	CycleRule           string `json:"cycle_rule"`
	CycleDays           int    `json:"cycle_days"`
	DeductionCents      int64  `json:"deduction_cents"`
	AutoRenew           bool   `json:"auto_renew"`
	NotificationEnabled bool   `json:"notification_enabled"`
	NotifyDaysBefore    int    `json:"notify_days_before"`
}

type cardKeepAliveChargePayload struct {
	ID                 int64  `json:"id"`
	ICCID              string `json:"iccid"`
	Status             string `json:"status"`
	Source             string `json:"source"`
	AmountCents        int64  `json:"amount_cents"`
	BalanceBeforeCents int64  `json:"balance_before_cents"`
	BalanceAfterCents  int64  `json:"balance_after_cents"`
	Currency           string `json:"currency"`
	CycleStartAt       int64  `json:"cycle_start_at"`
	CycleEndAt         int64  `json:"cycle_end_at"`
	Note               string `json:"note"`
	CreatedAt          int64  `json:"created_at"`
}

func (payload cardKeepAliveChargePayload) model() store.CardKeepAliveCharge {
	return store.CardKeepAliveCharge{
		ID:                 payload.ID,
		ICCID:              payload.ICCID,
		Status:             payload.Status,
		Source:             payload.Source,
		AmountCents:        payload.AmountCents,
		BalanceBeforeCents: payload.BalanceBeforeCents,
		BalanceAfterCents:  payload.BalanceAfterCents,
		Currency:           strings.ToUpper(strings.TrimSpace(payload.Currency)),
		CycleStartAt:       timeFromUnixSeconds(payload.CycleStartAt),
		CycleEndAt:         timeFromUnixSeconds(payload.CycleEndAt),
		Note:               strings.TrimSpace(payload.Note),
		CreatedAt:          timeFromUnixSeconds(payload.CreatedAt),
	}
}

func normalizeCardKeepAlivePayload(iccid string, payload cardKeepAlivePayload) (store.CardKeepAlive, error) {
	value := store.CardKeepAlive{
		ICCID:               iccid,
		Enabled:             payload.Enabled,
		PlanInfo:            payload.PlanInfo,
		Currency:            payload.Currency,
		BalanceCents:        payload.BalanceCents,
		PlanStartedAt:       timeFromUnixSeconds(payload.PlanStartAt),
		CurrentCycleStartAt: timeFromUnixSeconds(payload.CurrentCycleStartAt),
		ExpiresAt:           timeFromUnixSeconds(payload.ExpiresAt),
		CycleRule:           payload.CycleRule,
		CycleDays:           payload.CycleDays,
		DeductionCents:      payload.DeductionCents,
		AutoRenew:           payload.AutoRenew,
		NotificationEnabled: payload.NotificationEnabled,
		NotifyDaysBefore:    payload.NotifyDaysBefore,
	}
	if strings.TrimSpace(value.Currency) == "" {
		value.Currency = "CNY"
	}
	if strings.TrimSpace(value.CycleRule) == "" {
		value.CycleRule = store.CardKeepAliveCycleMonthly
	}
	if value.NotifyDaysBefore == 0 {
		value.NotifyDaysBefore = 3
	}
	return store.NormalizeCardKeepAlive(value)
}

func timeFromUnixSeconds(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func cardKeepAliveStatus(value store.CardKeepAlive, now time.Time) string {
	if !value.Enabled {
		return "disabled"
	}
	if value.ExpiresAt.IsZero() {
		return "incomplete"
	}
	if now.Before(value.ExpiresAt) {
		return "active"
	}
	return "expired"
}

func cardKeepAliveResponse(value store.CardKeepAlive, now time.Time) map[string]any {
	daysRemaining := 0
	if value.Enabled && now.Before(value.ExpiresAt) {
		daysRemaining = int((value.ExpiresAt.Sub(now) + 24*time.Hour - 1) / (24 * time.Hour))
	}
	return map[string]any{
		"enabled":                value.Enabled,
		"plan_info":              value.PlanInfo,
		"currency":               value.Currency,
		"balance_cents":          value.BalanceCents,
		"plan_start_at":          unixOrZeroTime(value.PlanStartedAt),
		"cycle_rule":             value.CycleRule,
		"cycle_days":             value.CycleDays,
		"deduction_cents":        value.DeductionCents,
		"auto_renew":             value.AutoRenew,
		"notification_enabled":   value.NotificationEnabled,
		"notify_days_before":     value.NotifyDaysBefore,
		"current_cycle_start_at": unixOrZeroTime(value.CurrentCycleStartAt),
		"expires_at":             unixOrZeroTime(value.ExpiresAt),
		"last_renewed_at":        unixOrZeroTime(value.LastRenewedAt),
		"last_charge_at":         unixOrZeroTime(value.LastChargeAt),
		"status":                 cardKeepAliveStatus(value, now),
		"days_remaining":         daysRemaining,
	}
}

func unixOrZeroTime(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func defaultCardKeepAliveResponse(now time.Time) map[string]any {
	return cardKeepAliveResponse(store.CardKeepAlive{
		Currency:         "CNY",
		CycleRule:        store.CardKeepAliveCycleMonthly,
		NotifyDaysBefore: 3,
	}, now)
}

func (s *Server) cardKeepAlivePayload(ctx context.Context, iccid string) map[string]any {
	value, err := s.store.CardKeepAlive(ctx, iccid)
	if errors.Is(err, store.ErrNotFound) {
		return defaultCardKeepAliveResponse(time.Now().UTC())
	}
	if err != nil {
		return defaultCardKeepAliveResponse(time.Now().UTC())
	}
	return cardKeepAliveResponse(value, time.Now().UTC())
}

func cardKeepAliveChargeResponse(value store.CardKeepAliveCharge) map[string]any {
	return map[string]any{
		"id":                   value.ID,
		"iccid":                value.ICCID,
		"status":               value.Status,
		"source":               value.Source,
		"amount_cents":         value.AmountCents,
		"balance_before_cents": value.BalanceBeforeCents,
		"balance_after_cents":  value.BalanceAfterCents,
		"currency":             value.Currency,
		"cycle_start_at":       unixOrZeroTime(value.CycleStartAt),
		"cycle_end_at":         unixOrZeroTime(value.CycleEndAt),
		"note":                 value.Note,
		"created_at":           unixOrZeroTime(value.CreatedAt),
	}
}

func cardKeepAliveRenewalResponse(value store.CardKeepAliveRenewal) map[string]any {
	return map[string]any{
		"iccid":                value.ICCID,
		"status":               value.Status,
		"source":               value.Source,
		"amount_cents":         value.AmountCents,
		"balance_before_cents": value.BalanceBeforeCents,
		"balance_after_cents":  value.BalanceAfterCents,
		"currency":             value.Currency,
		"cycle_start_at":       unixOrZeroTime(value.CycleStartAt),
		"cycle_end_at":         unixOrZeroTime(value.CycleEndAt),
		"new_expires_at":       unixOrZeroTime(value.NewExpiresAt),
		"note":                 value.Note,
		"created_at":           unixOrZeroTime(value.CreatedAt),
	}
}

func (s *Server) handleCardKeepAlive(w http.ResponseWriter, r *http.Request, iccid string) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.store.CardKeepAlive(r.Context(), iccid)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"data": defaultCardKeepAliveResponse(time.Now().UTC())})
			return
		}
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": cardKeepAliveResponse(value, time.Now().UTC())})
	case http.MethodPut:
		var payload cardKeepAlivePayload
		if err := s.decodeJSON(w, r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		value, err := normalizeCardKeepAlivePayload(iccid, payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_card_keepalive", err.Error())
			return
		}
		saved, err := s.store.UpsertCardKeepAlive(r.Context(), value)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": cardKeepAliveResponse(saved, time.Now().UTC())})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleCardKeepAliveCharges(w http.ResponseWriter, r *http.Request, iccid string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	values, err := s.store.ListCardKeepAliveCharges(r.Context(), iccid, 200)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, cardKeepAliveChargeResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"charges": result}})
}

func (s *Server) handleCardKeepAliveCharge(w http.ResponseWriter, r *http.Request, iccid string, idText string) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_charge_id", "charge ID must be positive")
		return
	}
	if err := s.store.DeleteCardKeepAliveCharge(r.Context(), iccid, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "charge_not_found", "charge record was not found")
			return
		}
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"deleted": true}})
}

func (s *Server) handleCardKeepAliveRenew(w http.ResponseWriter, r *http.Request, iccid string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	renewal, err := s.store.ApplyManualCardKeepAliveRenewal(r.Context(), iccid, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "card_keepalive_not_found", "keep-alive settings are not configured")
			return
		}
		s.writeStoreError(w, err)
		return
	}
	if renewal.Status != store.CardKeepAliveChargeSuccess {
		writeError(w, http.StatusConflict, "insufficient_keepalive_balance", "balance is insufficient for manual renewal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": cardKeepAliveRenewalResponse(renewal)})
}

func (s *Server) cardKeepAliveDeviceID(ctx context.Context, iccid string) string {
	if binding, err := s.store.SimCardBinding(ctx, iccid); err == nil {
		return binding.DeviceID
	}
	return iccid
}

func (s *Server) notifyCardKeepAlive(
	ctx context.Context,
	value store.CardKeepAlive,
	eventType string,
	title string,
	text string,
) {
	ctx = s.notificationDestinationContext(ctx)
	deviceID := s.cardKeepAliveDeviceID(ctx, value.ICCID)
	for _, channel := range notificationChannels {
		setting, err := s.store.NotificationSetting(ctx, channel)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !setting.Enabled) {
			continue
		}
		if err != nil {
			s.logger.Warn("read card keep-alive notification setting", "channel", channel, "error", err)
			continue
		}
		var config map[string]any
		if err := json.Unmarshal(setting.Config, &config); err != nil {
			s.logger.Warn("decode card keep-alive notification setting", "channel", channel, "error", err)
			continue
		}
		if err := sendCardKeepAliveNotification(ctx, channel, config, deviceID, value, title, text); err != nil {
			s.logger.Warn("send card keep-alive notification",
				"channel", channel, "iccid", value.ICCID, "event", eventType, "error", err)
		}
	}
}

func sendCardKeepAliveNotification(
	ctx context.Context,
	channel string,
	config map[string]any,
	deviceID string,
	value store.CardKeepAlive,
	title string,
	text string,
) error {
	switch channel {
	case "meow":
		return sendMeowNotification(ctx, config, &deviceID, title, text)
	case "qqbot":
		return sendQQBotNotification(ctx, config, &deviceID, title, text)
	case "telegram":
		return sendTelegramTextNotification(ctx, config, deviceID, text)
	case "bark":
		return sendBarkTextNotification(ctx, config, deviceID, title, text)
	case "email":
		return sendEmailTextNotification(ctx, config, deviceID, title, text)
	case "pushplus":
		return sendPushplusTextNotification(ctx, config, deviceID, title, text)
	case "webhook":
		return sendCardKeepAliveWebhook(ctx, config, deviceID, value, title, text)
	case "wecom":
		values := wecomTemplateValues{
			"event": "card.keepalive", "title": title, "message": text,
			"timestamp": time.Now().UTC().Format(time.RFC3339), "content": text,
			"device_id": deviceID, "device_name": value.ICCID, "device_label": value.ICCID,
			"time": time.Now().Local().Format("2006-01-02 15:04:05"),
		}
		return sendWecomNotification(ctx, config, &deviceID, values)
	case "lark":
		values := larkTemplateValues{
			"event": "card.keepalive", "title": title, "message": text,
			"timestamp": time.Now().UTC().Format(time.RFC3339), "content": text,
			"device_id": deviceID, "device_name": value.ICCID, "device_label": value.ICCID,
			"time": time.Now().Local().Format("2006-01-02 15:04:05"),
		}
		return sendLarkNotification(ctx, config, &deviceID, values)
	default:
		return fmt.Errorf("unsupported notification channel %q", channel)
	}
}

func sendCardKeepAliveWebhook(
	ctx context.Context,
	config map[string]any,
	deviceID string,
	value store.CardKeepAlive,
	title string,
	text string,
) error {
	if !notificationMatchesDevice(configStrings(config, "device_ids"), deviceID) {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"event": "card.keepalive", "title": title, "message": text,
		"iccid": value.ICCID, "currency": value.Currency,
		"balance_cents": value.BalanceCents, "expires_at": unixOrZeroTime(value.ExpiresAt),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	client, err := restrictedHTTPClient(ctx, durationMilliseconds(configInt(config, "timeout_ms"), 5*time.Second), "")
	if err != nil {
		return err
	}
	for _, destination := range configStrings(config, "urls") {
		parsed, err := validateOutboundURL(ctx, destination, false)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), strings.NewReader(string(payload)))
		if err != nil {
			return err
		}
		for name, value := range configStringMap(config, "headers") {
			request.Header.Set(name, value)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "vonet-card-keepalive/1")
		if secret := configString(config, "secret"); secret != "" {
			signature := hmac.New(sha256.New, []byte(secret))
			_, _ = signature.Write(payload)
			request.Header.Set("X-vonet-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
		}
		if err := performNotificationRequest(client, request, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) StartCardKeepAlive(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.processCardKeepAlive(ctx, time.Now().UTC())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.processCardKeepAlive(ctx, now.UTC())
		}
	}
}

func (s *Server) processCardKeepAlive(ctx context.Context, now time.Time) {
	values, err := s.store.ListCardKeepAlive(ctx)
	if err != nil {
		s.logger.Warn("list card keep-alive", "error", err)
		return
	}
	for _, original := range values {
		value := original
		if value.AutoRenew {
			for attempts := 0; attempts < 500 && (now.After(value.ExpiresAt) || now.Equal(value.ExpiresAt)); attempts++ {
				renewal, renewalErr := s.store.ApplyCardKeepAliveRenewal(ctx, value.ICCID, value.ExpiresAt, now)
				if renewalErr != nil {
					s.logger.Warn("renew card keep-alive", "iccid", value.ICCID, "error", renewalErr)
					break
				}
				value.BalanceCents = renewal.BalanceAfterCents
				value.CurrentCycleStartAt = renewal.CycleEndAt
				value.ExpiresAt = renewal.NewExpiresAt
				value.LastChargeAt = renewal.CreatedAt
				if renewal.Status == store.CardKeepAliveChargeSuccess {
					value.LastRenewedAt = renewal.CreatedAt
					continue
				}
				s.claimAndNotifyCardKeepAlive(ctx, value, "renewal_failed", "SIM 保号续期失败",
					fmt.Sprintf("卡片 %s 自动续期失败，余额不足。", value.ICCID))
				break
			}
		}
		if !value.NotificationEnabled || value.ExpiresAt.IsZero() {
			continue
		}
		expiryDay := startOfDay(value.ExpiresAt.In(time.Local))
		if now.Before(expiryDay) {
			notifyAt := expiryDay.AddDate(0, 0, -value.NotifyDaysBefore)
			if !now.Before(notifyAt) {
				days := int(expiryDay.Sub(startOfDay(now.In(time.Local))) / (24 * time.Hour))
				s.claimAndNotifyCardKeepAlive(ctx, value, "before_expiry", "SIM 到期提醒",
					fmt.Sprintf("卡片 %s 将在 %d 天后到期，请检查套餐和余额。", value.ICCID, days))
			}
			continue
		}
		s.claimAndNotifyCardKeepAlive(ctx, value, "expiry_day", "SIM 今日到期",
			fmt.Sprintf("卡片 %s 今天到期，请及时续期或充值。", value.ICCID))
	}
}

func startOfDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func (s *Server) claimAndNotifyCardKeepAlive(
	ctx context.Context,
	value store.CardKeepAlive,
	eventType string,
	title string,
	text string,
) {
	claimed, err := s.store.ClaimCardKeepAliveReminder(ctx, value.ICCID, eventType, value.ExpiresAt, time.Now().UTC())
	if err != nil {
		s.logger.Warn("claim card keep-alive reminder", "iccid", value.ICCID, "event", eventType, "error", err)
		return
	}
	if !claimed {
		return
	}
	s.notifyCardKeepAlive(ctx, value, eventType, title, text)
}
