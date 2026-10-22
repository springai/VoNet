package vowifi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type PCSCLeaseAdapter struct {
	*PCSCAdapter
	leases PCSCIMEILeaseSource
}

var _ SIMIdentityReaderWithoutRotation = (*PCSCLeaseAdapter)(nil)

func NewPCSCAdapterWithLeases(
	base *PCSCAdapter,
	leases PCSCIMEILeaseSource,
) (*PCSCLeaseAdapter, error) {
	if base == nil || leases == nil {
		return nil, errors.New("vonet: PC/SC adapter and IMEI lease source are required")
	}
	return &PCSCLeaseAdapter{PCSCAdapter: base, leases: leases}, nil
}

func (adapter *PCSCLeaseAdapter) CurrentIMEI(deviceID string) string {
	id := strings.TrimSpace(deviceID)
	imei, _, err := adapter.leases.CurrentPCSCIMEI(context.Background(), id)
	if err != nil {
		return adapter.PCSCAdapter.CurrentIMEI(deviceID)
	}
	rememberPCSCIMEI(adapter.PCSCAdapter, id, imei)
	return imei
}

func (adapter *PCSCLeaseAdapter) ReadIdentity(ctx context.Context, deviceID string) (SIMIdentity, error) {
	return adapter.readIdentity(ctx, deviceID, true)
}

func (adapter *PCSCLeaseAdapter) ReadIdentityWithoutRotation(ctx context.Context, deviceID string) (SIMIdentity, error) {
	return adapter.readIdentity(ctx, deviceID, false)
}

func (adapter *PCSCLeaseAdapter) readIdentity(ctx context.Context, deviceID string, rotate bool) (SIMIdentity, error) {
	id := strings.TrimSpace(deviceID)
	imei, mode, err := adapter.leases.CurrentPCSCIMEI(ctx, id)
	if err != nil {
		return SIMIdentity{}, fmt.Errorf("read PCSC IMEI lease: %w", err)
	}
	adapter.mu.RLock()
	authBlocked := adapter.authFailed[id] && adapter.PCSCAdapter.consecutiveFails[id] < pcscAuthFailRotateThreshold
	adapter.mu.RUnlock()
	if rotate && mode == "random" && !authBlocked {
		if next, _, rotateErr := adapter.leases.RotatePCSCIMEI(ctx, id); rotateErr == nil {
			imei = next
		}
	}
	rememberPCSCIMEI(adapter.PCSCAdapter, id, imei)

	selector, pin, err := adapter.resolve(ctx, id)
	if err != nil {
		return SIMIdentity{IMEI: imei}, err
	}
	if usbSerial := strings.TrimSpace(selector.USBSerial); usbSerial != "" {
		if newID := "Reader-" + usbSerial; newID != id {
			rememberPCSCIMEI(adapter.PCSCAdapter, newID, imei)
		}
	}
	identity, err := adapter.service.ReadIdentity(ctx, selector, pin)
	if err != nil {
		return SIMIdentity{IMEI: imei}, fmt.Errorf("read USB SIM identity: %w", err)
	}
	if len(identity.IMSI) < 5 {
		return SIMIdentity{IMEI: imei}, errors.New("vonet: USB SIM reader returned an invalid IMSI")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.bindings[identity.ICCID] = id
	mncLength := identity.MNCLength
	if mncLength != 2 && mncLength != 3 {
		if mcc, mnc, ok := assignedHomePLMN(identity.IMSI); ok {
			return applyAssignedCarrierRoute(SIMIdentity{
				ICCID: identity.ICCID, IMSI: identity.IMSI, ProvisionedIMSI: identity.ProvisionedIMSI,
				IMEI: imei, HomeMCC: mcc, HomeMNC: mnc, SMSC: identity.SMSC, SPN: identity.SPN,
			}), nil
		}
		return SIMIdentity{}, ErrEC20MNCUnavailable
	}
	if len(identity.IMSI) < 3+mncLength {
		return SIMIdentity{}, errors.New("vonet: USB SIM IMSI is shorter than its EF_AD home PLMN")
	}
	return applyAssignedCarrierRoute(SIMIdentity{
		ICCID: identity.ICCID, IMSI: identity.IMSI, ProvisionedIMSI: identity.ProvisionedIMSI,
		IMEI: imei, HomeMCC: pcscHomeMCC(identity.IMSI, mncLength),
		HomeMNC: pcscHomeMNC(identity.IMSI, mncLength), SMSC: identity.SMSC, SPN: identity.SPN,
	}), nil
}

type PCSCIMEILeaseSource interface {
	CurrentPCSCIMEI(ctx context.Context, deviceID string) (string, string, error)
	RotatePCSCIMEI(ctx context.Context, deviceID string) (string, bool, error)
}
