package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"vonet/internal/device"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

const testProfileICCID = "8944100000000000001"

func newProfileBindingTestServer(t *testing.T) (*Server, *store.Store, *fakeVoWiFiController) {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{ID: "ec20", Name: "EC20", VoWiFiEnabled: true}); err != nil {
		t.Fatal(err)
	}
	registerActiveTestCard(t, database, "ec20", "ec20", testProfileICCID)
	registerActiveTestCard(t, database, "ec20", "ec20", "89104100000000000001")
	for _, upstream := range []store.UpstreamProxy{
		{ID: "route-1", Name: "Route 1", Addr: "127.0.0.1:1080", Enabled: true},
		{ID: "route-2", Name: "Route 2", Addr: "127.0.0.1:1081", Enabled: true},
	} {
		if err := database.UpsertUpstreamProxy(context.Background(), upstream); err != nil {
			t.Fatal(err)
		}
	}
	controller := &fakeVoWiFiController{state: vowifi.State{DeviceID: "ec20", ICCID: testProfileICCID, Enabled: true}}
	return &Server{
		store: database,
		devices: fakeDeviceController{entry: device.Device{
			ID: "ec20", Discovered: true,
			Snapshot: &device.Snapshot{DeviceID: "ec20", Responsive: true, SIMReady: true, ICCID: testProfileICCID},
		}},
		vowifi: controller, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), maxRequestBodyBytes: 16 << 10,
	}, database, controller
}

func profileBindingRequest(t *testing.T, server *Server, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/upstream-proxy-profile-bindings", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleProfileProxyBindings(response, request)
	return response
}

func proxyBindingGroupRoutesRequest(t *testing.T, server *Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/proxy-binding-groups/"+id+"/routes",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleProxyBindingGroupRoutes(response, request, id)
	return response
}

func proxyBindingGroupCreateRequest(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/proxy-binding-groups",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleProxyBindingGroups(response, request)
	return response
}

func proxyBindingResponseData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	return payload.Data
}

func TestProxyBindingChangesRestartStableIMSAndEmptyRoutesUseMCC(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	controller.state = vowifi.State{
		DeviceID: "ec20",
		ICCID:    testProfileICCID,
		Enabled:  true,
		IMSReady: true,
	}
	group := store.DeviceProxyBindingGroup{
		DeviceID:    "ec20",
		ICCID:       testProfileICCID,
		ProfileName: "Profile",
	}
	if err := database.SaveDeviceProxyBindingGroup(context.Background(), group, []string{"route-1", "route-2"}); err != nil {
		t.Fatal(err)
	}

	groups, err := database.ListDeviceProxyBindingGroups(context.Background(), group.DeviceID)
	if err != nil || len(groups) != 1 {
		t.Fatalf("stored groups = %+v, %v", groups, err)
	}
	group.ID = groups[0].ID
	response := proxyBindingGroupRoutesRequest(t, server, group.ID, `{"proxy_ids":["route-2","route-1"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("reorder status = %d, body = %s", response.Code, response.Body.String())
	}
	if controller.reconnects != 1 {
		t.Fatalf("reconnects after reorder = %d, want 1", controller.reconnects)
	}
	saved, err := database.DeviceProxyBindingGroupByID(context.Background(), group.ID)
	if err != nil || len(saved.Routes) != 2 || saved.Routes[0].UpstreamProxyID != "route-2" {
		t.Fatalf("saved routes = %+v, %v", saved.Routes, err)
	}

	response = proxyBindingGroupRoutesRequest(t, server, group.ID, `{"proxy_ids":[]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("empty route status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := database.DeviceProxyBindingGroupByID(context.Background(), group.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("group after empty routes error = %v, want ErrNotFound", err)
	}
	if controller.reconnects != 2 {
		t.Fatalf("reconnects after MCC fallback = %d, want 2", controller.reconnects)
	}
}

func TestProxyBindingGroupReportsAdvisoryReconnectState(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	response := proxyBindingGroupCreateRequest(t, server, `{
		"device_id":"ec20",
		"iccid":"8944100000000000001",
		"profile_name":"Profile",
		"proxy_ids":["route-1"]
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	data := proxyBindingResponseData(t, response)
	if data["reconnect_requested"] != true {
		t.Fatalf("create reconnect metadata = %#v", data)
	}
	groupID := fmt.Sprint(data["id"])

	controller.reconnectErr = errors.New("reconnect timeout")
	response = proxyBindingGroupRoutesRequest(t, server, groupID, `{"proxy_ids":["route-2"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("failed reconnect status = %d, body = %s", response.Code, response.Body.String())
	}
	data = proxyBindingResponseData(t, response)
	if data["reconnect_requested"] != false || data["reconnect_error"] != "VoWiFi reconnect request failed" {
		t.Fatalf("failed reconnect metadata = %#v", data)
	}
	saved, err := database.DeviceProxyBindingGroupByID(context.Background(), groupID)
	if err != nil || len(saved.Routes) != 1 || saved.Routes[0].UpstreamProxyID != "route-2" {
		t.Fatalf("routes after reconnect failure = %+v, %v", saved.Routes, err)
	}

	controller.reconnectErr = nil
	controller.state.ICCID = "8944100000000000002"
	response = proxyBindingGroupRoutesRequest(t, server, groupID, `{"proxy_ids":["route-1"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("deferred reconnect status = %d, body = %s", response.Code, response.Body.String())
	}
	data = proxyBindingResponseData(t, response)
	if data["reconnect_requested"] != false {
		t.Fatalf("deferred reconnect metadata = %#v", data)
	}
	if _, exists := data["reconnect_error"]; exists {
		t.Fatalf("deferred reconnect unexpectedly reported an error: %#v", data)
	}
}

func TestUpstreamLifecycleReconnectsCardProxyGroups(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	controller.state = vowifi.State{
		DeviceID: "ec20",
		ICCID:    testProfileICCID,
		Enabled:  true,
		IMSReady: true,
	}
	group := store.DeviceProxyBindingGroup{
		DeviceID:    "ec20",
		ICCID:       testProfileICCID,
		ProfileName: "Profile",
	}
	if err := database.SaveDeviceProxyBindingGroup(context.Background(), group, []string{"route-1"}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/upstream-proxies/route-1", bytes.NewBufferString(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleUpstreamProxy(response, request, "route-1")
	if response.Code != http.StatusOK {
		t.Fatalf("disable upstream status = %d, body = %s", response.Code, response.Body.String())
	}
	if controller.reconnects != 1 {
		t.Fatalf("reconnects after upstream disable = %d, want 1", controller.reconnects)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/upstream-proxies/route-1", nil)
	response = httptest.NewRecorder()
	server.handleUpstreamProxy(response, request, "route-1")
	if response.Code != http.StatusOK {
		t.Fatalf("delete upstream status = %d, body = %s", response.Code, response.Body.String())
	}
	if controller.reconnects != 2 {
		t.Fatalf("reconnects after upstream delete = %d, want 2", controller.reconnects)
	}
}

func TestProfileProxyBindingPersistsAndReconnectsOnlyCurrentICCID(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	response := profileBindingRequest(t, server, http.MethodPost, `{
		"upstream_proxy_id":"route-1",
		"bindings":[
			{"device_id":"ec20","iccid":"8944100000000000001","profile_name":"Vodafone UK","state_text":"Enabled"},
			{"device_id":"ec20","iccid":"89104100000000000001","profile_name":"TIM"}
		]
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", response.Code, response.Body.String())
	}
	binding, err := database.DeviceProxyBinding(context.Background(), testProfileICCID)
	if err != nil || binding.UpstreamProxyID != "route-1" || binding.ProfileName != "Vodafone UK" {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
	if controller.reconnects != 1 {
		t.Fatalf("reconnects = %d, want only the current ICCID to reconnect", controller.reconnects)
	}

	response = profileBindingRequest(t, server, http.MethodDelete, `{"upstream_proxy_id":"route-1","iccids":["8944100000000000001","89104100000000000001"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := database.DeviceProxyBinding(context.Background(), testProfileICCID); err != store.ErrNotFound {
		t.Fatalf("binding after delete error = %v, want ErrNotFound", err)
	}
	if controller.reconnects != 2 {
		t.Fatalf("reconnects after delete = %d, want 2", controller.reconnects)
	}
}

func TestProfileProxyBindingRejectsSameICCIDOnDifferentProxy(t *testing.T) {
	server, database, _ := newProfileBindingTestServer(t)
	first := profileBindingRequest(t, server, http.MethodPost, `{"upstream_proxy_id":"route-1","bindings":[{"device_id":"ec20","iccid":"8944100000000000001","profile_name":"Profile"}]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("initial bind status = %d, body = %s", first.Code, first.Body.String())
	}
	second := profileBindingRequest(t, server, http.MethodPost, `{"upstream_proxy_id":"route-2","bindings":[{"device_id":"ec20","iccid":"8944100000000000001","profile_name":"Profile"}]}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("rebind status = %d, want 409, body = %s", second.Code, second.Body.String())
	}
	binding, err := database.DeviceProxyBinding(context.Background(), testProfileICCID)
	if err != nil || binding.UpstreamProxyID != "route-1" {
		t.Fatalf("binding after rejected rebind = %+v, %v", binding, err)
	}
}

func TestProfileProxyBindingSupportsPCSCReader(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	readerICCID := "89104100000000000001"
	if err := database.UpsertDevice(context.Background(), store.Device{
		ID: "reader-1", Name: "USB SIM Reader", DeviceType: store.DeviceTypeUSBSIMReader, VoWiFiEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	registerActiveTestCard(t, database, "reader-1", "reader-1", readerICCID)
	server.devices = fakeDeviceController{entry: device.Device{
		ID: "reader-1", Discovered: true,
		Snapshot: &device.Snapshot{DeviceID: "reader-1", Responsive: true, SIMReady: true, ICCID: readerICCID},
	}}
	controller.state = vowifi.State{DeviceID: "reader-1", ICCID: readerICCID, Enabled: true}
	if _, err := server.ResolveRegisteredActiveCard(context.Background(), "reader-1", readerICCID); err != nil {
		t.Fatalf("reader card gate: %v", err)
	}
	if requested, err := server.requestProfileProxyRouteReconnect("reader-1", readerICCID); err != nil || !requested {
		t.Fatalf("direct reader reconnect = %v, %v", requested, err)
	}
	controller.reconnects = 0
	response := profileBindingRequest(t, server, http.MethodPost, `{
		"upstream_proxy_id":"route-1",
		"bindings":[{"device_id":"reader-1","iccid":"89104100000000000001","profile_name":"Reader Profile"}]
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", response.Code, response.Body.String())
	}
	binding, err := database.DeviceProxyBinding(context.Background(), readerICCID)
	if err != nil || binding.DeviceID != "reader-1" || binding.UpstreamProxyID != "route-1" {
		t.Fatalf("reader binding = %+v, %v", binding, err)
	}
	if controller.reconnects != 1 {
		t.Fatalf("reader reconnects = %d, want 1", controller.reconnects)
	}
}

func TestProfileProxyBindingRejectsLegacyOfflinePrebind(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	offlineICCID := "89104100000000000002"
	if err := database.UpsertSimCard(context.Background(), store.SimCard{ICCID: offlineICCID}); err != nil {
		t.Fatal(err)
	}
	response := profileBindingRequest(t, server, http.MethodPost, `{
		"upstream_proxy_id":"route-1",
		"bindings":[{"device_id":"","iccid":"89104100000000000002","profile_name":"Offline Profile"}]
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("legacy offline prebind status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := database.DeviceProxyBinding(context.Background(), offlineICCID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("legacy offline binding was created: %v", err)
	}
	if controller.reconnects != 0 {
		t.Fatalf("legacy offline prebind reconnects = %d, want 0", controller.reconnects)
	}

	groupResponse := proxyBindingGroupCreateRequest(t, server, `{
		"device_id":"","iccid":"89104100000000000002","profile_name":"Offline Profile","proxy_ids":["route-1"]
	}`)
	if groupResponse.Code != http.StatusOK {
		t.Fatalf("group offline prebind status = %d, body = %s", groupResponse.Code, groupResponse.Body.String())
	}
	group, err := database.DeviceProxyBindingGroupByICCID(context.Background(), offlineICCID)
	if err != nil || group.DeviceID != "" || len(group.Routes) != 1 {
		t.Fatalf("group offline binding = %+v, %v", group, err)
	}
	if controller.reconnects != 0 {
		t.Fatalf("group offline prebind reconnects = %d, want 0", controller.reconnects)
	}
}

func TestProxyBindingGroupCreateIgnoresDeviceID(t *testing.T) {
	server, database, controller := newProfileBindingTestServer(t)
	offlineICCID := "89104100000000000003"
	if err := database.UpsertSimCard(context.Background(), store.SimCard{ICCID: offlineICCID}); err != nil {
		t.Fatal(err)
	}
	response := proxyBindingGroupCreateRequest(t, server, `{
		"device_id":"ec20",
		"iccid":"89104100000000000003",
		"profile_name":"ICCID-only binding",
		"proxy_ids":["route-1"]
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	group, err := database.DeviceProxyBindingGroupByICCID(context.Background(), offlineICCID)
	if err != nil || group.DeviceID != "" || len(group.Routes) != 1 {
		t.Fatalf("ICCID-only binding = %+v, %v", group, err)
	}
	data := proxyBindingResponseData(t, response)
	if data["device_id"] != "" {
		t.Fatalf("response exposed device association: %#v", data)
	}
	if controller.reconnects != 0 {
		t.Fatalf("offline ICCID-only binding reconnects = %d, want 0", controller.reconnects)
	}
}
