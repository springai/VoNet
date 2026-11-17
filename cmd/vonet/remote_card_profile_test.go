package main

import (
	"context"
	"testing"

	"vonet/internal/agentproto"
	"vonet/internal/store"
)

func TestUpsertRemoteCardProfilePreservesSavedMetadata(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	const iccid = "8944305293606904114"
	if err := database.UpsertSimCard(ctx, store.SimCard{
		ICCID: iccid, DisplayName: "UK CTExcel", Notes: "saved manually",
		CardType: store.SimCardTypePhysical, IMSI: "234336570994346",
		MSISDN: "+447888870669", Carrier: "EE", SPN: "CTExcel",
		MCC: "234", MNC: "33", HomePLMN: "23433",
		HostType: store.DeviceTypeUSBSIMReader, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	profile := &agentproto.CardProfile{
		Iccid: iccid, Imsi: "234336570994346", Msisdn: "",
		Smsc: "+447700000999", UsimAid: []byte{0xa0, 0x00, 0x00, 0x00},
	}
	if err := upsertRemoteCardProfile(ctx, database, profile, []byte(`{"remote_card":"2"}`)); err != nil {
		t.Fatal(err)
	}
	card, err := database.SimCard(ctx, iccid)
	if err != nil {
		t.Fatal(err)
	}
	if card.DisplayName != "UK CTExcel" || card.Notes != "saved manually" ||
		card.Carrier != "EE" || card.SPN != "CTExcel" || card.MCC != "234" ||
		card.MNC != "33" || card.HomePLMN != "23433" || card.HostType != store.DeviceTypeUSBSIMReader {
		t.Fatalf("saved metadata was overwritten: %+v", card)
	}
	if card.Source != "manual" {
		t.Fatalf("source=%q, want manual", card.Source)
	}
	if card.MSISDN != "+447888870669" {
		t.Fatalf("empty remote MSISDN cleared saved identity: %+v", card)
	}
	if card.SMSC != "+447700000999" || card.ProfileAID != "A0000000" {
		t.Fatalf("remote identity was not merged: %+v", card)
	}
	if string(card.DerivedJSON) != `{"remote_card":"2"}` {
		t.Fatalf("derived=%s", card.DerivedJSON)
	}
}
