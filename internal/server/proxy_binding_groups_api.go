package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"vonet/internal/store"
)

type proxyBindingGroupPayload struct {
	ID          string   `json:"id"`
	DeviceID    string   `json:"device_id"`
	ICCID       string   `json:"iccid"`
	ProfileName string   `json:"profile_name"`
	ProxyIDs    []string `json:"proxy_ids"`
}

func (s *Server) routeProxyBindingGroups(w http.ResponseWriter, r *http.Request, cleanPath string) bool {
	segments := splitAPIPath(cleanPath)
	switch {
	case len(segments) == 1 && segments[0] == "proxy-binding-groups":
		s.handleProxyBindingGroups(w, r)
	case len(segments) == 2 && segments[0] == "proxy-binding-groups":
		s.handleProxyBindingGroup(w, r, segments[1])
	case len(segments) == 3 && segments[0] == "proxy-binding-groups" && segments[2] == "routes":
		s.handleProxyBindingGroupRoutes(w, r, segments[1])
	default:
		return false
	}
	return true
}

func proxyBindingGroupResponse(value store.DeviceProxyBindingGroupWithRoutes) map[string]any {
	routes := make([]map[string]any, 0, len(value.Routes))
	for _, route := range value.Routes {
		routes = append(routes, map[string]any{
			"id":                route.ID,
			"upstream_proxy_id": route.UpstreamProxyID,
			"priority":          route.Priority,
		})
	}
	return map[string]any{
		"id":           value.ID,
		"device_id":    publicProxyDeviceID(value.DeviceID, value.ICCID),
		"iccid":        value.ICCID,
		"profile_name": value.ProfileName,
		"routes":       routes,
	}
}

func (s *Server) handleProxyBindingGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		values, err := s.store.ListDeviceProxyBindingGroups(r.Context(), deviceID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		result := make([]map[string]any, 0, len(values))
		for _, value := range values {
			result = append(result, proxyBindingGroupResponse(value))
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": result})
	case http.MethodPost:
		var payload proxyBindingGroupPayload
		if err := s.decodeJSON(w, r, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		group, err := s.validateProxyBindingGroupPayload(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_proxy_binding", err.Error())
			return
		}
		proxyIDs, err := validateProxyRouteIDs(payload.ProxyIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_proxy_routes", err.Error())
			return
		}
		if len(proxyIDs) == 0 {
			if err := s.store.DeleteDeviceProxyBindingGroup(r.Context(), group.DeviceID, group.ICCID); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.writeStoreError(w, err)
				return
			}
			reconnected, reconnectErr := s.requestProxyBindingRouteChange(group.DeviceID, group.ICCID)
			writeJSON(w, http.StatusOK, map[string]any{"data": s.proxyBindingReconnectMeta(map[string]any{
				"device_id":    group.DeviceID,
				"iccid":        group.ICCID,
				"profile_name": group.ProfileName,
				"routes":       []map[string]any{},
			}, reconnected, reconnectErr)})
			return
		}
		if err := s.store.SaveDeviceProxyBindingGroup(r.Context(), group, proxyIDs); err != nil {
			s.writeStoreError(w, err)
			return
		}
		reconnected, reconnectErr := s.requestProxyBindingRouteChange(group.DeviceID, group.ICCID)
		saved, err := s.store.DeviceProxyBindingGroup(r.Context(), group.DeviceID, group.ICCID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": s.proxyBindingReconnectMeta(proxyBindingGroupResponse(saved), reconnected, reconnectErr)})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleProxyBindingGroup(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.store.DeviceProxyBindingGroupByID(r.Context(), id)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": proxyBindingGroupResponse(value)})
	case http.MethodDelete:
		value, err := s.store.DeviceProxyBindingGroupByID(r.Context(), id)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if err := s.store.DeleteDeviceProxyBindingGroupByID(r.Context(), id); err != nil {
			s.writeStoreError(w, err)
			return
		}
		reconnected, reconnectErr := s.requestProxyBindingRouteChange(value.DeviceID, value.ICCID)
		writeJSON(w, http.StatusOK, map[string]any{"data": s.proxyBindingReconnectMeta(map[string]any{"deleted": true}, reconnected, reconnectErr)})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleProxyBindingGroupRoutes(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var payload struct {
		ProxyIDs []string `json:"proxy_ids"`
	}
	if err := s.decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	proxyIDs, err := validateProxyRouteIDs(payload.ProxyIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_proxy_routes", err.Error())
		return
	}
	group, err := s.store.DeviceProxyBindingGroupByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if len(proxyIDs) == 0 {
		if err := s.store.DeleteDeviceProxyBindingGroupByID(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, err)
			return
		}
		reconnected, reconnectErr := s.requestProxyBindingRouteChange(group.DeviceID, group.ICCID)
		group.Routes = nil
		writeJSON(w, http.StatusOK, map[string]any{"data": s.proxyBindingReconnectMeta(proxyBindingGroupResponse(group), reconnected, reconnectErr)})
		return
	}
	if err := s.store.ReplaceDeviceProxyBindingRoutes(r.Context(), id, proxyIDs); err != nil {
		s.writeStoreError(w, err)
		return
	}
	reconnected, reconnectErr := s.requestProxyBindingRouteChange(group.DeviceID, group.ICCID)
	saved, err := s.store.DeviceProxyBindingGroupByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.proxyBindingReconnectMeta(proxyBindingGroupResponse(saved), reconnected, reconnectErr)})
}

func (s *Server) validateProxyBindingGroupPayload(payload proxyBindingGroupPayload) (store.DeviceProxyBindingGroup, error) {
	deviceID := strings.TrimSpace(payload.DeviceID)
	iccid := strings.TrimSpace(payload.ICCID)
	if !validProfileICCID(iccid) {
		return store.DeviceProxyBindingGroup{}, errors.New("profile ICCID must contain 18 to 22 digits")
	}
	deviceID, err := s.proxyBindingDeviceIDForSave(context.Background(), deviceID, iccid, true)
	if err != nil {
		return store.DeviceProxyBindingGroup{}, err
	}
	if _, err := validateProxyRouteIDs(payload.ProxyIDs); err != nil {
		return store.DeviceProxyBindingGroup{}, err
	}
	for _, proxyID := range payload.ProxyIDs {
		upstream, err := s.store.UpstreamProxy(context.Background(), strings.TrimSpace(proxyID))
		if err != nil {
			return store.DeviceProxyBindingGroup{}, err
		}
		if !upstream.Enabled {
			return store.DeviceProxyBindingGroup{}, errors.New("disabled upstream proxies cannot be added to a manual route list")
		}
	}
	name := strings.TrimSpace(payload.ProfileName)
	if name == "" {
		name = iccid
	}
	value := store.DeviceProxyBindingGroup{DeviceID: deviceID, ICCID: iccid, ProfileName: name}
	value.ID = payload.ID
	return value, nil
}

// Route changes restart Wi-Fi Calling immediately so the persisted route list is
// re-evaluated from its first candidate, including when IMS is already stable.
func (s *Server) requestProxyBindingRouteChange(deviceID, iccid string) (bool, error) {
	if s.vowifi == nil {
		return false, nil
	}
	binding, err := s.store.SimCardBinding(context.Background(), strings.TrimSpace(iccid))
	if err == nil && binding.State == store.SimCardBindingActive && strings.TrimSpace(binding.DeviceID) != "" {
		deviceID = binding.DeviceID
	}
	if strings.TrimSpace(deviceID) == "" {
		return false, nil
	}
	return s.requestProfileProxyRouteReconnect(deviceID, iccid)
}

func (s *Server) proxyBindingReconnectMeta(response map[string]any, requested bool, err error) map[string]any {
	response["reconnect_requested"] = requested
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("VoWiFi reconnect request failed after proxy binding change", "error", err)
		}
		response["reconnect_error"] = "VoWiFi reconnect request failed"
	}
	return response
}
