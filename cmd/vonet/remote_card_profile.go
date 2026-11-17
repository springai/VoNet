package main

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"vonet/internal/agentproto"
	"vonet/internal/store"
)

func upsertRemoteCardProfile(
	ctx context.Context,
	database *store.Store,
	profile *agentproto.CardProfile,
	derived []byte,
) error {
	iccid := strings.TrimSpace(profile.GetIccid())
	existing, err := database.SimCard(ctx, iccid)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	cardType := store.SimCardTypePhysical
	source := "agent_remote"
	if err == nil {
		cardType = existing.CardType
		source = existing.Source
	}
	profileAID := strings.ToUpper(hex.EncodeToString(profile.GetUsimAid()))
	if profileAID == "" {
		profileAID = existing.ProfileAID
	}
	derivedJSON := derived
	if len(derivedJSON) == 0 {
		derivedJSON = existing.DerivedJSON
	}
	return database.UpsertSimCard(ctx, store.SimCard{
		ICCID:       iccid,
		DisplayName: existing.DisplayName,
		Notes:       existing.Notes,
		CardType:    cardType,
		IMSI:        firstNonEmpty(strings.TrimSpace(profile.GetImsi()), existing.IMSI),
		MSISDN:      firstNonEmpty(strings.TrimSpace(profile.GetMsisdn()), existing.MSISDN),
		Carrier:     existing.Carrier,
		SPN:         firstNonEmpty(strings.TrimSpace(profile.GetSpn()), existing.SPN),
		MCC:         existing.MCC,
		MNC:         existing.MNC,
		HomePLMN:    existing.HomePLMN,
		EID:         existing.EID,
		ProfileAID:  profileAID,
		GID1:        firstNonEmpty(strings.TrimSpace(profile.GetGid1()), existing.GID1),
		GID2:        firstNonEmpty(strings.TrimSpace(profile.GetGid2()), existing.GID2),
		SMSC:        firstNonEmpty(strings.TrimSpace(profile.GetSmsc()), existing.SMSC),
		DerivedJSON: derivedJSON,
		HostType:    existing.HostType,
		Source:      source,
	})
}
