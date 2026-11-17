package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vonet/internal/agentproto"
	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/store"
)

type discoverySnapshotController struct {
	fakeDeviceController
	entries       []device.Device
	discoverCalls int
}

type discoveryAgentStub struct {
	entries []device.Device
}

func (stub discoveryAgentStub) ServeHTTP(http.ResponseWriter, *http.Request) {}
func (stub discoveryAgentStub) Devices(string) []json.RawMessage {
	values := make([]json.RawMessage, 0, len(stub.entries))
	for _, entry := range stub.entries {
		raw, _ := json.Marshal(entry)
		values = append(values, raw)
	}
	return values
}
func (stub discoveryAgentStub) Execute(context.Context, string, *agentproto.AgentCommand) (*agentproto.AgentEvent, error) {
	return nil, errors.New("unexpected command")
}

type addRecoveryController struct {
	fakeDeviceController
	flightErr error
}

func (controller *addRecoveryController) SetFlight(context.Context, string, bool) (device.FlightResult, error) {
	return device.FlightResult{}, controller.flightErr
}

func (controller *discoverySnapshotController) Discover(context.Context) ([]device.Device, error) {
	controller.discoverCalls++
	return append([]device.Device(nil), controller.entries...), nil
}

func TestDiscoveredDevicesUsesAgentSnapshotAndOmitsAbsentEntries(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	agent := discoveryAgentStub{entries: []device.Device{
		{
			ID: "current-device", Discovered: true,
			Candidate: modem.Candidate{ID: "current-device", USBPath: "2-1"},
		},
		{
			ID: "ml307-device", Discovered: true,
			Candidate: modem.Candidate{ID: "ml307-device", VendorID: "2ecc", ProductID: "3012", Product: "ML307A", USBPath: "3-2"},
		},
		{
			ID: "absent-device", Discovered: false,
			Candidate: modem.Candidate{ID: "absent-device", USBPath: "3-1"},
		},
	}}
	server := &Server{
		store: database, logger: regionTestLogger(),
		maxRequestBodyBytes: 4096, agentGRPC: agent,
	}
	request := httptest.NewRequest(http.MethodGet, "/api/devices/discovered?agent_id=agent-1", nil)
	recorder := httptest.NewRecorder()

	if !server.handleDiscoveredDevices(recorder, request) {
		t.Fatal("handleDiscoveredDevices returned false")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "current-device") {
		t.Fatalf("response omits current device: %s", body)
	}
	if !strings.Contains(body, `"device_type":"ml307"`) {
		t.Fatalf("response does not classify ML307: %s", body)
	}
	if strings.Contains(body, "stale-device") || strings.Contains(body, "absent-device") {
		t.Fatalf("response contains an absent device: %s", body)
	}
}

func TestDiscoveredDevicesReturnsOnlyUnconfiguredAgentDevices(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{
		ID: "configured-reader", Name: "Configured Reader", DeviceType: store.DeviceTypeUSBSIMReader,
		AgentID: "agent-1", RemoteDeviceID: "Reader-configured",
	}); err != nil {
		t.Fatal(err)
	}

	agent := discoveryAgentStub{entries: []device.Device{
		{
			ID: "Reader-configured", Discovered: true,
			Candidate: modem.Candidate{ID: "Reader-configured", HardwareKind: "pcsc", ReaderName: "configured", USBPath: "3-1"},
		},
		{
			ID: "Reader-unconfigured", Discovered: true,
			Candidate: modem.Candidate{ID: "Reader-unconfigured", HardwareKind: "pcsc", ReaderName: "unconfigured", USBPath: "3-2"},
		},
	}}
	server := &Server{
		store: database, logger: regionTestLogger(),
		maxRequestBodyBytes: 4096, agentGRPC: agent,
	}
	request := httptest.NewRequest(http.MethodGet, "/api/devices/discovered?agent_id=agent-1", nil)
	recorder := httptest.NewRecorder()

	if !server.handleDiscoveredDevices(recorder, request) {
		t.Fatal("handleDiscoveredDevices returned false")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Reader-unconfigured") {
		t.Fatalf("response omits unconfigured device: %s", body)
	}
	if strings.Contains(body, "Reader-configured") {
		t.Fatalf("response contains configured device: %s", body)
	}
}

func TestAgentDeviceConfigAcceptsConfigurationIDAndRemoteID(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	config := store.Device{
		ID: "configured-reader", Name: "Configured Reader", DeviceType: store.DeviceTypeUSBSIMReader,
		AgentID: "agent-1", RemoteDeviceID: "Reader-remote",
	}
	if err := database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	agent := discoveryAgentStub{entries: []device.Device{{
		ID: "Reader-remote", Discovered: true,
		Candidate: modem.Candidate{ID: "Reader-remote", HardwareKind: "pcsc", ReaderName: "remote", USBPath: "3-1"},
	}}}
	server := &Server{
		store: database, logger: regionTestLogger(),
		maxRequestBodyBytes: 4096, agentGRPC: agent,
	}

	for _, id := range []string{"configured-reader", "Reader-remote"} {
		request := httptest.NewRequest(http.MethodGet, "/api/devices/"+id+"/config?agent_id=agent-1", nil)
		recorder := httptest.NewRecorder()
		if !server.handleDevicePath(recorder, request, id, []string{"config"}) {
			t.Fatalf("handleDevicePath(%q) returned false", id)
		}
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"configured-reader"`) {
			t.Fatalf("config lookup %q: code=%d body=%s", id, recorder.Code, recorder.Body.String())
		}
	}
}

func TestSuggestedPCSCDeviceIDFallsBackToReaderName(t *testing.T) {
	got := suggestedDeviceID(modem.Candidate{
		HardwareKind: "pcsc", ReaderName: "48FB4F9C3632",
	}, nil)
	if got != "Reader-48FB4F9C3632" {
		t.Fatalf("suggested ID=%q", got)
	}
}

func TestAddDevicePersistsConfigWhenInitialAirplaneModeIsTemporarilyUnavailable(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	entry := device.Device{
		ID: "usb-2c7c-0125-2-2", Discovered: true,
		Candidate: modem.Candidate{
			ID: "usb-2c7c-0125-2-2", VendorID: "2c7c", ProductID: "0125",
			Product: "Quectel EC20 / EC25", USBPath: "/sys/bus/usb/devices/2-2",
			ATPort:     modem.Port{Path: "/dev/ttyUSB2", Name: "ttyUSB2", Role: modem.PortRoleAT},
			QMIControl: "/dev/cdc-wdm0", NetworkInterface: "wwan0",
		},
	}
	controller := &addRecoveryController{
		fakeDeviceController: fakeDeviceController{entry: entry},
		flightErr:            errors.New("AT port is temporarily busy"),
	}
	server := &Server{
		store: database, logger: regionTestLogger(),
		maxRequestBodyBytes: 4096, devices: controller,
	}
	body := `{"config":{"id":"ec20","name":"EC20","device_type":"pcie_ec20_ec25","usb_path":"/sys/bus/usb/devices/2-2","at_port":"/dev/ttyUSB2","control_device":"/dev/cdc-wdm0","interface":"wwan0","device_backend":"qmi","esim_transport":"qmi"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	if !server.handleDevices(recorder, request) {
		t.Fatal("handleDevices returned false")
	}
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "saved") || !strings.Contains(recorder.Body.String(), "retry") {
		t.Fatalf("response lacks recovery warning: %s", recorder.Body.String())
	}
	config, err := database.Device(context.Background(), "ec20")
	if err != nil {
		t.Fatalf("saved device missing: %v", err)
	}
	if config.NetworkEnabled || config.VoWiFiEnabled || config.USBPath != entry.Candidate.USBPath {
		t.Fatalf("saved safe config = %+v", config)
	}
}
