package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/store"
)

// fakeModemClient is a minimal scripted modem.Client for exercising the region
// enforcement orchestration (flight-mode flips) without hardware.
type fakeModemClient struct {
	steps []fakeStep
	index int
}

type fakeStep struct {
	command string
	lines   []string
}

func (client *fakeModemClient) Execute(_ context.Context, command string) (modem.Response, error) {
	if client.index >= len(client.steps) {
		return modem.Response{}, fmt.Errorf("unexpected command %q", command)
	}
	step := client.steps[client.index]
	client.index++
	if command != step.command {
		return modem.Response{}, fmt.Errorf("command %q, want %q", command, step.command)
	}
	return modem.Response{Command: command, Lines: step.lines, Final: "OK"}, nil
}

func (client *fakeModemClient) WaitURC(context.Context, func(string) bool) (string, error) {
	return "", errors.New("no URC scripted")
}

func (client *fakeModemClient) Close() error { return nil }

func (client *fakeModemClient) assertExhausted(t *testing.T) {
	t.Helper()
	if client.index != len(client.steps) {
		t.Fatalf("consumed %d of %d scripted commands", client.index, len(client.steps))
	}
}

type fakeDiscoverer struct{ candidates []modem.Candidate }

func (discoverer fakeDiscoverer) Discover(context.Context) ([]modem.Candidate, error) {
	return discoverer.candidates, nil
}

type provisioningPCSCBackend struct{ readers []pcsc.Reader }

func (backend provisioningPCSCBackend) Readers(context.Context) ([]pcsc.Reader, error) {
	return append([]pcsc.Reader(nil), backend.readers...), nil
}

func (provisioningPCSCBackend) Open(context.Context, pcsc.Selector) (pcsc.Card, error) {
	return nil, pcsc.ErrNoCard
}

type fakeOpener struct{ client modem.Client }

func (opener fakeOpener) Open(context.Context, modem.Port) (modem.Client, error) {
	return opener.client, nil
}

const regionTestDeviceID = "quectel-region-test"

func newProvisioningManager(
	t *testing.T,
	candidates []modem.Candidate,
	readers []pcsc.Reader,
) *device.Manager {
	t.Helper()
	manager, err := device.NewManager(device.Options{
		Discoverer:  fakeDiscoverer{candidates: candidates},
		Opener:      fakeOpener{},
		CardReaders: pcsc.NewWithBackend(provisioningPCSCBackend{readers: readers}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background()) })
	return manager
}

func TestProvisionDiscoveredDevicesCreatesPCSCLeaseAndIdentity(t *testing.T) {
	ctx := context.Background()
	database := newRegionTestStore(t)
	manager := newProvisioningManager(t, nil, []pcsc.Reader{{
		Name: "TEST000000010100", ReaderName: "TEST000000010100", USBSerial: "TEST00000001",
		USBPath: "1-3", VendorID: "0bda", ProductID: "0165", Product: "USB CCID Reader",
	}})
	if err := provisionDiscoveredDevices(ctx, database, manager); err != nil {
		t.Fatal(err)
	}
	config, err := database.Device(ctx, "Reader-TEST00000001")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := database.PCSCIMEILease(ctx, config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mode != "random" || lease.IMEI == "" || config.DeviceType != store.DeviceTypeUSBSIMReader {
		t.Fatalf("config = %+v, lease = %+v", config, lease)
	}
	extra := string(config.Extra)
	for _, fragment := range []string{
		`"usb_serial_number":"TEST00000001"`,
		`"usb_vendor_id":"0bda"`,
		`"usb_product_id":"0165"`,
	} {
		if !strings.Contains(extra, fragment) {
			t.Fatalf("hardware identity %q missing from %s", fragment, extra)
		}
	}
}

func TestProvisionDiscoveredDevicesSkipsIssueAndNoAT(t *testing.T) {
	ctx := context.Background()
	database := newRegionTestStore(t)
	manager := newProvisioningManager(t, []modem.Candidate{
		{
			ID:             "mhi-wwan0",
			HardwareKind:   "wwan",
			DiscoveryIssue: "at_port_missing",
		},
		{
			ID:           "mhi-wwan1",
			HardwareKind: "wwan",
		},
		{
			ID:           "valid-modem",
			HardwareKind: "usb",
			Product:      "EC20",
			ATPort:       modem.Port{Path: "/dev/ttyUSB2", Role: modem.PortRoleAT},
		},
	}, nil)

	if err := provisionDiscoveredDevices(ctx, database, manager); err != nil {
		t.Fatal(err)
	}
	devices, err := database.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != "valid-modem" {
		t.Fatalf("provisioned devices = %#v, want only valid-modem", devices)
	}
}

func TestProvisionDiscoveredDevicesAllowsPCSCWithoutATPort(t *testing.T) {
	ctx := context.Background()
	database := newRegionTestStore(t)
	manager := newProvisioningManager(t, nil, []pcsc.Reader{{
		Name: "NOAT000000010100", ReaderName: "NOAT000000010100", USBSerial: "NOAT00000001",
		USBPath: "1-6", VendorID: "0bda", ProductID: "0165", Product: "USB CCID Reader",
	}})

	entries := manager.List()
	if len(entries) != 1 || entries[0].Candidate.HasATPort() {
		t.Fatalf("PCSC candidates = %#v, want one candidate without an AT port", entries)
	}
	if err := provisionDiscoveredDevices(ctx, database, manager); err != nil {
		t.Fatal(err)
	}
	config, err := database.Device(ctx, "Reader-NOAT00000001")
	if err != nil {
		t.Fatal(err)
	}
	if config.DeviceType != store.DeviceTypeUSBSIMReader || config.DeviceBackend != "pcsc" {
		t.Fatalf("PCSC config = %+v, want USB SIM reader using pcsc", config)
	}
}

func TestProvisionDiscoveredDevicesSkipsExhaustedReaderAndContinues(t *testing.T) {
	ctx := context.Background()
	database := newRegionTestStore(t)
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range pool {
		if err := database.DeletePCSCIMEIPool(ctx, entry.IMEI); err != nil {
			t.Fatal(err)
		}
	}
	manager := newProvisioningManager(t, []modem.Candidate{{
		ID: "ec20-auto", Product: "Quectel EC20",
		ATPort: modem.Port{Path: "/dev/ttyUSB2", Role: modem.PortRoleAT},
	}}, []pcsc.Reader{{
		Name: "SER-pool0000", ReaderName: "SER-pool0000", USBSerial: "SER-pool", USBPath: "1-4",
	}})
	err = provisionDiscoveredDevices(ctx, database, manager)
	if !errors.Is(err, store.ErrPCSCIMEIPoolExhausted) {
		t.Fatalf("provision error = %v, want pool exhausted", err)
	}
	if _, err := database.Device(ctx, "Reader-SER-pool"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("exhausted reader was created without a lease: %v", err)
	}
	if config, err := database.Device(ctx, "ec20-auto"); err != nil || config.DeviceType != store.DeviceTypePCIeEC20EC25 {
		t.Fatalf("later device was not provisioned: %+v, %v", config, err)
	}
}

func TestProvisionDiscoveredDevicesRepairsExistingMissingLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provision-repair.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	reader := store.Device{
		ID: "Reader-existing", Name: "Existing reader", DeviceType: store.DeviceTypeUSBSIMReader,
		ControlDevice: "SER-existing", USBPath: "1-5", DeviceBackend: "pcsc", ESIMTransport: "pcsc",
	}
	if err := database.UpsertDevice(ctx, reader); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM pcsc_imei_leases WHERE device_id = ?`, reader.ID); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	manager := newProvisioningManager(t, nil, nil)
	if err := provisionDiscoveredDevices(ctx, database, manager); err != nil {
		t.Fatal(err)
	}
	if lease, err := database.PCSCIMEILease(ctx, reader.ID); err != nil || lease.Mode != "random" {
		t.Fatalf("repaired existing lease = %+v, %v", lease, err)
	}
}

func newRegionTestManager(t *testing.T, client modem.Client) *device.Manager {
	t.Helper()
	manager, err := device.NewManager(device.Options{
		Discoverer: fakeDiscoverer{candidates: []modem.Candidate{{
			ID:      regionTestDeviceID,
			Product: "EC20",
			ATPort:  modem.Port{Path: "/dev/ttyUSB2", Role: modem.PortRoleAT},
		}}},
		Opener:         fakeOpener{client: client},
		CommandTimeout: time.Second,
		LongTimeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background()) })
	return manager
}

func newRegionTestStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func regionTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEnforceCardRegionNoLongerBlocksChinaSIM(t *testing.T) {
	client := &fakeModemClient{}
	manager := newRegionTestManager(t, client)
	database := newRegionTestStore(t)

	snapshot := &device.Snapshot{
		DeviceID: regionTestDeviceID,
		SIMReady: true,
		IMSI:     "460001234567890",
		ICCID:    "89860012345678901234",
	}
	enforceCardRegion(context.Background(), regionTestLogger(), database, manager, regionTestDeviceID, snapshot)
	client.assertExhausted(t)

	// No block policy should be persisted for China SIM.
	if _, err := database.CardPolicy(context.Background(), snapshot.ICCID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected no block policy for China SIM, got err=%v", err)
	}
}

func TestEnforceCardRegionNoBlockForChinaSIMAlreadyInFlight(t *testing.T) {
	client := &fakeModemClient{}
	manager := newRegionTestManager(t, client)
	database := newRegionTestStore(t)

	snapshot := &device.Snapshot{
		DeviceID:   regionTestDeviceID,
		SIMReady:   true,
		IMSI:       "461001234567890",
		ICCID:      "89860012345678901234",
		FlightMode: true,
	}
	enforceCardRegion(context.Background(), regionTestLogger(), database, manager, regionTestDeviceID, snapshot)
	client.assertExhausted(t)

	// No block policy should be persisted for China SIM.
	if _, err := database.CardPolicy(context.Background(), snapshot.ICCID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected no block policy for China SIM, got err=%v", err)
	}
}

func TestEnforceCardRegionLiftsBlockForAllowedSIM(t *testing.T) {
	client := &fakeModemClient{steps: []fakeStep{
		{command: "AT+CFUN?", lines: []string{"+CFUN: 4"}},
		{command: "AT+CFUN=1"},
		{command: "AT+CFUN?", lines: []string{"+CFUN: 1"}},
	}}
	manager := newRegionTestManager(t, client)
	database := newRegionTestStore(t)

	if err := database.UpsertSimCard(context.Background(), store.SimCard{
		ICCID: "89860012345678901234",
	}); err != nil {
		t.Fatalf("seed block SIM card: %v", err)
	}
	if err := database.UpsertCardPolicy(context.Background(), store.CardPolicy{
		ICCID:           "89860012345678901234",
		AirplaneEnabled: true,
		IPVersion:       "IPV4V6",
		Source:          cardPolicySourceRegionBlock,
	}); err != nil {
		t.Fatalf("seed block policy: %v", err)
	}

	snapshot := &device.Snapshot{
		DeviceID:   regionTestDeviceID,
		SIMReady:   true,
		IMSI:       "310260123456789",
		ICCID:      "89012601234567890123",
		FlightMode: true,
	}
	enforceCardRegion(context.Background(), regionTestLogger(), database, manager, regionTestDeviceID, snapshot)
	client.assertExhausted(t)

	if _, err := database.CardPolicy(context.Background(), "89860012345678901234"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected the auto block policy to be cleared, got err=%v", err)
	}
}

func TestEnforceCardRegionLeavesAllowedSIMWithoutPriorBlockAlone(t *testing.T) {
	client := &fakeModemClient{}
	manager := newRegionTestManager(t, client)
	database := newRegionTestStore(t)

	snapshot := &device.Snapshot{
		DeviceID: regionTestDeviceID,
		SIMReady: true,
		IMSI:     "310260123456789",
		ICCID:    "89012601234567890123",
	}
	enforceCardRegion(context.Background(), regionTestLogger(), database, manager, regionTestDeviceID, snapshot)
	client.assertExhausted(t)
}

func TestEnforceCardRegionIgnoresUnknownOrNotReadySIM(t *testing.T) {
	client := &fakeModemClient{}
	manager := newRegionTestManager(t, client)
	database := newRegionTestStore(t)

	// Not ready: no action at all.
	notReady := &device.Snapshot{DeviceID: regionTestDeviceID, SIMReady: false, IMSI: "460001234567890"}
	enforceCardRegion(context.Background(), regionTestLogger(), database, manager, regionTestDeviceID, notReady)

	// Ready but IMSI unknown: hold state, neither block nor lift.
	unknown := &device.Snapshot{DeviceID: regionTestDeviceID, SIMReady: true, IMSI: ""}
	enforceCardRegion(context.Background(), regionTestLogger(), database, manager, regionTestDeviceID, unknown)

	client.assertExhausted(t)
	policies, err := database.ListCardPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListCardPolicies: %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("expected no card policies, got %d", len(policies))
	}
}

func TestProvisionedDeviceTypeRecognizesNativeWWAN(t *testing.T) {
	native := modem.Candidate{
		HardwareKind: "wwan",
		USBPath:      "/sys/devices/pci0000:00/0000:00:00.0/wwan/wwan0",
		QMIControl:   "/dev/wwan0qmi0",
		ATPort:       modem.Port{Path: "/dev/wwan0at0"},
	}
	if got := provisionedDeviceType(native); got != store.DeviceTypeWiFi410 {
		t.Fatalf("native WWAN type = %q, want %q", got, store.DeviceTypeWiFi410)
	}

	usb := modem.Candidate{
		USBPath:    "/sys/bus/usb/devices/1-6",
		QMIControl: "/dev/cdc-wdm0",
		ATPort:     modem.Port{Path: "/dev/ttyUSB2"},
	}
	if got := provisionedDeviceType(usb); got != store.DeviceTypePCIeEC20EC25 {
		t.Fatalf("USB modem type = %q, want %q", got, store.DeviceTypePCIeEC20EC25)
	}

	// The verified ZXIC USB identity gets the dedicated type so the VoWiFi
	// adapter dispatch selects the ZXIC dialect. A sibling product under the
	// same vendor is not classified: a vendor ID alone does not identify a
	// chipset or its AT dialect.
	zxic := modem.Candidate{
		VendorID: "19d2", ProductID: "0536",
		ATPort: modem.Port{Path: "/dev/ttyUSB2"},
	}
	if got := provisionedDeviceType(zxic); got != store.DeviceTypeZXIC {
		t.Fatalf("ZXIC type = %q, want %q", got, store.DeviceTypeZXIC)
	}

	unverifiedZxic := modem.Candidate{
		VendorID: "19d2", ProductID: "ffff",
		ATPort: modem.Port{Path: "/dev/ttyUSB2"},
	}
	if got := provisionedDeviceType(unverifiedZxic); got == store.DeviceTypeZXIC {
		t.Fatalf("unverified ZXIC sibling type = %q, want a non-ZXIC type", got)
	}
}
