package store

import (
	"context"
	"testing"
)

func TestCallHistoryIsolatedByICCID(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, record := range []CallRecord{
		{DeviceID: "modem-1", ICCID: "8944000000000000001", Number: "+100", Direction: "outgoing", Status: "answered", StartedAt: 100},
		{DeviceID: "modem-1", ICCID: "8944000000000000002", Number: "+200", Direction: "incoming", Status: "missed", StartedAt: 200},
	} {
		if err := database.InsertCallRecord(record); err != nil {
			t.Fatalf("InsertCallRecord() error = %v", err)
		}
	}

	records, total, err := database.ListCallHistoryForCard(
		"modem-1", "8944000000000000001", 10, 0,
	)
	if err != nil || total != 1 || len(records) != 1 || records[0].Number != "+100" {
		t.Fatalf("ListCallHistoryForCard() = %#v, %d, %v", records, total, err)
	}

	found, err := database.SearchCallHistoryForCard(
		"modem-1", "8944000000000000002", "+100", 10,
	)
	if err != nil || len(found) != 0 {
		t.Fatalf("SearchCallHistoryForCard() crossed ICCID boundary: %#v, %v", found, err)
	}

	if err := database.DeleteAllCallHistoryForCard(
		"modem-1", "8944000000000000001",
	); err != nil {
		t.Fatalf("DeleteAllCallHistoryForCard() error = %v", err)
	}
	_, remaining, err := database.ListCallHistoryForCard(
		"modem-1", "8944000000000000002", 10, 0,
	)
	if err != nil || remaining != 1 {
		t.Fatalf("delete crossed ICCID boundary: remaining = %d, err = %v", remaining, err)
	}
}
