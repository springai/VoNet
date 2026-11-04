package vowifi

import (
	"context"
	"testing"
)

func TestATBindingAndCheckpointAreIsolatedByDeviceGenerationAndICCID(t *testing.T) {
	adapter := &atModemAdapter{
		bindings:    make(map[atStateKey]atSIMBinding),
		checkpoints: make(map[atStateKey]atRadioCheckpoint),
	}
	ctx := context.WithValue(
		context.Background(),
		atOperationKey{},
		atOperation{physicalID: "physical-1", generation: "usb-1"},
	)
	identity := SIMIdentity{
		DeviceID: "configured-1",
		ICCID:    "8900000000000000001",
		IMSI:     "001010123456789",
	}
	key := adapter.stateKey(ctx, identity.DeviceID, identity.ICCID)
	adapter.bindings[key] = atSIMBinding{
		deviceID: identity.DeviceID,
		iccid:    identity.ICCID,
		imsi:     identity.IMSI,
	}
	adapter.checkpoints[key] = atRadioCheckpoint{activeCIDs: []int{1}}

	if _, err := adapter.bindingFor(ctx, identity); err != nil {
		t.Fatalf("same device/generation/ICCID binding was not reusable: %v", err)
	}

	otherGeneration := context.WithValue(
		context.Background(),
		atOperationKey{},
		atOperation{physicalID: "physical-1", generation: "usb-2"},
	)
	if _, err := adapter.bindingFor(otherGeneration, identity); err == nil {
		t.Fatal("binding leaked across USB generations")
	}

	otherICCID := identity
	otherICCID.ICCID = "8900000000000000002"
	if _, err := adapter.bindingFor(ctx, otherICCID); err == nil {
		t.Fatal("binding leaked across ICCIDs")
	}

	otherDevice := identity
	otherDevice.DeviceID = "configured-2"
	if _, err := adapter.bindingFor(ctx, otherDevice); err == nil {
		t.Fatal("binding leaked across configured devices")
	}

	if len(adapter.checkpoints) != 1 {
		t.Fatalf("checkpoint map changed during isolation checks: %#v", adapter.checkpoints)
	}
}
