package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

var errVoWiFiAdapterIdentityChanged = errors.New("VoWiFi modem identity changed during an adapter operation")

type atVoWiFiAdapter interface {
	vowifiDeviceAdapter
	vowifi.SMSCenterReader
	ReadSMSCenterPSI(context.Context, string) (string, error)
}

type atAdapterIdentity struct {
	hardwareKind  string
	vendorID      string
	productID     string
	usbGeneration string
	atPath        string
}

// resolvedATVoWiFiAdapter selects the verified vendor dialect at operation
// start and keeps that adapter fixed until the operation ends. A lookup failure
// retains the last adapter so teardown can still restore the radio checkpoint.
type resolvedATVoWiFiAdapter struct {
	mu                  sync.Mutex
	lookup              func(context.Context) (device.Device, error)
	ec20, zxic          atVoWiFiAdapter
	selected            atVoWiFiAdapter
	identity            atAdapterIdentity
	identityInitialized bool
	simICCID            string
	simIMSI             string
	activeOperations    int
}

type resolvedATOperation struct {
	adapter atVoWiFiAdapter
	release func()
}

func voWiFiAdapterForDevice(
	deviceType string,
	lookup func(context.Context) (device.Device, error),
	ec20, zxic atVoWiFiAdapter,
	pcsc, native vowifiDeviceAdapter,
) vowifiDeviceAdapter {
	switch deviceType {
	case store.DeviceTypeUSBSIMReader:
		return pcsc
	case store.DeviceTypeWiFi410:
		return native
	default:
		return newResolvedATVoWiFiAdapter(deviceType, lookup, ec20, zxic)
	}
}

func newResolvedATVoWiFiAdapter(
	deviceType string,
	lookup func(context.Context) (device.Device, error),
	ec20, zxic atVoWiFiAdapter,
) *resolvedATVoWiFiAdapter {
	selected := ec20
	if deviceType == store.DeviceTypeZXIC {
		selected = zxic
	}
	return &resolvedATVoWiFiAdapter{
		lookup:   lookup,
		ec20:     ec20,
		zxic:     zxic,
		selected: selected,
	}
}

func atAdapterForIdentity(identity atAdapterIdentity, ec20, zxic atVoWiFiAdapter) atVoWiFiAdapter {
	if modem.ChipsetForUSB(identity.vendorID, identity.productID) == modem.ChipsetZXIC {
		return zxic
	}
	return ec20
}

// current refreshes the cached adapter without pinning an operation. Lookup
// happens outside the state lock so a canceled or slow lookup cannot block
// another caller. Offline lookup retains the previous adapter.
func (adapter *resolvedATVoWiFiAdapter) current(ctx context.Context) atVoWiFiAdapter {
	entry, lookupErr := adapter.lookup(ctx)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if lookupErr != nil || !entry.Discovered {
		if adapter.selected != nil {
			return adapter.selected
		}
		return adapter.ec20
	}
	identity := atAdapterIdentity{
		hardwareKind:  entry.Candidate.HardwareKind,
		vendorID:      entry.Candidate.VendorID,
		productID:     entry.Candidate.ProductID,
		usbGeneration: entry.Candidate.USBGeneration,
		atPath:        entry.Candidate.ATPort.OpenPath(),
	}
	if !adapter.identityInitialized || identity != adapter.identity {
		adapter.identity = identity
		adapter.identityInitialized = true
		adapter.simICCID = ""
		adapter.simIMSI = ""
	}
	adapter.selected = atAdapterForIdentity(identity, adapter.ec20, adapter.zxic)
	return adapter.selected
}

// beginOperation resolves once and returns the adapter that every command in
// this operation must use. Identity changes are accepted only after preceding
// operations finish, preventing one transaction from mixing two modems or SIMs.
func (adapter *resolvedATVoWiFiAdapter) beginOperation(
	ctx context.Context,
	allowStale bool,
) (resolvedATOperation, error) {
	entry, lookupErr := adapter.lookup(ctx)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	if lookupErr != nil || !entry.Discovered {
		if allowStale && adapter.selected != nil {
			return adapter.beginLocked(adapter.selected), nil
		}
		if lookupErr != nil {
			return resolvedATOperation{}, lookupErr
		}
		return resolvedATOperation{}, device.ErrNotFound
	}

	identity := atAdapterIdentity{
		hardwareKind:  entry.Candidate.HardwareKind,
		vendorID:      entry.Candidate.VendorID,
		productID:     entry.Candidate.ProductID,
		usbGeneration: entry.Candidate.USBGeneration,
		atPath:        entry.Candidate.ATPort.OpenPath(),
	}
	if adapter.identityInitialized && identity != adapter.identity {
		if adapter.activeOperations > 0 {
			return resolvedATOperation{}, errVoWiFiAdapterIdentityChanged
		}
		adapter.simICCID = ""
		adapter.simIMSI = ""
	}
	adapter.identity = identity
	adapter.identityInitialized = true
	selected := atAdapterForIdentity(identity, adapter.ec20, adapter.zxic)
	adapter.selected = selected
	return adapter.beginLocked(selected), nil
}

func (adapter *resolvedATVoWiFiAdapter) beginLocked(selected atVoWiFiAdapter) resolvedATOperation {
	adapter.activeOperations++
	released := false
	return resolvedATOperation{
		adapter: selected,
		release: func() {
			if released {
				return
			}
			released = true
			adapter.mu.Lock()
			adapter.activeOperations--
			adapter.mu.Unlock()
		},
	}
}

func (adapter *resolvedATVoWiFiAdapter) checkSIMIdentity(identity vowifi.SIMIdentity) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.simICCID != "" && adapter.simICCID != identity.ICCID {
		return errVoWiFiAdapterIdentityChanged
	}
	if adapter.simIMSI != "" && adapter.simIMSI != identity.IMSI {
		return errVoWiFiAdapterIdentityChanged
	}
	return nil
}

func (adapter *resolvedATVoWiFiAdapter) acceptSIMIdentity(identity vowifi.SIMIdentity) {
	adapter.mu.Lock()
	adapter.simICCID = identity.ICCID
	adapter.simIMSI = identity.IMSI
	adapter.mu.Unlock()
}

func (adapter *resolvedATVoWiFiAdapter) ReadIdentity(ctx context.Context, id string) (vowifi.SIMIdentity, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return vowifi.SIMIdentity{}, err
	}
	defer operation.release()
	identity, err := operation.adapter.ReadIdentity(ctx, id)
	if err == nil {
		adapter.acceptSIMIdentity(identity)
	}
	return identity, err
}

func (adapter *resolvedATVoWiFiAdapter) CheckReady(ctx context.Context, identity vowifi.SIMIdentity) (vowifi.AKAEvidence, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return vowifi.AKAEvidence{}, err
	}
	defer operation.release()
	if err := adapter.checkSIMIdentity(identity); err != nil {
		return vowifi.AKAEvidence{}, err
	}
	return operation.adapter.CheckReady(ctx, identity)
}

func (adapter *resolvedATVoWiFiAdapter) Authenticate(
	ctx context.Context,
	identity vowifi.SIMIdentity,
	challenge vowifi.AKAChallenge,
) (vowifi.AKAResult, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return vowifi.AKAResult{}, err
	}
	defer operation.release()
	if err := adapter.checkSIMIdentity(identity); err != nil {
		return vowifi.AKAResult{}, err
	}
	return operation.adapter.Authenticate(ctx, identity, challenge)
}

func (adapter *resolvedATVoWiFiAdapter) AuthenticateWithPreference(
	ctx context.Context,
	identity vowifi.SIMIdentity,
	challenge vowifi.AKAChallenge,
	preference string,
) (vowifi.AKAResult, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return vowifi.AKAResult{}, err
	}
	defer operation.release()
	if err := adapter.checkSIMIdentity(identity); err != nil {
		return vowifi.AKAResult{}, err
	}
	return operation.adapter.AuthenticateWithPreference(ctx, identity, challenge, preference)
}

func (adapter *resolvedATVoWiFiAdapter) Snapshot(ctx context.Context, id string) (vowifi.RadioSnapshot, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return vowifi.RadioSnapshot{}, err
	}
	defer operation.release()
	return operation.adapter.Snapshot(ctx, id)
}

func (adapter *resolvedATVoWiFiAdapter) StopCellularData(ctx context.Context, id string) error {
	operation, err := adapter.beginOperation(ctx, true)
	if err != nil {
		return err
	}
	defer operation.release()
	return operation.adapter.StopCellularData(ctx, id)
}

func (adapter *resolvedATVoWiFiAdapter) EnterVoWiFiRFOff(ctx context.Context, id string) error {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return err
	}
	defer operation.release()
	return operation.adapter.EnterVoWiFiRFOff(ctx, id)
}

func (adapter *resolvedATVoWiFiAdapter) Restore(
	ctx context.Context,
	id string,
	snapshot vowifi.RadioSnapshot,
) error {
	operation, err := adapter.beginOperation(ctx, true)
	if err != nil {
		return err
	}
	defer operation.release()
	return operation.adapter.Restore(ctx, id, snapshot)
}

func (adapter *resolvedATVoWiFiAdapter) ReadSMSCenter(ctx context.Context, id string) (string, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return "", err
	}
	defer operation.release()
	return operation.adapter.ReadSMSCenter(ctx, id)
}

func (adapter *resolvedATVoWiFiAdapter) ReadSMSCenterPSI(ctx context.Context, id string) (string, error) {
	operation, err := adapter.beginOperation(ctx, false)
	if err != nil {
		return "", err
	}
	defer operation.release()
	return operation.adapter.ReadSMSCenterPSI(ctx, id)
}

func (adapter *resolvedATVoWiFiAdapter) String() string {
	return fmt.Sprintf("resolved AT VoWiFi adapter(%p)", adapter)
}
