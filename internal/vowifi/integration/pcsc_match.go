package integration

import (
	"strings"

	"vonet/internal/device"
	"vonet/internal/pcsc"
	"vonet/internal/store"
)

func isPCSCConfig(config store.Device, entry device.Device) bool {
	return config.DeviceType == store.DeviceTypeUSBSIMReader ||
		entry.Candidate.HardwareKind == pcsc.HardwareKind
}

func pcscIntegrationScore(configuredID string, config store.Device, entry device.Device) int {
	if entry.Candidate.HardwareKind != pcsc.HardwareKind {
		return 0
	}
	serial := strings.TrimSpace(config.ControlDevice)
	entrySerial := strings.TrimSpace(entry.Candidate.SerialNumber)
	if serial != "" && entrySerial != "" {
		if serial != entrySerial {
			return 0
		}
		return 525
	}
	if configuredID != "" && entry.ID == configuredID {
		return 325
	}
	if config.USBPath != "" && strings.TrimSpace(entry.Candidate.USBPath) == strings.TrimSpace(config.USBPath) {
		return 300
	}
	return 0
}
