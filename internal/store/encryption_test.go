package store

import (
	"context"
	"strings"
	"testing"
)

func TestSensitiveCardFieldsAreEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.UpsertSimCard(ctx, SimCard{
		ICCID: "8944100000000000001", IMSI: "234120000000001", MSISDN: "1234567890",
		EID: "89000000000000000000000000000001", ProfileAID: "A0000005591010",
		GID1: "508FFFFF", GID2: "A0", SMSC: "+447785016005",
		DerivedJSON: []byte(`{"impu":"sip:test@example.invalid"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDevice(ctx, Device{ID: "reader-1", Name: "Reader", DeviceType: DeviceTypeUSBSIMReader, SIMPIN: "1234"}); err != nil {
		t.Fatal(err)
	}
	var cardIMSI, cardAID, devicePIN string
	if err := database.db.QueryRowContext(ctx, `SELECT imsi, profile_aid, '' FROM sim_cards WHERE iccid = ?`, "8944100000000000001").Scan(&cardIMSI, &cardAID, &devicePIN); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cardIMSI, "234120000000001") || strings.Contains(cardAID, "A0000005591010") {
		t.Fatalf("sensitive card fields were stored in plaintext: %q %q", cardIMSI, cardAID)
	}
	var storedPIN string
	if err := database.db.QueryRowContext(ctx, `SELECT sim_pin FROM devices WHERE id = ?`, "reader-1").Scan(&storedPIN); err != nil {
		t.Fatal(err)
	}
	if storedPIN != "" {
		t.Fatalf("SIM PIN was persisted: %q", storedPIN)
	}
	card, err := database.SimCard(ctx, "8944100000000000001")
	if err != nil || card.IMSI != "234120000000001" || card.ProfileAID != "A0000005591010" {
		t.Fatalf("decrypted card=%#v err=%v", card, err)
	}
	if _, err := database.SaveSMSMessage(ctx, SMSMessage{
		MessageID: "encrypted-sms", DeviceID: "reader-1", IMSI: "234120000000001",
		LocalPhone: "+12025550123", Peer: "+12025550456", Body: "private message",
		Direction: "inbound",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.InsertCallRecord(CallRecord{
		DeviceID: "reader-1", ICCID: "8944100000000000001", Number: "+12025550789",
	}); err != nil {
		t.Fatal(err)
	}
	var smsIMSI, smsPeer, smsBody, callNumber string
	if err := database.db.QueryRowContext(ctx,
		`SELECT imsi, peer, body FROM sms_messages WHERE message_id = ?`, "encrypted-sms",
	).Scan(&smsIMSI, &smsPeer, &smsBody); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx,
		`SELECT number FROM call_history WHERE device_id = ?`, "reader-1",
	).Scan(&callNumber); err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{smsIMSI, smsPeer, smsBody, callNumber} {
		if strings.Contains(stored, "234120000000001") ||
			strings.Contains(stored, "+1202555") ||
			strings.Contains(stored, "private message") {
			t.Fatalf("communication data was stored in plaintext: %q", stored)
		}
	}
}
