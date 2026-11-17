package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

func TestAgentDevicesUseSavedConfigurationName(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{
		ID: "Reader-1", Name: "Renamed Reader", DeviceType: store.DeviceTypeUSBSIMReader,
		AgentID: "agent-1", RemoteDeviceID: "Reader-1",
	}); err != nil {
		t.Fatal(err)
	}
	entry := device.Device{
		ID: "Reader-1", Discovered: true,
		Candidate: modem.Candidate{ID: "Reader-1", HardwareKind: "pcsc", Product: "ESTKme-DOT"},
	}
	server := &Server{
		store: database, logger: regionTestLogger(), devices: fakeDeviceController{entry: entry},
		agentGRPC: discoveryAgentStub{entries: []device.Device{entry}},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agents/agent-1/devices", nil)
	server.handleAgentDevices(recorder, request, "agent-1")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Devices []map[string]any `json:"devices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Devices) != 1 || body.Data.Devices[0]["name"] != "Renamed Reader" {
		t.Fatalf("agent devices=%#v", body.Data.Devices)
	}
}

func TestResponsiveDeviceIgnoresStaleOperationError(t *testing.T) {
	entry := device.Device{
		ID: "reader", Discovered: true,
		Snapshot:  &device.Snapshot{Responsive: true},
		LastError: "temporary operation failed",
	}
	if got := lifecyclePhase(entry); got != "ready" {
		t.Fatalf("lifecycle=%q, want ready", got)
	}
	summary := deviceSummary(entry)
	if summary["healthy"] != true || summary["control_online"] != true {
		t.Fatalf("summary=%#v", summary)
	}

	entry.Snapshot.Responsive = false
	if got := lifecyclePhase(entry); got != "degraded" {
		t.Fatalf("unresponsive lifecycle=%q, want degraded", got)
	}
}

func TestFillConfigFromPhysicalClassifiesDJI4G(t *testing.T) {
	config := store.Device{DeviceType: store.DeviceTypePCIeEC20EC25}
	entry := device.Device{Candidate: modem.Candidate{
		VendorID:     "2ca3",
		ProductID:    "4006",
		SerialNumber: "USB-SERIAL-1",
	}}

	fillConfigFromPhysical(&config, entry)

	if config.DeviceType != store.DeviceTypeDJI4G {
		t.Fatalf("device type = %q, want %q", config.DeviceType, store.DeviceTypeDJI4G)
	}
	extra := string(config.Extra)
	for _, fragment := range []string{
		`"usb_serial_number":"USB-SERIAL-1"`,
		`"usb_vendor_id":"2ca3"`,
		`"usb_product_id":"4006"`,
	} {
		if !strings.Contains(extra, fragment) {
			t.Fatalf("hardware identity %q missing from %s", fragment, extra)
		}
	}
	if got := discoveredDeviceType(entry.Candidate); got != store.DeviceTypeDJI4G {
		t.Fatalf("discovered device type = %q, want %q", got, store.DeviceTypeDJI4G)
	}
}

// The verified ZXIC USB identity must be reclassified both when discovered and
// when an existing configuration adopts the physical device.
func TestFillConfigFromPhysicalClassifiesZXIC(t *testing.T) {
	config := store.Device{DeviceType: store.DeviceTypePCIeEC20EC25}
	entry := device.Device{Candidate: modem.Candidate{
		VendorID:  "19d2",
		ProductID: "0536",
	}}

	fillConfigFromPhysical(&config, entry)

	if config.DeviceType != store.DeviceTypeZXIC {
		t.Fatalf("DeviceType = %q, want %q", config.DeviceType, store.DeviceTypeZXIC)
	}
	if got := discoveredDeviceType(entry.Candidate); got != store.DeviceTypeZXIC {
		t.Fatalf("discoveredDeviceType = %q, want %q", got, store.DeviceTypeZXIC)
	}
}

// A sibling product under a known vendor does not inherit the verified ZXIC
// dialect without the exact hardware identity.
func TestFillConfigFromPhysicalIgnoresUnverifiedZxicProduct(t *testing.T) {
	config := store.Device{DeviceType: store.DeviceTypePCIeEC20EC25}
	entry := device.Device{Candidate: modem.Candidate{
		VendorID:  "19d2",
		ProductID: "ffff",
	}}

	fillConfigFromPhysical(&config, entry)

	if config.DeviceType == store.DeviceTypeZXIC {
		t.Fatalf("unverified product was classified as %q", config.DeviceType)
	}
	if got := discoveredDeviceType(entry.Candidate); got == store.DeviceTypeZXIC {
		t.Fatalf("unverified discovered product was classified as %q", got)
	}
}

func TestConfiguredDeviceSummaryIgnoresVoWiFiRuntimeFromPreviousSIM(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{ID: "ec20_1", Name: "EC20"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertVoWiFiRuntime(context.Background(), store.VoWiFiRuntime{
		DeviceID:          "ec20_1",
		Phase:             "stopping",
		ICCID:             "8944100000000000001",
		IMSI:              "234150000000001",
		TunnelReady:       true,
		IMSReady:          true,
		SMSReady:          true,
		LocalPhone:        "+447700900123",
		PhoneNumberSource: "ims_p_associated_uri",
		UpdatedAt:         time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: database}
	entry := &device.Device{ID: "physical", Snapshot: &device.Snapshot{
		ICCID: "89104100000000000001",
		IMSI:  "310380000000001",
	}}
	got := s.configuredDeviceSummary(store.Device{ID: "ec20_1"}, entry)
	if got["vowifi_active"] != false {
		t.Fatalf("vowifi_active = %#v", got["vowifi_active"])
	}
	if got["local_phone"] == "+447700900123" {
		t.Fatalf("old phone leaked into current SIM summary: %#v", got)
	}
	runtime, ok := got["vowifi_runtime"].(map[string]any)
	if !ok || runtime["phase"] != "idle" || runtime["iccid"] != "89104100000000000001" {
		t.Fatalf("runtime = %#v", got["vowifi_runtime"])
	}
}

func TestConfiguredDeviceSummaryPrefersLiveVoWiFiStateOverStoredShutdownState(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{ID: "ec20_1", Name: "EC20"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertVoWiFiRuntime(context.Background(), store.VoWiFiRuntime{
		DeviceID:   "ec20_1",
		Phase:      "idle",
		ICCID:      "89104100000000000001",
		LastReason: "disabled",
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	live := vowifi.State{
		DeviceID:    "ec20_1",
		Phase:       vowifi.PhaseTunnelReady,
		Enabled:     true,
		Active:      true,
		ICCID:       "89104100000000000001",
		SIMReady:    true,
		AccessReady: true,
		TunnelReady: true,
		LastReason:  "ipsec_tunnel_ready",
		UpdatedAt:   time.Now().UTC(),
	}
	s := &Server{store: database, vowifi: &fakeVoWiFiController{state: live}}
	entry := &device.Device{ID: "physical", Snapshot: &device.Snapshot{ICCID: live.ICCID}}
	got := s.configuredDeviceSummary(store.Device{ID: "ec20_1", VoWiFiEnabled: true}, entry)
	runtime, ok := got["vowifi_runtime"].(map[string]any)
	if !ok || runtime["phase"] != string(vowifi.PhaseTunnelReady) || runtime["enabled"] != true {
		t.Fatalf("runtime = %#v", got["vowifi_runtime"])
	}
	if got["vowifi_active"] != true {
		t.Fatalf("vowifi_active = %#v", got["vowifi_active"])
	}
}

func TestConfiguredDeviceSummaryMarksIdleRuntimeAsNotInUse(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := &Server{
		store: database,
		vowifi: &fakeVoWiFiController{state: vowifi.State{
			DeviceID:   "ec20_1",
			Phase:      vowifi.PhaseIdle,
			Enabled:    false,
			LastReason: "disabled",
			UpdatedAt:  time.Now().UTC(),
		}},
	}
	got := s.configuredDeviceSummary(store.Device{ID: "ec20_1", VoWiFiEnabled: true}, nil)
	runtime := got["vowifi_runtime"].(map[string]any)
	if runtime["enabled"] != false || got["vowifi_active"] != false {
		t.Fatalf("summary = %#v", got)
	}
}

func TestConfiguredDeviceOverviewAlwaysUsesLiveDiscoveredATPort(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := &Server{store: database}
	config := store.Device{ID: "ec20_1", ATPort: "/dev/ttyUSB9"}
	entry := device.Device{Candidate: modem.Candidate{
		ATPort: modem.Port{Path: "/dev/ttyUSB2", Role: modem.PortRoleAT},
	}}

	connected := s.configuredDeviceOverview(config, entry, true)
	if got := connected["at_port"]; got != "/dev/ttyUSB2" {
		t.Fatalf("connected AT port = %#v, want live /dev/ttyUSB2", got)
	}

	offline := s.configuredDeviceOverview(config, entry, false)
	if got := offline["at_port"]; got != "" {
		t.Fatalf("offline AT port = %#v, want empty instead of stored port", got)
	}
}

func TestDeviceSummaryCellLockSupport(t *testing.T) {
	for _, test := range []struct {
		name      string
		candidate modem.Candidate
		supported bool
	}{
		{"ML307", modem.Candidate{Product: "ML307A"}, true},
		{"Quectel", modem.Candidate{VendorID: "2c7c"}, true},
		{"DJI EC25", modem.Candidate{VendorID: "2ca3", ProductID: "4006"}, true},
		{"unknown", modem.Candidate{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := deviceSummary(device.Device{Candidate: test.candidate})
			if got := summary["supports_cell_lock"]; got != test.supported {
				t.Fatalf("supports_cell_lock = %v, want %v", got, test.supported)
			}
		})
	}
}

func TestSnapshotHasSIMDoesNotTreatUnknownStatusAsInserted(t *testing.T) {
	for _, snapshot := range []*device.Snapshot{
		{IMEI: "867123456789012"},
		{IMEI: "867123456789012", SIMStatus: "unknown"},
		{IMEI: "867123456789012", SIMStatus: "not_inserted"},
	} {
		if snapshotHasSIM(snapshot) {
			t.Fatalf("snapshot was reported with a SIM: %#v", snapshot)
		}
	}
	for _, snapshot := range []*device.Snapshot{
		{SIMStatus: "pin_required"},
		{ICCID: "8944100000000000001"},
		{SIMReady: true},
	} {
		if !snapshotHasSIM(snapshot) {
			t.Fatalf("snapshot was reported without a SIM: %#v", snapshot)
		}
	}
}
