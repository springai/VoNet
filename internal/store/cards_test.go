package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testSimICCID      = "8944100000000000001"
	testSimICCIDOther = "8944100000000000002"
)

func TestSimCardCRUDPreservesCreatedTime(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")

	created := time.Unix(100, 0).UTC()
	if err := database.UpsertSimCard(ctx, SimCard{
		ICCID:       " " + testSimICCID + " ",
		CardType:    SimCardTypeESIM,
		DisplayName: "Primary eSIM",
		IMSI:        "234120000000001",
		Carrier:     "Test Mobile",
		EID:         "89000000000000000000000000000001",
		ProfileAID:  "a0000005591010ffffffff8900000100",
		CreatedAt:   created,
	}); err != nil {
		t.Fatalf("UpsertSimCard() error = %v", err)
	}
	card, err := database.SimCard(ctx, testSimICCID)
	if err != nil {
		t.Fatalf("SimCard() error = %v", err)
	}
	if card.ICCID != testSimICCID || card.CardType != SimCardTypeESIM ||
		card.ProfileAID != "A0000005591010FFFFFFFF8900000100" ||
		!card.CreatedAt.Equal(created) {
		t.Fatalf("stored SIM card = %+v", card)
	}

	card.DisplayName = "Renamed"
	card.Notes = "keep after device removal"
	if err := database.UpsertSimCard(ctx, card); err != nil {
		t.Fatalf("update SimCard() error = %v", err)
	}
	cards, err := database.ListSimCards(ctx)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListSimCards() = %+v, %v", cards, err)
	}
	if cards[0].DisplayName != "Renamed" || !cards[0].CreatedAt.Equal(created) {
		t.Fatalf("updated SIM card = %+v", cards[0])
	}

	if _, err := database.SimCard(ctx, "894410000000000000x"); err == nil {
		t.Fatal("invalid ICCID was accepted")
	}
}

func TestUpsertDevicePreservesReaderVoWiFiEnabled(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	device := Device{
		ID: "Reader-TEST", Name: "Reader", DeviceType: DeviceTypeUSBSIMReader,
		VoWiFiEnabled: true,
	}
	if err := database.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	stored, err := database.Device(ctx, device.ID)
	if err != nil || !stored.VoWiFiEnabled {
		t.Fatalf("reader VoWiFi state = %+v, %v", stored, err)
	}
}

func TestCreateAndRejectUpdateForDeletedSimCard(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	card := SimCard{ICCID: testSimICCID, CardType: SimCardTypePhysical}
	if err := database.CreateSimCard(ctx, card); err != nil {
		t.Fatalf("CreateSimCard() error = %v", err)
	}
	if err := database.CreateSimCard(ctx, card); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate CreateSimCard() error = %v, want ErrAlreadyExists", err)
	}
	if err := database.DeleteSimCard(ctx, testSimICCID); err != nil {
		t.Fatalf("DeleteSimCard() error = %v", err)
	}
	card.DisplayName = "must not resurrect"
	if err := database.UpdateSimCard(ctx, card); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateSimCard() after delete error = %v, want ErrNotFound", err)
	}
	if _, err := database.SimCard(ctx, testSimICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SimCard() was resurrected after stale update: %v", err)
	}
}

func TestCreateSimCardWithPolicyIsAtomic(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.CreateSimCardWithPolicy(ctx, SimCard{
		ICCID: testSimICCID, CardType: SimCardTypePhysical, HostType: DeviceTypeUSBSIMReader,
	}, CardPolicy{
		ICCID: testSimICCID, RuntimeMode: CardRuntimeModePCSC, CellularIMSMode: CellularIMSModeMBNDefault,
	}); err != nil {
		t.Fatal(err)
	}
	card, err := database.SimCard(ctx, testSimICCID)
	if err != nil || card.HostType != DeviceTypeUSBSIMReader {
		t.Fatalf("created card = %+v, %v", card, err)
	}
	if _, err := database.CardPolicy(ctx, testSimICCID); err != nil {
		t.Fatalf("initial policy = %v", err)
	}

	invalidICCID := testSimICCIDOther
	err = database.CreateSimCardWithPolicy(ctx, SimCard{
		ICCID: invalidICCID, CardType: SimCardTypePhysical,
	}, CardPolicy{ICCID: invalidICCID, RuntimeMode: "invalid"})
	if err == nil {
		t.Fatal("invalid initial policy was accepted")
	}
	if _, err := database.SimCard(ctx, invalidICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid policy left a card behind: %v", err)
	}
}

func TestImportSimCardsRollsBackWholeBatchOnFailure(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	const firstICCID = "8944100000000000011"
	const failedICCID = "8944100000000000012"
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_card_import BEFORE INSERT ON sim_cards
		WHEN NEW.iccid = '`+failedICCID+`'
		BEGIN SELECT RAISE(ABORT, 'forced import failure'); END
	`); err != nil {
		t.Fatal(err)
	}

	_, err := database.ImportSimCards(ctx, []SimCard{
		{ICCID: firstICCID, CardType: SimCardTypePhysical, DisplayName: "First"},
		{ICCID: failedICCID, CardType: SimCardTypePhysical, DisplayName: "Failed"},
	})
	if err == nil || !strings.Contains(err.Error(), "forced import failure") {
		t.Fatalf("ImportSimCards error = %v, want forced failure", err)
	}
	if _, err := database.SimCard(ctx, firstICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first imported card was not rolled back: %v", err)
	}
	if _, err := database.CardPolicy(ctx, firstICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first imported card policy was not rolled back: %v", err)
	}
}

func TestImportSimCardsCreatesMissingDefaultPolicy(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	result, err := database.ImportSimCards(ctx, []SimCard{{
		ICCID: testSimICCID, CardType: SimCardTypePhysical,
		DisplayName: "Imported", Notes: "Imported note",
	}})
	if err != nil || result.Created != 1 {
		t.Fatalf("ImportSimCards = %+v, %v", result, err)
	}
	policy, err := database.CardPolicy(ctx, testSimICCID)
	if err != nil || policy.Source != "import" || !policy.AirplaneEnabled ||
		policy.VoWiFiEnabled || policy.NetworkEnabled || policy.IPVersion != "IPV4V6" {
		t.Fatalf("imported default policy = %+v, %v", policy, err)
	}
}

func TestImportSimCardsPreservesExistingMetadataAndPolicy(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.UpsertSimCard(ctx, SimCard{
		ICCID: testSimICCID, CardType: SimCardTypePhysical,
		DisplayName: "Old", Notes: "Old note", IMSI: "234120000000001", HostType: DeviceTypeUSBSIMReader,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCID, VoWiFiEnabled: true, AirplaneEnabled: true,
		IPVersion: "IPV4V6", RuntimeMode: CardRuntimeModePCSC,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := database.ImportSimCards(ctx, []SimCard{{
		ICCID: testSimICCID, CardType: SimCardTypeESIM,
		DisplayName: "New", Notes: "New note",
	}})
	if err != nil || result.Updated != 1 || result.Created != 0 {
		t.Fatalf("ImportSimCards = %+v, %v", result, err)
	}
	card, err := database.SimCard(ctx, testSimICCID)
	if err != nil || card.DisplayName != "New" || card.Notes != "New note" ||
		card.CardType != SimCardTypeESIM || card.IMSI != "234120000000001" ||
		card.HostType != DeviceTypeUSBSIMReader {
		t.Fatalf("imported card = %+v, %v", card, err)
	}
	policy, err := database.CardPolicy(ctx, testSimICCID)
	if err != nil || !policy.VoWiFiEnabled || policy.RuntimeMode != CardRuntimeModePCSC {
		t.Fatalf("import changed card policy = %+v, %v", policy, err)
	}
}

func TestTouchSimCardCandidateTracksStableState(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	now := time.Now().UTC()
	first, err := database.TouchSimCardCandidate(ctx, SimCardCandidate{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		HostType: DeviceTypeUSBSIMReader, FirstSeenAt: now, LastSeenAt: now,
	})
	if err != nil || first.State != SimCardCandidatePending {
		t.Fatalf("first candidate = %+v, %v", first, err)
	}
	second, err := database.TouchSimCardCandidate(ctx, SimCardCandidate{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		FirstSeenAt: now.Add(-5 * time.Second), LastSeenAt: now,
	})
	if err != nil || second.State != SimCardCandidateStable {
		t.Fatalf("stable candidate = %+v, %v", second, err)
	}
}

func TestCardPolicyDatabaseRejectsUnregisteredICCID(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	_, err := database.db.ExecContext(ctx, `
		INSERT INTO card_policies (
			iccid, network_enabled, vowifi_enabled, airplane_enabled,
			apn, ip_version, custom_phone_number, mbn_profile,
			cellular_ims_enabled, cellular_ims_managed,
			runtime_mode, cellular_ims_mode, source, created_at, updated_at
		) VALUES (?, 0, 0, 0, '', '', '', '', 0, 0, '', '', 'manual', 1, 1)
	`, testSimICCID)
	if err == nil {
		t.Fatal("orphan card policy insert succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not registered") {
		t.Fatalf("orphan card policy error = %v", err)
	}
}

func TestSimCardObservationUpsertDeduplicatesIdentity(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCID)

	firstSeen := time.Unix(100, 0).UTC()
	first, err := database.UpsertSimCardObservation(ctx, SimCardObservation{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		ProfileAID: "AID-1", FirstSeenAt: firstSeen, LastSeenAt: firstSeen,
	})
	if err != nil {
		t.Fatalf("UpsertSimCardObservation() error = %v", err)
	}
	lastSeen := firstSeen.Add(30 * time.Second)
	second, err := database.UpsertSimCardObservation(ctx, SimCardObservation{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		ProfileAID: "aid-1", LastSeenAt: lastSeen, LastEnabledAt: lastSeen,
	})
	if err != nil {
		t.Fatalf("UpsertSimCardObservation() update error = %v", err)
	}
	if first.ID != second.ID || !second.FirstSeenAt.Equal(firstSeen) ||
		!second.LastSeenAt.Equal(lastSeen) || !second.LastEnabledAt.Equal(lastSeen) {
		t.Fatalf("observation identity was not preserved: first=%+v second=%+v", first, second)
	}
	observations, err := database.ListSimCardObservations(ctx, testSimICCID)
	if err != nil || len(observations) != 1 {
		t.Fatalf("ListSimCardObservations() = %+v, %v", observations, err)
	}

	replacedHardware := lastSeen.Add(30 * time.Second)
	third, err := database.UpsertSimCardObservation(ctx, SimCardObservation{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-2",
		ProfileAID: "aid-1", FirstSeenAt: replacedHardware, LastSeenAt: replacedHardware,
	})
	if err != nil {
		t.Fatalf("UpsertSimCardObservation() hardware replacement error = %v", err)
	}
	if third.ID != second.ID || !third.FirstSeenAt.Equal(replacedHardware) {
		t.Fatalf("hardware replacement kept stale stability window: %+v", third)
	}
}

func TestTouchAndClearSimCardBindingAdvancesGeneration(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCID)

	if err := database.TouchSimCardBinding(ctx, SimCardBinding{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		ProfileAID: "AID-1", State: SimCardBindingPending,
	}); err != nil {
		t.Fatalf("pending TouchSimCardBinding() error = %v", err)
	}
	pending, err := database.SimCardBinding(ctx, testSimICCID)
	if err != nil || pending.State != SimCardBindingPending || pending.Generation != 1 {
		t.Fatalf("pending binding = %+v, %v", pending, err)
	}

	if err := database.TouchSimCardBinding(ctx, SimCardBinding{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		ProfileAID: "AID-1", State: SimCardBindingActive,
	}); err != nil {
		t.Fatalf("active TouchSimCardBinding() error = %v", err)
	}
	active, err := database.SimCardBinding(ctx, testSimICCID)
	if err != nil || active.State != SimCardBindingActive || active.Generation != 1 ||
		active.StableSince.IsZero() || active.ObservationID == 0 {
		t.Fatalf("active binding = %+v, %v", active, err)
	}

	if err := database.ClearSimCardBinding(ctx, testSimICCID, "modem-1"); err != nil {
		t.Fatalf("ClearSimCardBinding() error = %v", err)
	}
	offline, err := database.SimCardBinding(ctx, testSimICCID)
	if err != nil || offline.State != SimCardBindingOffline || offline.Generation != 2 ||
		offline.DeviceID != "" || offline.ObservationID != 0 || offline.ClearedAt.IsZero() {
		t.Fatalf("offline binding = %+v, %v", offline, err)
	}
	if _, err := database.SimCard(ctx, testSimICCID); err != nil {
		t.Fatalf("clearing binding removed SIM card: %v", err)
	}
}

func TestDeleteDeviceUnbindsCardsWithoutDeletingThem(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCID)
	mustSaveDevice(t, database, "modem-1", "Modem")
	if err := database.TouchSimCardBinding(ctx, SimCardBinding{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		State: SimCardBindingActive,
	}); err != nil {
		t.Fatalf("TouchSimCardBinding() error = %v", err)
	}

	if err := database.DeleteDevice(ctx, "modem-1"); err != nil {
		t.Fatalf("DeleteDevice() error = %v", err)
	}
	binding, err := database.SimCardBinding(ctx, testSimICCID)
	if err != nil || binding.State != SimCardBindingOffline || binding.DeviceID != "" {
		t.Fatalf("binding after device delete = %+v, %v", binding, err)
	}
	if _, err := database.SimCard(ctx, testSimICCID); err != nil {
		t.Fatalf("SIM card after device delete = %v", err)
	}
}

func TestDeleteSimCardRemovesOwnedConfiguration(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCID)
	mustSaveDevice(t, database, "modem-1", "Modem")
	if err := database.TouchSimCardBinding(ctx, SimCardBinding{
		ICCID: testSimICCID, DeviceID: "modem-1", PhysicalDeviceID: "physical-1",
		State: SimCardBindingActive,
	}); err != nil {
		t.Fatalf("TouchSimCardBinding() error = %v", err)
	}
	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCID, RuntimeMode: CardRuntimeModeQMI,
		CellularIMSMode: CellularIMSModeForceEnabled,
	}); err != nil {
		t.Fatalf("UpsertCardPolicy() error = %v", err)
	}
	if _, err := database.UpsertCardAPNProfile(ctx, CardAPNProfile{
		ICCID: testSimICCID, APN: "internet", IPVersion: "IPV4V6",
	}); err != nil {
		t.Fatalf("UpsertCardAPNProfile() error = %v", err)
	}
	task, err := database.SaveAutomaticTask(ctx, AutomaticTask{
		Name: "card task", Enabled: true, DeviceID: "modem-1",
		ProfileICCID: testSimICCID, TaskType: "sms", Environment: "vowifi",
		IntervalDays: 1, StartDate: "2026-01-01", RunTime: "00:00",
		Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("SaveAutomaticTask() error = %v", err)
	}
	if err := database.InsertCallRecord(CallRecord{
		DeviceID: "modem-1", ICCID: testSimICCID,
		Direction: "outgoing", Status: "answered", Transport: "vowifi",
	}); err != nil {
		t.Fatalf("InsertCallRecord() error = %v", err)
	}

	if err := database.DeleteSimCard(ctx, testSimICCID); err != nil {
		t.Fatalf("DeleteSimCard() error = %v", err)
	}
	if _, err := database.SimCard(ctx, testSimICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SimCard() after delete error = %v, want ErrNotFound", err)
	}
	if _, err := database.CardPolicy(ctx, testSimICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CardPolicy() after delete error = %v, want ErrNotFound", err)
	}
	if _, err := database.DeviceProxyBindingGroupByICCID(ctx, testSimICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proxy binding group after delete error = %v, want ErrNotFound", err)
	}
	if _, err := database.AutomaticTask(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("automatic task after card delete error = %v, want ErrNotFound", err)
	}
	records, _, err := database.ListCallHistory("modem-1", 10, 0)
	if err != nil || len(records) != 0 {
		t.Fatalf("call history after card delete = %+v, %v", records, err)
	}
}

func TestDeleteSimCardRemovesProxyBindingGroups(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCID)
	if err := database.UpsertUpstreamProxy(ctx, UpstreamProxy{
		ID: "proxy-a", Name: "proxy-a", Addr: "127.0.0.1:1080", Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error = %v", err)
	}
	if err := database.SaveDeviceProxyBindingGroup(ctx, DeviceProxyBindingGroup{
		ICCID: testSimICCID, ProfileName: "Offline card",
	}, []string{"proxy-a"}); err != nil {
		t.Fatalf("SaveDeviceProxyBindingGroup() error = %v", err)
	}
	if _, err := database.DeviceProxyBindingGroupByICCID(ctx, testSimICCID); err != nil {
		t.Fatalf("proxy binding group before delete error = %v", err)
	}

	if err := database.DeleteSimCard(ctx, testSimICCID); err != nil {
		t.Fatalf("DeleteSimCard() error = %v", err)
	}
	if _, err := database.DeviceProxyBindingGroupByICCID(ctx, testSimICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proxy binding group after delete error = %v, want ErrNotFound", err)
	}
}

func TestCardPolicyPersistsRuntimeAndCellularIMSMode(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCIDOther)
	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: "8944100000000000003",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan UpsertCardPolicy() error = %v, want ErrNotFound", err)
	}
	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCIDOther, RuntimeMode: " PCSC ",
		CellularIMSMode: "force_disabled",
	}); err != nil {
		t.Fatalf("UpsertCardPolicy() error = %v", err)
	}
	policy, err := database.CardPolicy(ctx, testSimICCIDOther)
	if err != nil || policy.RuntimeMode != CardRuntimeModePCSC ||
		policy.CellularIMSMode != CellularIMSModeForceDisabled {
		t.Fatalf("CardPolicy() = %+v, %v", policy, err)
	}
	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCIDOther, RuntimeMode: "serial",
	}); err == nil {
		t.Fatal("invalid runtime mode was accepted")
	}
	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCIDOther, CellularIMSMode: "automatic",
	}); err == nil {
		t.Fatal("invalid cellular IMS mode was accepted")
	}
}

func TestCardPolicyNormalizesConflictingRFModes(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustCreateSimCard(t, database, testSimICCID)
	mustCreateSimCard(t, database, testSimICCIDOther)

	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCID, NetworkEnabled: true, VoWiFiEnabled: true, AirplaneEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	policy, err := database.CardPolicy(ctx, testSimICCID)
	if err != nil || !policy.VoWiFiEnabled || policy.NetworkEnabled || !policy.AirplaneEnabled {
		t.Fatalf("VoWiFi priority policy = %+v, %v", policy, err)
	}

	if err := database.UpsertCardPolicy(ctx, CardPolicy{
		ICCID: testSimICCIDOther, NetworkEnabled: true, AirplaneEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	policy, err = database.CardPolicy(ctx, testSimICCIDOther)
	if err != nil || policy.VoWiFiEnabled || !policy.NetworkEnabled || policy.AirplaneEnabled {
		t.Fatalf("cellular policy = %+v, %v", policy, err)
	}
}

func TestUpdateDeviceCardRuntimeStalePreservesOtherExtra(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	mustSaveDevice(t, database, "modem-1", "Modem")
	device, err := database.Device(ctx, "modem-1")
	if err != nil {
		t.Fatal(err)
	}
	device.Extra = []byte(`{"hardware":"keep"}`)
	if err := database.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateDeviceCardRuntimeStale(ctx, "modem-1", CardRuntimeStaleState{
		Binding: true, DeviceBackend: true, CellularIMS: true,
	}); err != nil {
		t.Fatal(err)
	}
	device, err = database.Device(ctx, "modem-1")
	if err != nil || !strings.Contains(string(device.Extra), `"hardware":"keep"`) {
		t.Fatalf("extra data after stale update = %q, %v", device.Extra, err)
	}
	if !device.CardRuntimeStale().Binding {
		t.Fatalf("stale state not persisted: %+v", device.CardRuntimeStale())
	}
}

func mustCreateSimCard(t *testing.T, database *Store, iccid string) {
	t.Helper()
	if err := database.UpsertSimCard(context.Background(), SimCard{
		ICCID: iccid, CardType: SimCardTypePhysical,
	}); err != nil {
		t.Fatalf("UpsertSimCard(%q) error = %v", iccid, err)
	}
}
