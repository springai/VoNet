package server

import (
	"net/http"

	"vonet/internal/developer"
)

func (s *Server) handleSystemLimits(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeSystemLimits(w, r)
	case http.MethodPut:
		var request struct {
			DeviceLimit           *int  `json:"device_limit"`
			SMSHourlyLimit        *int  `json:"sms_hourly_limit"`
			AutoClearModemStorage *bool `json:"auto_clear_modem_storage"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.DeviceLimit == nil && request.SMSHourlyLimit == nil && request.AutoClearModemStorage == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "at least one system limit is required")
			return
		}
		if request.DeviceLimit != nil && (*request.DeviceLimit < 1 || *request.DeviceLimit > developer.MaxDeviceLimit) {
			writeError(w, http.StatusBadRequest, "invalid_device_limit", "device limit is outside the supported range")
			return
		}
		if request.SMSHourlyLimit != nil && *request.SMSHourlyLimit < 1 {
			writeError(w, http.StatusBadRequest, "invalid_sms_hourly_limit", "SMS hourly limit must be greater than or equal to 1")
			return
		}
		if request.DeviceLimit != nil {
			if err := developer.SetDeviceLimit(r.Context(), s.store, *request.DeviceLimit); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_device_limit", err.Error())
				return
			}
			s.recordAudit(r.Context(), "admin", "settings.limits.device_limit", "settings", "limits", "success", "device limit updated")
		}
		if request.SMSHourlyLimit != nil {
			if err := developer.SetSMSHourlyLimit(r.Context(), s.store, *request.SMSHourlyLimit); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_sms_hourly_limit", err.Error())
				return
			}
			s.recordAudit(r.Context(), "admin", "settings.limits.sms_hourly_limit", "settings", "limits", "success", "global SMS hourly limit updated")
		}
		if request.AutoClearModemStorage != nil {
			if err := developer.SetAutoClearModemStorage(r.Context(), s.store, *request.AutoClearModemStorage); err != nil {
				s.writeStoreError(w, err)
				return
			}
			s.recordAudit(r.Context(), "admin", "settings.sms.auto_clear_modem_storage", "settings", "sms", "success", "modem SMS auto-clear updated")
		}
		s.writeSystemLimits(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) writeSystemLimits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"device_limit":             developer.DeviceLimit(r.Context(), s.store, true),
		"default_device_limit":     developer.DefaultDeviceLimit,
		"max_device_limit":         developer.MaxDeviceLimit,
		"sms_hourly_limit":         developer.SMSHourlyLimit(r.Context(), s.store),
		"default_sms_hourly_limit": developer.DefaultSMSHourlyLimit,
		"auto_clear_modem_storage": developer.AutoClearModemStorage(r.Context(), s.store),
	}})
}
