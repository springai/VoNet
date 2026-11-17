package vowifi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"vonet/internal/agentproto"
	"vonet/internal/device"
)

type RemoteAgentExecutor interface {
	Execute(context.Context, string, *agentproto.AgentCommand) (*agentproto.AgentEvent, error)
}

type RemoteDeviceBinding struct {
	AgentID    string
	DeviceID   string
	ReaderID   string
	Generation string
}

type RemoteSIMAdapter struct {
	executor  RemoteAgentExecutor
	resolve   func(context.Context, string) (RemoteDeviceBinding, error)
	onProfile func(context.Context, *agentproto.CardProfile) error
	leases    PCSCIMEILeaseSource

	sessionID        string
	identity         SIMIdentity
	cardGeneration   string
	mu               sync.RWMutex
	authFailed       map[string]bool
	consecutiveFails map[string]int
}

var (
	_ SIMIdentityReader                = (*RemoteSIMAdapter)(nil)
	_ SIMIdentityReaderWithoutRotation = (*RemoteSIMAdapter)(nil)
	_ SMSCenterReader                  = (*RemoteSIMAdapter)(nil)
	_ AKAProvider                      = (*RemoteSIMAdapter)(nil)
	_ PreferredAKAProvider             = (*RemoteSIMAdapter)(nil)
	_ RadioController                  = (*RemoteSIMAdapter)(nil)
)

func NewRemoteSIMAdapter(
	executor RemoteAgentExecutor,
	resolve func(context.Context, string) (RemoteDeviceBinding, error),
	leases PCSCIMEILeaseSource,
	sinks ...func(context.Context, *agentproto.CardProfile) error,
) (*RemoteSIMAdapter, error) {
	if executor == nil || resolve == nil || leases == nil {
		return nil, errors.New("remote SIM executor, binding resolver, and IMEI lease source are required")
	}
	adapter := &RemoteSIMAdapter{
		executor: executor, resolve: resolve, leases: leases,
		authFailed: make(map[string]bool), consecutiveFails: make(map[string]int),
	}
	if len(sinks) > 0 {
		adapter.onProfile = sinks[0]
	}
	return adapter, nil
}

func (adapter *RemoteSIMAdapter) ReadIdentity(ctx context.Context, deviceID string) (SIMIdentity, error) {
	return adapter.readIdentity(ctx, deviceID, true)
}

func (adapter *RemoteSIMAdapter) ReadIdentityWithoutRotation(ctx context.Context, deviceID string) (SIMIdentity, error) {
	return adapter.readIdentity(ctx, deviceID, false)
}

func (adapter *RemoteSIMAdapter) readIdentity(ctx context.Context, deviceID string, rotate bool) (SIMIdentity, error) {
	id := strings.TrimSpace(deviceID)
	imei, mode, err := adapter.leases.CurrentPCSCIMEI(ctx, id)
	if err != nil {
		return SIMIdentity{}, fmt.Errorf("read remote PCSC IMEI lease: %w", err)
	}
	adapter.mu.RLock()
	authBlocked := adapter.authFailed[id] && adapter.consecutiveFails[id] < pcscAuthFailRotateThreshold
	adapter.mu.RUnlock()
	if rotate && mode == "random" && !authBlocked {
		if next, _, rotateErr := adapter.leases.RotatePCSCIMEI(ctx, id); rotateErr == nil {
			imei = next
		}
	}

	binding, err := adapter.resolve(ctx, deviceID)
	if err != nil {
		return SIMIdentity{IMEI: imei}, err
	}
	event, err := adapter.executor.Execute(ctx, binding.AgentID, &agentproto.AgentCommand{
		Command: &agentproto.AgentCommand_CaptureCardProfile{CaptureCardProfile: &agentproto.CaptureCardProfileCommand{
			CommandId: remoteCommandID(), ReaderId: firstNonEmptyRemote(binding.ReaderID, binding.DeviceID),
		}},
	})
	if err != nil {
		return SIMIdentity{IMEI: imei}, err
	}
	profile := event.GetCardProfile()
	if profile == nil {
		return SIMIdentity{IMEI: imei}, fmt.Errorf("remote SIM returned %T for identity capture", event.GetEvent())
	}
	imsi := strings.TrimSpace(profile.GetImsi())
	if len(imsi) < 5 {
		return SIMIdentity{IMEI: imei}, errors.New("remote SIM returned an invalid IMSI")
	}
	mncLength := int(profile.GetMncLength())
	identity := SIMIdentity{
		DeviceID:        id,
		ICCID:           strings.TrimSpace(profile.GetIccid()),
		IMSI:            imsi,
		ProvisionedIMSI: strings.TrimSpace(profile.GetProvisionedImsi()),
		IMEI:            strings.TrimSpace(imei),
		SPN:             strings.TrimSpace(profile.GetSpn()),
		GID1:            strings.TrimSpace(profile.GetGid1()),
		GID2:            strings.TrimSpace(profile.GetGid2()),
		SMSC:            strings.TrimSpace(profile.GetSmsc()),
	}
	switch {
	case mncLength == 2, mncLength == 3:
		if len(imsi) < 3+mncLength {
			return SIMIdentity{IMEI: imei}, errors.New("remote SIM IMSI is shorter than its EF_AD home PLMN")
		}
		identity.HomeMCC = pcscHomeMCC(imsi, mncLength)
		identity.HomeMNC = pcscHomeMNC(imsi, mncLength)
	default:
		mcc, mnc, ok := assignedHomePLMN(imsi)
		if !ok {
			return SIMIdentity{IMEI: imei}, ErrEC20MNCUnavailable
		}
		identity.HomeMCC = mcc
		identity.HomeMNC = mnc
	}
	identity = applyAssignedCarrierRoute(identity)
	if err := identity.validate(); err != nil {
		return SIMIdentity{IMEI: imei}, err
	}
	if adapter.onProfile != nil {
		if err := adapter.onProfile(ctx, profile); err != nil {
			return SIMIdentity{}, err
		}
	}
	adapter.identity = identity
	adapter.cardGeneration = strings.TrimSpace(profile.GetCardGeneration())
	return identity, nil
}

func (adapter *RemoteSIMAdapter) CheckReady(ctx context.Context, identity SIMIdentity) (AKAEvidence, error) {
	binding, err := adapter.resolve(ctx, identity.DeviceID)
	if err != nil {
		return AKAEvidence{}, err
	}
	event, err := adapter.executor.Execute(ctx, binding.AgentID, &agentproto.AgentCommand{
		Command: &agentproto.AgentCommand_AcquireSimSession{AcquireSimSession: &agentproto.AcquireSimSessionCommand{
			CommandId: remoteCommandID(), ReaderId: firstNonEmptyRemote(binding.ReaderID, binding.DeviceID),
			ExpectedIccid: identity.ICCID,
		}},
	})
	if err != nil {
		return AKAEvidence{}, err
	}
	session := event.GetSimSession()
	if session == nil {
		return AKAEvidence{}, fmt.Errorf("remote SIM returned %T for session acquisition", event.GetEvent())
	}
	if session.GetCardGeneration() != "" && adapter.cardGeneration != "" && session.GetCardGeneration() != adapter.cardGeneration {
		return AKAEvidence{}, errors.New("remote SIM card generation changed")
	}
	if session.GetCardGeneration() != "" && binding.Generation != "" && session.GetCardGeneration() != binding.Generation {
		return AKAEvidence{}, errors.New("remote SIM card generation changed")
	}
	adapter.sessionID = session.GetSessionId()
	adapter.identity = identity
	return AKAEvidence{Ready: true, Application: session.GetApplication()}, nil
}

func (adapter *RemoteSIMAdapter) Authenticate(ctx context.Context, identity SIMIdentity, challenge AKAChallenge) (AKAResult, error) {
	return adapter.AuthenticateWithPreference(ctx, identity, challenge, "")
}

func (adapter *RemoteSIMAdapter) AuthenticateWithPreference(
	ctx context.Context,
	identity SIMIdentity,
	challenge AKAChallenge,
	_ string,
) (AKAResult, error) {
	if adapter.sessionID == "" {
		return AKAResult{}, errors.New("remote SIM session is not ready")
	}
	if adapter.identity.ICCID != identity.ICCID || adapter.identity.IMSI != identity.IMSI {
		return AKAResult{}, errors.New("remote SIM identity changed during authentication")
	}
	binding, err := adapter.resolve(ctx, identity.DeviceID)
	if err != nil {
		return AKAResult{}, err
	}
	challengeID := remoteCommandID()
	event, err := adapter.executor.Execute(ctx, binding.AgentID, &agentproto.AgentCommand{
		Command: &agentproto.AgentCommand_Authenticate{Authenticate: &agentproto.AuthenticateCommand{
			CommandId: remoteCommandID(), SessionId: adapter.sessionID, ExpectedIccid: identity.ICCID,
			Challenge: &agentproto.AkaChallenge{
				Rand: append([]byte(nil), challenge.RAND[:]...), Autn: append([]byte(nil), challenge.AUTN[:]...),
				SessionId: adapter.sessionID, ChallengeId: challengeID,
			},
		}},
	})
	if err != nil {
		return AKAResult{}, err
	}
	result := event.GetAkaResult()
	if result == nil {
		return AKAResult{}, fmt.Errorf("remote SIM returned %T for AKA", event.GetEvent())
	}
	if result.GetSessionId() != adapter.sessionID || result.GetChallengeId() != challengeID {
		return AKAResult{}, errors.New("remote AKA result binding mismatch")
	}
	return AKAResult{
		RES: append([]byte(nil), result.GetRes()...), CK: append([]byte(nil), result.GetCk()...),
		IK: append([]byte(nil), result.GetIk()...), AUTS: append([]byte(nil), result.GetAuts()...),
		SynchronizationFailure: result.GetSynchronizationFailure(),
	}, nil
}

func (adapter *RemoteSIMAdapter) ReadSMSCenter(ctx context.Context, deviceID string) (string, error) {
	if adapter.identity.ICCID != "" && adapter.identity.SMSC != "" {
		return adapter.identity.SMSC, nil
	}
	identity, err := adapter.ReadIdentity(ctx, deviceID)
	if err != nil {
		return "", err
	}
	return identity.SMSC, nil
}

func (adapter *RemoteSIMAdapter) Snapshot(ctx context.Context, deviceID string) (RadioSnapshot, error) {
	binding, err := adapter.resolve(ctx, deviceID)
	if err != nil {
		return RadioSnapshot{}, err
	}
	var snapshot device.Snapshot
	if err := adapter.executeHardware(ctx, deviceID, "refresh", map[string]any{}, &snapshot); err != nil {
		return RadioSnapshot{}, err
	}
	return RadioSnapshot{
		CellularDataEnabled: snapshot.PSAttached,
		OperatingMode:       snapshot.OperatingMode,
		PureAirplanePolicy:  snapshot.FlightMode,
		ICCID:               snapshot.ICCID,
		DeviceGeneration:    binding.Generation,
	}, nil
}

func (adapter *RemoteSIMAdapter) StopCellularData(ctx context.Context, deviceID string) error {
	return adapter.executeHardware(ctx, deviceID, "network", map[string]any{
		"request": device.NetworkRequest{Enabled: false},
	}, nil)
}

func (adapter *RemoteSIMAdapter) EnterVoWiFiRFOff(ctx context.Context, deviceID string) error {
	return adapter.executeHardware(ctx, deviceID, "flight", map[string]any{"enabled": true}, nil)
}

func (adapter *RemoteSIMAdapter) Restore(ctx context.Context, deviceID string, snapshot RadioSnapshot) error {
	if snapshot.PureAirplanePolicy {
		return adapter.executeHardware(ctx, deviceID, "flight", map[string]any{"enabled": true}, nil)
	}
	if err := adapter.executeHardware(ctx, deviceID, "flight", map[string]any{"enabled": false}, nil); err != nil {
		return err
	}
	return adapter.executeHardware(ctx, deviceID, "network", map[string]any{
		"request": device.NetworkRequest{Enabled: snapshot.CellularDataEnabled},
	}, nil)
}

func (adapter *RemoteSIMAdapter) MarkAuthFailed(deviceID string) {
	id := strings.TrimSpace(deviceID)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.authFailed[id] = true
	adapter.consecutiveFails[id]++
}

func (adapter *RemoteSIMAdapter) MarkAuthSuccess(deviceID string) {
	id := strings.TrimSpace(deviceID)
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.authFailed[id] = false
	adapter.consecutiveFails[id] = 0
}

func (adapter *RemoteSIMAdapter) CurrentIMEI(deviceID string) string {
	imei, _, err := adapter.leases.CurrentPCSCIMEI(context.Background(), strings.TrimSpace(deviceID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(imei)
}

func (adapter *RemoteSIMAdapter) executeHardware(ctx context.Context, deviceID, action string, payload, result any) error {
	binding, err := adapter.resolve(ctx, deviceID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event, err := adapter.executor.Execute(ctx, binding.AgentID, &agentproto.AgentCommand{
		Command: &agentproto.AgentCommand_ExecuteHardware{ExecuteHardware: &agentproto.ExecuteHardwareCommand{
			CommandId: remoteCommandID(), DeviceId: binding.DeviceID, Action: action, PayloadJson: string(raw),
		}},
	})
	if err != nil {
		return err
	}
	response := event.GetCommandResult()
	if response == nil {
		return fmt.Errorf("remote hardware returned %T for %q", event.GetEvent(), action)
	}
	if !response.GetSuccess() {
		return errors.New(response.GetErrorMessage())
	}
	if result == nil || response.GetPayloadJson() == "" {
		return nil
	}
	return json.Unmarshal([]byte(response.GetPayloadJson()), result)
}

func remoteCommandID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return "remote-" + hex.EncodeToString(value)
}

func firstNonEmptyRemote(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
