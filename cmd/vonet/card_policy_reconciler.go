package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vonet/internal/device"
	"vonet/internal/store"
	"vonet/internal/vowifi"
	"vonet/internal/vowifi/integration"
	vowifiruntime "vonet/internal/vowifi/runtime"
)

func reconcileCardPolicies(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	controller any,
	vowifiManager *vowifiruntime.Manager,
) {
	devices, ok := controller.(cardPolicyDevices)
	if !ok {
		logger.Error("card policy reconciler device controller is missing required capabilities")
		return
	}
	reconciler := &cardPolicyReconciler{
		logger: logger, database: database, devices: devices, wifi: vowifiManager,
		observedCards:           make(map[string]string),
		wifi410StartupNotBefore: time.Now().Add(wifi410VoWiFiStartupDelay),
		bindingStableAfter:      cardBindingStableAfter,
		bindingOfflineAfter:     cardBindingOfflineAfter,
		bindingObservationGap:   cardBindingObservationGap,
	}
	reconciler.reconcile(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconciler.reconcile(ctx)
		}
	}
}

type cardPolicyDevices interface {
	Get(string) (device.Device, error)
	List() []device.Device
	AcquireSIMRead(context.Context, string) (func(), error)
	SetFlight(context.Context, string, bool) (device.FlightResult, error)
}

type cardPolicyESIMInventory interface {
	ESIMInventory(context.Context, string) ([]device.EsimInventoryEntry, error)
}

type cardPolicyWiFi interface {
	InMaintenance(string) bool
	State(string) (vowifi.State, error)
	RequestEnabled(string, bool) (vowifi.State, error)
	RequestReconnect(string) (vowifi.State, error)
}

type cardPolicyReconciler struct {
	logger                  *slog.Logger
	database                *store.Store
	devices                 cardPolicyDevices
	wifi                    cardPolicyWiFi
	observedCards           map[string]string
	wifi410StartupNotBefore time.Time
	bindingStableAfter      time.Duration
	bindingOfflineAfter     time.Duration
	bindingObservationGap   time.Duration
}

const (
	cardBindingStableAfter    = 3 * time.Second
	cardBindingOfflineAfter   = 15 * time.Second
	cardBindingObservationGap = 15 * time.Second
)

func cardPolicyCanReconnect(state vowifi.State) bool {
	return state.Phase == vowifi.PhaseIdle || state.Phase == vowifi.PhaseFailed
}

// reconcile performs one pass so policy decisions can be tested without
// starting the service's permanent polling goroutine.
func (r *cardPolicyReconciler) reconcile(ctx context.Context) {
	policies, policyListErr := r.database.ListCardPolicies(ctx)
	if policyListErr == nil {
		for _, policy := range policies {
			if !policy.VoWiFiEnabled || (policy.AirplaneEnabled && !policy.NetworkEnabled) {
				continue
			}
			policy.AirplaneEnabled = true
			policy.NetworkEnabled = false
			if err := r.database.UpsertCardPolicy(ctx, policy); err != nil {
				r.logger.Warn("reconcile card policy: normalize stored RF-safe VoWiFi policy", "iccid", policy.ICCID, "error", err)
			}
		}
	}
	configs, err := r.database.ListDevices(ctx)
	if err != nil {
		return
	}
	for _, config := range configs {
		r.reconcileDevice(ctx, config.ID)
	}
}

func (r *cardPolicyReconciler) reconcileDevice(ctx context.Context, configuredID string) {
	physicalID, err := integration.ResolvePhysicalID(ctx, r.database, r.devices, configuredID)
	if err != nil {
		// Losing the reader/modem retires its old IMS session but preserves the
		// saved intent so a ready card can resume after replug.
		if errors.Is(err, device.ErrNotFound) && !r.wifi.InMaintenance(configuredID) {
			if state, stateErr := r.wifi.State(configuredID); stateErr == nil && (state.Enabled || state.Active) {
				_, _ = r.wifi.RequestEnabled(configuredID, false)
			}
			r.observedCards[configuredID] = "missing"
			r.expireDeviceBindings(ctx, configuredID, "", true)
		}
		return
	}
	release, err := r.devices.AcquireSIMRead(ctx, physicalID)
	if err != nil {
		return
	}
	defer release()

	// Re-read identity and saved intent under the same gate that excludes
	// profile REFRESH/reset work.
	entry, err := r.devices.Get(physicalID)
	if err != nil || !entry.Discovered {
		return
	}
	config, err := r.database.Device(ctx, configuredID)
	if err != nil || r.wifi.InMaintenance(configuredID) {
		return
	}
	currentPhysicalID, err := integration.ResolvePhysicalID(ctx, r.database, r.devices, configuredID)
	if err != nil || currentPhysicalID != physicalID {
		return
	}
	hotSwappableSIM := config.DeviceType == store.DeviceTypeUSBSIMReader || config.DeviceType == store.DeviceTypeWiFi410
	if entry.Snapshot == nil || !entry.Snapshot.Responsive || !entry.Snapshot.SIMReady || strings.TrimSpace(entry.Snapshot.ICCID) == "" {
		if r.observedCards[configuredID] != "missing" {
			r.observedCards[configuredID] = "missing"
		}
		if r.expireDeviceBindings(ctx, configuredID, "", true) {
			r.suspendCardRuntime(ctx, config, entry.ID, true)
		}
		return
	}

	iccid := strings.TrimSpace(entry.Snapshot.ICCID)
	previousObserved := r.observedCards[configuredID]
	r.observedCards[configuredID] = iccid
	_, active, bindingErr := r.refreshCardBinding(ctx, config, physicalID, *entry.Snapshot)
	if bindingErr != nil {
		if !errors.Is(bindingErr, store.ErrNotFound) {
			r.logger.Warn("reconcile card binding", "device_id", config.ID, "iccid", iccid, "error", bindingErr)
		}
		r.suspendCardRuntime(ctx, config, physicalID, false)
		return
	}
	r.expireDeviceBindings(ctx, config.ID, iccid, false)
	if !active {
		r.suspendCardRuntime(ctx, config, physicalID, false)
		return
	}
	r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
		stale.Binding = false
	})
	policy, policyErr := r.database.CardPolicy(ctx, iccid)
	if errors.Is(policyErr, store.ErrNotFound) {
		if config.DeviceType != store.DeviceTypeUSBSIMReader {
			r.suspendCardRuntime(ctx, config, physicalID, false)
			return
		}
		policy = store.CardPolicy{
			ICCID:           iccid,
			NetworkEnabled:  false,
			VoWiFiEnabled:   true,
			AirplaneEnabled: true,
			IPVersion:       "IPV4V6",
			RuntimeMode:     store.CardRuntimeModePCSC,
			CellularIMSMode: store.CellularIMSModeMBNDefault,
			Source:          "default",
		}
		if err := r.database.UpsertCardPolicy(ctx, policy); err != nil {
			r.logger.Warn("reconcile card policy: create fixed reader policy", "device_id", config.ID, "iccid", iccid, "error", err)
			return
		}
	} else if policyErr != nil {
		r.logger.Warn("reconcile card policy: read policy", "device_id", config.ID, "iccid", iccid, "error", policyErr)
		return
	}
	if config.DeviceType == store.DeviceTypeUSBSIMReader {
		policy.VoWiFiEnabled = true
		policy.AirplaneEnabled = true
		policy.NetworkEnabled = false
		policy.RuntimeMode = "pcsc"
		if err := r.database.UpsertCardPolicy(ctx, policy); err != nil {
			r.logger.Warn("reconcile card policy: persist fixed reader policy", "device_id", config.ID, "iccid", iccid, "error", err)
			return
		}
	}
	if policy.VoWiFiEnabled && (!policy.AirplaneEnabled || policy.NetworkEnabled) {
		policy.AirplaneEnabled = true
		policy.NetworkEnabled = false
		if err := r.database.UpsertCardPolicy(ctx, policy); err != nil {
			r.logger.Warn("reconcile card policy: normalize RF-safe VoWiFi policy", "device_id", config.ID, "iccid", iccid, "error", err)
			return
		}
	}
	deviceChanged := false
	if config.VoWiFiEnabled != policy.VoWiFiEnabled || (policy.VoWiFiEnabled && config.NetworkEnabled) {
		config.VoWiFiEnabled = policy.VoWiFiEnabled
		if policy.VoWiFiEnabled {
			config.NetworkEnabled = false
		}
		deviceChanged = true
	}
	if config.APN != strings.TrimSpace(policy.APN) {
		config.APN = strings.TrimSpace(policy.APN)
		deviceChanged = true
	}
	if config.NetworkEnabled != policy.NetworkEnabled {
		config.NetworkEnabled = policy.NetworkEnabled
		deviceChanged = true
	}
	if deviceChanged {
		if err := r.database.UpsertDevice(ctx, config); err != nil {
			r.logger.Warn("reconcile card policy: update device", "device_id", config.ID, "error", err)
			return
		}
	}

	r.applyRuntimeMode(ctx, &config, physicalID, policy)
	r.applyCellularIMSPolicy(ctx, config, physicalID, policy)

	state, stateErr := r.wifi.State(config.ID)
	if policy.VoWiFiEnabled {
		if !entry.Snapshot.FlightMode {
			flightContext, cancel := context.WithTimeout(ctx, flightModeTransitionTimeout)
			_, _ = r.devices.SetFlight(flightContext, entry.ID, true)
			cancel()
		}
		switch {
		case stateErr != nil || !state.Enabled:
			if shouldDelayWiFi410VoWiFi(config.DeviceType, time.Now(), r.wifi410StartupNotBefore) {
				return
			}
			_, _ = r.wifi.RequestEnabled(config.ID, true)
		case state.ICCID != "" && !strings.EqualFold(strings.TrimSpace(state.ICCID), iccid) &&
			cardPolicyCanReconnect(state):
			_, _ = r.wifi.RequestReconnect(config.ID)
		case hotSwappableSIM && previousObserved == "missing" && cardPolicyCanReconnect(state):
			_, _ = r.wifi.RequestReconnect(config.ID)
		}
		return
	}
	if stateErr == nil && state.Enabled {
		_, _ = r.wifi.RequestEnabled(config.ID, false)
	}
	if policy.AirplaneEnabled != entry.Snapshot.FlightMode {
		flightContext, cancel := context.WithTimeout(ctx, flightModeTransitionTimeout)
		_, _ = r.devices.SetFlight(flightContext, entry.ID, policy.AirplaneEnabled)
		cancel()
	}
	if policy.NetworkEnabled {
		r.applyCellularPolicy(ctx, config, physicalID, policy)
	} else if controller, ok := r.devices.(interface {
		SetNetwork(context.Context, string, device.NetworkRequest) (device.NetworkResult, error)
	}); ok {
		_, _ = controller.SetNetwork(ctx, physicalID, device.NetworkRequest{Enabled: false})
	}
}

func (r *cardPolicyReconciler) refreshCardBinding(
	ctx context.Context,
	config store.Device,
	physicalID string,
	snapshot device.Snapshot,
) (store.SimCardBinding, bool, error) {
	iccid, err := store.NormalizeSimICCID(snapshot.ICCID)
	if err != nil {
		return store.SimCardBinding{}, false, err
	}
	card, cardErr := r.database.SimCard(ctx, iccid)
	candidateType := store.SimCardTypePhysical
	if cardErr == nil && card.CardType != "" {
		candidateType = card.CardType
	}
	now := time.Now().UTC()
	if _, err := r.database.TouchSimCardCandidate(ctx, store.SimCardCandidate{
		ICCID:            iccid,
		DeviceID:         config.ID,
		PhysicalDeviceID: physicalID,
		HostType:         config.DeviceType,
		CardType:         candidateType,
		IMSI:             strings.TrimSpace(snapshot.IMSI),
		Carrier:          strings.TrimSpace(snapshot.OperatorName),
		SPN:              strings.TrimSpace(snapshot.SPN),
		FirstSeenAt:      now,
		LastSeenAt:       now,
	}); err != nil {
		return store.SimCardBinding{}, false, err
	}
	if cardErr != nil {
		return store.SimCardBinding{}, false, cardErr
	}
	if card.HostType == "" {
		card.HostType = config.DeviceType
		card.UpdatedAt = now
		if err := r.database.UpsertSimCard(ctx, card); err != nil {
			return store.SimCardBinding{}, false, err
		}
	}
	eid, profileAID := strings.TrimSpace(card.EID), strings.TrimSpace(card.ProfileAID)
	if card.CardType == store.SimCardTypeESIM && (eid == "" || profileAID == "") {
		if binding, bindingErr := r.database.SimCardBinding(ctx, iccid); bindingErr == nil {
			eid = firstNonEmpty(eid, strings.TrimSpace(binding.EID))
			profileAID = firstNonEmpty(profileAID, strings.TrimSpace(binding.ProfileAID))
		}
		if eid == "" || profileAID == "" {
			eid, profileAID, err = r.resolveESIMBindingIdentity(ctx, physicalID, iccid)
			if err != nil {
				return store.SimCardBinding{}, false, err
			}
			card.EID = eid
			card.ProfileAID = profileAID
			card.UpdatedAt = now
			if err := r.database.UpsertSimCard(ctx, card); err != nil {
				return store.SimCardBinding{}, false, err
			}
		}
	}
	observation, err := r.database.UpsertSimCardObservation(ctx, store.SimCardObservation{
		ICCID:            iccid,
		DeviceID:         config.ID,
		PhysicalDeviceID: physicalID,
		EID:              eid,
		ProfileAID:       profileAID,
		Source:           "live",
		FirstSeenAt:      now,
		LastSeenAt:       now,
		LastEnabledAt:    now,
	})
	if err != nil {
		return store.SimCardBinding{}, false, err
	}
	observations, err := r.database.ListSimCardObservations(ctx, iccid)
	if err != nil {
		return store.SimCardBinding{}, false, err
	}
	seenDevices := make(map[string]struct{})
	for _, candidate := range observations {
		if now.Sub(candidate.LastSeenAt) <= r.bindingObservationGap {
			seenDevices[candidate.DeviceID] = struct{}{}
		}
	}
	if len(seenDevices) > 1 {
		if err := r.database.TouchSimCardBinding(ctx, store.SimCardBinding{
			ICCID:         iccid,
			State:         store.SimCardBindingConflict,
			LastSeenAt:    now,
			ObservationID: observation.ID,
		}); err != nil {
			return store.SimCardBinding{}, false, err
		}
		return store.SimCardBinding{ICCID: iccid, State: store.SimCardBindingConflict}, false, nil
	}

	state := store.SimCardBindingPending
	if now.Sub(observation.FirstSeenAt) >= r.bindingStableAfter {
		state = store.SimCardBindingActive
	}
	if err := r.database.TouchSimCardBinding(ctx, store.SimCardBinding{
		ICCID:            iccid,
		ObservationID:    observation.ID,
		DeviceID:         config.ID,
		PhysicalDeviceID: physicalID,
		EID:              eid,
		ProfileAID:       profileAID,
		State:            state,
		LastSeenAt:       now,
	}); err != nil {
		return store.SimCardBinding{}, false, err
	}
	binding, err := r.database.SimCardBinding(ctx, iccid)
	if err != nil {
		return store.SimCardBinding{}, false, err
	}
	return binding, binding.State == store.SimCardBindingActive, nil
}

func (r *cardPolicyReconciler) resolveESIMBindingIdentity(
	ctx context.Context,
	physicalID string,
	iccid string,
) (string, string, error) {
	controller, ok := r.devices.(cardPolicyESIMInventory)
	if !ok {
		return "", "", nil
	}
	inventory, err := controller.ESIMInventory(ctx, physicalID)
	if err != nil {
		if errors.Is(err, device.ErrNoEUICC) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("read eSIM identity for card binding: %w", err)
	}
	var eid, profileAID string
	matches := 0
	for _, entry := range inventory {
		for _, profile := range entry.Info.Profiles {
			if !strings.EqualFold(strings.TrimSpace(profile.ICCID), strings.TrimSpace(iccid)) {
				continue
			}
			matches++
			eid = strings.TrimSpace(entry.Info.EID)
			profileAID = strings.ToUpper(strings.TrimSpace(profile.AID))
		}
	}
	if matches > 1 {
		return "", "", fmt.Errorf("ICCID %q is present in multiple eSIM inventories", iccid)
	}
	return eid, profileAID, nil
}

func (r *cardPolicyReconciler) suspendCardRuntime(
	ctx context.Context,
	config store.Device,
	physicalID string,
	restoreIMSDefault bool,
) {
	if state, err := r.wifi.State(config.ID); err == nil && (state.Enabled || state.Active) {
		_, _ = r.wifi.RequestEnabled(config.ID, false)
	}
	if controller, ok := r.devices.(interface {
		SetNetwork(context.Context, string, device.NetworkRequest) (device.NetworkResult, error)
	}); ok {
		_, _ = controller.SetNetwork(ctx, physicalID, device.NetworkRequest{Enabled: false})
	}
	if restoreIMSDefault && config.DeviceType != store.DeviceTypeUSBSIMReader {
		if controller, ok := r.devices.(interface {
			CellularIMS(context.Context, string) (device.CellularIMSStatus, error)
			SetCellularIMS(context.Context, string, device.CellularIMSMode) (device.CellularIMSStatus, error)
		}); ok {
			if _, err := controller.SetCellularIMS(
				ctx,
				physicalID,
				device.CellularIMSModeMBNDefault,
			); err != nil {
				r.logger.Warn("restore safe cellular IMS default", "device_id", config.ID, "error", err)
			}
		}
	}
	config.NetworkEnabled = false
	config.VoWiFiEnabled = false
	stale := config.CardRuntimeStale()
	stale.Binding = true
	stale.DeviceBackend = true
	stale.CellularIMS = config.DeviceType != store.DeviceTypeUSBSIMReader
	if err := config.SetCardRuntimeStale(stale); err != nil {
		r.logger.Warn("mark card runtime stale", "device_id", config.ID, "error", err)
	}
	if err := r.database.UpsertDevice(ctx, config); err != nil {
		r.logger.Warn("suspend card runtime: update device", "device_id", config.ID, "error", err)
		return
	}
	if err := r.database.UpdateDeviceCardRuntimeStale(ctx, config.ID, stale); err != nil {
		r.logger.Warn("persist suspended card runtime stale state", "device_id", config.ID, "error", err)
	}
}

func (r *cardPolicyReconciler) expireDeviceBindings(
	ctx context.Context,
	deviceID string,
	keepICCID string,
	waitForOfflineTimeout bool,
) bool {
	bindings, err := r.database.ListSimCardBindings(ctx)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	expired := false
	for _, binding := range bindings {
		if binding.DeviceID != deviceID ||
			(keepICCID != "" && strings.EqualFold(binding.ICCID, keepICCID)) ||
			(waitForOfflineTimeout && now.Sub(binding.LastSeenAt) < r.bindingOfflineAfter) {
			continue
		}
		if err := r.database.ClearSimCardBinding(ctx, binding.ICCID, deviceID); err != nil {
			r.logger.Warn("expire card binding", "device_id", deviceID, "iccid", binding.ICCID, "error", err)
			continue
		}
		expired = true
	}
	return expired
}

func (r *cardPolicyReconciler) applyRuntimeMode(
	ctx context.Context,
	config *store.Device,
	physicalID string,
	policy store.CardPolicy,
) {
	mode := strings.ToLower(strings.TrimSpace(policy.RuntimeMode))
	if config.DeviceType == store.DeviceTypeUSBSIMReader {
		mode = "pcsc"
	}
	if mode == "" {
		// Empty means the card follows the device's hardware-selected backend.
		r.updateCardRuntimeStale(ctx, config, func(stale *store.CardRuntimeStaleState) {
			stale.DeviceBackend = false
		})
		return
	}
	if strings.EqualFold(config.DeviceBackend, mode) {
		r.updateCardRuntimeStale(ctx, config, func(stale *store.CardRuntimeStaleState) {
			stale.DeviceBackend = false
		})
		return
	}
	selector, ok := r.devices.(interface{ SetBackend(string, string) error })
	if !ok {
		r.updateCardRuntimeStale(ctx, config, func(stale *store.CardRuntimeStaleState) {
			stale.DeviceBackend = true
		})
		return
	}
	if err := selector.SetBackend(physicalID, mode); err != nil {
		r.logger.Warn("apply card runtime mode", "device_id", config.ID, "mode", mode, "error", err)
		r.updateCardRuntimeStale(ctx, config, func(stale *store.CardRuntimeStaleState) {
			stale.DeviceBackend = true
		})
		return
	}
	config.DeviceBackend = mode
	if err := r.database.UpdateDeviceBackend(ctx, config.ID, mode); err != nil {
		r.logger.Warn("persist card runtime mode", "device_id", config.ID, "error", err)
		r.updateCardRuntimeStale(ctx, config, func(stale *store.CardRuntimeStaleState) {
			stale.DeviceBackend = true
		})
		return
	}
	r.updateCardRuntimeStale(ctx, config, func(stale *store.CardRuntimeStaleState) {
		stale.DeviceBackend = false
	})
}

func (r *cardPolicyReconciler) applyCellularIMSPolicy(
	ctx context.Context,
	config store.Device,
	physicalID string,
	policy store.CardPolicy,
) {
	if config.DeviceType == store.DeviceTypeUSBSIMReader {
		r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
			stale.CellularIMS = false
		})
		return
	}
	desiredMode := strings.TrimSpace(policy.CellularIMSMode)
	if desiredMode == "" {
		// Empty means the card does not manage module-wide IMS settings. The
		// confirmed-offline path still restores the safe MBN default.
		r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
			stale.CellularIMS = false
		})
		return
	}
	controller, ok := r.devices.(interface {
		CellularIMS(context.Context, string) (device.CellularIMSStatus, error)
		SetCellularIMS(context.Context, string, device.CellularIMSMode) (device.CellularIMSStatus, error)
	})
	if !ok {
		r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
			stale.CellularIMS = true
		})
		return
	}
	desired := device.CellularIMSMode(desiredMode)
	if status, err := controller.CellularIMS(ctx, physicalID); err == nil && status.Mode == desired {
		r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
			stale.CellularIMS = false
		})
		return
	}
	if _, err := controller.SetCellularIMS(ctx, physicalID, desired); err != nil {
		r.logger.Warn("apply card cellular IMS policy", "device_id", config.ID, "iccid", policy.ICCID, "error", err)
		r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
			stale.CellularIMS = true
		})
		return
	}
	r.updateCardRuntimeStale(ctx, &config, func(stale *store.CardRuntimeStaleState) {
		stale.CellularIMS = false
	})
}

func (r *cardPolicyReconciler) updateCardRuntimeStale(
	ctx context.Context,
	config *store.Device,
	update func(*store.CardRuntimeStaleState),
) {
	stale := config.CardRuntimeStale()
	update(&stale)
	if err := config.SetCardRuntimeStale(stale); err != nil {
		r.logger.Warn("update card runtime stale state", "device_id", config.ID, "error", err)
		return
	}
	if err := r.database.UpdateDeviceCardRuntimeStale(ctx, config.ID, stale); err != nil {
		r.logger.Warn("persist card runtime stale state", "device_id", config.ID, "error", err)
	}
}

func (r *cardPolicyReconciler) applyCellularPolicy(
	ctx context.Context,
	config store.Device,
	physicalID string,
	policy store.CardPolicy,
) {
	if config.DeviceType == store.DeviceTypeUSBSIMReader {
		return
	}
	controller, ok := r.devices.(interface {
		SetNetwork(context.Context, string, device.NetworkRequest) (device.NetworkResult, error)
	})
	if !ok {
		return
	}
	_, err := controller.SetNetwork(ctx, physicalID, device.NetworkRequest{
		Enabled:   true,
		APN:       strings.TrimSpace(policy.APN),
		IPVersion: strings.TrimSpace(policy.IPVersion),
		Backend:   firstNonEmpty(strings.TrimSpace(policy.RuntimeMode), config.DeviceBackend),
	})
	if err != nil {
		r.logger.Warn("apply card cellular policy", "device_id", config.ID, "iccid", policy.ICCID, "error", err)
	}
}
