package server

import (
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleHTTPSSettings(w http.ResponseWriter, r *http.Request) {
	if s.https == nil {
		writeError(w, http.StatusServiceUnavailable, "https_unavailable", "self-signed HTTPS is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.https.State(r.Host)})
	case http.MethodPut:
		var request struct {
			Enabled *bool `json:"enabled"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.Enabled != nil && !*request.Enabled {
			writeError(w, http.StatusBadRequest, "https_required", "HTTPS is required and cannot be disabled")
			return
		}
		state, err := s.https.SetEnabled(r.Context(), true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "https_update_failed", err.Error())
			return
		}
		state = s.https.State(r.Host)
		s.recordAudit(r.Context(), "admin", "settings.https.update", "settings", "https", "success", "enabled")
		writeJSON(w, http.StatusOK, map[string]any{"data": state})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleHTTPSCertificate(w http.ResponseWriter, r *http.Request) {
	if s.https == nil {
		writeError(w, http.StatusServiceUnavailable, "https_unavailable", "self-signed HTTPS is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		certificate, err := s.https.CertificatePEM()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "certificate_unavailable", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="vonet-https.crt"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(certificate)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(certificate)
	case http.MethodPost:
		var request struct {
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"private_key"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		state, err := s.https.InstallCertificate(
			r.Context(),
			[]byte(request.Certificate),
			[]byte(request.PrivateKey),
		)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_certificate", err.Error())
			return
		}
		state = s.https.State(r.Host)
		state.RestartPending = s.updateRestart != nil
		s.recordAudit(r.Context(), "admin", "settings.https.certificate.update", "settings", "https", "success", "custom certificate installed; service restart scheduled")
		writeJSON(w, http.StatusOK, map[string]any{"data": state})
		s.scheduleRestartAfterResponse(w)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) scheduleRestartAfterResponse(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if s.updateRestart == nil {
		return
	}
	restart := s.updateRestart
	logger := s.logger
	go func() {
		time.Sleep(time.Second)
		if err := restart(logger); err != nil && logger != nil {
			logger.Error("restart after HTTPS certificate update failed", "error", err)
		}
	}()
}
