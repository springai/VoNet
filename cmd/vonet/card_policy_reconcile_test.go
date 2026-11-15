package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

const (
	oldCardICCID    = "8944100000000000001"
	newCardICCID    = "8944100000000000002"
	staleCardICCID  = "8944100000000000003"
	unseenCardICCID = "8944100000000000004"
)

type policyReconcileDevices struct {
	gate           sync.RWMutex
	mu             sync.Mutex
	entry          device.Device
	flights        []bool
	backends       []string
	networks       []device.NetworkRequest
	imsModes       []device.CellularIMSMode
	imsStatus      device.CellularIMSStatus
	acquireErr     error
	onAcquire      func()
	readers        atomic.Int32
	unsafeRF       atomic.Bool
	inventory      []device.EsimInventoryEntry
	inventoryErr   error
	inventoryReads int
}

func (d *policyReconcileDevices) Get(string) (device.Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry := d.entry
	if entry.Snapshot != nil {
		snapshot := *entry.Snapshot
		entry.Snapshot = &snapshot
	}
	return entry, nil
}

func (d *policyReconcileDevices) List() []device.Device {
	entry, _ := d.Get("")
	return []device.Device{entry}
}

func (d *policyReconcileDevices) AcquireSIMRead(ctx context.Context, _ string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.acquireErr != nil {
		return nil, d.acquireErr
	}
	if !d.gate.TryRLock() {
		return nil, device.ErrESIMRecovering
	}
	d.readers.Add(1)
	if d.onAcquire != nil {
		d.onAcquire()
	}
	return func() { d.readers.Add(-1); d.gate.RUnlock() }, nil
}

func (d *policyReconcileDevices) SetFlight(_ context.Context, _ string, enabled bool) (device.FlightResult, error) {
	if d.readers.Load() == 0 {
		d.unsafeRF.Store(true)
	}
	d.mu.Lock()
	d.flights = append(d.flights, enabled)
	d.mu.Unlock()
	return device.FlightResult{}, nil
}

func (d *policyReconcileDevices) SetBackend(_ string, backend string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.backends = append(d.backends, backend)
	return nil
}

func (d *policyReconcileDevices) SetNetwork(_ context.Context, _ string, request device.NetworkRequest) (device.NetworkResult, error) {
	if d.readers.Load() == 0 {
		d.unsafeRF.Store(true)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.networks = append(d.networks, request)
	return device.NetworkResult{Enabled: request.Enabled, Backend: request.Backend}, nil
}

func (d *policyReconcileDevices) CellularIMS(context.Context, string) (device.CellularIMSStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.imsStatus, nil
}

func (d *policyReconcileDevices) SetCellularIMS(_ context.Context, _ string, mode device.CellularIMSMode) (device.CellularIMSStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.imsModes = append(d.imsModes, mode)
	d.imsStatus.Mode = mode
	d.imsStatus.Configured = mode == device.CellularIMSModeForceEnabled
	return d.imsStatus, nil
}

func (d *policyReconcileDevices) ESIMInventory(context.Context, string) ([]device.EsimInventoryEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inventoryReads++
	return d.inventory, d.inventoryErr
}

type policyReconcileWiFi struct {
	mu          sync.Mutex
	maintenance bool
	state       vowifi.State
	enables     []bool
	reconnects  int
	onState     func()
	onRequest   func()
}

func (w *policyReconcileWiFi) InMaintenance(string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.maintenance
}

func (w *policyReconcileWiFi) State(string) (vowifi.State, error) {
	if w.onState != nil {
		w.onState()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, nil
}

func (w *policyReconcileWiFi) RequestEnabled(_ string, enabled bool) (vowifi.State, error) {
	if w.onRequest != nil {
		w.onRequest()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enables = append(w.enables, enabled)
	return w.state, nil
}

func (w *policyReconcileWiFi) RequestReconnect(string) (vowifi.State, error) {
	if w.onRequest != nil {
		w.onRequest()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reconnects++
	return w.state, nil
}

func newPolicyReconcileFixture(t *testing.T) (*cardPolicyReconciler, *policyReconcileDevices, *policyReconcileWiFi) {
	t.Helper()
	db := newRegionTestStore(t)
	if err := db.UpsertDevice(context.Background(), store.Device{
		ID: "dev1", Name: "dev1", DeviceType: store.DeviceTypeWiFi410,
		ATPort: "/dev/policy-test", APN: "current.apn",
	}); err != nil {
		t.Fatal(err)
	}
	mustSeedPolicyCard(t, db, oldCardICCID, "dev1")
	mustSeedPolicyCard(t, db, newCardICCID, "dev1")
	if err := db.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID: oldCardICCID, VoWiFiEnabled: true, AirplaneEnabled: true, APN: "old.apn",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID: newCardICCID, AirplaneEnabled: true, APN: "new.apn",
	}); err != nil {
		t.Fatal(err)
	}
	d := &policyReconcileDevices{entry: device.Device{
		ID: "physical1", Discovered: true,
		Candidate: modem.Candidate{ATPort: modem.Port{Path: "/dev/policy-test"}},
		Snapshot:  &device.Snapshot{Responsive: true, SIMReady: true, ICCID: oldCardICCID, FlightMode: true},
	}}
	w := &policyReconcileWiFi{}
	r := &cardPolicyReconciler{
		logger: regionTestLogger(), database: db, devices: d, wifi: w,
		observedCards:      make(map[string]string),
		bindingStableAfter: 0, bindingOfflineAfter: 15 * time.Second,
		bindingObservationGap: 15 * time.Second,
	}
	return r, d, w
}

func mustSeedPolicyCard(t *testing.T, db *store.Store, iccid, deviceID string) {
	t.Helper()
	if err := db.UpsertSimCard(context.Background(), store.SimCard{
		ICCID: iccid, CardType: store.SimCardTypePhysical,
	}); err != nil {
		t.Fatalf("UpsertSimCard(%q) error = %v", iccid, err)
	}
	now := time.Now().UTC()
	if _, err := db.UpsertSimCardObservation(context.Background(), store.SimCardObservation{
		ICCID: iccid, DeviceID: deviceID, PhysicalDeviceID: "physical1",
		FirstSeenAt: now.Add(-time.Minute), LastSeenAt: now.Add(-time.Second),
		LastEnabledAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatalf("UpsertSimCardObservation(%q) error = %v", iccid, err)
	}
}

func TestCardPolicyReconcileSkipsMaintenanceAndBlockedSIM(t *testing.T) {
	for _, blockedBy := range []string{"runtime maintenance", "SIM writer"} {
		t.Run(blockedBy, func(t *testing.T) {
			r, d, w := newPolicyReconcileFixture(t)
			if blockedBy == "runtime maintenance" {
				w.maintenance = true
			} else {
				d.acquireErr = device.ErrESIMRecovering
			}
			r.reconcile(context.Background())
			config, err := r.database.Device(context.Background(), "dev1")
			if err != nil {
				t.Fatal(err)
			}
			if config.VoWiFiEnabled || config.APN != "current.apn" || len(w.enables) != 0 || w.reconnects != 0 || len(d.flights) != 0 {
				t.Fatalf("old card policy applied during %s: config=%+v wifi=%+v", blockedBy, config, w)
			}
			if d.readers.Load() != 0 {
				t.Fatal("SIM read lease leaked")
			}
		})
	}
}

func TestCardPolicyReconcileReadsIdentityAndConfigAfterSIMLease(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	d.onAcquire = func() {
		// The mapping pass saw the old ICCID. A completed switch before read-lease
		// acquisition must replace both that snapshot and its saved intent.
		d.mu.Lock()
		d.entry.Snapshot = &device.Snapshot{Responsive: true, SIMReady: true, ICCID: newCardICCID, FlightMode: true}
		d.mu.Unlock()
		config, err := r.database.Device(context.Background(), "dev1")
		if err != nil {
			t.Fatal(err)
		}
		config.VoWiFiEnabled = false
		config.APN = "new.apn"
		config.Name = "after switch"
		if err := r.database.UpsertDevice(context.Background(), config); err != nil {
			t.Fatal(err)
		}
	}
	r.reconcile(context.Background())
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if config.VoWiFiEnabled || config.APN != "new.apn" || config.Name != "after switch" || len(w.enables) != 0 || w.reconnects != 0 {
		t.Fatalf("reused pre-lease card/config: config=%+v wifi=%+v", config, w)
	}
}

func TestCardPolicyReconcileDrainsReaderBeforeProfileWriter(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	d.entry.Snapshot.FlightMode = false
	stateRead := make(chan struct{})
	allowState := make(chan struct{})
	var releaseState sync.Once
	t.Cleanup(func() { releaseState.Do(func() { close(allowState) }) })
	w.onState = func() { close(stateRead); <-allowState }
	var unsafeRequest atomic.Bool
	w.onRequest = func() {
		if d.readers.Load() == 0 {
			unsafeRequest.Store(true)
		}
	}
	done := make(chan struct{})
	go func() { r.reconcile(context.Background()); close(done) }()
	select {
	case <-stateRead:
	case <-time.After(time.Second):
		t.Fatal("reconciler did not reach its lifecycle decision")
	}
	writerStarted := make(chan struct{})
	writerEntered := make(chan struct{})
	go func() {
		close(writerStarted)
		d.gate.Lock()
		close(writerEntered)
		d.gate.Unlock()
	}()
	<-writerStarted
	select {
	case <-writerEntered:
		t.Fatal("profile writer entered before the reader's RF/lifecycle actions")
	case <-time.After(20 * time.Millisecond):
	}
	releaseState.Do(func() { close(allowState) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not complete")
	}
	select {
	case <-writerEntered:
	case <-time.After(time.Second):
		t.Fatal("reader did not release the profile gate")
	}
	if unsafeRequest.Load() || d.unsafeRF.Load() || len(w.enables) != 1 || !w.enables[0] || len(d.flights) != 1 || !d.flights[0] {
		t.Fatal("policy lifecycle/RF decision escaped its SIM read lease")
	}
}

func TestCardPolicyReconcileMissingIdentityCannotEnableIdleRuntime(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured bool
		state      vowifi.State
		snapshot   *device.Snapshot
		wantRetry  int
	}{
		{name: "disabled intent with old active runtime", state: vowifi.State{Enabled: true, Active: true, ICCID: oldCardICCID}},
		{name: "idle retains old ICCID", configured: true, state: vowifi.State{ICCID: oldCardICCID, Phase: vowifi.PhaseIdle}},
		{name: "failed setup does not retry without identity", configured: true, state: vowifi.State{Enabled: true, ICCID: oldCardICCID, Phase: vowifi.PhaseFailed}},
		{name: "unresponsive old snapshot", snapshot: &device.Snapshot{ICCID: oldCardICCID, SIMReady: true}},
		{name: "not ready old snapshot", snapshot: &device.Snapshot{ICCID: oldCardICCID, Responsive: true}},
		{name: "active runtime is not interrupted by missing identity", configured: true, state: vowifi.State{Enabled: true, Active: true, ICCID: oldCardICCID, Phase: vowifi.PhaseSMSReady}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, d, w := newPolicyReconcileFixture(t)
			d.entry.Snapshot = tc.snapshot
			w.state = tc.state
			config, err := r.database.Device(context.Background(), "dev1")
			if err != nil {
				t.Fatal(err)
			}
			config.VoWiFiEnabled = tc.configured
			if err := r.database.UpsertDevice(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			r.reconcile(context.Background())
			stored, err := r.database.Device(context.Background(), "dev1")
			if err != nil {
				t.Fatal(err)
			}
			if stored.VoWiFiEnabled != tc.configured || stored.APN != "current.apn" || len(w.enables) != 0 || w.reconnects != tc.wantRetry || len(d.flights) != 0 {
				t.Fatalf("missing/stale identity changed intent: config=%+v reconnects=%d", stored, w.reconnects)
			}
		})
	}
}

func TestCardPolicyReconcileReconnectsOnlyIdleOrFailedRuntime(t *testing.T) {
	for _, test := range []struct {
		name          string
		phase         vowifi.Phase
		previous      string
		stateICCID    string
		wantReconnect int
	}{
		{name: "active identity mismatch", phase: vowifi.PhaseSMSReady, stateICCID: staleCardICCID, wantReconnect: 0},
		{name: "idle identity mismatch", phase: vowifi.PhaseIdle, stateICCID: staleCardICCID, wantReconnect: 1},
		{name: "failed identity mismatch", phase: vowifi.PhaseFailed, stateICCID: staleCardICCID, wantReconnect: 1},
		{name: "active replug", phase: vowifi.PhaseIMSReady, previous: "missing", wantReconnect: 0},
		{name: "idle replug", phase: vowifi.PhaseIdle, previous: "missing", wantReconnect: 1},
		{name: "failed replug", phase: vowifi.PhaseFailed, previous: "missing", wantReconnect: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, _, w := newPolicyReconcileFixture(t)
			config, err := r.database.Device(context.Background(), "dev1")
			if err != nil {
				t.Fatal(err)
			}
			config.VoWiFiEnabled = true
			if err := r.database.UpsertDevice(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			if test.previous != "" {
				r.observedCards["dev1"] = test.previous
			}
			stateICCID := test.stateICCID
			if stateICCID == "" {
				stateICCID = oldCardICCID
			}
			w.state = vowifi.State{
				Enabled: true, Active: true, ICCID: stateICCID, Phase: test.phase,
			}
			r.reconcile(context.Background())
			if w.reconnects != test.wantReconnect {
				t.Fatalf("reconnects = %d, want %d", w.reconnects, test.wantReconnect)
			}
		})
	}
}

func TestCardPolicyReconcilePhysicalRemovalStopsOldSessionAndReplugResumes(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	config.VoWiFiEnabled = true
	if err := r.database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	w.state = vowifi.State{Enabled: true, Active: true, ICCID: oldCardICCID, Phase: vowifi.PhaseSMSReady}
	r.observedCards["dev1"] = oldCardICCID
	d.entry.Discovered = false
	r.reconcile(context.Background())
	stored, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.VoWiFiEnabled || len(w.enables) != 1 || w.enables[0] || w.reconnects != 0 || r.observedCards["dev1"] != "missing" {
		t.Fatalf("physical removal did not retire only the old session: config=%+v calls=%v", stored, w.enables)
	}
	// Disable finishes asynchronously, preserving the ICCID for diagnostics.
	// The same card reappears ready and must use its saved policy again.
	w.state = vowifi.State{ICCID: oldCardICCID, Phase: vowifi.PhaseIdle}
	d.entry.Discovered = true
	r.reconcile(context.Background())
	if len(w.enables) != 2 || !w.enables[1] || w.reconnects != 0 || r.observedCards["dev1"] != oldCardICCID {
		t.Fatalf("same-card replug did not resume its saved policy: calls=%v reconnects=%d", w.enables, w.reconnects)
	}
}

func TestCardPolicyReconcilePhysicalRemovalDoesNotInterruptMaintenance(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	d.entry.Discovered = false
	w.maintenance = true
	w.state = vowifi.State{Enabled: true, Active: true, ICCID: oldCardICCID}
	r.observedCards["dev1"] = oldCardICCID
	r.reconcile(context.Background())
	if len(w.enables) != 0 || w.reconnects != 0 || r.observedCards["dev1"] != oldCardICCID {
		t.Fatal("temporary disappearance interrupted profile maintenance")
	}
}

func TestCardPolicyReconcileStableBindingDefersPolicyUntilReady(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	if err := r.database.UpsertSimCard(context.Background(), store.SimCard{
		ICCID: unseenCardICCID, CardType: store.SimCardTypePhysical,
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.database.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID: unseenCardICCID, VoWiFiEnabled: true, AirplaneEnabled: true,
		CellularIMSMode: store.CellularIMSModeForceDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	r.bindingStableAfter = time.Second
	d.entry.Snapshot.ICCID = unseenCardICCID

	r.reconcile(context.Background())
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	pendingIMS := append([]device.CellularIMSMode(nil), d.imsModes...)
	pendingNetworks := append([]device.NetworkRequest(nil), d.networks...)
	d.mu.Unlock()
	if config.VoWiFiEnabled || len(w.enables) != 0 || len(pendingIMS) != 0 {
		t.Fatalf("pending binding applied policy: config=%+v calls=%v", config, w.enables)
	}
	if len(pendingNetworks) != 1 || pendingNetworks[0].Enabled {
		t.Fatalf("pending binding left unsafe network state: %+v", pendingNetworks)
	}

	time.Sleep(1200 * time.Millisecond)
	r.reconcile(context.Background())
	config, err = r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if !config.VoWiFiEnabled || len(w.enables) != 1 || !w.enables[0] {
		t.Fatalf("stable binding did not apply policy: config=%+v calls=%v", config, w.enables)
	}
	d.mu.Lock()
	activeIMS := append([]device.CellularIMSMode(nil), d.imsModes...)
	d.mu.Unlock()
	if len(activeIMS) != 1 || activeIMS[0] != device.CellularIMSModeForceDisabled {
		t.Fatalf("stable binding IMS calls = %v", activeIMS)
	}
}

func TestCardPolicyReconcileMultipleDevicesCreateConflictAndSuspend(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	policy, err := r.database.CardPolicy(context.Background(), oldCardICCID)
	if err != nil {
		t.Fatal(err)
	}
	policy.CellularIMSMode = store.CellularIMSModeForceEnabled
	if err := r.database.UpsertCardPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := r.database.UpsertSimCardObservation(context.Background(), store.SimCardObservation{
		ICCID: oldCardICCID, DeviceID: "dev2", PhysicalDeviceID: "physical2",
		FirstSeenAt: now.Add(-time.Minute), LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	w.state = vowifi.State{Enabled: true, Active: true, ICCID: oldCardICCID, Phase: vowifi.PhaseIMSReady}
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	config.VoWiFiEnabled = true
	if err := r.database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}

	r.reconcile(context.Background())
	binding, err := r.database.SimCardBinding(context.Background(), oldCardICCID)
	if err != nil || binding.State != store.SimCardBindingConflict || binding.DeviceID != "" {
		t.Fatalf("conflict binding = %+v, %v", binding, err)
	}
	stored, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.VoWiFiEnabled || len(w.enables) != 1 || w.enables[0] {
		t.Fatalf("conflict did not suspend runtime: config=%+v calls=%v", stored, w.enables)
	}
	d.mu.Lock()
	imsModes := append([]device.CellularIMSMode(nil), d.imsModes...)
	d.mu.Unlock()
	if len(imsModes) != 0 {
		t.Fatalf("conflict touched CellularIMS: %v", imsModes)
	}
}

func TestCardPolicyReconcileReleasesPreviousCardAfterVerifiedSwitch(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	r.reconcile(context.Background())
	if _, err := r.database.SimCardBinding(context.Background(), oldCardICCID); err != nil {
		t.Fatalf("old card was not bound: %v", err)
	}

	d.entry.Snapshot.ICCID = newCardICCID
	r.reconcile(context.Background())
	oldBinding, err := r.database.SimCardBinding(context.Background(), oldCardICCID)
	if err != nil || oldBinding.State != store.SimCardBindingOffline || oldBinding.DeviceID != "" {
		t.Fatalf("stale old binding = %+v, %v", oldBinding, err)
	}
	newBinding, err := r.database.SimCardBinding(context.Background(), newCardICCID)
	if err != nil || newBinding.State != store.SimCardBindingActive {
		t.Fatalf("new binding = %+v, %v", newBinding, err)
	}
	_ = w
}

func TestCardPolicyReconcileMissingIdentityWaitsForOfflineTimeout(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	r.bindingOfflineAfter = time.Second
	r.reconcile(context.Background())
	d.entry.Snapshot = nil
	r.reconcile(context.Background())
	if binding, err := r.database.SimCardBinding(context.Background(), oldCardICCID); err != nil ||
		binding.State == store.SimCardBindingOffline {
		t.Fatalf("binding expired before offline timeout: %+v, %v", binding, err)
	}

	time.Sleep(1200 * time.Millisecond)
	r.reconcile(context.Background())
	binding, err := r.database.SimCardBinding(context.Background(), oldCardICCID)
	if err != nil || binding.State != store.SimCardBindingOffline {
		t.Fatalf("binding after offline timeout = %+v, %v", binding, err)
	}
	_ = w
}

func TestCardPolicyReconcileMissingIdentityExpiresBindingAndSuspends(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	r.bindingOfflineAfter = 0
	r.reconcile(context.Background())
	w.state = vowifi.State{Enabled: true, Active: true, ICCID: oldCardICCID, Phase: vowifi.PhaseIMSReady}
	d.entry.Snapshot = nil

	r.reconcile(context.Background())
	binding, err := r.database.SimCardBinding(context.Background(), oldCardICCID)
	if err != nil || binding.State != store.SimCardBindingOffline {
		t.Fatalf("offline binding = %+v, %v", binding, err)
	}
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if config.VoWiFiEnabled || len(w.enables) != 2 || !w.enables[0] || w.enables[1] {
		t.Fatalf("expired identity did not suspend runtime: config=%+v calls=%v", config, w.enables)
	}
}

func TestCardPolicyReconcileAppliesNonReaderCellularAndIMSPolicy(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	if err := r.database.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID: newCardICCID, NetworkEnabled: true, VoWiFiEnabled: false,
		AirplaneEnabled: false, APN: "card.apn", IPVersion: "IPV4V6",
		RuntimeMode:     store.CardRuntimeModeQMI,
		CellularIMSMode: store.CellularIMSModeForceEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	d.entry.Snapshot.ICCID = newCardICCID
	d.entry.Snapshot.FlightMode = true

	r.reconcile(context.Background())
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if !config.NetworkEnabled || config.DeviceBackend != store.CardRuntimeModeQMI || len(w.enables) != 0 {
		t.Fatalf("cellular policy state = config=%+v wifi=%+v", config, w.enables)
	}
	d.mu.Lock()
	backends := append([]string(nil), d.backends...)
	networks := append([]device.NetworkRequest(nil), d.networks...)
	imsModes := append([]device.CellularIMSMode(nil), d.imsModes...)
	flights := append([]bool(nil), d.flights...)
	d.mu.Unlock()
	if len(backends) != 1 || backends[0] != store.CardRuntimeModeQMI ||
		len(imsModes) != 1 || imsModes[0] != device.CellularIMSModeForceEnabled {
		t.Fatalf("runtime/IMS calls = backends=%v ims=%v", backends, imsModes)
	}
	if len(networks) != 1 || !networks[0].Enabled || networks[0].APN != "card.apn" ||
		networks[0].IPVersion != "IPV4V6" || networks[0].Backend != store.CardRuntimeModeQMI {
		t.Fatalf("network calls = %+v", networks)
	}
	if len(flights) != 1 || flights[0] {
		t.Fatalf("flight calls = %+v", flights)
	}
}

func TestCardPolicyReconcileReaderForcesPCSCAndVoWiFiSafePolicy(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	config.DeviceType = store.DeviceTypeUSBSIMReader
	config.DeviceBackend = store.CardRuntimeModeQMI
	config.ControlDevice = "reader-serial"
	if err := r.database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	d.entry.ID = "dev1"
	d.entry.Candidate.HardwareKind = pcsc.HardwareKind
	d.entry.Candidate.SerialNumber = "reader-serial"
	if err := r.database.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID: oldCardICCID, NetworkEnabled: true, VoWiFiEnabled: false,
		AirplaneEnabled: false, APN: "ignored.apn", RuntimeMode: store.CardRuntimeModeQMI,
		CellularIMSMode: store.CellularIMSModeForceEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	d.entry.Snapshot.FlightMode = false

	r.reconcile(context.Background())
	policy, err := r.database.CardPolicy(context.Background(), oldCardICCID)
	if err != nil || !policy.VoWiFiEnabled || !policy.AirplaneEnabled ||
		policy.NetworkEnabled || policy.RuntimeMode != store.CardRuntimeModePCSC {
		t.Fatalf("reader policy = %+v, %v", policy, err)
	}
	stored, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	backends := append([]string(nil), d.backends...)
	networks := append([]device.NetworkRequest(nil), d.networks...)
	imsModes := append([]device.CellularIMSMode(nil), d.imsModes...)
	flights := append([]bool(nil), d.flights...)
	d.mu.Unlock()
	if stored.DeviceBackend != store.CardRuntimeModePCSC || len(imsModes) != 0 {
		t.Fatalf("reader runtime calls = config=%+v backends=%v ims=%v", stored, backends, imsModes)
	}
	for _, backend := range backends {
		if backend != store.CardRuntimeModePCSC {
			t.Fatalf("reader selected non-PCSC backend %q", backend)
		}
	}
	if len(w.enables) != 1 || !w.enables[0] || len(flights) != 1 || !flights[0] {
		t.Fatalf("reader RF policy = wifi=%v flights=%v", w.enables, flights)
	}
	for _, request := range networks {
		if request.Enabled {
			t.Fatalf("reader enabled cellular network: %+v", request)
		}
	}
}

func TestCardPolicyReconcileReaderCreatesMissingPolicyAndRequestsVoWiFi(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	config.DeviceType = store.DeviceTypeUSBSIMReader
	config.DeviceBackend = store.CardRuntimeModeAT
	config.ControlDevice = "reader-serial"
	if err := r.database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	d.entry.ID = "dev1"
	d.entry.Candidate.HardwareKind = pcsc.HardwareKind
	d.entry.Candidate.SerialNumber = "reader-serial"
	if err := r.database.DeleteCardPolicy(context.Background(), oldCardICCID); err != nil {
		t.Fatal(err)
	}

	r.reconcile(context.Background())

	policy, err := r.database.CardPolicy(context.Background(), oldCardICCID)
	if err != nil || policy.Source != "default" || !policy.VoWiFiEnabled ||
		!policy.AirplaneEnabled || policy.NetworkEnabled ||
		policy.RuntimeMode != store.CardRuntimeModePCSC {
		t.Fatalf("recreated reader policy = %+v, %v", policy, err)
	}
	if len(w.enables) != 1 || !w.enables[0] {
		t.Fatalf("reader VoWiFi enable requests = %v", w.enables)
	}
}

func TestCardPolicyReconcileResolvesESIMIdentityOnlyOnce(t *testing.T) {
	r, d, _ := newPolicyReconcileFixture(t)
	ctx := context.Background()
	card, err := r.database.SimCard(ctx, oldCardICCID)
	if err != nil {
		t.Fatal(err)
	}
	card.CardType = store.SimCardTypeESIM
	card.EID = ""
	card.ProfileAID = ""
	if err := r.database.UpsertSimCard(ctx, card); err != nil {
		t.Fatal(err)
	}
	d.inventory = []device.EsimInventoryEntry{{
		Info: device.EsimInfo{
			EID: "89000000000000000000000000000001",
			Profiles: []device.EsimProfile{{
				ICCID: oldCardICCID,
				AID:   "AID-OLD",
			}},
		},
	}}

	r.reconcile(ctx)
	r.reconcile(ctx)

	if d.inventoryReads != 1 {
		t.Fatalf("eSIM inventory reads = %d, want 1", d.inventoryReads)
	}
	card, err = r.database.SimCard(ctx, oldCardICCID)
	if err != nil || card.EID != "89000000000000000000000000000001" || card.ProfileAID != "AID-OLD" {
		t.Fatalf("persisted eSIM identity = %+v, %v", card, err)
	}
}

func TestCardPolicyReconcileAppliesAllCellularIMSModesOnce(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
		want device.CellularIMSMode
	}{
		{name: "MBN default", mode: store.CellularIMSModeMBNDefault, want: device.CellularIMSModeMBNDefault},
		{name: "force enabled", mode: store.CellularIMSModeForceEnabled, want: device.CellularIMSModeForceEnabled},
		{name: "force disabled", mode: store.CellularIMSModeForceDisabled, want: device.CellularIMSModeForceDisabled},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, d, _ := newPolicyReconcileFixture(t)
			policy, err := r.database.CardPolicy(context.Background(), oldCardICCID)
			if err != nil {
				t.Fatal(err)
			}
			policy.CellularIMSMode = test.mode
			if err := r.database.UpsertCardPolicy(context.Background(), policy); err != nil {
				t.Fatal(err)
			}

			r.reconcile(context.Background())
			r.reconcile(context.Background())
			d.mu.Lock()
			modes := append([]device.CellularIMSMode(nil), d.imsModes...)
			d.mu.Unlock()
			if len(modes) != 1 || modes[0] != test.want {
				t.Fatalf("CellularIMS calls = %v, want one %q", modes, test.want)
			}
		})
	}
}

func TestCardPolicyReconcileUnregisteredCardNeverTouchesCellularIMS(t *testing.T) {
	r, d, w := newPolicyReconcileFixture(t)
	d.entry.Snapshot.ICCID = unseenCardICCID
	w.state = vowifi.State{Enabled: true, Active: true, ICCID: unseenCardICCID, Phase: vowifi.PhaseIMSReady}
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	config.VoWiFiEnabled = true
	config.NetworkEnabled = true
	if err := r.database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}

	r.reconcile(context.Background())
	d.mu.Lock()
	imsModes := append([]device.CellularIMSMode(nil), d.imsModes...)
	networks := append([]device.NetworkRequest(nil), d.networks...)
	d.mu.Unlock()
	if len(imsModes) != 0 {
		t.Fatalf("unregistered card touched CellularIMS: %v", imsModes)
	}
	if len(networks) != 1 || networks[0].Enabled {
		t.Fatalf("unregistered card network calls = %+v", networks)
	}
	stored, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.VoWiFiEnabled || stored.NetworkEnabled {
		t.Fatalf("unregistered card retained runtime intent: %+v", stored)
	}
}

func TestCardPolicyReconcileStoresESIMBindingIdentity(t *testing.T) {
	r, d, _ := newPolicyReconcileFixture(t)
	if err := r.database.UpsertSimCard(context.Background(), store.SimCard{
		ICCID: oldCardICCID, CardType: store.SimCardTypeESIM,
	}); err != nil {
		t.Fatal(err)
	}
	d.inventory = []device.EsimInventoryEntry{{
		Info: device.EsimInfo{
			EID: "89000000000000000000000000000001",
			Profiles: []device.EsimProfile{{
				ICCID: oldCardICCID,
				AID:   "AID-OLD",
				State: 1,
			}},
		},
	}}

	r.reconcile(context.Background())
	binding, err := r.database.SimCardBinding(context.Background(), oldCardICCID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.EID != "89000000000000000000000000000001" ||
		binding.ProfileAID != "AID-OLD" {
		t.Fatalf("binding identity = %+v", binding)
	}
}

func TestCardPolicyReconcileMarksStaleRuntimeAndClearsAfterApply(t *testing.T) {
	r, d, _ := newPolicyReconcileFixture(t)
	config, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	config.VoWiFiEnabled = true
	config.NetworkEnabled = true
	config.DeviceBackend = store.CardRuntimeModeAT
	if err := r.database.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}

	r.suspendCardRuntime(context.Background(), config, "physical1", true)
	stored, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	stale := stored.CardRuntimeStale()
	if !stale.Binding || !stale.DeviceBackend || !stale.CellularIMS {
		t.Fatalf("stale state after suspend = %+v", stale)
	}
	d.mu.Lock()
	safeIMS := append([]device.CellularIMSMode(nil), d.imsModes...)
	d.mu.Unlock()
	if len(safeIMS) == 0 || safeIMS[len(safeIMS)-1] != device.CellularIMSModeMBNDefault {
		t.Fatalf("safe IMS default calls = %v", safeIMS)
	}

	stored.DeviceBackend = store.CardRuntimeModeAT
	r.applyRuntimeMode(context.Background(), &stored, "physical1", store.CardPolicy{
		ICCID: oldCardICCID, RuntimeMode: store.CardRuntimeModeQMI,
	})
	r.applyCellularIMSPolicy(context.Background(), stored, "physical1", store.CardPolicy{
		ICCID: oldCardICCID, CellularIMSMode: store.CellularIMSModeForceEnabled,
	})
	stored, err = r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	stale = stored.CardRuntimeStale()
	if stale.DeviceBackend || stale.CellularIMS {
		t.Fatalf("stale state after apply = %+v", stale)
	}
}

func TestCardPolicyReconcilePhysicalSIMDoesNotRequireEUICC(t *testing.T) {
	r, d, _ := newPolicyReconcileFixture(t)
	d.inventoryErr = device.ErrNoEUICC

	r.reconcile(context.Background())
	binding, err := r.database.SimCardBinding(context.Background(), oldCardICCID)
	if err != nil || binding.State != store.SimCardBindingActive {
		t.Fatalf("physical SIM binding = %+v, %v", binding, err)
	}
}

func TestCardPolicyReconcileEmptyModesFollowHardwareWithoutStale(t *testing.T) {
	r, d, _ := newPolicyReconcileFixture(t)
	policy, err := r.database.CardPolicy(context.Background(), oldCardICCID)
	if err != nil {
		t.Fatal(err)
	}
	policy.RuntimeMode = ""
	policy.CellularIMSMode = ""
	if err := r.database.UpsertCardPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}

	r.reconcile(context.Background())
	stored, err := r.database.Device(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	stale := stored.CardRuntimeStale()
	if stale.DeviceBackend || stale.CellularIMS {
		t.Fatalf("empty modes marked stale: %+v", stale)
	}
	d.mu.Lock()
	modes := append([]device.CellularIMSMode(nil), d.imsModes...)
	d.mu.Unlock()
	if len(modes) != 0 {
		t.Fatalf("empty cellular IMS mode touched module: %v", modes)
	}
}
