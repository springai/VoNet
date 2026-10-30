package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"encoding/json"
	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

type ATDeviceController interface {
	Get(string) (device.Device, error)
	List() []device.Device
	ExecuteAT(context.Context, string, string) (modem.Response, error)
	ExecuteSensitiveAT(context.Context, string, string) (modem.Response, error)
	ExecuteAPDU(context.Context, string, modem.APDUCommand, int, []byte) ([]byte, error)
}

// ATMapper lets runtime IDs remain stable configuration IDs even when Linux
// re-enumerates a physical modem under a discovery-derived ID.
type ATMapper struct {
	Store   *store.Store
	Devices ATDeviceController
}

type uiccLocker interface {
	LockUICC()
	UnlockUICC()
}

func (mapper ATMapper) LockUICC() {
	if locker, ok := mapper.Devices.(uiccLocker); ok {
		locker.LockUICC()
	}
}

func (mapper ATMapper) UnlockUICC() {
	if locker, ok := mapper.Devices.(uiccLocker); ok {
		locker.UnlockUICC()
	}
}

func (mapper ATMapper) Get(configuredID string) (device.Device, error) {
	physicalID, err := mapper.resolve(context.Background(), configuredID)
	if err != nil {
		return device.Device{}, err
	}
	return mapper.Devices.Get(physicalID)
}

func (mapper ATMapper) ExecuteAT(
	ctx context.Context,
	configuredID string,
	command string,
) (modem.Response, error) {
	physicalID, err := mapper.resolve(ctx, configuredID)
	if err != nil {
		return modem.Response{}, err
	}
	return mapper.Devices.ExecuteAT(ctx, physicalID, command)
}

func (mapper ATMapper) ExecuteSensitiveAT(
	ctx context.Context,
	configuredID string,
	command string,
) (modem.Response, error) {
	physicalID, err := mapper.resolve(ctx, configuredID)
	if err != nil {
		return modem.Response{}, err
	}
	return mapper.Devices.ExecuteSensitiveAT(ctx, physicalID, command)
}

func (mapper ATMapper) ExecuteAPDU(
	ctx context.Context,
	configuredID string,
	command modem.APDUCommand,
	channel int,
	apdu []byte,
) ([]byte, error) {
	physicalID, err := mapper.resolve(ctx, configuredID)
	if err != nil {
		return nil, err
	}
	return mapper.Devices.ExecuteAPDU(ctx, physicalID, command, channel, apdu)
}

// ReadSMSCenter reads the SIM-provisioned service-centre address through the
// modem's read-only AT interface. Native QMI devices still expose a companion
// AT port, and SMS-over-IMS needs this value to address RP-DATA submissions.
func (mapper ATMapper) ReadSMSCenter(ctx context.Context, configuredID string) (string, error) {
	response, err := mapper.ExecuteAT(ctx, configuredID, "AT+CSCA?")
	if err != nil {
		return "", fmt.Errorf("read SMS service centre: %w", err)
	}
	for _, line := range response.Lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+CSCA:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "+CSCA:"))
		if comma := strings.IndexByte(value, ','); comma >= 0 {
			value = value[:comma]
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		digits := strings.TrimPrefix(value, "+")
		if len(digits) < 3 || len(digits) > 20 {
			break
		}
		valid := true
		for _, digit := range digits {
			if digit < '0' || digit > '9' {
				valid = false
				break
			}
		}
		if valid {
			return value, nil
		}
		break
	}
	return "", errors.New("modem returned no valid SMS service-centre address")
}

// ReadSIMMetadata reuses the device manager's per-ICCID EF cache. VoWiFi
// identity discovery therefore gains Android-style SPN/GID MVNO selectors
// without issuing duplicate APDUs on every reconnect.
func (mapper ATMapper) ReadSIMMetadata(ctx context.Context, configuredID string) (vowifi.SIMMetadata, error) {
	physicalID, err := mapper.resolve(ctx, configuredID)
	if err != nil {
		return vowifi.SIMMetadata{}, err
	}
	entry, err := mapper.Devices.Get(physicalID)
	if err != nil {
		return vowifi.SIMMetadata{}, err
	}
	if entry.Snapshot == nil {
		return vowifi.SIMMetadata{}, nil
	}
	return vowifi.SIMMetadata{
		SPN:  strings.TrimSpace(entry.Snapshot.SPN),
		GID1: strings.TrimSpace(entry.Snapshot.GID1),
		GID2: strings.TrimSpace(entry.Snapshot.GID2),
	}, nil
}

// DeviceLister is the read side ResolvePhysicalID needs.
type DeviceLister interface {
	List() []device.Device
}

// ResolvePhysicalID maps a configured device ID to the live physical device
// ID using the same scored matching as the AT paths. MCP and other non-HTTP
// consumers use this so every transport selects the same modem.
func ResolvePhysicalID(
	ctx context.Context,
	database *store.Store,
	devices DeviceLister,
	configuredID string,
) (string, error) {
	if database == nil || devices == nil {
		return "", errors.New("physical device resolver is not configured")
	}
	config, err := database.Device(ctx, configuredID)
	if err != nil {
		return "", err
	}
	// Score every physical candidate before choosing one. Returning the first
	// partial match made two EC20s on the same hub vulnerable to iteration order:
	// a stale USB path on one configured row could win before the other
	// candidate's AT port, QMI node, or live IMEI was considered. The result was
	// two logical devices issuing APDUs to the same SIM.
	bestID := ""
	bestScore := 0
	for _, entry := range devices.List() {
		if !entry.Discovered {
			continue
		}
		score := physicalMatchScore(configuredID, config, entry)
		if score > bestScore {
			bestID = entry.ID
			bestScore = score
		}
	}
	if bestID != "" {
		return bestID, nil
	}
	return "", device.ErrNotFound
}

func (mapper ATMapper) resolve(
	ctx context.Context,
	configuredID string,
) (string, error) {
	return ResolvePhysicalID(ctx, mapper.Store, mapper.Devices, configuredID)
}

// extraHWIdentity reads persisted USB hardware identifiers stored in Extra JSON.
type extraHWIdentity struct {
	SerialNumber string
	VendorID     string
	ProductID    string
}

func parseExtraHWIdentity(extra json.RawMessage) extraHWIdentity {
	var h extraHWIdentity
	if len(extra) == 0 {
		return h
	}
	var m map[string]any
	if json.Unmarshal(extra, &m) != nil {
		return h
	}
	if v, ok := m["usb_serial_number"].(string); ok {
		h.SerialNumber = strings.TrimSpace(v)
	}
	if v, ok := m["usb_vendor_id"].(string); ok {
		h.VendorID = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := m["usb_product_id"].(string); ok {
		h.ProductID = strings.ToLower(strings.TrimSpace(v))
	}
	return h
}

func physicalMatchScore(configuredID string, config store.Device, entry device.Device) int {
	if isPCSCConfig(config, entry) {
		return pcscIntegrationScore(configuredID, config, entry)
	}
	candidate := entry.Candidate
	score := 0

	// Reject mismatched hardware identity early — two distinct physical devices
	// always carry different serial numbers and/or vendor/product IDs.
	stored := parseExtraHWIdentity(config.Extra)
	candSerial := strings.TrimSpace(candidate.SerialNumber)
	candVID := strings.ToLower(strings.TrimSpace(candidate.VendorID))
	candPID := strings.ToLower(strings.TrimSpace(candidate.ProductID))
	if stored.SerialNumber != "" && candSerial != "" && stored.SerialNumber != candSerial {
		return 0
	}
	if stored.VendorID != "" && candVID != "" && stored.VendorID != candVID {
		return 0
	}
	if stored.ProductID != "" && candPID != "" && stored.ProductID != candPID {
		return 0
	}

	// Serial+vendor match is very strong evidence.
	if stored.SerialNumber != "" && candSerial != "" && stored.SerialNumber == candSerial &&
		stored.VendorID != "" && stored.VendorID == candVID {
		score += 15000
	}

	// PCSC fake IMEIs may collide with a real modem fixed IMEI. Skip the
	// IMEI signal when the device kind does not agree.
	isPCSC := candidate.HardwareKind == pcsc.HardwareKind || config.DeviceType == store.DeviceTypeUSBSIMReader
	if !isPCSC && config.ModemIMEI != "" && entry.Snapshot != nil &&
		strings.EqualFold(strings.TrimSpace(config.ModemIMEI), strings.TrimSpace(entry.Snapshot.IMEI)) {
		score += 10000
	}

	// For PCSC readers, match by USB serial number only.
	// USB serial is stable across USB port changes.
	if isPCSC {
		if config.ControlDevice != "" && candidate.SerialNumber != "" {
			if config.ControlDevice == candidate.SerialNumber {
				score += 300 // USB serial number match
			} else {
				return 0 // Different PCSC reader
			}
		} else {
			return 0 // Cannot determine PCSC match
		}
	}
	// For DJI/Baiwang devices, match by IMEI stored in ControlDevice.
	// Each module has a unique fixed IMEI that serves as primary identity.
	isDJI := config.DeviceType == store.DeviceTypeDJI4G || modem.IsDJI4GUSB(candidate.VendorID, candidate.ProductID) || modem.IsBaiwangDevice(candidate.Product)
	if isDJI {
		if config.ControlDevice != "" {
			candIMEI := ""
			if entry.Snapshot != nil {
				candIMEI = strings.TrimSpace(entry.Snapshot.IMEI)
			}
			if candIMEI == "" {
				candIMEI = strings.TrimSpace(candidate.SerialNumber)
			}
			if candIMEI != "" && strings.EqualFold(config.ControlDevice, candIMEI) {
				score += 300 // IMEI match = same DJI device
			} else {
				return 0 // Different DJI device
			}
		} else {
			return 0 // Cannot determine DJI match
		}
	}
	if config.ATPort != "" &&
		(config.ATPort == candidate.ATPort.Path || config.ATPort == candidate.ATPort.OpenPath()) {
		score += 300
	}
	if config.ControlDevice != "" &&
		(config.ControlDevice == candidate.QMIControl || config.ControlDevice == candidate.ATPort.OpenPath()) {
		score += 300
	}
	if config.USBPath != "" && config.USBPath == candidate.USBPath {
		score += 100
	}
	if entry.ID == configuredID {
		score += 25
	}
	return score
}
