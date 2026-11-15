package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vonet/internal/device"
	"vonet/internal/loghub"
	"vonet/internal/store"
)

func TestApplySimCardPatchAllowsClearingIdentityFields(t *testing.T) {
	updated, err := applySimCardPatch(store.SimCard{
		ICCID:       "8944100000000000001",
		DisplayName: "Old name",
		Notes:       "Old notes",
		IMSI:        "234120000000001",
		MSISDN:      "+447700900001",
		Carrier:     "Old carrier",
	}, simCardPatchPayload{
		DisplayName: stringPointer(""),
		Notes:       stringPointer(""),
		IMSI:        stringPointer(""),
		MSISDN:      stringPointer(""),
		Carrier:     stringPointer(""),
	})
	if err != nil {
		t.Fatalf("applySimCardPatch() error = %v", err)
	}
	if updated.DisplayName != "" || updated.Notes != "" || updated.IMSI != "" ||
		updated.MSISDN != "" || updated.Carrier != "" {
		t.Fatalf("clearable fields were not cleared: %+v", updated)
	}
}

func TestLogUserOperationRedactsICCIDPath(t *testing.T) {
	hub := loghub.New(slog.NewTextHandler(io.Discard, nil), 100)
	server := &Server{logger: slog.New(hub)}
	const iccid = "8944100000000000001"
	handler := server.logUserOperation(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/api/cards/"+iccid+"/policy", nil),
	)

	entries := hub.History(10, slog.LevelInfo, "")
	if len(entries) != 1 {
		t.Fatalf("log entry count = %d, want 1", len(entries))
	}
	operation, _ := entries[0].Fields["operation"].(string)
	if strings.Contains(operation, iccid) || !strings.Contains(operation, "REDACTED") {
		t.Fatalf("operation was not redacted: %q", operation)
	}
}

type multiEuiccVerificationController struct {
	fakeDeviceController
	targetState int
}

type multiCardDeviceController struct {
	fakeDeviceController
	entries []device.Device
}

func (controller multiCardDeviceController) List() []device.Device {
	return controller.entries
}

func (controller multiEuiccVerificationController) Refresh(context.Context, string) (device.Snapshot, error) {
	return device.Snapshot{
		Responsive: true,
		SIMReady:   true,
		ICCID:      "8944100000000000002",
	}, nil
}

func (controller multiEuiccVerificationController) ESIMInventory(context.Context, string) ([]device.EsimInventoryEntry, error) {
	return []device.EsimInventoryEntry{
		{Info: device.EsimInfo{Profiles: []device.EsimProfile{{
			ICCID: "8944100000000000001", AID: "PROFILE-1", State: 1,
		}}}},
		{Info: device.EsimInfo{
			AID: "ISD-R-2",
			Profiles: []device.EsimProfile{{
				ICCID: "8944100000000000002", AID: "PROFILE-2", State: controller.targetState,
			}},
		}},
	}, nil
}

func TestVerifyActiveESIMProfileUsesMatchingInventoryEntry(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.UpsertSimCard(context.Background(), store.SimCard{
		ICCID:      "8944100000000000002",
		CardType:   store.SimCardTypeESIM,
		ProfileAID: "PROFILE-2",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database, devices: multiEuiccVerificationController{targetState: 1}}
	if err := server.verifyActiveESIMProfile(
		context.Background(),
		"physical-1",
		"8944100000000000002",
		"PROFILE-2",
	); err != nil {
		t.Fatalf("verifyActiveESIMProfile() error = %v", err)
	}

	server.devices = multiEuiccVerificationController{targetState: 0}
	if err := server.verifyActiveESIMProfile(
		context.Background(),
		"physical-1",
		"8944100000000000002",
		"PROFILE-2",
	); err == nil {
		t.Fatal("disabled target profile was accepted")
	}
}

func TestCardPolicyStatusAcceptsUnspecifiedRuntimeMode(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.UpsertDevice(context.Background(), store.Device{
		ID: "modem-1", Name: "Modem", DeviceType: store.DeviceTypePCIeEC20EC25,
		DeviceBackend: "at",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database}
	status := server.cardPolicyStatus(store.CardPolicy{
		ICCID: "8944100000000000001",
	}, []store.SimCardBinding{{
		ICCID:    "8944100000000000001",
		DeviceID: "modem-1",
		State:    store.SimCardBindingActive,
	}})
	if status != "applied" {
		t.Fatalf("policy status = %q, want applied", status)
	}
}

func TestCardPolicyReaderConstraintSurvivesOfflineBinding(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mustCreateTestSimCard(t, database, "8944100000000000001")
	if err := database.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID: "8944100000000000001", RuntimeMode: store.CardRuntimeModePCSC,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database}
	if !server.cardPolicyIsReader(context.Background(), "8944100000000000001") {
		t.Fatal("reader policy constraint was lost without a current binding")
	}
}

func TestProxyBindingRejectsLiveICCIDConflict(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const iccid = "8944100000000000001"
	mustCreateTestSimCard(t, database, iccid)
	snapshot := device.Snapshot{Responsive: true, SIMReady: true, ICCID: iccid}
	server := &Server{
		store: database,
		devices: multiCardDeviceController{entries: []device.Device{
			{ID: "dev-1", Discovered: true, Snapshot: &snapshot},
			{ID: "dev-2", Discovered: true, Snapshot: &snapshot},
		}},
	}
	if _, err := server.proxyBindingDeviceIDForSave(
		context.Background(), "", iccid, true,
	); err == nil {
		t.Fatal("proxy binding accepted a live ICCID conflict")
	}
}

func TestDeviceConfigSavePreservesCardRuntimeStale(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	config := store.Device{
		ID: "dev1", Name: "dev1", DeviceType: store.DeviceTypePCIeEC20EC25,
		DeviceBackend: "at", ESIMTransport: "at", BaudRate: 115200,
		DataBits: 8, StopBits: 1, Parity: "none",
	}
	if err := config.SetCardRuntimeStale(store.CardRuntimeStaleState{
		Binding: true, DeviceBackend: true, CellularIMS: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		store: database, logger: regionTestLogger(), maxRequestBodyBytes: 4096,
		devices: fakeDeviceController{},
	}
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/devices/dev1",
		strings.NewReader(`{"config":{"id":"dev1","name":"renamed","device_type":"pcie_ec20_ec25","device_backend":"at","esim_transport":"at","baud_rate":115200,"data_bits":8,"stop_bits":1,"parity":"none","qmi_use_proxy":false,"sms_enabled":true,"network_enabled":false,"vowifi_enabled":false}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	if !server.handleDevicePath(recorder, request, "dev1", nil) {
		t.Fatal("handleDevicePath did not handle the request")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("config save status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	saved, err := database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	stale := saved.CardRuntimeStale()
	if !stale.Binding || !stale.DeviceBackend || !stale.CellularIMS {
		t.Fatalf("config save cleared stale state: %+v", stale)
	}
}

func mustCreateTestSimCard(t *testing.T, database *store.Store, iccid string) {
	t.Helper()
	if err := database.UpsertSimCard(context.Background(), store.SimCard{ICCID: iccid}); err != nil {
		t.Fatal(err)
	}
}

func stringPointer(value string) *string {
	return &value
}
