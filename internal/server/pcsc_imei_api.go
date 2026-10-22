package server

import (
	"errors"
	"net/http"
	"strings"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/pcscimei"
	"vonet/internal/store"
)

func (s *Server) routePCSCIMEIPoolAPI(w http.ResponseWriter, r *http.Request, cleanPath string) bool {
	if cleanPath == "pcsc-imei-pool" {
		s.handlePCSCIMEIPool(w, r)
		return true
	}
	if strings.HasPrefix(cleanPath, "pcsc-imei-pool/") {
		s.handlePCSCIMEIPoolItem(w, r, strings.TrimPrefix(cleanPath, "pcsc-imei-pool/"))
		return true
	}
	return false
}

func (s *Server) handlePCSCIMEIPool(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writePCSCIMEIPool(w, r)
	case http.MethodPost:
		var request struct {
			IMEI string `json:"imei"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if _, err := s.store.AddPCSCIMEIPool(r.Context(), request.IMEI); err != nil {
			s.writePCSCIMEIError(w, err)
			return
		}
		s.recordAudit(r.Context(), "admin", "pcsc_imei_pool.add", "pcsc_imei", strings.TrimSpace(request.IMEI), "success", "")
		s.writePCSCIMEIPool(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handlePCSCIMEIPoolItem(w http.ResponseWriter, r *http.Request, imei string) {
	switch r.Method {
	case http.MethodPut:
		var request struct {
			IMEI string `json:"imei"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if _, err := s.store.UpdatePCSCIMEIPool(r.Context(), imei, request.IMEI); err != nil {
			s.writePCSCIMEIError(w, err)
			return
		}
		s.recordAudit(r.Context(), "admin", "pcsc_imei_pool.update", "pcsc_imei", imei, "success", request.IMEI)
		s.writePCSCIMEIPool(w, r)
	case http.MethodDelete:
		if err := s.store.DeletePCSCIMEIPool(r.Context(), imei); err != nil {
			s.writePCSCIMEIError(w, err)
			return
		}
		s.recordAudit(r.Context(), "admin", "pcsc_imei_pool.delete", "pcsc_imei", imei, "success", "")
		s.writePCSCIMEIPool(w, r)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) writePCSCIMEIPool(w http.ResponseWriter, r *http.Request) {
	values, err := s.store.ListPCSCIMEIPool(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		status := "available"
		if value.DeviceID != "" {
			status = "leased_" + value.LeaseMode
		}
		items = append(items, map[string]any{
			"imei":              value.IMEI,
			"status":            status,
			"bound_device_id":   value.DeviceID,
			"bound_device_name": value.DeviceName,
			"lease_mode":        value.LeaseMode,
			"created_at":        value.CreatedAt.Unix(),
			"updated_at":        value.UpdatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"items": items}})
}

func (s *Server) writePCSCIMEIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPCSCIMEIExists):
		writeError(w, http.StatusConflict, "pcsc_imei_exists", "this IMEI already exists in the pool")
	case errors.Is(err, store.ErrPCSCIMEIInUse):
		writeError(w, http.StatusConflict, "pcsc_imei_in_use", "this IMEI is bound to a reader; rebind the reader before editing or deleting it")
	case errors.Is(err, store.ErrPCSCIMEIUnavailable):
		writeError(w, http.StatusConflict, "pcsc_imei_unavailable", "this IMEI is unavailable")
	case errors.Is(err, store.ErrPCSCIMEIPoolExhausted):
		writeError(w, http.StatusConflict, "pcsc_imei_pool_exhausted", "the PCSC IMEI pool has no available values")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "pcsc_imei_not_found", "the device IMEI lease or requested pool IMEI was not found")
	case errors.Is(err, store.ErrImmutableReaderDeviceType):
		writeError(w, http.StatusConflict, "immutable_reader_device_type", "USB SIM reader device type cannot be changed to or from another device type")
	case errors.Is(err, pcscimei.ErrInvalidIMEI):
		writeError(w, http.StatusBadRequest, "invalid_imei", pcscimei.ErrInvalidIMEI.Error())
	default:
		s.writeStoreError(w, err)
	}
}

func isReaderDeviceType(value string) bool {
	return store.NormalizeDeviceType(value) == store.DeviceTypeUSBSIMReader
}

func validateReaderTypeTransition(current, next string) error {
	if isReaderDeviceType(current) != isReaderDeviceType(next) {
		return store.ErrImmutableReaderDeviceType
	}
	return nil
}

func validatePCSCPhysicalType(requested string, entry device.Device) error {
	if (entry.Candidate.HardwareKind == pcsc.HardwareKind) != isReaderDeviceType(requested) {
		return store.ErrImmutableReaderDeviceType
	}
	return nil
}

func pcscSerial(entry device.Device) string {
	return strings.TrimSpace(entry.Candidate.SerialNumber)
}

func pcscEntryMatches(entry device.Device, id, serial, usbPath string) bool {
	if entry.Candidate.HardwareKind != pcsc.HardwareKind {
		return false
	}
	entrySerial := pcscSerial(entry)
	if serial != "" && entrySerial != "" {
		return serial == entrySerial
	}
	if id != "" && entry.ID == id && (serial == "" || entrySerial == serial) {
		return true
	}
	return usbPath != "" && strings.TrimSpace(entry.Candidate.USBPath) == usbPath
}

func findPCSCDiscoveredDevice(devices []device.Device, id, serial, usbPath string) *device.Device {
	var matches []*device.Device
	for index := range devices {
		if pcscEntryMatches(devices[index], id, serial, usbPath) {
			matches = append(matches, &devices[index])
		}
	}
	if len(matches) != 1 {
		return nil
	}
	return matches[0]
}

func pcscPhysicalMatchScore(configuredID string, config store.Device, entry device.Device) int {
	if !pcscEntryMatches(entry, configuredID, strings.TrimSpace(config.ControlDevice), strings.TrimSpace(config.USBPath)) {
		return 0
	}
	score := 300
	if pcscSerial(entry) != "" && strings.TrimSpace(config.ControlDevice) == pcscSerial(entry) {
		score += 200
	}
	if entry.ID == configuredID {
		score += 25
	}
	return score
}

func isDJIEntry(entry device.Device) bool {
	return modem.IsDJI4GUSB(entry.Candidate.VendorID, entry.Candidate.ProductID) ||
		modem.IsBaiwangDevice(entry.Candidate.Product)
}
