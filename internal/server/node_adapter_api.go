package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"vonet/internal/nodeadapter"
)

func (s *Server) routeNodeAdapterAPI(w http.ResponseWriter, r *http.Request, cleanPath string) bool {
	if cleanPath != "node-adapters" &&
		!strings.HasPrefix(cleanPath, "node-adapters/") &&
		cleanPath != "node-adapters/global-config" {
		return false
	}
	if s.nodeAdapter == nil {
		writeError(w, http.StatusForbidden, "service_unavailable", "Node adapter service is not initialized")
		return true
	}

	segments := splitAPIPath(cleanPath)

	// /api/node-adapters/parse-link
	if len(segments) == 2 && segments[1] == "parse-link" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		var body struct {
			Link string `json:"link"`
		}
		if err := s.decodeJSON(w, r, &body); err != nil {
			return true
		}
		parsed, err := nodeadapter.ParseShareLink(body.Link)
		if err != nil {
			writeError(w, http.StatusBadRequest, "parse_failed", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": parsed})
		return true
	}

	// /api/node-adapters/parse-batch
	if len(segments) == 2 && segments[1] == "parse-batch" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := s.decodeJSON(w, r, &body); err != nil {
			return true
		}
		parsed, err := nodeadapter.ParseSubscriptionLinks(body.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, "parse_failed", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": parsed})
		return true
	}

	// /api/node-adapters/tcp-ping
	if len(segments) == 2 && segments[1] == "tcp-ping" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		var body struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if err := s.decodeJSON(w, r, &body); err != nil {
			return true
		}
		result := nodeadapter.TCPPing(body.Host, body.Port)
		writeJSON(w, http.StatusOK, map[string]any{"data": result})
		return true
	}

	// /api/node-adapters/detect-ips
	if len(segments) == 2 && segments[1] == "detect-ips" {
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		ips := nodeadapter.DetectLocalIPv4()
		writeJSON(w, http.StatusOK, map[string]any{"data": ips})
		return true
	}

	// /api/node-adapters/environment
	if len(segments) == 2 && segments[1] == "environment" {
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		status := s.nodeAdapter.EnvironmentCheck(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"data": status})
		return true
	}

	// /api/node-adapters/environment/create-db
	if len(segments) == 3 && segments[1] == "environment" && segments[2] == "create-db" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.CreateDB(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "create_db_failed", err.Error())
			return true
		}
		status := s.nodeAdapter.EnvironmentCheck(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"data": status})
		return true
	}

	// /api/node-adapters/singbox/status
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "status" {
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		status := nodeadapter.CheckSingboxStatus()
		writeJSON(w, http.StatusOK, map[string]any{"data": status})
		return true
	}

	// /api/node-adapters/singbox/install
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "install" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		var body struct {
			Version string `json:"version"`
		}
		_ = s.decodeJSON(w, r, &body)
		err := nodeadapter.InstallSingbox(body.Version, func(msg string) {
			s.logger.Info("singbox install", "msg", msg)
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "install_failed", err.Error())
			return true
		}
		status := nodeadapter.CheckSingboxStatus()
		writeJSON(w, http.StatusOK, map[string]any{"data": status})
		return true
	}

	// /api/node-adapters/singbox/uninstall
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "uninstall" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := nodeadapter.UninstallSingbox(); err != nil {
			writeError(w, http.StatusInternalServerError, "uninstall_failed", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"uninstalled": true}})
		return true
	}

	// /api/node-adapters/singbox/scan — scan local directory for sing-box binary
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "scan" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		status, err := nodeadapter.ScanSingbox()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"data": status})
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": status})
		return true
	}

	// /api/node-adapters/singbox/logs — recent sing-box logs
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "logs" {
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		logs := s.nodeAdapter.SingboxLogs().All()
		writeJSON(w, http.StatusOK, map[string]any{"data": logs})
		return true
	}

	// /api/node-adapters/singbox/logs/stream — SSE stream of sing-box logs
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "logs-stream" {
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming_not_supported", "SSE not supported")
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// Send recent logs first.
		recent := s.nodeAdapter.SingboxLogs().Recent(100)
		for _, entry := range recent {
			encoded, _ := json.Marshal(entry)
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", string(encoded))
		}
		flusher.Flush()

		ch, unsub := s.nodeAdapter.SingboxLogs().Subscribe(64)
		defer unsub()

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return true
			case entry, ok := <-ch:
				if !ok {
					return true
				}
				encoded, _ := json.Marshal(entry)
				_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", string(encoded))
				flusher.Flush()
			case <-heartbeat.C:
				_, _ = fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
				flusher.Flush()
			}
		}
	}

	// /api/node-adapters/singbox/pause — stop all running sing-box processes
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "pause" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		n := s.nodeAdapter.PauseAll()
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"paused": n}})
		return true
	}

	// /api/node-adapters/singbox/resume — restart all enabled adapters
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "resume" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		n, err := s.nodeAdapter.ResumeAll(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, "resume_failed", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"resumed": n}})
		return true
	}

	// /api/node-adapters/singbox/install-stream — SSE endpoint for real-time install progress
	if len(segments) == 3 && segments[1] == "singbox" && segments[2] == "install-stream" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming_not_supported", "SSE not supported")
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		var body struct {
			Version string `json:"version"`
		}
		_ = s.decodeJSON(w, r, &body)

		sendEvent := func(event string, data any) {
			encoded, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(encoded))
			flusher.Flush()
		}

		sendEvent("progress", map[string]string{"msg": "开始安装..."})

		err := nodeadapter.InstallSingbox(body.Version, func(msg string) {
			sendEvent("progress", map[string]string{"msg": msg})
		})
		if err != nil {
			sendEvent("error", map[string]string{"msg": err.Error()})
			return true
		}
		status := nodeadapter.CheckSingboxStatus()
		sendEvent("done", status)
		return true
	}

	// /api/node-adapters/global-config
	if len(segments) == 2 && segments[1] == "global-config" {
		switch r.Method {
		case http.MethodGet:
			cfg := s.nodeAdapter.GlobalConfig(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{"data": cfg})
		case http.MethodPut, http.MethodPost:
			var cfg nodeadapter.GlobalConfig
			if err := s.decodeJSON(w, r, &cfg); err != nil {
				return true
			}
			if err := s.nodeAdapter.SaveGlobalConfig(r.Context(), cfg); err != nil {
				writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": cfg})
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		return true
	}

	// /api/node-adapters
	if len(segments) == 1 {
		switch r.Method {
		case http.MethodGet:
			adapters, err := s.nodeAdapter.List(r.Context())
			if err != nil {
				s.logger.Error("LIST node adapters FAILED", "error", err)
				writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
				return true
			}
			result := make([]nodeadapter.NodeAdapter, len(adapters))
			for i, a := range adapters {
				result[i] = a.Redacted()
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": result})
		case http.MethodPost:
			var payload nodeAdapterPayload
			if err := s.decodeJSON(w, r, &payload); err != nil {
				return true
			}
			a := payload.toModel()
			s.logger.Info("POST node-adapters payload", "name", a.Name, "socks5_configured", a.Socks5Configured, "http_configured", a.HTTPConfigured, "listen_port", a.ListenPort)
			created, err := s.nodeAdapter.Create(r.Context(), a)
			if err != nil {
				s.logger.Error("CREATE node adapter FAILED", "error", err)
				writeError(w, http.StatusBadRequest, "node_adapter_invalid", err.Error())
				return true
			}
			writeJSON(w, http.StatusCreated, map[string]any{"data": created.Redacted()})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		return true
	}

	if len(segments) < 2 || strings.TrimSpace(segments[1]) == "" {
		writeError(w, http.StatusNotFound, "not_found", "node adapter endpoint not found")
		return true
	}
	id := segments[1]

	// /api/node-adapters/:id/actions/check
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "check" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		result, err := s.nodeAdapter.Check(r.Context(), id)
		response := probeResultToMap(result)
		if err != nil {
			response["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": response})
		return true
	}

	// /api/node-adapters/:id/actions/enable
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "enable" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.Enable(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "enable_failed", err.Error())
			return true
		}
		a, _ := s.nodeAdapter.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		return true
	}

	// /api/node-adapters/:id/actions/disable
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "disable" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.Disable(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "disable_failed", err.Error())
			return true
		}
		a, _ := s.nodeAdapter.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		return true
	}

	// /api/node-adapters/:id/actions/convert — start sing-box SOCKS5 proxy
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "convert" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.ConvertToSocks5(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "convert_failed", err.Error())
			return true
		}
		a, _ := s.nodeAdapter.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		return true
	}

	// /api/node-adapters/:id/actions/remove-socks5 — stop sing-box SOCKS5 proxy
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "remove-socks5" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.RemoveSocks5(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "remove_failed", err.Error())
			return true
		}
		a, _ := s.nodeAdapter.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		return true
	}

	// /api/node-adapters/:id/actions/convert-http — start sing-box HTTP proxy
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "convert-http" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.ConvertToHTTP(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "convert_failed", err.Error())
			return true
		}
		a, _ := s.nodeAdapter.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		return true
	}

	// /api/node-adapters/:id/actions/remove-http — stop sing-box HTTP proxy
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "remove-http" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if err := s.nodeAdapter.RemoveHTTP(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "remove_failed", err.Error())
			return true
		}
		a, _ := s.nodeAdapter.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		return true
	}

	// /api/node-adapters/:id/actions/check-http — HTTP proxy connectivity check
	// /api/node-adapters/:id/actions/sync-status — sync running status with actual process state
	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "sync-status" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		err := s.nodeAdapter.SyncStatus(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "sync_failed", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"synced": true}})
		return true
	}

	if len(segments) == 4 && segments[2] == "actions" && segments[3] == "check-http" {
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		result, err := s.nodeAdapter.CheckHTTP(r.Context(), id)
		response := probeResultToMap(result)
		if err != nil {
			response["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": response})
		return true
	}

	// /api/node-adapters/:id
	if len(segments) == 2 {
		switch r.Method {
		case http.MethodGet:
			a, err := s.nodeAdapter.Get(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusNotFound, "not_found", "node adapter not found")
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": a.Redacted()})
		case http.MethodPut:
			var payload nodeAdapterPayload
			if err := s.decodeJSON(w, r, &payload); err != nil {
				return true
			}
			a := payload.toModel()
			updated, err := s.nodeAdapter.Update(r.Context(), id, a)
			if err != nil {
				writeError(w, http.StatusBadRequest, "update_failed", err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": updated.Redacted()})
		case http.MethodDelete:
			if err := s.nodeAdapter.Delete(r.Context(), id); err != nil {
				writeError(w, http.StatusNotFound, "not_found", "node adapter not found")
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"deleted": true}})
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		return true
	}

	writeError(w, http.StatusNotFound, "not_found", "node adapter endpoint not found")
	return true
}

type nodeAdapterPayload struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Protocol       string          `json:"protocol"`
	NodeConfigJSON json.RawMessage `json:"node_config_json"`
	ListenHost     string          `json:"listen_host"`
	ListenPort     int             `json:"listen_port"`
	HTTPListenHost string          `json:"http_listen_host"`
	HTTPListenPort int             `json:"http_listen_port"`
	UDPIP          string          `json:"udp_ip"`
	AuthEnabled    bool            `json:"auth_enabled"`
	Username       string          `json:"username"`
	Password       string          `json:"password"`
	Enabled        bool            `json:"enabled"`
	Socks5Configured bool           `json:"socks5_configured"`
	HTTPConfigured  bool            `json:"http_configured"`
}

func (p nodeAdapterPayload) toModel() nodeadapter.NodeAdapter {
	return nodeadapter.NodeAdapter{
		ID:             strings.TrimSpace(p.ID),
		Name:           strings.TrimSpace(p.Name),
		Protocol:       strings.TrimSpace(p.Protocol),
		NodeConfigJSON: p.NodeConfigJSON,
		ListenHost:     strings.TrimSpace(p.ListenHost),
		ListenPort:     p.ListenPort,
		HTTPListenHost: strings.TrimSpace(p.HTTPListenHost),
		HTTPListenPort: p.HTTPListenPort,
		UDPIP:          strings.TrimSpace(p.UDPIP),
		AuthEnabled:    p.AuthEnabled,
		Username:       strings.TrimSpace(p.Username),
		Password:       p.Password,
		Enabled:        p.Enabled,
		Socks5Configured: p.Socks5Configured,
		HTTPConfigured:  p.HTTPConfigured,
	}
}

func probeResultToMap(result any) map[string]any {
	encoded, _ := json.Marshal(result)
	response := map[string]any{}
	_ = json.Unmarshal(encoded, &response)
	return response
}