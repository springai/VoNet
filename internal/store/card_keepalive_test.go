package store

import (
	"context"
	"testing"
	"time"
)

func TestCardKeepAliveRenewalRecordsChargeAndAdvancesCycle(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.CreateSimCard(ctx, SimCard{ICCID: testSimICCID, CardType: SimCardTypePhysical}); err != nil {
		t.Fatalf("CreateSimCard() error = %v", err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	saved, err := database.UpsertCardKeepAlive(ctx, CardKeepAlive{
		ICCID:               testSimICCID,
		Enabled:             true,
		Currency:            "GBP",
		BalanceCents:        500,
		PlanStartedAt:       start,
		CycleRule:           CardKeepAliveCycleMonthly,
		DeductionCents:      300,
		AutoRenew:           true,
		NotifyDaysBefore:    3,
		NotificationEnabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertCardKeepAlive() error = %v", err)
	}
	if !saved.ExpiresAt.Equal(start.AddDate(0, 1, 0)) {
		t.Fatalf("ExpiresAt = %v, want %v", saved.ExpiresAt, start.AddDate(0, 1, 0))
	}
	renewal, err := database.ApplyCardKeepAliveRenewal(ctx, testSimICCID, saved.ExpiresAt, saved.ExpiresAt)
	if err != nil {
		t.Fatalf("ApplyCardKeepAliveRenewal() error = %v", err)
	}
	if renewal.Status != CardKeepAliveChargeSuccess || renewal.BalanceAfterCents != 200 ||
		!renewal.NewExpiresAt.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)) ||
		!renewal.CycleStartAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("renewal = %+v", renewal)
	}
	stored, err := database.CardKeepAlive(ctx, testSimICCID)
	if err != nil || !stored.CurrentCycleStartAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("automatic cycle baseline = %+v, %v", stored, err)
	}
	charges, err := database.ListCardKeepAliveCharges(ctx, testSimICCID, 10)
	if err != nil || len(charges) != 1 {
		t.Fatalf("ListCardKeepAliveCharges() = %+v, %v", charges, err)
	}
	if charges[0].AmountCents != 300 || charges[0].BalanceBeforeCents != 500 ||
		charges[0].BalanceAfterCents != 200 || charges[0].Source != "automatic" {
		t.Fatalf("charge = %+v", charges[0])
	}
	failed, err := database.ApplyCardKeepAliveRenewal(ctx, testSimICCID, renewal.NewExpiresAt, renewal.NewExpiresAt)
	if err != nil {
		t.Fatalf("second ApplyCardKeepAliveRenewal() error = %v", err)
	}
	if failed.Status != CardKeepAliveChargeInsufficient || failed.BalanceAfterCents != 200 {
		t.Fatalf("insufficient renewal = %+v", failed)
	}
}

func TestManualCardKeepAliveRenewalRecordsSourceAndExtendsCycle(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.CreateSimCard(ctx, SimCard{ICCID: testSimICCID, CardType: SimCardTypePhysical}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	renewAt := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	_, err := database.UpsertCardKeepAlive(ctx, CardKeepAlive{
		ICCID:          testSimICCID,
		Enabled:        true,
		Currency:       "GBP",
		BalanceCents:   1000,
		PlanStartedAt:  start,
		CycleRule:      CardKeepAliveCycleCustomDays,
		CycleDays:      90,
		DeductionCents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	renewal, err := database.ApplyManualCardKeepAliveRenewal(ctx, testSimICCID, renewAt)
	if err != nil {
		t.Fatal(err)
	}
	if renewal.Source != "manual" || renewal.BalanceAfterCents != 999 ||
		!renewal.NewExpiresAt.Equal(time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local)) ||
		!renewal.CycleStartAt.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("manual renewal = %+v", renewal)
	}
	stored, err := database.CardKeepAlive(ctx, testSimICCID)
	if err != nil || !stored.CurrentCycleStartAt.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("manual cycle baseline = %+v, %v", stored, err)
	}
	charges, err := database.ListCardKeepAliveCharges(ctx, testSimICCID, 10)
	if err != nil || len(charges) != 1 || charges[0].Source != "manual" {
		t.Fatalf("manual charges = %+v, %v", charges, err)
	}
	renewAtSameDay := renewAt.Add(2 * time.Hour)
	secondRenewal, err := database.ApplyManualCardKeepAliveRenewal(ctx, testSimICCID, renewAtSameDay)
	if err != nil {
		t.Fatal(err)
	}
	if !secondRenewal.NewExpiresAt.Equal(renewal.NewExpiresAt) {
		t.Fatalf("same-day renewal expiry = %v, want %v", secondRenewal.NewExpiresAt, renewal.NewExpiresAt)
	}
	chargesAfterSecondRenewal, err := database.ListCardKeepAliveCharges(ctx, testSimICCID, 10)
	if err != nil || len(chargesAfterSecondRenewal) != 2 {
		t.Fatalf("charges after same-day renewal = %+v, %v", chargesAfterSecondRenewal, err)
	}
	for _, charge := range chargesAfterSecondRenewal {
		if err := database.DeleteCardKeepAliveCharge(ctx, testSimICCID, charge.ID); err != nil {
			t.Fatalf("DeleteCardKeepAliveCharge() error = %v", err)
		}
	}
	remaining, err := database.ListCardKeepAliveCharges(ctx, testSimICCID, 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("charges after delete = %+v, %v", remaining, err)
	}
}

func TestCardKeepAliveKeepsExplicitExpiryDate(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.CreateSimCard(ctx, SimCard{ICCID: testSimICCID, CardType: SimCardTypePhysical}); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	saved, err := database.UpsertCardKeepAlive(ctx, CardKeepAlive{
		ICCID:         testSimICCID,
		Enabled:       true,
		Currency:      "GBP",
		PlanStartedAt: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		CycleRule:     CardKeepAliveCycleCustomDays,
		CycleDays:     90,
		ExpiresAt:     expiresAt,
	})
	if err != nil || !saved.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("explicit expiry = %+v, %v", saved, err)
	}
}

func TestCardKeepAliveReminderClaimIsIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.CreateSimCard(ctx, SimCard{ICCID: testSimICCID, CardType: SimCardTypePhysical}); err != nil {
		t.Fatal(err)
	}
	cycleEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	first, err := database.ClaimCardKeepAliveReminder(ctx, testSimICCID, "expiry_day", cycleEnd, cycleEnd.Add(-time.Minute))
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	second, err := database.ClaimCardKeepAliveReminder(ctx, testSimICCID, "expiry_day", cycleEnd, cycleEnd)
	if err != nil || second {
		t.Fatalf("second claim = %v, %v", second, err)
	}
}

func TestNextCardKeepAliveExpiryRules(t *testing.T) {
	start := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if value, err := NextCardKeepAliveExpiry(start, CardKeepAliveCycleCustomDays, 10); err != nil || value.Day() != 10 {
		t.Fatalf("custom expiry = %v, %v", value, err)
	}
	if value, err := NextCardKeepAliveExpiry(start, CardKeepAliveCycleQuarterly, 0); err != nil ||
		value.Month() != time.April || value.Day() != 30 {
		t.Fatalf("quarterly expiry = %v, %v", value, err)
	}
	if value, err := NextCardKeepAliveExpiry(start, CardKeepAliveCycleYearly, 0); err != nil ||
		value.Year() != 2027 || value.Month() != time.January || value.Day() != 31 {
		t.Fatalf("yearly expiry = %v, %v", value, err)
	}
}
