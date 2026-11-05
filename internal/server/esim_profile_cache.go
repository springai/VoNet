package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"vonet/internal/store"
)

const esimProfileCacheKeyPrefix = "device.esim_profile_cache:"
const esimProfileCacheVersion = 2

type esimProfileCacheDocument struct {
	Version            int              `json:"version"`
	CachedAt           int64            `json:"cached_at"`
	HardwareIdentity   string           `json:"hardware_identity,omitempty"`
	HardwareGeneration string           `json:"hardware_generation,omitempty"`
	NoEUICCStrikes     int              `json:"no_euicc_strikes,omitempty"`
	Profiles           []map[string]any `json:"profiles"`
}

func esimProfileCacheKey(configuredID string) string {
	return esimProfileCacheKeyPrefix + configuredID
}

func sanitizedEsimProfileGroups(groups []map[string]any) []map[string]any {
	normalized := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		profiles, _ := group["profiles"].([]map[string]any)
		sanitizedProfiles := make([]map[string]any, 0, len(profiles))
		for _, profile := range profiles {
			sanitizedProfiles = append(sanitizedProfiles, map[string]any{
				"iccid":               profile["iccid"],
				"name":                profile["name"],
				"serviceProviderName": profile["serviceProviderName"],
				"state":               profile["state"],
				"stateText":           profile["stateText"],
				"classText":           profile["classText"],
			})
		}
		normalized = append(normalized, map[string]any{"profiles": sanitizedProfiles})
	}
	return normalized
}

func (s *Server) saveEsimProfileCache(
	ctx context.Context,
	configuredID string,
	physicalID string,
	groups []map[string]any,
) error {
	if !validDeviceID(configuredID) {
		return fmt.Errorf("invalid device ID for eSIM profile cache")
	}
	value := esimProfileCacheDocument{
		Version:            esimProfileCacheVersion,
		CachedAt:           time.Now().Unix(),
		HardwareIdentity:   s.esimProfileCacheHardwareIdentity(ctx, configuredID, physicalID),
		HardwareGeneration: s.esimProfileCacheHardwareGeneration(physicalID),
		Profiles:           sanitizedEsimProfileGroups(groups),
	}
	if value.HardwareIdentity == "" {
		return fmt.Errorf("device %q has no stable hardware identity for eSIM cache", configuredID)
	}
	return s.writeEsimProfileCache(ctx, configuredID, value)
}

func (s *Server) loadEsimProfileCache(
	ctx context.Context,
	configuredID string,
	physicalID string,
) (esimProfileCacheDocument, bool) {
	if !validDeviceID(configuredID) {
		return esimProfileCacheDocument{}, false
	}
	setting, err := s.store.AppSetting(ctx, esimProfileCacheKey(configuredID))
	if err != nil {
		return esimProfileCacheDocument{}, false
	}
	var value esimProfileCacheDocument
	if err := json.Unmarshal(setting.Value, &value); err != nil ||
		value.Version != esimProfileCacheVersion ||
		value.CachedAt <= 0 ||
		value.Profiles == nil {
		s.discardInvalidEsimProfileCache(ctx, configuredID)
		return esimProfileCacheDocument{}, false
	}
	if value.HardwareIdentity == "" {
		s.discardInvalidEsimProfileCache(ctx, configuredID)
		return esimProfileCacheDocument{}, false
	}
	if identity := s.esimProfileCacheHardwareIdentity(ctx, configuredID, physicalID); identity == "" ||
		identity != value.HardwareIdentity {
		s.discardInvalidEsimProfileCache(ctx, configuredID)
		return esimProfileCacheDocument{}, false
	}
	if value.HardwareGeneration != "" {
		if generation := s.esimProfileCacheHardwareGeneration(physicalID); generation != "" &&
			generation != value.HardwareGeneration {
			s.discardInvalidEsimProfileCache(ctx, configuredID)
			return esimProfileCacheDocument{}, false
		}
	}
	return value, true
}

func (s *Server) clearEsimProfileCache(ctx context.Context, configuredID string) error {
	if !validDeviceID(configuredID) {
		return nil
	}
	err := s.store.DeleteAppSetting(ctx, esimProfileCacheKey(configuredID))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Server) invalidateEsimProfileCache(ctx context.Context, configuredID string) error {
	if s.esimCacheInvalidator != nil {
		return retryESIMProfileCacheInvalidation(ctx, func(retryCtx context.Context) error {
			return s.esimCacheInvalidator(retryCtx, configuredID)
		})
	}
	return retryESIMProfileCacheInvalidation(ctx, func(retryCtx context.Context) error {
		return s.clearEsimProfileCache(retryCtx, configuredID)
	})
}

func retryESIMProfileCacheInvalidation(
	ctx context.Context,
	clear func(context.Context) error,
) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lastErr = clear(ctx)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (s *Server) discardInvalidEsimProfileCache(ctx context.Context, configuredID string) {
	if err := s.clearEsimProfileCache(ctx, configuredID); err != nil && s.logger != nil {
		s.logger.Warn("discard invalid eSIM profile cache", "device_id", configuredID, "error", err)
	}
}

func (s *Server) writeEsimProfileCache(
	ctx context.Context,
	configuredID string,
	value esimProfileCacheDocument,
) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode eSIM profile cache: %w", err)
	}
	return s.store.UpsertAppSetting(ctx, store.AppSetting{
		Key:       esimProfileCacheKey(configuredID),
		Value:     raw,
		Sensitive: true,
	})
}

func (s *Server) esimProfileCacheHardwareIdentity(
	ctx context.Context,
	configuredID string,
	physicalID string,
) string {
	var identity extraHardwareIdentity
	var deviceType, controlDevice, modemIMEI string
	if config, err := s.store.Device(ctx, configuredID); err == nil {
		identity = parseExtraHardwareIdentity(config.Extra)
		deviceType = strings.TrimSpace(config.DeviceType)
		controlDevice = strings.TrimSpace(config.ControlDevice)
		modemIMEI = strings.TrimSpace(config.ModemIMEI)
	}
	serial := identity.SerialNumber
	if s.devices != nil && strings.TrimSpace(physicalID) != "" {
		if entry, err := s.devices.Get(physicalID); err == nil {
			if candidateSerial := strings.TrimSpace(entry.Candidate.SerialNumber); candidateSerial != "" {
				serial = candidateSerial
			}
		}
	}
	stable := firstNonEmpty(controlDevice, identity.SerialNumber, modemIMEI, serial)
	if stable == "" {
		return ""
	}
	return strings.Join([]string{deviceType, identity.VendorID, identity.ProductID, stable}, "|")
}

func (s *Server) esimProfileCacheHardwareGeneration(physicalID string) string {
	if s.devices == nil || strings.TrimSpace(physicalID) == "" {
		return ""
	}
	entry, err := s.devices.Get(physicalID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(entry.Candidate.USBGeneration)
}

func (s *Server) noteESIMNoEUICC(ctx context.Context, configuredID string, physicalID string) error {
	cached, ok := s.loadEsimProfileCache(ctx, configuredID, physicalID)
	if !ok {
		return nil
	}
	cached.NoEUICCStrikes++
	if cached.NoEUICCStrikes >= 3 {
		return s.clearEsimProfileCache(ctx, configuredID)
	}
	return s.writeEsimProfileCache(ctx, configuredID, cached)
}
