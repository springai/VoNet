package main

import (
	"strings"

	"vonet/internal/device"
	"vonet/internal/pcsc"
)

func pcscEntryMatchesForStartup(entry device.Device, id, serial, usbPath string) bool {
	if entry.Candidate.HardwareKind != pcsc.HardwareKind {
		return false
	}
	entrySerial := strings.TrimSpace(entry.Candidate.SerialNumber)
	if serial != "" && entrySerial != "" {
		return serial == entrySerial
	}
	if id != "" && entry.ID == id {
		return true
	}
	return usbPath != "" && strings.TrimSpace(entry.Candidate.USBPath) == usbPath
}
