package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/store"
)

type failingESIMInventoryController struct {
	fakeDeviceController
	err error
}

type renameTrackingController struct {
	fakeDeviceController
	renamed bool
}

func (controller *renameTrackingController) ESIMRenameProfile(context.Context, string, string, string, string) error {
	controller.renamed = true
	return nil
}

func (controller failingESIMInventoryController) ESIMInventory(context.Context, string) ([]device.EsimInventoryEntry, error) {
	return nil, controller.err
}

func newESIMProfileCacheTestServer(t *testing.T) *Server {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{
		ID:            "reader-1",
		Name:          "Reader",
		ControlDevice: "reader-serial",
	}); err != nil {
		t.Fatal(err)
	}
	return &Server{store: database, logger: regionTestLogger(), maxRequestBodyBytes: 16 << 10}
}

func TestESIMProfileCacheStripsChipIdentityAndPersistsProfileMetadata(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	groups := []map[string]any{{
		"eid":    "89000000000000000000000000000000",
		"aidHex": "A0000005591010FFFFFFFF8900000001",
		"profiles": []map[string]any{{
			"iccid":               "8944100000000000001",
			"name":                "Travel",
			"serviceProviderName": "Example Mobile",
			"state":               1,
			"stateText":           "Enabled",
			"classText":           "Provisioning",
			"secret":              "must-not-be-cached",
		}},
	}}

	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", groups); err != nil {
		t.Fatal(err)
	}
	cached, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader")
	if !ok || len(cached.Profiles) != 1 {
		t.Fatalf("cached document = %+v, %v", cached, ok)
	}
	if _, exists := cached.Profiles[0]["eid"]; exists {
		t.Fatalf("cached group kept EID: %#v", cached.Profiles[0])
	}
	if _, exists := cached.Profiles[0]["aidHex"]; exists {
		t.Fatalf("cached group kept AID: %#v", cached.Profiles[0])
	}
	profiles, _ := cached.Profiles[0]["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("cached profiles = %#v", profiles)
	}
	profile, _ := profiles[0].(map[string]any)
	if _, exists := profile["secret"]; exists {
		t.Fatalf("cached profile kept unknown field: %#v", profile)
	}
	if cached.CachedAt <= 0 {
		t.Fatalf("cachedAt = %d", cached.CachedAt)
	}
	setting, err := server.store.AppSetting(context.Background(), esimProfileCacheKey("reader-1"))
	if err != nil || !setting.Sensitive {
		t.Fatalf("cache setting = %+v, %v", setting, err)
	}
}

func TestESIMProfileCacheIsReturnedOnlyForCachedOfflineOverview(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	groups := []map[string]any{{
		"profiles": []map[string]any{{
			"iccid":     "8944100000000000001",
			"name":      "Travel",
			"stateText": "Enabled",
		}},
	}}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", groups); err != nil {
		t.Fatal(err)
	}

	cachedRequest := httptest.NewRequest(http.MethodGet, "/esim?cached=1", nil)
	cachedResponse := httptest.NewRecorder()
	server.writeEsimOverview(cachedResponse, cachedRequest, "physical-reader", false, "reader-1")
	var cachedPayload struct {
		Data struct {
			Profiles []map[string]any `json:"profiles"`
			Stale    bool             `json:"stale"`
			CachedAt int64            `json:"cached_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(cachedResponse.Body.Bytes(), &cachedPayload); err != nil {
		t.Fatalf("decode cached overview: %v", err)
	}
	if !cachedPayload.Data.Stale || cachedPayload.Data.CachedAt <= 0 || len(cachedPayload.Data.Profiles) != 1 {
		t.Fatalf("cached overview = %#v", cachedPayload)
	}

	liveRequest := httptest.NewRequest(http.MethodGet, "/esim", nil)
	liveResponse := httptest.NewRecorder()
	server.writeEsimOverview(liveResponse, liveRequest, "physical-reader", false, "reader-1")
	var livePayload struct {
		Data struct {
			Profiles []map[string]any `json:"profiles"`
			Stale    bool             `json:"stale"`
		} `json:"data"`
	}
	if err := json.Unmarshal(liveResponse.Body.Bytes(), &livePayload); err != nil {
		t.Fatalf("decode live overview: %v", err)
	}
	if livePayload.Data.Stale || len(livePayload.Data.Profiles) != 0 {
		t.Fatalf("ordinary overview leaked cached profiles: %#v", livePayload)
	}
}

func TestCachedESIMOverviewFallsBackWhenPresentDeviceReadFails(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	groups := []map[string]any{{
		"profiles": []map[string]any{{
			"iccid":     "8944100000000000001",
			"name":      "Travel",
			"stateText": "Enabled",
		}},
	}}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", groups); err != nil {
		t.Fatal(err)
	}
	server.devices = failingESIMInventoryController{
		fakeDeviceController: fakeDeviceController{entry: device.Device{ID: "physical-reader", Discovered: true}},
		err:                  errors.New("PCSC reader temporarily unavailable"),
	}

	overviewRequest := httptest.NewRequest(http.MethodGet, "/esim?cached=1", nil)
	overviewResponse := httptest.NewRecorder()
	server.writeEsimOverview(overviewResponse, overviewRequest, "physical-reader", true, "reader-1")
	var overviewPayload struct {
		Data struct {
			Profiles []map[string]any `json:"profiles"`
			Stale    bool             `json:"stale"`
		} `json:"data"`
	}
	if err := json.Unmarshal(overviewResponse.Body.Bytes(), &overviewPayload); err != nil {
		t.Fatalf("decode cached overview: %v", err)
	}
	if !overviewPayload.Data.Stale || len(overviewPayload.Data.Profiles) != 1 {
		t.Fatalf("cached overview after read failure = %#v", overviewPayload)
	}

	profilesRequest := httptest.NewRequest(http.MethodGet, "/esim/profiles?cached=1", nil)
	profilesResponse := httptest.NewRecorder()
	server.writeEsimGroups(profilesResponse, profilesRequest, "physical-reader", true, "reader-1")
	var profilesPayload struct {
		Data     []map[string]any `json:"data"`
		Stale    bool             `json:"stale"`
		CachedAt int64            `json:"cached_at"`
	}
	if err := json.Unmarshal(profilesResponse.Body.Bytes(), &profilesPayload); err != nil {
		t.Fatalf("decode cached profiles: %v", err)
	}
	if len(profilesPayload.Data) != 1 || !profilesPayload.Stale || profilesPayload.CachedAt <= 0 {
		t.Fatalf("cached profiles after read failure = %#v", profilesPayload)
	}

	liveRequest := httptest.NewRequest(http.MethodGet, "/esim", nil)
	liveResponse := httptest.NewRecorder()
	server.writeEsimOverview(liveResponse, liveRequest, "physical-reader", true, "reader-1")
	if liveResponse.Code == http.StatusOK {
		t.Fatalf("ordinary overview hid a live read failure: %d, %s", liveResponse.Code, liveResponse.Body.String())
	}
}

func TestESIMProfileCacheRejectsHardwareGenerationChange(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	server.devices = fakeDeviceController{entry: device.Device{
		ID:         "physical-reader",
		Discovered: true,
		Candidate: modem.Candidate{
			SerialNumber:  "reader-serial",
			USBGeneration: "generation-1",
		},
	}}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", []map[string]any{}); err != nil {
		t.Fatal(err)
	}

	server.devices = fakeDeviceController{entry: device.Device{
		ID:         "physical-reader",
		Discovered: true,
		Candidate: modem.Candidate{
			SerialNumber:  "reader-serial",
			USBGeneration: "generation-2",
		},
	}}
	if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); ok {
		t.Fatal("cache survived a USB generation change")
	}
}

func TestESIMProfileCacheStableIdentitySurvivesUSBPathChange(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	if err := server.store.UpsertDevice(context.Background(), store.Device{
		ID:            "reader-1",
		Name:          "Reader",
		ControlDevice: "reader-serial",
		USBPath:       "1-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", []map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpsertDevice(context.Background(), store.Device{
		ID:            "reader-1",
		Name:          "Reader",
		ControlDevice: "reader-serial",
		USBPath:       "2-4",
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); !ok {
		t.Fatal("stable hardware cache was rejected after a USB path change")
	}
}

func TestESIMNoEUICCStrikesPreserveCacheUntilConfirmed(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	server.devices = fakeDeviceController{entry: device.Device{
		ID:         "physical-reader",
		Discovered: true,
		Candidate:  modem.Candidate{SerialNumber: "reader-serial"},
	}}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", []map[string]any{}); err != nil {
		t.Fatal(err)
	}

	for strike := 1; strike < 3; strike++ {
		if err := server.noteESIMNoEUICC(context.Background(), "reader-1", "physical-reader"); err != nil {
			t.Fatal(err)
		}
		cached, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader")
		if !ok || cached.NoEUICCStrikes != strike {
			t.Fatalf("cache after strike %d = %+v, %v", strike, cached, ok)
		}
	}
	if err := server.noteESIMNoEUICC(context.Background(), "reader-1", "physical-reader"); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); ok {
		t.Fatal("cache remained after three confirmed missing-eUICC states")
	}
}

func TestESIMProfileMutationInvalidatesCachedInventory(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	server.devices = fakeDeviceController{entry: device.Device{
		ID:         "physical-reader",
		Discovered: true,
		Candidate:  modem.Candidate{SerialNumber: "reader-serial"},
	}}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", []map[string]any{}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPatch,
		"/esim/profiles/8944100000000000001",
		bytes.NewBufferString(`{"name":"Renamed"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleEsimRename(response, request, "physical-reader", true, "8944100000000000001", "reader-1")
	if response.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); ok {
		t.Fatal("profile rename kept the previous cached inventory")
	}
}

func TestESIMProfileMutationIsBlockedWhenCacheInvalidationFails(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	controller := &renameTrackingController{fakeDeviceController: fakeDeviceController{entry: device.Device{
		ID:         "physical-reader",
		Discovered: true,
		Candidate:  modem.Candidate{SerialNumber: "reader-serial"},
	}}}
	server.devices = controller
	server.esimCacheInvalidator = func(context.Context, string) error {
		return errors.New("cache delete failed")
	}

	request := httptest.NewRequest(
		http.MethodPatch,
		"/esim/profiles/8944100000000000001",
		bytes.NewBufferString(`{"name":"Renamed"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleEsimRename(response, request, "physical-reader", true, "8944100000000000001", "reader-1")
	if response.Code == http.StatusOK {
		t.Fatalf("mutation succeeded with cache invalidation failure: %s", response.Body.String())
	}
	if controller.renamed {
		t.Fatal("hardware rename ran before cache invalidation succeeded")
	}
}

func TestESIMProfileCacheCanBeCleared(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", []map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := server.clearEsimProfileCache(context.Background(), "reader-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); ok {
		t.Fatal("cache remained after clear")
	}
	if err := server.clearEsimProfileCache(context.Background(), "reader-1"); err != nil {
		t.Fatal(err)
	}
}

func TestESIMProfileCacheRejectsUnknownOrMalformedDocuments(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	for _, raw := range []string{
		`{"version":2,"cached_at":1,"profiles":[]}`,
		`{"version":1,"cached_at":1}`,
		`"not-json"`,
	} {
		if err := server.store.UpsertAppSetting(context.Background(), store.AppSetting{
			Key:       esimProfileCacheKey("reader-1"),
			Value:     []byte(raw),
			Sensitive: true,
		}); err != nil {
			t.Fatal(err)
		}
		if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); ok {
			t.Fatalf("accepted invalid cache document %q", raw)
		}
		if _, err := server.store.AppSetting(context.Background(), esimProfileCacheKey("reader-1")); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("invalid cache document %q was not discarded: %v", raw, err)
		}
	}
}

func TestESIMProfileCacheInvalidationRetriesTwice(t *testing.T) {
	attempts := 0
	err := retryESIMProfileCacheInvalidation(context.Background(), func(context.Context) error {
		attempts++
		return errors.New("delete failed")
	})
	if err == nil || attempts != 2 {
		t.Fatalf("persistent failure = %v, attempts = %d", err, attempts)
	}

	attempts = 0
	err = retryESIMProfileCacheInvalidation(context.Background(), func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("transient failure")
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("transient failure = %v, attempts = %d", err, attempts)
	}
}

func TestESIMProfileCacheRequiresStableHardwareIdentity(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	if err := server.saveEsimProfileCache(context.Background(), "reader-no-config", "physical-reader", []map[string]any{}); err == nil {
		t.Fatal("cache was saved without a configured stable hardware identity")
	}
}

func TestDeletingDeviceClearsItsESIMProfileCache(t *testing.T) {
	server := newESIMProfileCacheTestServer(t)
	if err := server.store.UpsertDevice(context.Background(), store.Device{
		ID:            "reader-1",
		Name:          "Reader",
		ControlDevice: "reader-serial",
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.saveEsimProfileCache(context.Background(), "reader-1", "physical-reader", []map[string]any{}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/devices/reader-1", nil)
	response := httptest.NewRecorder()
	if !server.handleDevicePath(response, request, "reader-1", nil) {
		t.Fatal("device delete was not handled")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, ok := server.loadEsimProfileCache(context.Background(), "reader-1", "physical-reader"); ok {
		t.Fatal("device cache remained after device deletion")
	}
}
