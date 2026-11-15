package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

func TestParseCLCC(t *testing.T) {
	calls := parseCLCC(modem.Response{Lines: []string{
		`+CLCC: 1,1,4,0,0,"+447700900000",145`,
		`+CLCC: 2,0,0,0,0,"12345",129`,
	}})
	if len(calls) != 2 || calls[0]["number"] != "+447700900000" || calls[1]["state"] != 0 {
		t.Fatalf("parseCLCC = %#v", calls)
	}
}

func TestValidDialNumber(t *testing.T) {
	for _, value := range []string{"+447700900000", "12345", "*100#"} {
		if !validDialNumber(value) {
			t.Errorf("validDialNumber(%q) = false", value)
		}
	}
	for _, value := range []string{"", "+", "12;ATH", "12 34", "abc"} {
		if validDialNumber(value) {
			t.Errorf("validDialNumber(%q) = true", value)
		}
	}
}

func TestCallTransportRequiresIMSReady(t *testing.T) {
	controller := &fakeVoWiFiController{state: vowifi.State{Enabled: true}}
	server := &Server{vowifi: controller}
	if got := server.callTransport("ec20"); got != "cellular" {
		t.Fatalf("callTransport before IMS registration = %q, want cellular", got)
	}
	controller.state.IMSReady = true
	if got := server.callTransport("ec20"); got != "vowifi" {
		t.Fatalf("callTransport with IMS ready = %q, want vowifi", got)
	}
}

func TestCallStopMatchesOnlyExpectedICCID(t *testing.T) {
	server := &Server{devices: fakeDeviceController{entry: device.Device{
		ID:         "dev1",
		Discovered: true,
		Snapshot:   &device.Snapshot{Responsive: true, SIMReady: true, ICCID: testICCIDCurrent},
	}}}
	config := store.Device{ID: "dev1"}
	if !server.callStopMatchesICCID(config, testICCIDCurrent) {
		t.Fatal("current ICCID was rejected for stop-only call action")
	}
	if server.callStopMatchesICCID(config, testICCIDOld) {
		t.Fatal("stale ICCID was accepted for stop-only call action")
	}
}

func TestResolveVoWiFiCallIDIgnoresTerminalCalls(t *testing.T) {
	controller := &fakeCallController{calls: []vowifi.Call{
		{ID: "failed", State: "failed"},
		{ID: "active", State: "active"},
	}}
	got, err := resolveVoWiFiCallID(controller, "ec20", "", "")
	if err != nil || got != "active" {
		t.Fatalf("resolveVoWiFiCallID() = %q, %v; want active", got, err)
	}
}

func TestCallHistoryAcceptsFrontendRecordPost(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server := &Server{store: database, maxRequestBodyBytes: 4096}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/devices/ec20/calls/history",
		bytes.NewBufferString(`{"number":"+447700900001","direction":"incoming","status":"answered","iccid":"89441000000000000001"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	if !server.handleCallHistory(response, request, store.Device{ID: "ec20"}) {
		t.Fatal("call history POST was not handled")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	records, total, err := database.ListCallHistoryForCard("ec20", "89441000000000000001", 10, 0)
	if err != nil || total != 1 || len(records) != 1 {
		t.Fatalf("history = %#v total=%d err=%v", records, total, err)
	}
	if records[0].Direction != "incoming" || records[0].Status != "answered" || records[0].ICCID != "89441000000000000001" {
		t.Fatalf("record = %#v", records[0])
	}
}

func TestCallHistoryReadDeleteRequireRegisteredICCID(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{ID: "dev1", Name: "Modem"}); err != nil {
		t.Fatal(err)
	}
	registerActiveTestCard(t, database, "dev1", "dev1", testICCIDCurrent)
	if err := database.InsertCallRecord(store.CallRecord{
		DeviceID: "dev1", ICCID: testICCIDCurrent, Number: "+447700900002",
		Direction: "incoming", Status: "missed", StartedAt: 100,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		store: database,
		devices: fakeDeviceController{entry: device.Device{
			ID:         "dev1",
			Discovered: true,
			Snapshot:   &device.Snapshot{Responsive: true, SIMReady: true, ICCID: testICCIDCurrent},
		}},
		maxRequestBodyBytes: 4096,
	}

	missing := httptest.NewRequest(http.MethodGet, "/api/devices/dev1/calls/history", nil)
	missingResponse := httptest.NewRecorder()
	if !server.handleCallHistory(missingResponse, missing, store.Device{ID: "dev1"}) ||
		missingResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing ICCID read = %d %s", missingResponse.Code, missingResponse.Body.String())
	}

	read := httptest.NewRequest(http.MethodGet, "/api/devices/dev1/calls/history?iccid="+testICCIDCurrent, nil)
	readResponse := httptest.NewRecorder()
	if !server.handleCallHistory(readResponse, read, store.Device{ID: "dev1"}) || readResponse.Code != http.StatusOK {
		t.Fatalf("registered ICCID read = %d %s", readResponse.Code, readResponse.Body.String())
	}

	stale := httptest.NewRequest(http.MethodDelete, "/api/devices/dev1/calls/history/delete?iccid="+testICCIDOld, nil)
	staleResponse := httptest.NewRecorder()
	if !server.handleDeleteCallRecord(staleResponse, stale, store.Device{ID: "dev1"}) ||
		staleResponse.Code < 400 {
		t.Fatalf("stale ICCID delete = %d %s", staleResponse.Code, staleResponse.Body.String())
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/devices/dev1/calls/history/delete?iccid="+testICCIDCurrent, nil)
	removeResponse := httptest.NewRecorder()
	if !server.handleDeleteCallRecord(removeResponse, remove, store.Device{ID: "dev1"}) ||
		removeResponse.Code != http.StatusOK {
		t.Fatalf("registered ICCID delete = %d %s", removeResponse.Code, removeResponse.Body.String())
	}
}

type fakeCallController struct {
	calls []vowifi.Call
}

func (controller *fakeCallController) Calls(string) ([]vowifi.Call, error) {
	return controller.calls, nil
}

func (*fakeCallController) DialCall(context.Context, string, string) (vowifi.Call, error) {
	return vowifi.Call{}, nil
}

func (*fakeCallController) AnswerCall(context.Context, string, string) (vowifi.Call, error) {
	return vowifi.Call{}, nil
}

func (*fakeCallController) HangupCall(context.Context, string, string) error { return nil }

func (*fakeCallController) SendDTMF(context.Context, string, string, string) error { return nil }
