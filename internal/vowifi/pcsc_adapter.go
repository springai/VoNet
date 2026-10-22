package vowifi

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"vonet/internal/pcsc"
	"vonet/internal/pcscimei"
)

// pcscIMEIPool is the predefined set of IMEIs used by USB SIM card readers
// during Wi-Fi Calling. A card reader has no modem, so it cannot report a
// hardware IMEI. These values are rotated on every VoWiFi attempt and retry.
var pcscIMEIPool = pcscimei.DefaultPool

type PCSCBindingResolver func(context.Context, string) (pcsc.Selector, string, error)

// PCSCAdapter uses a directly attached USB smart-card reader as the UICC for
// Wi-Fi Calling. It deliberately exposes no cellular-radio behaviour.
type PCSCAdapter struct {
	service  *pcsc.Service
	resolve  PCSCBindingResolver
	mu       sync.RWMutex
	bindings map[string]string
	// imeiMap tracks the current random IMEI assigned to each device so that
	// the device manager can expose it in the hardware snapshot.
	imeiMap          map[string]string
	authFailed       map[string]bool
	consecutiveFails map[string]int
}

var (
	_ SIMIdentityReader                = (*PCSCAdapter)(nil)
	_ SIMIdentityReaderWithoutRotation = (*PCSCAdapter)(nil)
	_ SMSCenterReader                  = (*PCSCAdapter)(nil)
	_ AKAProvider                      = (*PCSCAdapter)(nil)
	_ PreferredAKAProvider             = (*PCSCAdapter)(nil)
	_ RadioController                  = (*PCSCAdapter)(nil)
)

func NewPCSCAdapter(service *pcsc.Service, resolver PCSCBindingResolver) (*PCSCAdapter, error) {
	if service == nil || resolver == nil {
		return nil, errors.New("vonet: PC/SC service and reader resolver are required")
	}
	return &PCSCAdapter{
		service:          service,
		resolve:          resolver,
		bindings:         make(map[string]string),
		imeiMap:          make(map[string]string),
		authFailed:       make(map[string]bool),
		consecutiveFails: make(map[string]int),
	}, nil
}

// MarkAuthFailed records that the device experienced an EAP-AKA AUTH_FAILED
// (responder fatal notification 7). Subsequent ReadIdentity calls will NOT
// rotate the IMEI until MarkAuthSuccess clears the flag, preventing further
// SQN desynchronization with the HSS.
// pcscAuthFailRotateThreshold is the number of consecutive AUTH_FAILED
// failures after which the IMEI is forcibly rotated even in the authFailed
// state. This prevents getting permanently stuck on a blacklisted IMEI.
const pcscAuthFailRotateThreshold = 3

func (adapter *PCSCAdapter) MarkAuthFailed(deviceID string) {
	id := strings.TrimSpace(deviceID)
	adapter.mu.Lock()
	adapter.authFailed[id] = true
	adapter.consecutiveFails[id]++
	adapter.mu.Unlock()
}

// MarkAuthSuccess clears the auth-failed flag after a successful tunnel
// establishment, allowing normal IMEI rotation on the next attempt.
func (adapter *PCSCAdapter) MarkAuthSuccess(deviceID string) {
	id := strings.TrimSpace(deviceID)
	adapter.mu.Lock()
	adapter.authFailed[id] = false
	adapter.consecutiveFails[id] = 0
	adapter.mu.Unlock()
}

// CurrentIMEI returns the most recently assigned random IMEI for the given
// device. Returns an empty string if no IMEI has been assigned yet.
func (adapter *PCSCAdapter) CurrentIMEI(deviceID string) string {
	adapter.mu.RLock()
	imei := adapter.imeiMap[strings.TrimSpace(deviceID)]
	adapter.mu.RUnlock()
	return imei
}

// rotateIMEI picks a random IMEI from the pool that differs from the current
// one (when possible) and stores it for the device.
func (adapter *PCSCAdapter) rotateIMEI(deviceID string) string {
	deviceID = strings.TrimSpace(deviceID)
	if len(pcscIMEIPool) == 0 {
		return ""
	}
	previous := adapter.imeiMap[deviceID]
	var next string
	if len(pcscIMEIPool) == 1 {
		next = pcscIMEIPool[0]
	} else {
		for {
			next = pcscIMEIPool[rand.Intn(len(pcscIMEIPool))]
			if next != previous {
				break
			}
		}
	}
	adapter.mu.Lock()
	adapter.imeiMap[deviceID] = next
	adapter.mu.Unlock()
	return next
}

func pcscHomeMCC(imsi string, _ int) string {
	if isDITORoamingIMSIPrefix(imsi) {
		if mcc, _, ok := assignedHomePLMN(imsi); ok {
			return mcc
		}
	}
	return imsi[:3]
}

func pcscHomeMNC(imsi string, mncLength int) string {
	if isDITORoamingIMSIPrefix(imsi) {
		if _, mnc, ok := assignedHomePLMN(imsi); ok {
			return mnc
		}
	}
	return imsi[3 : 3+mncLength]
}

func isDITORoamingIMSIPrefix(imsi string) bool {
	return strings.HasPrefix(imsi, "204047616") || strings.HasPrefix(imsi, "204043610")
}

func (adapter *PCSCAdapter) ReadIdentity(ctx context.Context, deviceID string) (SIMIdentity, error) {
	return adapter.readIdentity(ctx, deviceID, true)
}

func (adapter *PCSCAdapter) ReadIdentityWithoutRotation(ctx context.Context, deviceID string) (SIMIdentity, error) {
	return adapter.readIdentity(ctx, deviceID, false)
}

func (adapter *PCSCAdapter) readIdentity(ctx context.Context, deviceID string, rotate bool) (SIMIdentity, error) {
	// Assign IMEI early so it is available even if the SIM read fails.
	id := strings.TrimSpace(deviceID)
	adapter.mu.RLock()
	currentIMEI := adapter.imeiMap[id]
	authBlocked := adapter.authFailed[id] && adapter.consecutiveFails[id] < pcscAuthFailRotateThreshold
	adapter.mu.RUnlock()
	imei := currentIMEI
	if (rotate && !authBlocked) || imei == "" {
		imei = adapter.rotateIMEI(deviceID)
	}

	// Ensure IMEI is always stored for this device
	if imei != "" {
		adapter.mu.Lock()
		adapter.imeiMap[id] = imei
		adapter.mu.Unlock()
	}

	selector, pin, err := adapter.resolve(ctx, id)
	if err != nil {
		return SIMIdentity{IMEI: imei}, err
	}

	// Also store IMEI under the new Reader- format ID
	usbSerial := strings.TrimSpace(selector.USBSerial)
	if usbSerial != "" {
		newID := "Reader-" + usbSerial
		if newID != id {
			adapter.mu.Lock()
			adapter.imeiMap[newID] = imei
			adapter.mu.Unlock()
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
	if identity.IMSI != "" {
		adapter.bindings[identity.IMSI] = id
	}
	mncLength := identity.MNCLength
	if mncLength != 2 && mncLength != 3 {
		if mcc, mnc, ok := assignedHomePLMN(identity.IMSI); ok {
			return applyAssignedCarrierRoute(SIMIdentity{ICCID: identity.ICCID, IMSI: identity.IMSI, ProvisionedIMSI: identity.ProvisionedIMSI, IMEI: imei, HomeMCC: mcc, HomeMNC: mnc, SMSC: identity.SMSC, SPN: identity.SPN}), nil
		}
		return SIMIdentity{}, ErrEC20MNCUnavailable
	}
	if len(identity.IMSI) < 3+mncLength {
		return SIMIdentity{}, errors.New("vonet: USB SIM IMSI is shorter than its EF_AD home PLMN")
	}
	return applyAssignedCarrierRoute(SIMIdentity{
		ICCID: identity.ICCID, IMSI: identity.IMSI, ProvisionedIMSI: identity.ProvisionedIMSI, IMEI: imei,
		HomeMCC: pcscHomeMCC(identity.IMSI, mncLength), HomeMNC: pcscHomeMNC(identity.IMSI, mncLength),
		SMSC: identity.SMSC, SPN: identity.SPN,
	}), nil
}

func (adapter *PCSCAdapter) ReadSMSCenter(ctx context.Context, deviceID string) (string, error) {
	selector, pin, err := adapter.resolve(ctx, strings.TrimSpace(deviceID))
	if err != nil {
		return "", err
	}
	identity, err := adapter.service.ReadIdentity(ctx, selector, pin)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(identity.SMSC) == "" {
		return "", errors.New("vonet: USB SIM does not expose a service-centre address")
	}
	return identity.SMSC, nil
}

func (adapter *PCSCAdapter) CheckReady(ctx context.Context, identity SIMIdentity) (AKAEvidence, error) {
	selector, pin, err := adapter.resolve(ctx, adapter.deviceID(identity))
	if err != nil {
		return AKAEvidence{}, err
	}
	aid, err := adapter.service.CheckReady(ctx, selector, identity.ICCID, pin)
	if err != nil {
		return AKAEvidence{}, fmt.Errorf("check USB SIM AKA application: %w", err)
	}
	return AKAEvidence{Ready: true, Application: aid}, nil
}

func (adapter *PCSCAdapter) deviceID(identity SIMIdentity) string {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	if id, ok := adapter.bindings[identity.ICCID]; ok && id != "" {
		return id
	}
	if id, ok := adapter.bindings[identity.IMSI]; ok && id != "" {
		return id
	}
	var candidate string
	for _, id := range adapter.bindings {
		if id == "" {
			continue
		}
		if candidate == "" {
			candidate = id
		} else if candidate != id {
			return ""
		}
	}
	return candidate
}

func (adapter *PCSCAdapter) Authenticate(ctx context.Context, identity SIMIdentity, challenge AKAChallenge) (AKAResult, error) {
	return adapter.AuthenticateWithPreference(ctx, identity, challenge, "")
}

func (adapter *PCSCAdapter) AuthenticateWithPreference(
	ctx context.Context,
	identity SIMIdentity,
	challenge AKAChallenge,
	preference string,
) (AKAResult, error) {
	selector, pin, err := adapter.resolve(ctx, adapter.deviceID(identity))
	if err != nil {
		return AKAResult{}, err
	}
	result, err := adapter.service.AuthenticateWithPreference(
		ctx,
		selector,
		identity.ICCID,
		pin,
		pcsc.AKAChallenge(challenge),
		preference,
	)
	if err != nil {
		if errors.Is(err, pcsc.ErrISIMUnavailable) || errors.Is(err, pcsc.ErrUSIMUnavailable) {
			return AKAResult{}, errors.Join(ErrEC20ApplicationAbsent, err)
		}
		if errors.Is(err, pcsc.ErrAKARejected) {
			return AKAResult{}, errors.Join(ErrEC20AKAMACFailure, err)
		}
		return AKAResult{}, fmt.Errorf("authenticate with USB SIM: %w", err)
	}
	return AKAResult(result), nil
}

func (*PCSCAdapter) Snapshot(context.Context, string) (RadioSnapshot, error) {
	return RadioSnapshot{OperatingMode: 4, PureAirplanePolicy: true}, nil
}
func (*PCSCAdapter) StopCellularData(context.Context, string) error       { return nil }
func (*PCSCAdapter) EnterVoWiFiRFOff(context.Context, string) error       { return nil }
func (*PCSCAdapter) Restore(context.Context, string, RadioSnapshot) error { return nil }
