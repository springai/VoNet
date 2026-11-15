package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"vonet/internal/device"
	"vonet/internal/store"
)

type simCardPayload struct {
	ICCID       string `json:"iccid"`
	DisplayName string `json:"display_name"`
	Notes       string `json:"notes"`
	CardType    string `json:"card_type"`
	IMSI        string `json:"imsi"`
	MSISDN      string `json:"msisdn"`
	Carrier     string `json:"carrier"`
	SPN         string `json:"spn"`
	MCC         string `json:"mcc"`
	MNC         string `json:"mnc"`
	HomePLMN    string `json:"home_plmn"`
	EID         string `json:"eid"`
	ProfileAID  string `json:"profile_aid"`
	DeviceID    string `json:"device_id"`
	HostType    string `json:"host_type"`
	RuntimeMode string `json:"runtime_mode"`
}

type simCardPatchPayload struct {
	DisplayName *string `json:"display_name"`
	Notes       *string `json:"notes"`
	CardType    *string `json:"card_type"`
	IMSI        *string `json:"imsi"`
	MSISDN      *string `json:"msisdn"`
	Carrier     *string `json:"carrier"`
	SPN         *string `json:"spn"`
	MCC         *string `json:"mcc"`
	MNC         *string `json:"mnc"`
	HomePLMN    *string `json:"home_plmn"`
	EID         *string `json:"eid"`
	ProfileAID  *string `json:"profile_aid"`
	HostType    *string `json:"host_type"`
}

type simCardImportPayload struct {
	Cards []simCardPayload `json:"cards"`
}

func (s *Server) routeCardAPI(w http.ResponseWriter, r *http.Request, cleanPath string) bool {
	cleanPath = strings.Trim(cleanPath, "/")
	if cleanPath == "cards" {
		s.handleSimCards(w, r)
		return true
	}
	if cleanPath == "cards/candidates" {
		s.handleSimCardCandidates(w, r)
		return true
	}
	if cleanPath == "cards/policies" {
		s.handleCardPolicies(w, r)
		return true
	}
	if cleanPath == "cards/import" {
		s.handleSimCardImport(w, r)
		return true
	}
	segments := splitAPIPath(cleanPath)
	if len(segments) < 2 || segments[0] != "cards" {
		return false
	}
	iccid := segments[1]
	if iccid == "" {
		writeError(w, http.StatusBadRequest, "invalid_iccid", "ICCID is empty")
		return true
	}
	if len(segments) == 2 {
		s.handleSimCard(w, r, iccid)
		return true
	}
	if _, err := s.store.SimCard(r.Context(), iccid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "card_not_registered", "save the SIM card before managing it")
			return true
		}
		s.writeStoreError(w, err)
		return true
	}
	switch {
	case len(segments) == 3 && segments[2] == "policy":
		s.handleCardPolicy(w, r, iccid)
	case len(segments) == 3 && segments[2] == "apns":
		s.handleCardAPNProfiles(w, r, iccid, "")
	case len(segments) == 4 && segments[2] == "apns":
		s.handleCardAPNProfiles(w, r, iccid, segments[3])
	default:
		return false
	}
	return true
}

func (s *Server) handleSimCardImport(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var request simCardImportPayload
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(request.Cards) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_card_import", "card import batch is empty")
		return
	}
	if len(request.Cards) > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_card_import", "card import batch cannot contain more than 1000 cards")
		return
	}
	cards := make([]store.SimCard, 0, len(request.Cards))
	for index, payload := range request.Cards {
		card, err := normalizeSimCardPayload(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_card_import", fmt.Sprintf("card %d: %v", index+1, err))
			return
		}
		card.Source = "import"
		cards = append(cards, card)
	}
	result, err := s.store.ImportSimCards(r.Context(), cards)
	if err != nil {
		writeError(w, http.StatusBadRequest, "card_import_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"imported": len(cards),
		"created":  result.Created,
		"updated":  result.Updated,
	}})
}

func (s *Server) handleSimCards(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cards, err := s.store.ListSimCards(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		bindings, err := s.store.ListSimCardBindings(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		bindingByICCID := make(map[string][]store.SimCardBinding, len(bindings))
		for _, binding := range bindings {
			bindingByICCID[binding.ICCID] = append(bindingByICCID[binding.ICCID], binding)
		}
		result := make([]map[string]any, 0, len(cards))
		for _, card := range cards {
			response := s.simCardPresentation(card, bindingByICCID[card.ICCID])
			if policy, policyErr := s.store.CardPolicy(r.Context(), card.ICCID); policyErr == nil {
				response["policy"] = cardPolicyResponse(policy)
				response["policy_status"] = s.cardPolicyStatus(policy, bindingByICCID[card.ICCID])
			} else {
				response["policy_status"] = "missing"
			}
			result = append(result, response)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"cards": result}})
	case http.MethodPost:
		var request simCardPayload
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		card, err := normalizeSimCardPayload(request)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_sim_card", err.Error())
			return
		}
		if _, err := s.store.SimCard(r.Context(), card.ICCID); err == nil {
			writeError(w, http.StatusConflict, "iccid_exists", "a card with this ICCID is already registered")
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, err)
			return
		}
		now := time.Now().UTC()
		card.CreatedAt = now
		card.UpdatedAt = now
		policy := defaultCardPolicy(card.ICCID)
		policy.Source = "manual"
		policy.VoWiFiEnabled = false
		policy.AirplaneEnabled = true
		policy.NetworkEnabled = false
		if requestedRuntimeMode, modeErr := store.NormalizeCardRuntimeMode(request.RuntimeMode); modeErr == nil {
			policy.RuntimeMode = requestedRuntimeMode
		} else {
			writeError(w, http.StatusBadRequest, "invalid_card_policy", modeErr.Error())
			return
		}
		if policy.RuntimeMode == "" && strings.TrimSpace(request.DeviceID) != "" {
			policy.RuntimeMode = s.defaultRuntimeMode(r, request.DeviceID)
		}
		if policy.RuntimeMode == "pcsc" {
			policy.VoWiFiEnabled = true
			policy.AirplaneEnabled = true
			policy.NetworkEnabled = false
		}
		if err := s.store.CreateSimCardWithPolicy(r.Context(), card, policy); err != nil {
			if errors.Is(err, store.ErrAlreadyExists) {
				writeError(w, http.StatusConflict, "iccid_exists", "a card with this ICCID is already registered")
				return
			}
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": simCardResponse(card)})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleSimCard(w http.ResponseWriter, r *http.Request, iccid string) {
	normalized, err := store.NormalizeSimICCID(iccid)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_iccid", err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		card, err := s.store.SimCard(r.Context(), normalized)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		matches := make([]store.SimCardBinding, 0, 1)
		if bindings, bindingErr := s.store.ListSimCardBindings(r.Context()); bindingErr == nil {
			for _, binding := range bindings {
				if binding.ICCID == card.ICCID {
					matches = append(matches, binding)
				}
			}
		}
		response := s.simCardPresentation(card, matches)
		if policy, policyErr := s.store.CardPolicy(r.Context(), card.ICCID); policyErr == nil {
			response["policy"] = cardPolicyResponse(policy)
			response["policy_status"] = s.cardPolicyStatus(policy, matches)
		} else {
			response["policy_status"] = "missing"
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": response})
	case http.MethodPatch:
		existing, err := s.store.SimCard(r.Context(), normalized)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		var request simCardPatchPayload
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := applySimCardPatch(existing, request)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_sim_card", err.Error())
			return
		}
		if err := s.store.UpdateSimCard(r.Context(), updated); err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": simCardResponse(updated)})
	case http.MethodDelete:
		if err := s.store.DeleteSimCard(r.Context(), normalized); err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"deleted": true}})
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleSimCardCandidates(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if s.devices == nil {
		writeError(w, http.StatusServiceUnavailable, "device_manager_unavailable", "device manager is unavailable")
		return
	}
	registered := make(map[string]struct{})
	cards, err := s.store.ListSimCards(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	for _, card := range cards {
		registered[card.ICCID] = struct{}{}
	}
	now := time.Now().UTC()
	for _, entry := range s.devices.List() {
		if !entry.Discovered || entry.Snapshot == nil {
			continue
		}
		iccid, normalizeErr := store.NormalizeSimICCID(entry.Snapshot.ICCID)
		if normalizeErr != nil {
			continue
		}
		hostType := ""
		if entry.Candidate.HardwareKind == "pcsc" {
			hostType = store.DeviceTypeUSBSIMReader
		}
		if _, err := s.store.TouchSimCardCandidate(r.Context(), store.SimCardCandidate{
			ICCID:            iccid,
			DeviceID:         s.candidateDeviceID(entry),
			PhysicalDeviceID: entry.ID,
			HostType:         hostType,
			CardType:         store.SimCardTypePhysical,
			IMSI:             strings.TrimSpace(entry.Snapshot.IMSI),
			Carrier:          strings.TrimSpace(entry.Snapshot.OperatorName),
			SPN:              strings.TrimSpace(entry.Snapshot.SPN),
			FirstSeenAt:      now,
			LastSeenAt:       now,
		}); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	candidates, err := s.store.ListSimCardCandidates(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	result := make([]map[string]any, 0)
	for _, candidate := range candidates {
		conflict := s.liveCardConflict(candidate.ICCID)
		_, isRegistered := registered[candidate.ICCID]
		state := candidate.State
		if now.Sub(candidate.LastSeenAt) > 30*time.Second {
			state = store.SimCardCandidateOffline
		}
		if conflict {
			state = "conflict"
		}
		result = append(result, map[string]any{
			"iccid":              candidate.ICCID,
			"device_id":          candidate.DeviceID,
			"physical_device_id": candidate.PhysicalDeviceID,
			"host_type":          candidate.HostType,
			"card_type":          candidate.CardType,
			"imsi":               candidate.IMSI,
			"carrier":            candidate.Carrier,
			"spn":                candidate.SPN,
			"state":              state,
			"stable":             state == store.SimCardCandidateStable,
			"conflict":           conflict,
			"first_seen_at":      candidate.FirstSeenAt,
			"last_seen_at":       candidate.LastSeenAt,
			"registered":         isRegistered,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"cards": result}})
}

func (s *Server) candidateDeviceID(entry device.Device) string {
	configs, err := s.store.ListDevices(context.Background())
	if err != nil {
		return entry.ID
	}
	for _, config := range configs {
		if physical, physicalID, present := s.physicalForConfig(config); present &&
			(strings.EqualFold(physical.ID, entry.ID) || strings.EqualFold(physicalID, entry.ID)) {
			return config.ID
		}
	}
	return entry.ID
}

func normalizeSimCardPayload(payload simCardPayload) (store.SimCard, error) {
	iccid, err := store.NormalizeSimICCID(payload.ICCID)
	if err != nil {
		return store.SimCard{}, err
	}
	cardType := strings.ToLower(strings.TrimSpace(payload.CardType))
	if cardType == "" {
		if strings.TrimSpace(payload.EID) != "" || strings.TrimSpace(payload.ProfileAID) != "" {
			cardType = store.SimCardTypeESIM
		} else {
			cardType = store.SimCardTypePhysical
		}
	}
	if cardType != store.SimCardTypePhysical && cardType != store.SimCardTypeESIM {
		return store.SimCard{}, errors.New("card type must be physical_sim or esim_profile")
	}
	return store.SimCard{
		ICCID:       iccid,
		DisplayName: strings.TrimSpace(payload.DisplayName),
		Notes:       strings.TrimSpace(payload.Notes),
		CardType:    cardType,
		IMSI:        strings.TrimSpace(payload.IMSI),
		MSISDN:      strings.TrimSpace(payload.MSISDN),
		Carrier:     strings.TrimSpace(payload.Carrier),
		SPN:         strings.TrimSpace(payload.SPN),
		MCC:         strings.TrimSpace(payload.MCC),
		MNC:         strings.TrimSpace(payload.MNC),
		HomePLMN:    strings.TrimSpace(payload.HomePLMN),
		EID:         strings.TrimSpace(payload.EID),
		ProfileAID:  strings.TrimSpace(payload.ProfileAID),
		HostType:    strings.ToLower(strings.TrimSpace(payload.HostType)),
		Source:      "manual",
	}, nil
}

func applySimCardPatch(existing store.SimCard, patch simCardPatchPayload) (store.SimCard, error) {
	assign := func(target *string, value *string, normalize func(string) string) {
		if value != nil {
			*target = normalize(*value)
		}
	}
	assign(&existing.DisplayName, patch.DisplayName, strings.TrimSpace)
	assign(&existing.Notes, patch.Notes, strings.TrimSpace)
	assign(&existing.IMSI, patch.IMSI, strings.TrimSpace)
	assign(&existing.MSISDN, patch.MSISDN, strings.TrimSpace)
	assign(&existing.Carrier, patch.Carrier, strings.TrimSpace)
	assign(&existing.SPN, patch.SPN, strings.TrimSpace)
	assign(&existing.MCC, patch.MCC, strings.TrimSpace)
	assign(&existing.MNC, patch.MNC, strings.TrimSpace)
	assign(&existing.HomePLMN, patch.HomePLMN, strings.TrimSpace)
	assign(&existing.EID, patch.EID, strings.TrimSpace)
	assign(&existing.ProfileAID, patch.ProfileAID, func(value string) string {
		return strings.ToUpper(strings.TrimSpace(value))
	})
	assign(&existing.HostType, patch.HostType, func(value string) string {
		return strings.ToLower(strings.TrimSpace(value))
	})
	if patch.CardType != nil {
		cardType := strings.ToLower(strings.TrimSpace(*patch.CardType))
		if cardType != store.SimCardTypePhysical && cardType != store.SimCardTypeESIM {
			return store.SimCard{}, errors.New("card type must be physical_sim or esim_profile")
		}
		existing.CardType = cardType
	}
	existing.UpdatedAt = time.Now().UTC()
	return existing, nil
}

func (s *Server) defaultRuntimeMode(r *http.Request, deviceID string) string {
	config, err := s.store.Device(r.Context(), strings.TrimSpace(deviceID))
	if err != nil {
		return ""
	}
	if config.DeviceType == store.DeviceTypeUSBSIMReader {
		return "pcsc"
	}
	return strings.ToLower(strings.TrimSpace(config.DeviceBackend))
}

func (s *Server) cardPolicyIsReader(ctx context.Context, iccid string) bool {
	if card, err := s.store.SimCard(ctx, iccid); err == nil &&
		card.HostType == store.DeviceTypeUSBSIMReader {
		return true
	}
	if binding, err := s.store.SimCardBinding(ctx, iccid); err == nil && binding.DeviceID != "" {
		if config, deviceErr := s.store.Device(ctx, binding.DeviceID); deviceErr == nil {
			return config.DeviceType == store.DeviceTypeUSBSIMReader
		}
	}
	if policy, err := s.store.CardPolicy(ctx, iccid); err == nil {
		return strings.EqualFold(strings.TrimSpace(policy.RuntimeMode), "pcsc")
	}
	return false
}

func simCardResponse(card store.SimCard) map[string]any {
	return map[string]any{
		"iccid":        card.ICCID,
		"display_name": card.DisplayName,
		"notes":        card.Notes,
		"card_type":    card.CardType,
		"imsi":         card.IMSI,
		"msisdn":       card.MSISDN,
		"carrier":      card.Carrier,
		"spn":          card.SPN,
		"mcc":          card.MCC,
		"mnc":          card.MNC,
		"mcc_mnc":      strings.TrimSpace(strings.TrimSpace(card.MCC) + strings.TrimSpace(card.MNC)),
		"home_plmn":    card.HomePLMN,
		"eid":          card.EID,
		"profile_aid":  card.ProfileAID,
		"host_type":    card.HostType,
		"source":       card.Source,
		"created_at":   card.CreatedAt,
		"updated_at":   card.UpdatedAt,
	}
}

func (s *Server) simCardPresentation(card store.SimCard, bindings []store.SimCardBinding) map[string]any {
	response := simCardResponse(card)
	response["registered"] = true
	response["bindings"] = simCardBindingsResponse(bindings)
	response["status"] = simCardStatus(bindings)
	response["conflict"] = response["status"] == "conflict"
	if s.liveCardConflict(card.ICCID) {
		response["status"] = "conflict"
		response["conflict"] = true
	}
	if len(bindings) > 0 {
		binding := bindings[0]
		online := binding.State == store.SimCardBindingActive
		presentation := map[string]any{
			"device_id":          binding.DeviceID,
			"physical_device_id": binding.PhysicalDeviceID,
			"eid":                binding.EID,
			"profile_aid":        binding.ProfileAID,
			"generation":         binding.Generation,
			"state":              binding.State,
			"bound_at":           binding.BoundAt,
			"last_seen_at":       binding.LastSeenAt,
			"online":             online,
		}
		if binding.DeviceID != "" {
			if config, err := s.store.Device(context.Background(), binding.DeviceID); err == nil {
				presentation["device_name"] = config.Name
				presentation["device_type"] = config.DeviceType
				presentation["backend_mode"] = config.DeviceBackend
				presentation["at_port"] = config.ATPort
			}
			if entry, _, present := s.deviceEntryByID(binding.DeviceID); present {
				presentation["running"] = entry.Discovered
				presentation["online"] = online && entry.Discovered
			}
		}
		response["binding"] = presentation
		response["first_seen_at"] = binding.BoundAt
		response["last_seen_at"] = binding.LastSeenAt
		response["last_active_at"] = binding.BoundAt
	}
	return response
}

func (s *Server) liveCardMatches(iccid string) int {
	if s.devices == nil {
		return 0
	}
	target := strings.TrimSpace(iccid)
	count := 0
	for _, entry := range s.devices.List() {
		if !entry.Discovered || entry.Snapshot == nil ||
			!strings.EqualFold(strings.TrimSpace(entry.Snapshot.ICCID), target) {
			continue
		}
		count++
	}
	return count
}

func (s *Server) liveCardConflict(iccid string) bool {
	return s.liveCardMatches(iccid) > 1
}

func (s *Server) deviceEntryByID(deviceID string) (entry device.Device, physicalID string, present bool) {
	config, err := s.store.Device(context.Background(), deviceID)
	if err != nil {
		return device.Device{}, "", false
	}
	value, physical, ok := s.physicalForConfig(config)
	return value, physical, ok
}

func simCardBindingsResponse(bindings []store.SimCardBinding) []map[string]any {
	result := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, map[string]any{
			"iccid":              binding.ICCID,
			"device_id":          binding.DeviceID,
			"physical_device_id": binding.PhysicalDeviceID,
			"eid":                binding.EID,
			"profile_aid":        binding.ProfileAID,
			"generation":         binding.Generation,
			"state":              binding.State,
			"stable_since":       binding.StableSince,
			"bound_at":           binding.BoundAt,
			"last_seen_at":       binding.LastSeenAt,
			"cleared_at":         binding.ClearedAt,
		})
	}
	return result
}

func simCardStatus(bindings []store.SimCardBinding) string {
	active := 0
	for _, binding := range bindings {
		switch binding.State {
		case store.SimCardBindingConflict:
			return "conflict"
		case store.SimCardBindingActive:
			active++
		}
	}
	if active == 1 {
		return "active"
	}
	if active > 1 {
		return "conflict"
	}
	if len(bindings) > 0 {
		return "offline"
	}
	return "offline"
}

func (s *Server) cardPolicyStatus(policy store.CardPolicy, bindings []store.SimCardBinding) string {
	if len(bindings) == 0 {
		return "offline"
	}
	binding := bindings[0]
	if binding.State == store.SimCardBindingConflict {
		return "suspended"
	}
	if binding.State != store.SimCardBindingActive {
		return "waiting"
	}
	config, err := s.store.Device(context.Background(), binding.DeviceID)
	if err != nil {
		return "failed"
	}
	reader := config.DeviceType == store.DeviceTypeUSBSIMReader
	if reader {
		if config.VoWiFiEnabled && !config.NetworkEnabled && strings.EqualFold(config.DeviceBackend, "pcsc") {
			return "applied"
		}
		return "pending"
	}
	runtimeMatches := strings.TrimSpace(policy.RuntimeMode) == "" ||
		strings.EqualFold(config.DeviceBackend, strings.TrimSpace(policy.RuntimeMode))
	if config.VoWiFiEnabled == policy.VoWiFiEnabled &&
		config.NetworkEnabled == policy.NetworkEnabled &&
		runtimeMatches {
		return "applied"
	}
	return "pending"
}
