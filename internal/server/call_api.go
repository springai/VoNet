package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"vonet/internal/modem"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

const maxCallDuration = 10 * time.Minute

// CLIP (Calling Line Identification Presentation) support.
// Some modems (e.g. Quectel) do not include the caller number in +CLCC
// responses for incoming calls. We enable +CLIP=1 to get the number via
// the +CLIP unsolicited result code and cache it per device.
var (
	clipCallerNumbers sync.Map // map[string]string deviceID -> last caller number
	clipEnabled       sync.Map // map[string]bool deviceID -> whether AT+CLIP=1 was sent
)

// parseCLIPNumber extracts the phone number from a +CLIP URC line.
// Format: +CLIP: "<number>",<type>[,...]
func parseCLIPNumber(urc string) string {
	upper := strings.ToUpper(strings.TrimSpace(urc))
	if !strings.HasPrefix(upper, "+CLIP:") {
		return ""
	}
	body := strings.TrimSpace(urc[6:]) // skip "+CLIP:"
	if len(body) < 2 || body[0] != '"' {
		return ""
	}
	end := strings.Index(body[1:], `"`)
	if end < 0 {
		return ""
	}
	return body[1 : end+1]
}

// updateCLIPFromURCs scans URC lines for +CLIP entries and caches the caller number.
// Returns the cached number for the device, or empty string if not available.
func (s *Server) updateCLIPFromURCs(deviceID string, urcs []string) string {
	for _, urc := range urcs {
		if num := parseCLIPNumber(urc); num != "" {
			clipCallerNumbers.Store(deviceID, num)
			return num
		}
	}
	if v, ok := clipCallerNumbers.Load(deviceID); ok {
		return v.(string)
	}
	return ""
}

// ensureCLIPEnabled sends AT+CLIP=1 once per device to enable caller ID URCs.
func (s *Server) ensureCLIPEnabled(ctx context.Context, physicalID, deviceID string) {
	if _, loaded := clipEnabled.LoadOrStore(deviceID, true); loaded {
		return
	}
	if _, err := s.devices.ExecuteAT(ctx, physicalID, "AT+CLIP=1"); err != nil {
		clipCallerNumbers.Delete(deviceID)
		clipEnabled.Delete(deviceID)
		s.logger.Warn("failed to enable CLIP", "device_id", deviceID, "error", err)
	}
}

// clearCLIPCache removes cached CLIP data for a device (e.g. on hangup).
func clearCLIPCache(deviceID string) {
	clipCallerNumbers.Delete(deviceID)
}

func (s *Server) handleCalls(w http.ResponseWriter, r *http.Request, config store.Device, physicalID string) bool {
	if !requireMethod(w, r, http.MethodGet) {
		return true
	}
	transport := s.callTransport(config.ID)
	if transport == "vowifi" {
		controller, ok := s.vowifi.(VoWiFiCallController)
		if !ok {
			writeError(w, http.StatusNotImplemented, "vowifi_voice_unavailable", "the active VoWiFi IMS session does not expose voice-call signalling")
			return true
		}
		calls, err := controller.Calls(config.ID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "vowifi_call_failed", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"device_id": config.ID, "transport": transport, "calls": calls,
		}})
		return true
	}
	response, err := s.devices.ExecuteAT(r.Context(), physicalID, "AT+CLCC")
	if err != nil {
		s.writeDeviceError(w, err)
		return true
	}
	clipNum := s.updateCLIPFromURCs(config.ID, response.URCs)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"device_id": config.ID,
			"transport": transport,
			"calls":     parseCLCCWithCLIP(config.ID, response, clipNum),
			"raw":       response.Text(),
		},
	})
	return true
}

func (s *Server) handleCallAction(w http.ResponseWriter, r *http.Request, config store.Device, physicalID, action string) bool {
	if !requireMethod(w, r, http.MethodPost) {
		return true
	}
	command := ""
	duration := time.Duration(0)
	number := ""
	callID := ""
	var digit string
	switch action {
	case "dial":
		var request struct {
			Number          string `json:"number"`
			DurationSeconds int    `json:"duration_seconds"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		number = strings.TrimSpace(request.Number)
		if !validDialNumber(number) {
			writeError(w, http.StatusBadRequest, "invalid_number", "phone number is invalid")
			return true
		}
		duration = time.Duration(request.DurationSeconds) * time.Second
		if duration < 0 || duration > maxCallDuration {
			writeError(w, http.StatusBadRequest, "invalid_duration", "duration_seconds must be 0 (no automatic hang-up) or between 1 and 600")
			return true
		}
		command = "ATD" + number + ";"
	case "answer":
		var request struct {
			CallID string `json:"call_id"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		callID = strings.TrimSpace(request.CallID)
		command = "ATA"
	case "hangup":
		var request struct {
			CallID string `json:"call_id"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		callID = strings.TrimSpace(request.CallID)
		command = "ATH"
	case "reject":
		var rejectReq struct {
			CallID string `json:"call_id"`
		}
		if err := s.decodeJSON(w, r, &rejectReq); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		callID = strings.TrimSpace(rejectReq.CallID)
		command = "ATH"
	case "dtmf":
		var dtmfReq struct {
			Digit  string `json:"digit"`
			CallID string `json:"call_id"`
		}
		if err := s.decodeJSON(w, r, &dtmfReq); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		digit = strings.TrimSpace(dtmfReq.Digit)
		if len(digit) != 1 || !((digit[0] >= '0' && digit[0] <= '9') || digit[0] == '*' || digit[0] == '#') {
			writeError(w, http.StatusBadRequest, "invalid_digit", "dtmf digit must be 0-9, *, or #")
			return true
		}
		callID = strings.TrimSpace(dtmfReq.CallID)
		command = "AT+VTS=" + digit
	default:
		writeError(w, http.StatusNotFound, "not_found", "call action not found")
		return true
	}

	transport := s.callTransport(config.ID)
	if transport == "vowifi" {
		controller, ok := s.vowifi.(VoWiFiCallController)
		if !ok {
			writeError(w, http.StatusNotImplemented, "vowifi_voice_unavailable", "the active VoWiFi IMS session does not expose voice-call signalling")
			return true
		}
		var result any
		var err error
		switch action {
		case "dial":
			result, err = controller.DialCall(context.Background(), config.ID, number)
		case "answer":
			callID, err = resolveVoWiFiCallID(controller, config.ID, callID, "ringing")
			if err == nil {
				result, err = controller.AnswerCall(context.Background(), config.ID, callID)
			}
		case "hangup":
			callID, err = resolveVoWiFiCallID(controller, config.ID, callID, "")
			if err == nil {
				err = controller.HangupCall(context.Background(), config.ID, callID)
			}
		case "dtmf":
			err = controller.SendDTMF(context.Background(), config.ID, callID, digit)
		}
		if err != nil {
			s.logger.Warn("VoWiFi call operation failed",
				"category", "call", "event", "call."+action,
				"device_id", config.ID, "number", number, "call_id", callID,
				"transport", transport, "raw_error", err,
			)
			writeError(w, http.StatusBadGateway, "vowifi_call_failed", err.Error())
			return true
		}
		if action == "dial" {
			if call, ok := result.(vowifi.Call); ok {
				callID = call.ID
			}
			if duration > 0 {
				go s.hangupVoWiFiAfter(config.ID, callID, duration)
			}
		}
		s.recordAudit(r.Context(), "admin", "call."+action, "device", config.ID, "success", transport)
		if action == "dial" || action == "answer" || action == "reject" {
			recNumber := number
			if recNumber == "" {
				if call, ok := result.(vowifi.Call); ok {
					recNumber = call.Number
				}
			}
			recDir := "outgoing"
			recStatus := "answered"
			if action == "answer" {
				recDir = "incoming"
			}
			if action == "reject" {
				recDir = "incoming"
				recStatus = "missed"
			}
			_ = s.store.InsertCallRecord(store.CallRecord{DeviceID: config.ID, Number: recNumber, Direction: recDir, Status: recStatus, Transport: transport, StartedAt: time.Now().Unix()})
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]any{
			"accepted": true, "action": action, "number": number, "call_id": callID,
			"duration_seconds": int(duration / time.Second), "transport": transport, "call": result,
		}})
		return true
	}
	operationContext, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	response, err := s.devices.ExecuteAT(operationContext, physicalID, command)
	cancel()
	if err != nil {
		s.writeDeviceError(w, err)
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(response.Final), "OK") {
		s.logger.Warn("cellular call operation rejected",
			"category", "call", "event", "call."+action,
			"device_id", config.ID, "number", number, "transport", transport,
			"modem_final", response.Final, "raw_response", response.Text(),
		)
		writeError(w, http.StatusBadGateway, "call_rejected", "modem did not accept the call action")
		return true
	}
	if action == "dial" {
		if duration > 0 {
			go s.hangupAfter(config.ID, physicalID, duration)
		}
	}
	s.recordAudit(r.Context(), "admin", "call."+action, "device", config.ID, "success", transport)
	if action == "dial" || action == "answer" || action == "reject" {
		recNumber := number
		if recNumber == "" {
			if clccResp, clccErr := s.devices.ExecuteAT(r.Context(), physicalID, "AT+CLCC"); clccErr == nil {
				for _, clccLine := range clccResp.Lines {
					clccLine = strings.TrimSpace(clccLine)
					if !strings.HasPrefix(strings.ToUpper(clccLine), "+CLCC:") {
						continue
					}
					clccFields := strings.Split(strings.TrimSpace(strings.TrimPrefix(clccLine, "+CLCC:")), ",")
					if len(clccFields) > 5 {
						recNumber = strings.Trim(strings.TrimSpace(clccFields[5]), "\"")
						if recNumber != "" {
							break
						}
					}
				}
			}
		}
		recDir := "outgoing"
		recStatus := "answered"
		if action == "answer" {
			recDir = "incoming"
		}
		if action == "reject" {
			recDir = "incoming"
			recStatus = "missed"
		}
		_ = s.store.InsertCallRecord(store.CallRecord{DeviceID: config.ID, Number: recNumber, Direction: recDir, Status: recStatus, Transport: transport, StartedAt: time.Now().Unix()})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"data": map[string]any{
			"accepted": true, "action": action, "number": number,
			"duration_seconds": int(duration / time.Second), "transport": transport,
		},
	})
	return true
}

func resolveVoWiFiCallID(controller VoWiFiCallController, deviceID, id, requiredState string) (string, error) {
	if id != "" {
		return id, nil
	}
	calls, err := controller.Calls(deviceID)
	if err != nil {
		return "", err
	}
	for _, call := range calls {
		if call.State != "ended" && call.State != "failed" && (requiredState == "" || call.State == requiredState) {
			return call.ID, nil
		}
	}
	return "", errors.New("no matching active call")
}

func (s *Server) hangupVoWiFiAfter(deviceID, callID string, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	<-timer.C
	controller, ok := s.vowifi.(VoWiFiCallController)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := controller.HangupCall(ctx, deviceID, callID); err != nil {
		s.logger.Warn("automatic VoWiFi call hangup failed", "device_id", deviceID, "call_id", callID, "error", err)
	}
}

func (s *Server) callTransport(deviceID string) string {
	if s.vowifi != nil {
		// Enabled is only the desired card policy. Calls can use IMS only after
		// registration has actually completed; otherwise keep using the modem's
		// circuit-switched call path instead of routing into an unavailable IMS
		// session.
		if state, err := s.vowifi.State(deviceID); err == nil && state.IMSReady {
			return "vowifi"
		}
	}
	return "cellular"
}

func (s *Server) hangupAfter(deviceID, physicalID string, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	<-timer.C
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.devices.ExecuteAT(ctx, physicalID, "ATH"); err != nil {
		s.logger.Warn("automatic call hangup failed", "device_id", deviceID, "error", err)
	}
}

func validDialNumber(value string) bool {
	if len(value) < 2 || len(value) > 32 {
		return false
	}
	for index, character := range value {
		if character >= '0' && character <= '9' || (index == 0 && character == '+') || character == '*' || character == '#' {
			continue
		}
		return false
	}
	return true
}

func parseCLCCWithCLIP(deviceID string, response modem.Response, clipNum string) []map[string]any {
	result := make([]map[string]any, 0)
	for _, line := range response.Lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "+CLCC:") {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+CLCC:")), ",")
		if len(fields) < 5 {
			continue
		}
		integer := func(index int) int {
			value, _ := strconv.Atoi(strings.TrimSpace(fields[index]))
			return value
		}
		call := map[string]any{
			"index": integer(0), "direction": integer(1), "state": integer(2),
			"mode": integer(3), "multiparty": integer(4), "raw": line,
		}
		if len(fields) > 5 {
			call["number"] = strings.Trim(strings.TrimSpace(fields[5]), `"`)
		}
		result = append(result, call)
	}
	// Filter phantom calls BEFORE supplementing with CLIP, so stale entries
	// with empty numbers are not accidentally given a CLIP number.
	if len(result) > 0 {
		allPhantom := true
		for _, c := range result {
			state, _ := c["state"].(int)
			dir, _ := c["direction"].(int)
			num, _ := c["number"].(string)
			if state != 0 || dir != 1 || num != "" {
				allPhantom = false
				break
			}
		}
		if allPhantom {
			return make([]map[string]any, 0)
		}
	}
	// Supplement empty numbers with CLIP caller ID after phantom filtering.
	if clipNum != "" {
		for _, c := range result {
			if num, _ := c["number"].(string); num == "" {
				c["number"] = clipNum
			}
		}
	}
	return result
}

func parseCLCC(response modem.Response) []map[string]any {
	return parseCLCCWithCLIP("", response, "")
}
func (s *Server) handleCallHistory(w http.ResponseWriter, r *http.Request, config store.Device) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return true
	}
	if r.Method == http.MethodPost {
		var request struct {
			Number    string `json:"number"`
			Direction string `json:"direction"`
			Status    string `json:"status"`
		}
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return true
		}
		direction := strings.ToLower(strings.TrimSpace(request.Direction))
		status := strings.ToLower(strings.TrimSpace(request.Status))
		if direction != "incoming" && direction != "outgoing" {
			writeError(w, http.StatusBadRequest, "invalid_direction", "direction must be incoming or outgoing")
			return true
		}
		if status != "answered" && status != "missed" && status != "rejected" {
			writeError(w, http.StatusBadRequest, "invalid_status", "status must be answered, missed, or rejected")
			return true
		}
		_ = s.store.InsertCallRecord(store.CallRecord{
			DeviceID:  config.ID,
			Number:    strings.TrimSpace(request.Number),
			Direction: direction,
			Status:    status,
			Transport: s.callTransport(config.ID),
			StartedAt: time.Now().Unix(),
		})
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"recorded": true}})
		return true
	}
	query := r.URL.Query().Get("q")
	limit := 50
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	if query != "" {
		records, err := s.store.SearchCallHistory(config.ID, query, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"records": records,
			"total":   len(records),
		}})
		return true
	}
	records, total, err := s.store.ListCallHistory(config.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"records": records,
		"total":   total,
	}})
	return true
}

func (s *Server) handleDeleteCallRecord(w http.ResponseWriter, r *http.Request, config store.Device) bool {
	if !requireMethod(w, r, http.MethodDelete) {
		return true
	}
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		// Delete all
		if err := s.store.DeleteAllCallHistory(config.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"deleted": true}})
		return true
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "call record id must be an integer")
		return true
	}
	if err := s.store.DeleteCallRecord(config.ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"deleted": true}})
	return true

}
func (s *Server) handleRecordMissedCall(w http.ResponseWriter, r *http.Request, config store.Device) bool {
	if !requireMethod(w, r, http.MethodPost) {
		return true
	}
	var request struct {
		Number string `json:"number"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return true
	}
	number := strings.TrimSpace(request.Number)
	if number == "" {
		number = "unknown"
	}
	_ = s.store.InsertCallRecord(store.CallRecord{
		DeviceID:  config.ID,
		Number:    number,
		Direction: "incoming",
		Status:    "missed",
		Transport: "cellular",
		StartedAt: time.Now().Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"recorded": true}})
	return true
}
