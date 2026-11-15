package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vonet/internal/device"
	"vonet/internal/store"
)

const (
	testGateICCID      = "8944100000000000001"
	testGateDeviceID   = "modem-1"
	testGatePhysicalID = "physical-1"

	testICCIDCurrent = "89441000000000000001"
	testICCIDOld     = "89441000000000000002"
	testICCIDInitial = "89441000000000000003"
	testICCIDTask    = "89441000000000000004"
	testICCIDSame    = "89441000000000000005"
)

func registerActiveTestCard(
	t *testing.T,
	database *store.Store,
	deviceID, physicalID, iccid string,
) {
	t.Helper()
	ctx := context.Background()
	if err := database.UpsertSimCard(ctx, store.SimCard{ICCID: iccid}); err != nil {
		t.Fatalf("UpsertSimCard(%q) error = %v", iccid, err)
	}
	now := time.Now().UTC()
	if err := database.TouchSimCardBinding(ctx, store.SimCardBinding{
		ICCID:            iccid,
		DeviceID:         deviceID,
		PhysicalDeviceID: physicalID,
		State:            store.SimCardBindingActive,
		LastSeenAt:       now,
	}); err != nil {
		t.Fatalf("TouchSimCardBinding(%q) error = %v", iccid, err)
	}
}

type cardGateRegistryStub struct {
	card       RegisteredCard
	cardErr    error
	bindings   []CardBinding
	bindingErr error
}

func (stub cardGateRegistryStub) RegisteredCard(context.Context, string) (RegisteredCard, error) {
	return stub.card, stub.cardErr
}

func (stub cardGateRegistryStub) CardBindings(context.Context, string) ([]CardBinding, error) {
	return stub.bindings, stub.bindingErr
}

type cardGateDevicesStub struct {
	current    CardDeviceState
	currentErr error
	matches    []CardDeviceState
	matchErr   error
}

func (stub cardGateDevicesStub) CurrentCardDevice(context.Context, string) (CardDeviceState, error) {
	return stub.current, stub.currentErr
}

func (stub cardGateDevicesStub) MatchingCardDevices(context.Context, string) ([]CardDeviceState, error) {
	return stub.matches, stub.matchErr
}

func readyGateSnapshot() device.Snapshot {
	return device.Snapshot{
		DeviceID:   testGatePhysicalID,
		Responsive: true,
		SIMReady:   true,
		ICCID:      testGateICCID,
	}
}

func validGateRegistry() cardGateRegistryStub {
	return cardGateRegistryStub{
		card: RegisteredCard{ICCID: testGateICCID, DisplayName: "Test SIM"},
		bindings: []CardBinding{{
			ICCID:      testGateICCID,
			DeviceID:   testGateDeviceID,
			PhysicalID: testGatePhysicalID,
			Active:     true,
			State:      store.SimCardBindingActive,
		}},
	}
}

func validGateDevices() cardGateDevicesStub {
	snapshot := readyGateSnapshot()
	return cardGateDevicesStub{
		current: CardDeviceState{
			DeviceID:   testGateDeviceID,
			PhysicalID: testGatePhysicalID,
			Snapshot:   snapshot,
			Present:    true,
		},
		matches: []CardDeviceState{{
			DeviceID:   testGateDeviceID,
			PhysicalID: testGatePhysicalID,
			Snapshot:   snapshot,
			Present:    true,
		}},
	}
}

func requireCardGateCode(t *testing.T, err error, code string) {
	t.Helper()
	var gateErr *CardGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("error = %v, want CardGateError", err)
	}
	if gateErr.Code != code {
		t.Fatalf("error code = %q, want %q", gateErr.Code, code)
	}
}

func TestResolveRegisteredCardSuccess(t *testing.T) {
	got, err := resolveRegisteredCard(
		context.Background(),
		validGateRegistry(),
		validGateDevices(),
		testGateDeviceID,
		testGateICCID,
	)
	if err != nil {
		t.Fatalf("resolveRegisteredCard() error = %v", err)
	}
	if got.ICCID != testGateICCID || got.DeviceID != testGateDeviceID ||
		got.PhysicalID != testGatePhysicalID || got.Card.DisplayName != "Test SIM" {
		t.Fatalf("unexpected execution context: %+v", got)
	}
}

func TestResolveRegisteredCardDerivesExpectedICCID(t *testing.T) {
	got, err := resolveRegisteredCard(
		context.Background(),
		validGateRegistry(),
		validGateDevices(),
		testGateDeviceID,
		"",
	)
	if err != nil {
		t.Fatalf("resolveRegisteredCard() error = %v", err)
	}
	if got.ICCID != testGateICCID {
		t.Fatalf("ICCID = %q, want %q", got.ICCID, testGateICCID)
	}
}

func TestResolveRegisteredCardRejectsMismatch(t *testing.T) {
	_, err := resolveRegisteredCard(
		context.Background(),
		validGateRegistry(),
		validGateDevices(),
		testGateDeviceID,
		"8944100000000000002",
	)
	requireCardGateCode(t, err, cardGateCodeBindingStale)
	if !errors.Is(err, ErrCardBindingStale) {
		t.Fatalf("error does not wrap ErrCardBindingStale: %v", err)
	}
}

func TestResolveRegisteredCardRejectsUnregistered(t *testing.T) {
	registry := validGateRegistry()
	registry.cardErr = store.ErrNotFound
	_, err := resolveRegisteredCard(
		context.Background(),
		registry,
		validGateDevices(),
		testGateDeviceID,
		testGateICCID,
	)
	requireCardGateCode(t, err, cardGateCodeNotRegistered)
	if !errors.Is(err, ErrCardNotRegistered) {
		t.Fatalf("error does not wrap ErrCardNotRegistered: %v", err)
	}
}

func TestResolveRegisteredCardRejectsMissingIdentity(t *testing.T) {
	devices := validGateDevices()
	devices.current.Snapshot.ICCID = ""
	_, err := resolveRegisteredCard(
		context.Background(),
		validGateRegistry(),
		devices,
		testGateDeviceID,
		"",
	)
	requireCardGateCode(t, err, cardGateCodeIdentityUnavailable)
}

func TestResolveRegisteredCardRejectsLiveConflict(t *testing.T) {
	devices := validGateDevices()
	devices.matches = append(devices.matches, CardDeviceState{
		DeviceID:   "modem-2",
		PhysicalID: "physical-2",
		Snapshot:   readyGateSnapshot(),
		Present:    true,
	})
	_, err := resolveRegisteredCard(
		context.Background(),
		validGateRegistry(),
		devices,
		testGateDeviceID,
		testGateICCID,
	)
	requireCardGateCode(t, err, cardGateCodeConflict)
	if !errors.Is(err, ErrCardICCIDConflict) {
		t.Fatalf("error does not wrap ErrCardICCIDConflict: %v", err)
	}
}

func TestResolveRegisteredCardRejectsBindingConflict(t *testing.T) {
	registry := validGateRegistry()
	registry.bindings = append(registry.bindings, CardBinding{
		ICCID:      testGateICCID,
		DeviceID:   "modem-2",
		PhysicalID: "physical-2",
		Active:     true,
		State:      store.SimCardBindingActive,
	})
	_, err := resolveRegisteredCard(
		context.Background(),
		registry,
		validGateDevices(),
		testGateDeviceID,
		testGateICCID,
	)
	requireCardGateCode(t, err, cardGateCodeConflict)
}

func TestResolveRegisteredCardRejectsStoredConflictState(t *testing.T) {
	registry := validGateRegistry()
	registry.bindings[0].Active = false
	registry.bindings[0].State = store.SimCardBindingConflict
	_, err := resolveRegisteredCard(
		context.Background(),
		registry,
		validGateDevices(),
		testGateDeviceID,
		testGateICCID,
	)
	requireCardGateCode(t, err, cardGateCodeConflict)
	if !errors.Is(err, ErrCardICCIDConflict) {
		t.Fatalf("error does not wrap ErrCardICCIDConflict: %v", err)
	}
}

func TestResolveRegisteredCardRejectsOfflineBinding(t *testing.T) {
	registry := validGateRegistry()
	registry.bindings[0].Active = false
	registry.bindings[0].State = store.SimCardBindingOffline
	_, err := resolveRegisteredCard(
		context.Background(),
		registry,
		validGateDevices(),
		testGateDeviceID,
		testGateICCID,
	)
	requireCardGateCode(t, err, cardGateCodeOffline)
	if !errors.Is(err, ErrCardOffline) {
		t.Fatalf("error does not wrap ErrCardOffline: %v", err)
	}
}

func TestResolveRegisteredCardRejectsStaleBinding(t *testing.T) {
	registry := validGateRegistry()
	registry.bindings[0].DeviceID = "modem-2"
	registry.bindings[0].PhysicalID = "physical-2"
	_, err := resolveRegisteredCard(
		context.Background(),
		registry,
		validGateDevices(),
		testGateDeviceID,
		testGateICCID,
	)
	requireCardGateCode(t, err, cardGateCodeBindingStale)
}

func TestAllowCardStopDoesNotRequireRegistration(t *testing.T) {
	if !AllowCardStop(testGateDeviceID) {
		t.Fatal("AllowCardStop() = false for a valid device ID")
	}
	if AllowCardStop("") || AllowCardStop("../device") {
		t.Fatal("AllowCardStop() accepted an invalid device ID")
	}
}

func TestRequireRegisteredActiveCardWritesStructuredError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeCardGateError(recorder, cardGateError(
		cardGateCodeNotRegistered,
		"the active ICCID is not registered in card management",
		http.StatusConflict,
		ErrCardNotRegistered,
	))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, cardGateCodeNotRegistered) {
		t.Fatalf("response body does not contain %q: %s", cardGateCodeNotRegistered, body)
	}
}
