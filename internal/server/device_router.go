package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"vonet/internal/agentproto"
	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/store"
)

type deviceRouter struct {
	store  *store.Store
	local  DeviceController
	agents AgentGRPCService
}

func newDeviceRouter(database *store.Store, local DeviceController, agents AgentGRPCService) DeviceController {
	if database == nil || local == nil || agents == nil {
		return local
	}
	return &deviceRouter{store: database, local: local, agents: agents}
}

func (router *deviceRouter) binding(ctx context.Context, id string) (store.Device, string, bool, error) {
	id = strings.TrimSpace(id)
	config, err := router.store.Device(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		configs, listErr := router.store.ListDevices(ctx)
		if listErr != nil {
			return store.Device{}, "", false, listErr
		}
		var match *store.Device
		matches := 0
		for index := range configs {
			candidate := configs[index]
			if strings.TrimSpace(candidate.AgentID) == "" {
				continue
			}
			remoteID := strings.TrimSpace(candidate.RemoteDeviceID)
			if remoteID == "" {
				remoteID = candidate.ID
			}
			if remoteID == id {
				match = &candidate
				matches++
			}
		}
		if matches == 1 {
			config = *match
		} else if matches > 1 {
			return store.Device{}, "", false, fmt.Errorf("remote device ID %q is ambiguous across agents", id)
		} else {
			return store.Device{}, id, false, nil
		}
	} else if err != nil {
		return store.Device{}, "", false, err
	}
	if strings.TrimSpace(config.AgentID) == "" {
		return config, id, false, nil
	}
	remoteID := strings.TrimSpace(config.RemoteDeviceID)
	if remoteID == "" {
		remoteID = config.ID
	}
	return config, remoteID, true, nil
}

func (router *deviceRouter) remoteJSON(ctx context.Context, agentID, deviceID, action string, payload, result any) error {
	return router.remoteJSONWithProgress(ctx, agentID, deviceID, action, payload, result, nil)
}

func (router *deviceRouter) remoteJSONWithProgress(
	ctx context.Context,
	agentID, deviceID, action string,
	payload, result any,
	progress func(*agentproto.AgentEvent),
) error {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	commandID := "cmd-" + hex.EncodeToString(random)
	idempotencyKey := ""
	switch action {
	case "refresh", "network_status", "supports_clcc", "usb_net_mode", "operator_selection",
		"operator_scan", "cells", "cell_lock", "cellular_ims", "sms_list", "esim_inventory",
		"esim_list", "esim_chip_info":
		digest := sha256.Sum256([]byte(agentID + "\x00" + deviceID + "\x00" + action + "\x00" + string(rawPayload)))
		idempotencyKey = hex.EncodeToString(digest[:])
	}
	command := &agentproto.AgentCommand{
		Command: &agentproto.AgentCommand_ExecuteHardware{ExecuteHardware: &agentproto.ExecuteHardwareCommand{
			CommandId: commandID, DeviceId: deviceID, Action: action, PayloadJson: string(rawPayload),
			CommandIdempotencyKey: idempotencyKey,
		}},
	}
	var event *agentproto.AgentEvent
	if progressService, ok := router.agents.(AgentGRPCProgressService); ok {
		event, err = progressService.ExecuteWithProgress(ctx, agentID, command, progress)
	} else {
		event, err = router.agents.Execute(ctx, agentID, command)
	}
	if err != nil {
		return err
	}
	response := event.GetCommandResult()
	if response == nil {
		return fmt.Errorf("agent returned %T for hardware action %q", event.GetEvent(), action)
	}
	if !response.GetSuccess() {
		if response.GetErrorMessage() != "" {
			return errors.New(response.GetErrorMessage())
		}
		return fmt.Errorf("agent hardware action %q failed", action)
	}
	if result == nil || strings.TrimSpace(response.GetPayloadJson()) == "" {
		return nil
	}
	return json.Unmarshal([]byte(response.GetPayloadJson()), result)
}

func (router *deviceRouter) route(
	ctx context.Context,
	id, action string,
	payload, result any,
) error {
	return router.routeWithProgress(ctx, id, action, payload, result, nil)
}

func (router *deviceRouter) routeWithProgress(
	ctx context.Context,
	id, action string,
	payload, result any,
	progress func(*agentproto.AgentEvent),
) error {
	_, remoteID, remote, err := router.binding(ctx, id)
	if err != nil {
		return err
	}
	if !remote {
		return errors.New("local device cannot execute remote-only operation")
	}
	config, _, _, err := router.binding(ctx, id)
	if err != nil {
		return err
	}
	return router.remoteJSONWithProgress(ctx, config.AgentID, remoteID, action, payload, result, progress)
}

type hardwarePayload struct {
	Command          string                    `json:"command,omitempty"`
	Code             string                    `json:"code,omitempty"`
	SessionID        string                    `json:"session_id,omitempty"`
	Enabled          bool                      `json:"enabled,omitempty"`
	Request          device.NetworkRequest     `json:"request,omitempty"`
	Mode             int                       `json:"mode,omitempty"`
	Automatic        bool                      `json:"automatic,omitempty"`
	Operator         string                    `json:"operator,omitempty"`
	AccessTechnology *int                      `json:"access_technology,omitempty"`
	Target           *device.CellLockTarget    `json:"target,omitempty"`
	CellularIMSMode  device.CellularIMSMode    `json:"cellular_ims_mode,omitempty"`
	Recipient        string                    `json:"recipient,omitempty"`
	Text             string                    `json:"text,omitempty"`
	Index            int                       `json:"index,omitempty"`
	Storage          string                    `json:"storage,omitempty"`
	RawPDU           string                    `json:"raw_pdu,omitempty"`
	ICCID            string                    `json:"iccid,omitempty"`
	AIDHex           string                    `json:"aid_hex,omitempty"`
	Nickname         string                    `json:"nickname,omitempty"`
	Download         device.EsimDownloadParams `json:"download,omitempty"`
	Pin              string                    `json:"pin,omitempty"`
	Backend          string                    `json:"backend,omitempty"`
	ESIMTransport    string                    `json:"esim_transport,omitempty"`
}

type remoteDeviceSetup interface {
	SetRemoteSIMPin(agentID, remoteID, pin string) error
	SetRemoteBackend(agentID, remoteID, backend string) error
	SetRemoteESIMTransport(agentID, remoteID, transport string) error
}

func (router *deviceRouter) SetRemoteSIMPin(agentID, remoteID, pin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	return router.remoteJSON(ctx, agentID, remoteID, "set_sim_pin", hardwarePayload{Pin: pin}, nil)
}

func (router *deviceRouter) SetRemoteBackend(agentID, remoteID, backend string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	return router.remoteJSON(ctx, agentID, remoteID, "set_backend", hardwarePayload{Backend: backend}, nil)
}

func (router *deviceRouter) SetRemoteESIMTransport(agentID, remoteID, transport string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	return router.remoteJSON(ctx, agentID, remoteID, "set_esim_transport", hardwarePayload{ESIMTransport: transport}, nil)
}

func (router *deviceRouter) Discover(ctx context.Context) ([]device.Device, error) {
	return nil, device.ErrUnsupportedCapability
}

func (router *deviceRouter) List() []device.Device {
	local := router.local.List()
	configs, err := router.store.ListDevices(context.Background())
	if err != nil {
		return local
	}
	values := append([]device.Device(nil), local...)
	for _, config := range configs {
		if strings.TrimSpace(config.AgentID) == "" {
			continue
		}
		if value, err := router.Get(config.ID); err == nil {
			values = append(values, value)
		}
	}
	return values
}

func (router *deviceRouter) Get(id string) (device.Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	config, remoteID, remote, err := router.binding(ctx, id)
	if err != nil {
		return device.Device{}, err
	}
	if !remote {
		return router.local.Get(id)
	}
	for _, raw := range router.agents.Devices(config.AgentID) {
		var value device.Device
		if json.Unmarshal(raw, &value) == nil && (value.ID == remoteID || value.Candidate.ID == remoteID) {
			return value, nil
		}
	}
	return device.Device{}, device.ErrNotFound
}

func (router *deviceRouter) Refresh(ctx context.Context, id string) (device.Snapshot, error) {
	_, remoteID, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.Snapshot{}, err
		}
		return router.local.Refresh(ctx, id)
	}
	var result device.Snapshot
	err = router.route(ctx, id, "refresh", hardwarePayload{}, &result)
	_ = remoteID
	return result, err
}

func (router *deviceRouter) ExecuteAT(ctx context.Context, id, command string) (modem.Response, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return modem.Response{}, err
		}
		return router.local.ExecuteAT(ctx, id, command)
	}
	var result modem.Response
	action := "at"
	if sensitiveATCommand(command) {
		action = "at_sensitive"
	}
	err = router.route(ctx, id, action, hardwarePayload{Command: command}, &result)
	return result, err
}

func sensitiveATCommand(command string) bool {
	upper := strings.ToUpper(strings.TrimSpace(command))
	for _, prefix := range []string{"AT+CPIN", "AT+CPWD", "AT+CARDLOCK", "AT+CLCK"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func (router *deviceRouter) Reboot(ctx context.Context, id string) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		return router.local.Reboot(ctx, id)
	}
	return router.route(ctx, id, "reboot", hardwarePayload{}, nil)
}

func (router *deviceRouter) ReleaseDevice(ctx context.Context, id string) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		if local, ok := router.local.(interface {
			ReleaseDevice(context.Context, string) error
		}); ok {
			return local.ReleaseDevice(ctx, id)
		}
		return nil
	}
	return router.route(ctx, id, "release", hardwarePayload{}, nil)
}

func (router *deviceRouter) USSD(ctx context.Context, id, code string) (device.USSDResult, error) {
	return router.ussd(ctx, id, code, "ussd")
}

func (router *deviceRouter) ContinueUSSD(ctx context.Context, id, code string) (device.USSDResult, error) {
	return router.ussd(ctx, id, code, "ussd_continue")
}

func (router *deviceRouter) ussd(ctx context.Context, id, code, action string) (device.USSDResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.USSDResult{}, err
		}
		if action == "ussd" {
			return router.local.USSD(ctx, id, code)
		}
		return router.local.ContinueUSSD(ctx, id, code)
	}
	var result device.USSDResult
	err = router.route(ctx, id, action, hardwarePayload{Code: code}, &result)
	return result, err
}

func (router *deviceRouter) CancelUSSD(ctx context.Context, sessionID string) error {
	configs, err := router.store.ListDevices(ctx)
	if err != nil {
		return err
	}
	sawRemote := false
	var firstErr error
	for _, config := range configs {
		if strings.TrimSpace(config.AgentID) != "" {
			sawRemote = true
			if err := router.route(ctx, config.ID, "ussd_cancel", hardwarePayload{SessionID: sessionID}, nil); err == nil {
				return nil
			} else if firstErr == nil {
				firstErr = err
			}
		}
	}
	if sawRemote {
		return firstErr
	}
	return router.local.CancelUSSD(ctx, sessionID)
}

func (router *deviceRouter) SetFlight(ctx context.Context, id string, enabled bool) (device.FlightResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.FlightResult{}, err
		}
		return router.local.SetFlight(ctx, id, enabled)
	}
	var result device.FlightResult
	err = router.route(ctx, id, "flight", hardwarePayload{Enabled: enabled}, &result)
	return result, err
}

func (router *deviceRouter) SetNetwork(ctx context.Context, id string, request device.NetworkRequest) (device.NetworkResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.NetworkResult{}, err
		}
		return router.local.SetNetwork(ctx, id, request)
	}
	var result device.NetworkResult
	err = router.route(ctx, id, "network", hardwarePayload{Request: request}, &result)
	return result, err
}

func (router *deviceRouter) NetworkStatus(ctx context.Context, id string) (device.NetworkStatus, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.NetworkStatus{}, err
		}
		if local, ok := router.local.(interface {
			NetworkStatus(context.Context, string) (device.NetworkStatus, error)
		}); ok {
			return local.NetworkStatus(ctx, id)
		}
		return device.NetworkStatus{}, device.ErrUnsupportedCapability
	}
	var result device.NetworkStatus
	err = router.route(ctx, id, "network_status", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) USBNetMode(ctx context.Context, id string) (device.USBNetMode, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.USBNetMode{}, err
		}
		return router.local.USBNetMode(ctx, id)
	}
	var result device.USBNetMode
	err = router.route(ctx, id, "usb_net_mode", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) SetUSBNetMode(ctx context.Context, id string, mode int) (device.USBNetMode, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.USBNetMode{}, err
		}
		return router.local.SetUSBNetMode(ctx, id, mode)
	}
	var result device.USBNetMode
	err = router.route(ctx, id, "usb_net_mode_set", hardwarePayload{Mode: mode}, &result)
	return result, err
}

func (router *deviceRouter) SetUSBNetModeByPort(ctx context.Context, id string, mode int) (device.USBNetMode, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.USBNetMode{}, err
		}
		return router.local.SetUSBNetModeByPort(ctx, id, mode)
	}
	var result device.USBNetMode
	err = router.route(ctx, id, "usb_net_mode_set", hardwarePayload{Mode: mode}, &result)
	return result, err
}

func (router *deviceRouter) OperatorSelection(ctx context.Context, id string) (device.OperatorSelection, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.OperatorSelection{}, err
		}
		return router.local.OperatorSelection(ctx, id)
	}
	var result device.OperatorSelection
	err = router.route(ctx, id, "operator_selection", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) SetOperatorSelection(ctx context.Context, id string, automatic bool, operator string, accessTechnology *int) (device.OperatorSelection, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.OperatorSelection{}, err
		}
		return router.local.SetOperatorSelection(ctx, id, automatic, operator, accessTechnology)
	}
	var result device.OperatorSelection
	err = router.route(ctx, id, "operator_selection_set", hardwarePayload{
		Automatic: automatic, Operator: operator, AccessTechnology: accessTechnology,
	}, &result)
	return result, err
}

func (router *deviceRouter) ReRegisterOperator(ctx context.Context, id string) (device.OperatorSelection, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.OperatorSelection{}, err
		}
		return router.local.ReRegisterOperator(ctx, id)
	}
	var result device.OperatorSelection
	err = router.route(ctx, id, "operator_reregister", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) ScanOperators(ctx context.Context, id string) (device.OperatorScanResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.OperatorScanResult{}, err
		}
		return router.local.ScanOperators(ctx, id)
	}
	var result device.OperatorScanResult
	err = router.route(ctx, id, "operator_scan", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) SendSMS(ctx context.Context, id, recipient, text string) (device.SMSSendResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.SMSSendResult{}, err
		}
		return router.local.SendSMS(ctx, id, recipient, text)
	}
	var result device.SMSSendResult
	err = router.route(ctx, id, "sms_send", hardwarePayload{Recipient: recipient, Text: text}, &result)
	return result, err
}

func (router *deviceRouter) ListSMS(ctx context.Context, id string) (device.SMSListing, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.SMSListing{}, err
		}
		return router.local.ListSMS(ctx, id)
	}
	var result device.SMSListing
	err = router.route(ctx, id, "sms_list", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) ReadSMS(ctx context.Context, id string, index int) (device.SMSMessage, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.SMSMessage{}, err
		}
		return router.local.ReadSMS(ctx, id, index)
	}
	var result device.SMSMessage
	err = router.route(ctx, id, "sms_read", hardwarePayload{Index: index}, &result)
	return result, err
}

func (router *deviceRouter) DeleteSMS(ctx context.Context, id string, index int) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		return router.local.DeleteSMS(ctx, id, index)
	}
	return router.route(ctx, id, "sms_delete", hardwarePayload{Index: index}, nil)
}

func (router *deviceRouter) DeleteSMSFromStorage(ctx context.Context, id, storage string, index int) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		return router.local.DeleteSMSFromStorage(ctx, id, storage, index)
	}
	return router.route(ctx, id, "sms_delete_storage", hardwarePayload{Storage: storage, Index: index}, nil)
}

func (router *deviceRouter) DeleteSMSFromStorageIfMatches(ctx context.Context, id, storage string, index int, rawPDU string) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		return router.local.DeleteSMSFromStorageIfMatches(ctx, id, storage, index, rawPDU)
	}
	return router.route(ctx, id, "sms_delete_storage_match", hardwarePayload{
		Storage: storage, Index: index, RawPDU: rawPDU,
	}, nil)
}

func (router *deviceRouter) ESIMInventory(ctx context.Context, id string) ([]device.EsimInventoryEntry, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return nil, err
		}
		return router.local.ESIMInventory(ctx, id)
	}
	var result []device.EsimInventoryEntry
	err = router.route(ctx, id, "esim_inventory", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) ESIMListProfiles(ctx context.Context, id string) (device.EsimInfo, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.EsimInfo{}, err
		}
		return router.local.ESIMListProfiles(ctx, id)
	}
	var result device.EsimInfo
	err = router.route(ctx, id, "esim_list", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) ESIMSwitchProfile(ctx context.Context, id, iccid, aid string) error {
	return router.esimProfileAction(ctx, id, "esim_switch", iccid, aid, "")
}

func (router *deviceRouter) ESIMDisableProfile(ctx context.Context, id, iccid, aid string) error {
	return router.esimProfileAction(ctx, id, "esim_disable", iccid, aid, "")
}

func (router *deviceRouter) ESIMRenameProfile(ctx context.Context, id, iccid, nickname, aid string) error {
	return router.esimProfileAction(ctx, id, "esim_rename", iccid, aid, nickname)
}

func (router *deviceRouter) esimProfileAction(ctx context.Context, id, action, iccid, aid, nickname string) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		switch action {
		case "esim_switch":
			return router.local.ESIMSwitchProfile(ctx, id, iccid, aid)
		case "esim_disable":
			return router.local.ESIMDisableProfile(ctx, id, iccid, aid)
		default:
			return router.local.ESIMRenameProfile(ctx, id, iccid, nickname, aid)
		}
	}
	return router.route(ctx, id, action, hardwarePayload{ICCID: iccid, AIDHex: aid, Nickname: nickname}, nil)
}

func (router *deviceRouter) ESIMDownloadProfile(ctx context.Context, id string, params device.EsimDownloadParams, progress func(device.EsimProgress)) (*device.EsimDownloadResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return nil, err
		}
		return router.local.ESIMDownloadProfile(ctx, id, params, progress)
	}
	var result device.EsimDownloadResult
	err = router.routeWithProgress(ctx, id, "esim_download", hardwarePayload{Download: params}, &result, func(event *agentproto.AgentEvent) {
		if progress == nil {
			return
		}
		update := event.GetCommandProgress()
		if update == nil {
			return
		}
		progress(device.EsimProgress{
			Step: update.GetStage(), Msg: update.GetMessage(), Pct: int(update.GetPercent()),
		})
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (router *deviceRouter) ESIMDeleteProfile(ctx context.Context, id, iccid, aid string) (*device.EsimDeleteResult, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return nil, err
		}
		return router.local.ESIMDeleteProfile(ctx, id, iccid, aid)
	}
	var result device.EsimDeleteResult
	err = router.route(ctx, id, "esim_delete", hardwarePayload{ICCID: iccid, AIDHex: aid}, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (router *deviceRouter) ESIMChipInfo(ctx context.Context, id string) (*device.EsimChipInfo, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return nil, err
		}
		return router.local.ESIMChipInfo(ctx, id)
	}
	var result device.EsimChipInfo
	err = router.route(ctx, id, "esim_chip_info", hardwarePayload{}, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (router *deviceRouter) CellularIMS(ctx context.Context, id string) (device.CellularIMSStatus, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.CellularIMSStatus{}, err
		}
		if local, ok := router.local.(interface {
			CellularIMS(context.Context, string) (device.CellularIMSStatus, error)
		}); ok {
			return local.CellularIMS(ctx, id)
		}
		return device.CellularIMSStatus{}, device.ErrUnsupportedCapability
	}
	var result device.CellularIMSStatus
	err = router.route(ctx, id, "cellular_ims", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) SetCellularIMS(ctx context.Context, id string, mode device.CellularIMSMode) (device.CellularIMSStatus, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.CellularIMSStatus{}, err
		}
		if local, ok := router.local.(interface {
			SetCellularIMS(context.Context, string, device.CellularIMSMode) (device.CellularIMSStatus, error)
		}); ok {
			return local.SetCellularIMS(ctx, id, mode)
		}
		return device.CellularIMSStatus{}, device.ErrUnsupportedCapability
	}
	var result device.CellularIMSStatus
	err = router.route(ctx, id, "cellular_ims_set", hardwarePayload{CellularIMSMode: mode}, &result)
	return result, err
}

func (router *deviceRouter) Cells(ctx context.Context, id string) (device.CellList, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.CellList{}, err
		}
		if local, ok := router.local.(interface {
			Cells(context.Context, string) (device.CellList, error)
		}); ok {
			return local.Cells(ctx, id)
		}
		return device.CellList{}, device.ErrUnsupportedCapability
	}
	var result device.CellList
	err = router.route(ctx, id, "cells", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) CellLock(ctx context.Context, id string) (device.CellLockStatus, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.CellLockStatus{}, err
		}
		if local, ok := router.local.(interface {
			CellLock(context.Context, string) (device.CellLockStatus, error)
		}); ok {
			return local.CellLock(ctx, id)
		}
		return device.CellLockStatus{}, device.ErrUnsupportedCapability
	}
	var result device.CellLockStatus
	err = router.route(ctx, id, "cell_lock", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) SetCellLock(ctx context.Context, id string, target *device.CellLockTarget) (device.CellLockStatus, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return device.CellLockStatus{}, err
		}
		if local, ok := router.local.(interface {
			SetCellLock(context.Context, string, *device.CellLockTarget) (device.CellLockStatus, error)
		}); ok {
			return local.SetCellLock(ctx, id, target)
		}
		return device.CellLockStatus{}, device.ErrUnsupportedCapability
	}
	var result device.CellLockStatus
	err = router.route(ctx, id, "cell_lock_set", hardwarePayload{Target: target}, &result)
	return result, err
}

func (router *deviceRouter) SupportsCLCC(ctx context.Context, id string) (bool, error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return false, err
		}
		if local, ok := router.local.(interface {
			SupportsCLCC(context.Context, string) (bool, error)
		}); ok {
			return local.SupportsCLCC(ctx, id)
		}
		return true, nil
	}
	var result bool
	err = router.route(ctx, id, "supports_clcc", hardwarePayload{}, &result)
	return result, err
}

func (router *deviceRouter) SetSIMPin(id, pin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		if local, ok := router.local.(interface{ SetSIMPin(string, string) error }); ok {
			return local.SetSIMPin(id, pin)
		}
		return nil
	}
	return router.route(ctx, id, "set_sim_pin", hardwarePayload{Pin: pin}, nil)
}

func (router *deviceRouter) PINStatus(ctx context.Context, id string) (pcsc.PINState, error) {
	return router.pinCommand(ctx, id, "status", "", "", "")
}

func (router *deviceRouter) VerifyPIN(ctx context.Context, id, pin string) (pcsc.PINState, error) {
	return router.pinCommand(ctx, id, "verify", pin, "", "")
}

func (router *deviceRouter) ChangePIN(ctx context.Context, id, current, next string) (pcsc.PINState, error) {
	return router.pinCommand(ctx, id, "change", current, next, "")
}

func (router *deviceRouter) UnblockPIN(ctx context.Context, id, puk, next string) (pcsc.PINState, error) {
	return router.pinCommand(ctx, id, "unblock", "", next, puk)
}

func (router *deviceRouter) pinCommand(ctx context.Context, id, operation, pin, newPin, puk string) (pcsc.PINState, error) {
	config, remoteID, remote, err := router.binding(ctx, id)
	if err != nil {
		return pcsc.PINState{}, err
	}
	if !remote {
		local, ok := router.local.(interface {
			PINStatus(context.Context, string) (pcsc.PINState, error)
			VerifyPIN(context.Context, string, string) (pcsc.PINState, error)
			ChangePIN(context.Context, string, string, string) (pcsc.PINState, error)
			UnblockPIN(context.Context, string, string, string) (pcsc.PINState, error)
		})
		if !ok {
			return pcsc.PINState{}, device.ErrUnsupportedCapability
		}
		switch operation {
		case "status":
			return local.PINStatus(ctx, id)
		case "verify":
			return local.VerifyPIN(ctx, id, pin)
		case "change":
			return local.ChangePIN(ctx, id, pin, newPin)
		default:
			return local.UnblockPIN(ctx, id, puk, newPin)
		}
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return pcsc.PINState{}, err
	}
	event, err := router.agents.Execute(ctx, config.AgentID, &agentproto.AgentCommand{
		Command: &agentproto.AgentCommand_Pin{Pin: &agentproto.PINCommand{
			CommandId: "pin-" + hex.EncodeToString(random), ReaderId: remoteID,
			Operation: operation, Pin: pin, NewPin: newPin, Puk: puk,
		}},
	})
	if err != nil {
		return pcsc.PINState{}, err
	}
	status := event.GetPinStatus()
	if status == nil {
		return pcsc.PINState{}, fmt.Errorf("agent returned %T for PIN operation %q", event.GetEvent(), operation)
	}
	return pcsc.PINState{
		Required: status.GetRequired(), Tries: int(status.GetTries()), PUKRequired: status.GetPukRequired(),
	}, nil
}

func (router *deviceRouter) SetBackend(id, backend string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		if local, ok := router.local.(interface{ SetBackend(string, string) error }); ok {
			return local.SetBackend(id, backend)
		}
		return nil
	}
	return router.route(ctx, id, "set_backend", hardwarePayload{Backend: backend}, nil)
}

func (router *deviceRouter) SetESIMTransport(id, transport string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRemoteDeviceTimeout)
	defer cancel()
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		if local, ok := router.local.(interface{ SetESIMTransport(string, string) error }); ok {
			return local.SetESIMTransport(id, transport)
		}
		return nil
	}
	return router.route(ctx, id, "set_esim_transport", hardwarePayload{ESIMTransport: transport}, nil)
}

func (router *deviceRouter) AcquireSIMRead(ctx context.Context, id string) (func(), error) {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return nil, err
		}
		if local, ok := router.local.(interface {
			AcquireSIMRead(context.Context, string) (func(), error)
		}); ok {
			return local.AcquireSIMRead(ctx, id)
		}
		return func() {}, nil
	}
	return func() {}, nil
}

func (router *deviceRouter) ReconcileEC20MBNAfterProfileSwitch(ctx context.Context, id, iccid string) error {
	_, _, remote, err := router.binding(ctx, id)
	if err != nil || !remote {
		if err != nil {
			return err
		}
		if local, ok := router.local.(interface {
			ReconcileEC20MBNAfterProfileSwitch(context.Context, string, string) error
		}); ok {
			return local.ReconcileEC20MBNAfterProfileSwitch(ctx, id, iccid)
		}
		return nil
	}
	return nil
}

const defaultRemoteDeviceTimeout = 30 * time.Second
