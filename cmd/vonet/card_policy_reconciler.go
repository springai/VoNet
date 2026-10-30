package main

import (
	"context"
	"errors"
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
	manager *device.Manager,
	vowifiManager *vowifiruntime.Manager,
) {
	reconciler := &cardPolicyReconciler{
		logger: logger, database: database, devices: manager, wifi: vowifiManager,
		observedCards:           make(map[string]string),
		wifi410StartupNotBefore: time.Now().Add(wifi410VoWiFiStartupDelay),
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
}

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
		if hotSwappableSIM && r.observedCards[configuredID] != "missing" {
			// Disable deliberately retains identity for diagnostics. That
			// identity must not restart an idle or failed runtime after an
			// unknown card switch.
			if config.VoWiFiEnabled {
				if state, stateErr := r.wifi.State(configuredID); stateErr == nil &&
					state.Enabled && state.Active && strings.TrimSpace(state.ICCID) != "" &&
					cardPolicyCanReconnect(state) {
					_, _ = r.wifi.RequestReconnect(configuredID)
				}
			}
			r.observedCards[configuredID] = "missing"
		}
		return
	}

	iccid := strings.TrimSpace(entry.Snapshot.ICCID)
	previousObserved := r.observedCards[configuredID]
	r.observedCards[configuredID] = iccid
	policy, policyErr := r.database.CardPolicy(ctx, iccid)
	if policyErr != nil {
		return
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
	if deviceChanged {
		if err := r.database.UpsertDevice(ctx, config); err != nil {
			r.logger.Warn("reconcile card policy: update device", "device_id", config.ID, "error", err)
			return
		}
	}

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
		return
	}
	if policy.AirplaneEnabled != entry.Snapshot.FlightMode {
		flightContext, cancel := context.WithTimeout(ctx, flightModeTransitionTimeout)
		_, _ = r.devices.SetFlight(flightContext, entry.ID, policy.AirplaneEnabled)
		cancel()
	}
}
