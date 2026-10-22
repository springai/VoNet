package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

func pcscServerTestEntry(serial string) device.Device {
	return device.Device{
		ID:         "discovery-" + serial,
		Discovered: true,
		Candidate: modem.Candidate{
			ID:           "discovery-" + serial,
			HardwareKind: pcsc.HardwareKind,
			SerialNumber: serial,
			USBPath:      "usb-" + serial,
			ReaderName:   "Reader " + serial,
		},
	}
}

func pcscServerTestRequest(t *testing.T, config map[string]any) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"config": config})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestPCSCDeviceCreateUsesTransactionalLease(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	entry := pcscServerTestEntry("SER-CREATE")
	controller := &discoverySnapshotController{
		fakeDeviceController: fakeDeviceController{entry: entry},
		entries:              []device.Device{entry},
	}
	server := &Server{
		store: database, devices: controller,
		logger: regionTestLogger(), maxRequestBodyBytes: 64 << 10,
	}
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fixedIMEI := pool[0].IMEI
	request := pcscServerTestRequest(t, map[string]any{
		"id":              "ignored-by-serial",
		"name":            "Reader create",
		"device_type":     "usb_sim_reader",
		"control_device":  "SER-CREATE",
		"usb_path":        "usb-SER-CREATE",
		"device_backend":  "pcsc",
		"esim_transport":  "pcsc",
		"pcsc_imei_mode":  "fixed",
		"pcsc_imei":       fixedIMEI,
		"network_enabled": false,
		"sms_enabled":     true,
		"vowifi_enabled":  true,
	})
	recorder := httptest.NewRecorder()
	if !server.handleDevices(recorder, request) {
		t.Fatal("handleDevices returned false")
	}
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	config, err := database.Device(ctx, "Reader-SER-CREATE")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := database.PCSCIMEILease(ctx, config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mode != "fixed" || lease.IMEI != fixedIMEI || config.ModemIMEI != "" {
		t.Fatalf("created config = %+v, lease = %+v", config, lease)
	}
	if !strings.Contains(recorder.Body.String(), `"pcsc_imei":"`+fixedIMEI+`"`) {
		t.Fatalf("create response lacks lease: %s", recorder.Body.String())
	}
}

func TestPCSCDeviceAddRejectsPhysicalTypeMismatchWithConflict(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	modemEntry := device.Device{
		ID:         "discovery-modem",
		Discovered: true,
		Candidate: modem.Candidate{
			ID:        "discovery-modem",
			USBPath:   "usb-modem",
			ATPort:    modem.Port{Path: "/dev/ttyUSB2", Name: "ttyUSB2", Role: modem.PortRoleAT},
			Product:   "Quectel EC20",
			VendorID:  "2c7c",
			ProductID: "0125",
		},
	}
	readerEntry := pcscServerTestEntry("SER-MISMATCH")

	for _, test := range []struct {
		name   string
		entry  device.Device
		config map[string]any
	}{
		{
			name:  "reader requested for modem hardware",
			entry: modemEntry,
			config: map[string]any{
				"id": "Reader-request", "name": "Reader", "device_type": "usb_sim_reader",
				"control_device": "/dev/ttyUSB2", "usb_path": "usb-modem",
				"device_backend": "pcsc", "esim_transport": "pcsc",
			},
		},
		{
			name:  "non-reader requested for PCSC hardware",
			entry: readerEntry,
			config: map[string]any{
				"id": "Reader-SER-MISMATCH", "name": "Modem", "device_type": "pcie_ec20_ec25",
				"control_device": "SER-MISMATCH", "usb_path": "usb-SER-MISMATCH",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := &discoverySnapshotController{
				fakeDeviceController: fakeDeviceController{entry: test.entry},
				entries:              []device.Device{test.entry},
			}
			server := &Server{
				store: database, devices: controller,
				logger: regionTestLogger(), maxRequestBodyBytes: 64 << 10,
			}
			recorder := httptest.NewRecorder()
			if !server.handleDevices(recorder, pcscServerTestRequest(t, test.config)) {
				t.Fatal("handleDevices returned false")
			}
			if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "immutable_reader_device_type") {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestPCSCIMEIEditReconnectsAndKeepsRandomModeStableDuringSave(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	entry := pcscServerTestEntry("SER-EDIT")
	controller := &discoverySnapshotController{
		fakeDeviceController: fakeDeviceController{entry: entry},
		entries:              []device.Device{entry},
	}
	voWiFi := &fakeVoWiFiController{state: vowifi.State{DeviceID: "Reader-SER-EDIT", Enabled: true}}
	server := &Server{
		store: database, devices: controller, vowifi: voWiFi,
		logger: regionTestLogger(), maxRequestBodyBytes: 64 << 10,
	}
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) < 2 {
		t.Fatal("default pool should contain at least two values")
	}
	if _, _, err := database.CreateDeviceWithPCSCIMEILease(ctx, store.Device{
		ID: "Reader-SER-EDIT", Name: "Reader edit", DeviceType: store.DeviceTypeUSBSIMReader,
		ControlDevice: "SER-EDIT", USBPath: "usb-SER-EDIT", DeviceBackend: "pcsc", ESIMTransport: "pcsc",
		SMSEnabled: true, VoWiFiEnabled: true,
	}, store.PCSCIMEILeaseRequest{Mode: "fixed", IMEI: pool[0].IMEI}); err != nil {
		t.Fatal(err)
	}

	sendEdit := func(mode, imei string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{"config": map[string]any{
			"id": "Reader-SER-EDIT", "name": "Reader edit", "device_type": "usb_sim_reader",
			"control_device": "SER-EDIT", "usb_path": "usb-SER-EDIT",
			"device_backend": "pcsc", "esim_transport": "pcsc",
			"network_enabled": false, "sms_enabled": true, "vowifi_enabled": true,
			"pcsc_imei_mode": mode, "pcsc_imei": imei,
		}})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/api/devices/Reader-SER-EDIT", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		if !server.handleDevicePath(recorder, request, "Reader-SER-EDIT", nil) {
			t.Fatal("handleDevicePath returned false")
		}
		return recorder
	}

	recorder := sendEdit("fixed", pool[1].IMEI)
	if recorder.Code != http.StatusOK || voWiFi.reconnects != 1 {
		t.Fatalf("fixed edit status = %d, body = %s, reconnects = %d", recorder.Code, recorder.Body.String(), voWiFi.reconnects)
	}
	lease, err := database.PCSCIMEILease(ctx, "Reader-SER-EDIT")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mode != "fixed" || lease.IMEI != pool[1].IMEI {
		t.Fatalf("fixed lease = %+v", lease)
	}

	recorder = sendEdit("random", "")
	if recorder.Code != http.StatusOK || voWiFi.reconnects != 2 {
		t.Fatalf("random edit status = %d, body = %s, reconnects = %d", recorder.Code, recorder.Body.String(), voWiFi.reconnects)
	}
	lease, err = database.PCSCIMEILease(ctx, "Reader-SER-EDIT")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mode != "random" || lease.IMEI != pool[1].IMEI {
		t.Fatalf("random lease should retain current value during save: %+v", lease)
	}
}

func TestPCSCIMEIPoolInvalidValueReturnsBadRequest(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server := &Server{store: database, logger: regionTestLogger(), maxRequestBodyBytes: 4096}
	request := httptest.NewRequest(http.MethodPost, "/api/pcsc-imei-pool", strings.NewReader(`{"imei":"123"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handlePCSCIMEIPool(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_imei") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestPCSCDeviceRejectsCellLockCapabilities(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "Reader-SER-CELLS", Name: "Reader", DeviceType: store.DeviceTypeUSBSIMReader,
		ControlDevice: "SER-CELLS", USBPath: "usb-SER-CELLS",
		DeviceBackend: "pcsc", ESIMTransport: "pcsc",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		store:   database,
		devices: fakeDeviceController{entry: pcscServerTestEntry("SER-CELLS")},
		logger:  regionTestLogger(), maxRequestBodyBytes: 4096,
	}
	for _, capability := range []string{"cells", "cell-lock"} {
		t.Run(capability, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/devices/Reader-SER-CELLS/"+capability, nil)
			if !server.handleDevicePath(recorder, request, "Reader-SER-CELLS", []string{capability}) {
				t.Fatal("handleDevicePath returned false")
			}
			if recorder.Code != http.StatusConflict ||
				!strings.Contains(recorder.Body.String(), "wifi_calling_only_device") {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestDeviceTypeTransitionReturnsConflict(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "Reader-SER-TYPE", Name: "Reader", DeviceType: store.DeviceTypeUSBSIMReader,
		DeviceBackend: "pcsc", ESIMTransport: "pcsc",
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database, logger: regionTestLogger(), maxRequestBodyBytes: 4096}
	body := `{"config":{"id":"Reader-CHANGED","name":"Reader","device_type":"pcie_ec20_ec25","device_backend":"at","esim_transport":"at"}}`
	request := httptest.NewRequest(http.MethodPut, "/api/devices/Reader-SER-TYPE", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	if !server.handleDevicePath(recorder, request, "Reader-SER-TYPE", nil) {
		t.Fatal("handleDevicePath returned false")
	}
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "immutable_reader_device_type") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
