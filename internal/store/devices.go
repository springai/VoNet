package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type contextExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const (
	DeviceTypeWiFi410      = "wifi_410"
	DeviceTypeDJI4G        = "dji_4g"
	DeviceTypeML307        = "ml307"
	DeviceTypePCIeEC20EC25 = "pcie_ec20_ec25"
	DeviceTypeUSBSIMReader = "usb_sim_reader"
	// DeviceTypeZXIC marks the verified ZXIC platform (USB 19d2:0536).
	// Behavioral adaptation keys on the candidate's chipset; the type is the
	// stable user-facing marker for the adapted platform.
	DeviceTypeZXIC = "zxic"
)

// NormalizeDeviceType returns a stable persisted device type identifier.
// Empty values use the legacy EC20/EC25 type for backwards compatibility.
func NormalizeDeviceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DeviceTypeWiFi410:
		return DeviceTypeWiFi410
	case DeviceTypeDJI4G:
		return DeviceTypeDJI4G
	case DeviceTypeML307:
		return DeviceTypeML307
	case DeviceTypeZXIC:
		return DeviceTypeZXIC
	case DeviceTypeUSBSIMReader:
		return DeviceTypeUSBSIMReader
	case "", DeviceTypePCIeEC20EC25:
		return DeviceTypePCIeEC20EC25
	default:
		return ""
	}
}

func (s *Store) UpsertDevice(ctx context.Context, value Device) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin device upsert: %w", err)
	}
	defer tx.Rollback()
	if err := s.upsertDeviceWithPCSCIMEILease(ctx, tx, value); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit device upsert: %w", err)
	}
	return nil
}

// SaveDeviceState stores configuration and the supplied runtime snapshots in
// one transaction. A nil runtime leaves that snapshot untouched.
func (s *Store) SaveDeviceState(
	ctx context.Context,
	value Device,
	runtime *DeviceRuntime,
	vowifi *VoWiFiRuntime,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin device state update: %w", err)
	}
	defer tx.Rollback()

	if err := upsertDevice(ctx, tx, value, s.ensureSensitive()); err != nil {
		return err
	}
	if err := ensureDevicePCSCIMEILease(ctx, tx, value); err != nil {
		return err
	}
	if runtime != nil {
		snapshot := *runtime
		if snapshot.DeviceID == "" {
			snapshot.DeviceID = value.ID
		}
		if snapshot.DeviceID != value.ID {
			return errors.New("device runtime belongs to a different device")
		}
		if err := upsertDeviceRuntime(ctx, tx, snapshot, s.ensureSensitive()); err != nil {
			return err
		}
	}
	if vowifi != nil {
		snapshot := *vowifi
		if snapshot.DeviceID == "" {
			snapshot.DeviceID = value.ID
		}
		if snapshot.DeviceID != value.ID {
			return errors.New("VoWiFi runtime belongs to a different device")
		}
		if err := upsertVoWiFiRuntime(ctx, tx, snapshot, s.ensureSensitive()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit device state update: %w", err)
	}
	return nil
}

func (s *Store) upsertDeviceWithPCSCIMEILease(
	ctx context.Context,
	executor contextQueryExecer,
	value Device,
) error {
	if err := upsertDevice(ctx, executor, value, s.ensureSensitive()); err != nil {
		return err
	}
	return ensureDevicePCSCIMEILease(ctx, executor, value)
}

func ensureDevicePCSCIMEILease(
	ctx context.Context,
	executor contextQueryExecer,
	value Device,
) error {
	if NormalizeDeviceType(value.DeviceType) != DeviceTypeUSBSIMReader {
		return nil
	}
	_, err := ensurePCSCIMEILease(ctx, executor, value.ID)
	return err
}

func upsertDevice(ctx context.Context, executor contextExecer, value Device, sensitive *fieldCipher) error {
	value.ID = strings.TrimSpace(value.ID)
	value.Name = strings.TrimSpace(value.Name)
	if value.ID == "" {
		return errors.New("device id is required")
	}
	if value.Name == "" {
		return errors.New("device name is required")
	}
	value.DeviceType = NormalizeDeviceType(value.DeviceType)
	if value.DeviceType == "" {
		return errors.New("unsupported device type")
	}
	if value.ProxyPort < 0 || value.ProxyPort > 65535 {
		return errors.New("device proxy port must be between 0 and 65535")
	}
	if value.BaudRate == 0 {
		value.BaudRate = 115200
	}
	if value.BaudRate < 1 {
		return errors.New("device baud rate must be positive")
	}
	if value.DataBits == 0 {
		value.DataBits = 8
	}
	if value.DataBits < 5 || value.DataBits > 8 {
		return errors.New("device data bits must be between 5 and 8")
	}
	if value.StopBits == 0 {
		value.StopBits = 1
	}
	if value.StopBits != 1 && value.StopBits != 2 {
		return errors.New("device stop bits must be 1 or 2")
	}
	if value.Parity == "" {
		value.Parity = "none"
	}
	value.Parity = strings.ToLower(strings.TrimSpace(value.Parity))
	switch value.Parity {
	case "none", "even", "odd", "mark", "space":
	default:
		return fmt.Errorf("unsupported device parity %q", value.Parity)
	}
	if value.DeviceBackend == "" {
		value.DeviceBackend = "at"
	}
	value.DeviceBackend = strings.ToLower(strings.TrimSpace(value.DeviceBackend))
	if value.DeviceBackend != "at" && value.DeviceBackend != "qmi" && value.DeviceBackend != "pcsc" {
		return fmt.Errorf("unsupported device backend %q", value.DeviceBackend)
	}
	if value.ESIMTransport == "" {
		value.ESIMTransport = "at"
	}
	value.ESIMTransport = strings.ToLower(strings.TrimSpace(value.ESIMTransport))
	if value.ESIMTransport != "at" && value.ESIMTransport != "qmi" && value.ESIMTransport != "pcsc" && value.ESIMTransport != "none" {
		return fmt.Errorf("unsupported eSIM transport %q", value.ESIMTransport)
	}
	value.SIMPIN = strings.TrimSpace(value.SIMPIN)
	if value.SIMPIN != "" {
		if len(value.SIMPIN) < 4 || len(value.SIMPIN) > 8 || strings.Trim(value.SIMPIN, "0123456789") != "" {
			return errors.New("SIM PIN must contain 4 to 8 digits")
		}
	}
	if value.DeviceType == DeviceTypeUSBSIMReader {
		value.ModemIMEI = ""
		value.DeviceBackend = "pcsc"
		value.ESIMTransport = "pcsc"
		value.NetworkEnabled = false
		value.SMSEnabled = true
		// Preserve the reader's desired VoWiFi state. The card-policy
		// reconciler turns VoWiFi on after a stable card binding; overwriting
		// this flag here leaves the runtime enabled while the device summary
		// incorrectly reports VoWiFi as disabled.
	}
	extra, err := normalizeJSONObject(value.Extra)
	if err != nil {
		return fmt.Errorf("normalize device extra data: %w", err)
	}
	sealedExtra, err := sensitive.seal("devices.extra_json", string(extra))
	if err != nil {
		return fmt.Errorf("encrypt device extra data: %w", err)
	}
	value.AgentID = strings.TrimSpace(value.AgentID)
	value.RemoteDeviceID = strings.TrimSpace(value.RemoteDeviceID)
	value.RemoteHardwareGeneration = strings.TrimSpace(value.RemoteHardwareGeneration)
	now := time.Now().UTC()
	createdAt := value.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := value.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}

	_, err = executor.ExecContext(ctx, `
		INSERT INTO devices (
			id, agent_id, remote_device_id, remote_hardware_generation,
			name, device_type, interface, control_device, at_port, usb_path,
			audio_device, modem_imei, apn, proxy_port, baud_rate,
			data_bits, stop_bits, parity, device_backend, esim_transport,
			qmi_use_proxy, qmi_proxy_path, qmi_proxy_executable,
			network_enabled, sms_enabled, vowifi_enabled, extra_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent_id = excluded.agent_id,
			remote_device_id = excluded.remote_device_id,
			remote_hardware_generation = excluded.remote_hardware_generation,
			name = excluded.name,
			device_type = excluded.device_type,
			interface = excluded.interface,
			control_device = excluded.control_device,
			at_port = excluded.at_port,
			usb_path = excluded.usb_path,
			audio_device = excluded.audio_device,
			modem_imei = excluded.modem_imei,
			apn = excluded.apn,
			proxy_port = excluded.proxy_port,
			baud_rate = excluded.baud_rate,
			data_bits = excluded.data_bits,
			stop_bits = excluded.stop_bits,
			parity = excluded.parity,
			device_backend = excluded.device_backend,
			esim_transport = excluded.esim_transport,
			qmi_use_proxy = excluded.qmi_use_proxy,
			qmi_proxy_path = excluded.qmi_proxy_path,
			qmi_proxy_executable = excluded.qmi_proxy_executable,
			network_enabled = excluded.network_enabled,
			sms_enabled = excluded.sms_enabled,
			vowifi_enabled = excluded.vowifi_enabled,
			extra_json = excluded.extra_json,
			updated_at = excluded.updated_at
	`,
		value.ID, value.AgentID, value.RemoteDeviceID, value.RemoteHardwareGeneration,
		value.Name, value.DeviceType, value.Interface, value.ControlDevice, value.ATPort,
		value.USBPath, value.AudioDevice, value.ModemIMEI, value.APN,
		value.ProxyPort, value.BaudRate, value.DataBits, value.StopBits,
		value.Parity, value.DeviceBackend, value.ESIMTransport,
		boolInt(value.QMIUseProxy), value.QMIProxyPath, value.QMIProxyExecutable,
		boolInt(value.NetworkEnabled), boolInt(value.SMSEnabled),
		boolInt(value.VoWiFiEnabled), sealedExtra, createdAt.Unix(),
		updatedAt.Unix(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no column named agent_id") {
			return upsertLegacyDevice(ctx, executor, value, string(extra), createdAt, updatedAt)
		}
		return fmt.Errorf("upsert device %q: %w", value.ID, err)
	}
	return nil
}

func upsertLegacyDevice(
	ctx context.Context,
	executor contextExecer,
	value Device,
	extra string,
	createdAt, updatedAt time.Time,
) error {
	_, err := executor.ExecContext(ctx, `
		INSERT INTO devices (
			id, name, device_type, interface, control_device, at_port, usb_path,
			audio_device, modem_imei, sim_pin, apn, proxy_port, baud_rate,
			data_bits, stop_bits, parity, device_backend, esim_transport,
			qmi_use_proxy, qmi_proxy_path, qmi_proxy_executable,
			network_enabled, sms_enabled, vowifi_enabled, extra_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, device_type = excluded.device_type,
			interface = excluded.interface, control_device = excluded.control_device,
			at_port = excluded.at_port, usb_path = excluded.usb_path,
			audio_device = excluded.audio_device, modem_imei = excluded.modem_imei,
			apn = excluded.apn, proxy_port = excluded.proxy_port,
			baud_rate = excluded.baud_rate, data_bits = excluded.data_bits,
			stop_bits = excluded.stop_bits, parity = excluded.parity,
			device_backend = excluded.device_backend,
			esim_transport = excluded.esim_transport,
			qmi_use_proxy = excluded.qmi_use_proxy,
			qmi_proxy_path = excluded.qmi_proxy_path,
			qmi_proxy_executable = excluded.qmi_proxy_executable,
			network_enabled = excluded.network_enabled,
			sms_enabled = excluded.sms_enabled,
			vowifi_enabled = excluded.vowifi_enabled,
			extra_json = excluded.extra_json, updated_at = excluded.updated_at
	`, value.ID, value.Name, value.DeviceType, value.Interface, value.ControlDevice,
		value.ATPort, value.USBPath, value.AudioDevice, value.ModemIMEI, value.APN,
		value.ProxyPort, value.BaudRate, value.DataBits, value.StopBits, value.Parity,
		value.DeviceBackend, value.ESIMTransport, boolInt(value.QMIUseProxy),
		value.QMIProxyPath, value.QMIProxyExecutable, boolInt(value.NetworkEnabled),
		boolInt(value.SMSEnabled), boolInt(value.VoWiFiEnabled), extra,
		createdAt.Unix(), updatedAt.Unix(),
	)
	return err
}

func (s *Store) Device(ctx context.Context, id string) (Device, error) {
	return s.scanDevice(s.db.QueryRowContext(ctx, deviceSelect+` WHERE id = ?`, id))
}

func (s *Store) DeviceByRemoteBinding(ctx context.Context, agentID, remoteDeviceID string) (Device, error) {
	agentID = strings.TrimSpace(agentID)
	remoteDeviceID = strings.TrimSpace(remoteDeviceID)
	if agentID == "" || remoteDeviceID == "" {
		return Device{}, ErrNotFound
	}
	return s.scanDevice(s.db.QueryRowContext(ctx,
		deviceSelect+` WHERE agent_id = ? AND remote_device_id = ?`, agentID, remoteDeviceID))
}

func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, deviceSelect+` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	values := make([]Device, 0)
	for rows.Next() {
		value, err := s.scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return values, nil
}

func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete device %q: %w", id, err)
	}
	defer tx.Rollback()
	var modemIMEI string
	if err := tx.QueryRowContext(ctx, `SELECT modem_imei FROM devices WHERE id = ?`, id).Scan(&modemIMEI); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read device %q before deletion: %w", id, err)
	}
	// SMS history must outlive a mutable configured device ID. Anchor any
	// legacy ID-owned rows to the physical modem before the device row and its
	// runtime records are removed.
	if strings.TrimSpace(modemIMEI) != "" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sms_messages AS legacy
			WHERE legacy.device_id = ? AND legacy.modem_imei = '' AND legacy.message_id <> ''
				AND EXISTS (
					SELECT 1 FROM sms_messages current
					WHERE current.modem_imei = ?
						AND current.message_id = legacy.message_id
				)
		`, id, strings.TrimSpace(modemIMEI)); err != nil {
			return fmt.Errorf("deduplicate SMS history for device %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sms_messages
			SET modem_imei = ?, updated_at = ?
			WHERE device_id = ? AND modem_imei = ''
		`, strings.TrimSpace(modemIMEI), time.Now().UTC().Unix(), id); err != nil {
			return fmt.Errorf("anchor SMS history for device %q: %w", id, err)
		}
	}
	if err := clearSimCardBindingsForDevice(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM sim_card_candidates WHERE device_id = ?
	`, id); err != nil {
		return fmt.Errorf("clear SIM card candidates for device %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device_proxy_binding_groups SET device_id = '', updated_at = ?
		WHERE device_id = ?
	`, time.Now().UTC().Unix(), id); err != nil {
		return fmt.Errorf("unbind proxy groups for device %q: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device_proxy_bindings SET device_id = '', updated_at = ?
		WHERE device_id = ?
	`, time.Now().UTC().Unix(), id); err != nil {
		return fmt.Errorf("unbind profile proxy bindings for device %q: %w", id, err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete device %q: %w", id, err)
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete device %q: %w", id, err)
	}
	return nil
}

const deviceSelect = `
	SELECT id, agent_id, remote_device_id, remote_hardware_generation,
		name, device_type, interface, control_device, at_port, usb_path,
		audio_device, modem_imei, apn, proxy_port, baud_rate, data_bits,
		stop_bits, parity, device_backend, esim_transport, qmi_use_proxy,
		qmi_proxy_path, qmi_proxy_executable, network_enabled, sms_enabled,
		vowifi_enabled, extra_json, created_at, updated_at
	FROM devices`

func (s *Store) scanDevice(row rowScanner) (Device, error) {
	var value Device
	var qmiUseProxy, networkEnabled, smsEnabled, vowifiEnabled int
	var extra string
	var createdAt, updatedAt int64
	err := row.Scan(
		&value.ID, &value.AgentID, &value.RemoteDeviceID, &value.RemoteHardwareGeneration,
		&value.Name, &value.DeviceType, &value.Interface, &value.ControlDevice,
		&value.ATPort, &value.USBPath, &value.AudioDevice, &value.ModemIMEI,
		&value.APN, &value.ProxyPort, &value.BaudRate, &value.DataBits,
		&value.StopBits, &value.Parity, &value.DeviceBackend,
		&value.ESIMTransport, &qmiUseProxy, &value.QMIProxyPath,
		&value.QMIProxyExecutable, &networkEnabled, &smsEnabled,
		&vowifiEnabled, &extra, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, err
	}
	value.QMIUseProxy = qmiUseProxy != 0
	value.NetworkEnabled = networkEnabled != 0
	value.SMSEnabled = smsEnabled != 0
	value.VoWiFiEnabled = vowifiEnabled != 0
	value.DeviceType = NormalizeDeviceType(value.DeviceType)
	openExtra, err := s.openField("devices.extra_json", extra)
	if err != nil {
		return Device{}, fmt.Errorf("decrypt device extra data: %w", err)
	}
	value.Extra = []byte(openExtra)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func (s *Store) UpsertDeviceRuntime(ctx context.Context, value DeviceRuntime) error {
	return upsertDeviceRuntime(ctx, s.db, value, s.ensureSensitive())
}

func upsertDeviceRuntime(
	ctx context.Context,
	executor contextExecer,
	value DeviceRuntime,
	sensitive *fieldCipher,
) error {
	if strings.TrimSpace(value.DeviceID) == "" {
		return errors.New("device runtime device id is required")
	}
	traffic, err := normalizeJSONObject(value.Traffic)
	if err != nil {
		return fmt.Errorf("normalize device traffic: %w", err)
	}
	extra, err := normalizeJSONObject(value.Extra)
	if err != nil {
		return fmt.Errorf("normalize device runtime extra data: %w", err)
	}
	sealedIMSI, err := sensitive.seal("device_runtime.imsi", value.IMSI)
	if err != nil {
		return err
	}
	sealedPhone, err := sensitive.seal("device_runtime.phone_number", value.PhoneNumber)
	if err != nil {
		return err
	}
	sealedTraffic, err := sensitive.seal("device_runtime.traffic_json", string(traffic))
	if err != nil {
		return err
	}
	sealedExtra, err := sensitive.seal("device_runtime.extra_json", string(extra))
	if err != nil {
		return err
	}
	updatedAt := value.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = executor.ExecContext(ctx, `
		INSERT INTO device_runtime (
			device_id, running, healthy, control_online, physical_present,
			worker_running, data_connected, radio_registered, network_connected,
			flight_mode, lifecycle_phase, lifecycle_reason, public_ip, private_ip,
			operator, native_mcc, native_mnc, native_spn, network_mode,
			network_duplex, radio_band, radio_channel, signal_dbm, signal_rsrp,
			signal_rsrq, signal_sinr, imei, iccid, imsi, firmware, reg_status,
			reg_status_text, ps_attached, sim_inserted, operating_mode,
			phone_number, phone_number_source, traffic_json, extra_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			running = excluded.running,
			healthy = excluded.healthy,
			control_online = excluded.control_online,
			physical_present = excluded.physical_present,
			worker_running = excluded.worker_running,
			data_connected = excluded.data_connected,
			radio_registered = excluded.radio_registered,
			network_connected = excluded.network_connected,
			flight_mode = excluded.flight_mode,
			lifecycle_phase = excluded.lifecycle_phase,
			lifecycle_reason = excluded.lifecycle_reason,
			public_ip = excluded.public_ip,
			private_ip = excluded.private_ip,
			operator = excluded.operator,
			native_mcc = excluded.native_mcc,
			native_mnc = excluded.native_mnc,
			native_spn = excluded.native_spn,
			network_mode = excluded.network_mode,
			network_duplex = excluded.network_duplex,
			radio_band = excluded.radio_band,
			radio_channel = excluded.radio_channel,
			signal_dbm = excluded.signal_dbm,
			signal_rsrp = excluded.signal_rsrp,
			signal_rsrq = excluded.signal_rsrq,
			signal_sinr = excluded.signal_sinr,
			imei = excluded.imei,
			iccid = excluded.iccid,
			imsi = excluded.imsi,
			firmware = excluded.firmware,
			reg_status = excluded.reg_status,
			reg_status_text = excluded.reg_status_text,
			ps_attached = excluded.ps_attached,
			sim_inserted = excluded.sim_inserted,
			operating_mode = excluded.operating_mode,
			phone_number = excluded.phone_number,
			phone_number_source = excluded.phone_number_source,
			traffic_json = excluded.traffic_json,
			extra_json = excluded.extra_json,
			updated_at = excluded.updated_at
	`,
		value.DeviceID, boolInt(value.Running), boolInt(value.Healthy),
		boolInt(value.ControlOnline), boolInt(value.PhysicalPresent),
		boolInt(value.WorkerRunning), boolInt(value.DataConnected),
		boolInt(value.RadioRegistered), boolInt(value.NetworkConnected),
		boolInt(value.FlightMode), value.LifecyclePhase, value.LifecycleReason,
		value.PublicIP, value.PrivateIP, value.Operator, value.NativeMCC,
		value.NativeMNC, value.NativeSPN, value.NetworkMode,
		value.NetworkDuplex, value.RadioBand, value.RadioChannel,
		value.SignalDBM, nullableInt(value.SignalRSRP), nullableInt(value.SignalRSRQ),
		nullableInt(value.SignalSINR), value.IMEI, value.ICCID, sealedIMSI,
		value.Firmware, value.RegStatus, value.RegStatusText,
		nullableBool(value.PSAttached), nullableBool(value.SIMInserted),
		nullableInt(value.OperatingMode), sealedPhone,
		value.PhoneNumberSource, sealedTraffic, sealedExtra, updatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert runtime for device %q: %w", value.DeviceID, err)
	}
	return nil
}

func (s *Store) DeviceRuntime(ctx context.Context, deviceID string) (DeviceRuntime, error) {
	return s.scanDeviceRuntime(s.db.QueryRowContext(ctx, deviceRuntimeSelect+` WHERE device_id = ?`, deviceID))
}

const deviceRuntimeSelect = `
	SELECT device_id, running, healthy, control_online, physical_present,
		worker_running, data_connected, radio_registered, network_connected,
		flight_mode, lifecycle_phase, lifecycle_reason, public_ip, private_ip,
		operator, native_mcc, native_mnc, native_spn, network_mode,
		network_duplex, radio_band, radio_channel, signal_dbm, signal_rsrp,
		signal_rsrq, signal_sinr, imei, iccid, imsi, firmware, reg_status,
		reg_status_text, ps_attached, sim_inserted, operating_mode,
		phone_number, phone_number_source, traffic_json, extra_json, updated_at
	FROM device_runtime`

func (s *Store) scanDeviceRuntime(row rowScanner) (DeviceRuntime, error) {
	var value DeviceRuntime
	var running, healthy, controlOnline, physicalPresent int
	var workerRunning, dataConnected, radioRegistered, networkConnected int
	var flightMode int
	var signalRSRP, signalRSRQ, signalSINR sql.NullInt64
	var psAttached, simInserted, operatingMode sql.NullInt64
	var traffic, extra string
	var updatedAt int64
	err := row.Scan(
		&value.DeviceID, &running, &healthy, &controlOnline, &physicalPresent,
		&workerRunning, &dataConnected, &radioRegistered, &networkConnected,
		&flightMode, &value.LifecyclePhase, &value.LifecycleReason,
		&value.PublicIP, &value.PrivateIP, &value.Operator, &value.NativeMCC,
		&value.NativeMNC, &value.NativeSPN, &value.NetworkMode,
		&value.NetworkDuplex, &value.RadioBand, &value.RadioChannel,
		&value.SignalDBM, &signalRSRP, &signalRSRQ, &signalSINR, &value.IMEI,
		&value.ICCID, &value.IMSI, &value.Firmware, &value.RegStatus,
		&value.RegStatusText, &psAttached, &simInserted, &operatingMode,
		&value.PhoneNumber, &value.PhoneNumberSource, &traffic, &extra, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceRuntime{}, ErrNotFound
	}
	if err != nil {
		return DeviceRuntime{}, err
	}
	value.Running = running != 0
	value.Healthy = healthy != 0
	value.ControlOnline = controlOnline != 0
	value.PhysicalPresent = physicalPresent != 0
	value.WorkerRunning = workerRunning != 0
	value.DataConnected = dataConnected != 0
	value.RadioRegistered = radioRegistered != 0
	value.NetworkConnected = networkConnected != 0
	value.FlightMode = flightMode != 0
	value.SignalRSRP = nullIntPointer(signalRSRP)
	value.SignalRSRQ = nullIntPointer(signalRSRQ)
	value.SignalSINR = nullIntPointer(signalSINR)
	value.PSAttached = nullBoolPointer(psAttached)
	value.SIMInserted = nullBoolPointer(simInserted)
	value.OperatingMode = nullIntPointer(operatingMode)
	value.IMSI, err = s.openField("device_runtime.imsi", value.IMSI)
	if err != nil {
		return DeviceRuntime{}, err
	}
	value.PhoneNumber, err = s.openField("device_runtime.phone_number", value.PhoneNumber)
	if err != nil {
		return DeviceRuntime{}, err
	}
	openTraffic, err := s.openField("device_runtime.traffic_json", traffic)
	if err != nil {
		return DeviceRuntime{}, err
	}
	openExtra, err := s.openField("device_runtime.extra_json", extra)
	if err != nil {
		return DeviceRuntime{}, err
	}
	value.Traffic = []byte(openTraffic)
	value.Extra = []byte(openExtra)
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func (s *Store) DeleteDeviceRuntime(ctx context.Context, deviceID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM device_runtime WHERE device_id = ?`, deviceID)
	if err != nil {
		return fmt.Errorf("delete runtime for device %q: %w", deviceID, err)
	}
	return requireAffected(result)
}

func (s *Store) UpsertVoWiFiRuntime(ctx context.Context, value VoWiFiRuntime) error {
	return upsertVoWiFiRuntime(ctx, s.db, value, s.ensureSensitive())
}

func upsertVoWiFiRuntime(
	ctx context.Context,
	executor contextExecer,
	value VoWiFiRuntime,
	sensitive *fieldCipher,
) error {
	if strings.TrimSpace(value.DeviceID) == "" {
		return errors.New("VoWiFi runtime device id is required")
	}
	tunnel, err := normalizeJSONObject(value.Tunnel)
	if err != nil {
		return fmt.Errorf("normalize VoWiFi tunnel state: %w", err)
	}
	imscore, err := normalizeJSONObject(value.IMSCore)
	if err != nil {
		return fmt.Errorf("normalize VoWiFi IMS state: %w", err)
	}
	smsip, err := normalizeJSONObject(value.SMSIP)
	if err != nil {
		return fmt.Errorf("normalize VoWiFi SMS state: %w", err)
	}
	extra, err := normalizeJSONObject(value.Extra)
	if err != nil {
		return fmt.Errorf("normalize VoWiFi extra state: %w", err)
	}
	sealedIMSI, err := sensitive.seal("vowifi_runtime.imsi", value.IMSI)
	if err != nil {
		return err
	}
	sealedPhone, err := sensitive.seal("vowifi_runtime.local_phone", value.LocalPhone)
	if err != nil {
		return err
	}
	sealedTunnel, err := sensitive.seal("vowifi_runtime.tunnel_json", string(tunnel))
	if err != nil {
		return err
	}
	sealedIMSCore, err := sensitive.seal("vowifi_runtime.imscore_json", string(imscore))
	if err != nil {
		return err
	}
	sealedSMSIP, err := sensitive.seal("vowifi_runtime.smsip_json", string(smsip))
	if err != nil {
		return err
	}
	sealedExtra, err := sensitive.seal("vowifi_runtime.extra_json", string(extra))
	if err != nil {
		return err
	}
	updatedAt := value.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = executor.ExecContext(ctx, `
		INSERT INTO vowifi_runtime (
			device_id, phase, dataplane_mode, iccid, imsi, sim_ready,
			access_ready, tunnel_ready, ims_ready, sms_ready, reg_status,
			reg_status_text, network_mode, local_phone, phone_number_source,
			last_error_class, last_error, last_reason, tunnel_json,
			imscore_json, smsip_json, extra_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			phase = excluded.phase,
			dataplane_mode = excluded.dataplane_mode,
			iccid = excluded.iccid,
			imsi = excluded.imsi,
			sim_ready = excluded.sim_ready,
			access_ready = excluded.access_ready,
			tunnel_ready = excluded.tunnel_ready,
			ims_ready = excluded.ims_ready,
			sms_ready = excluded.sms_ready,
			reg_status = excluded.reg_status,
			reg_status_text = excluded.reg_status_text,
			network_mode = excluded.network_mode,
			local_phone = excluded.local_phone,
			phone_number_source = excluded.phone_number_source,
			last_error_class = excluded.last_error_class,
			last_error = excluded.last_error,
			last_reason = excluded.last_reason,
			tunnel_json = excluded.tunnel_json,
			imscore_json = excluded.imscore_json,
			smsip_json = excluded.smsip_json,
			extra_json = excluded.extra_json,
			updated_at = excluded.updated_at
	`,
		value.DeviceID, value.Phase, value.DataplaneMode, value.ICCID,
		sealedIMSI, boolInt(value.SIMReady), boolInt(value.AccessReady),
		boolInt(value.TunnelReady), boolInt(value.IMSReady),
		boolInt(value.SMSReady), value.RegStatus, value.RegStatusText,
		value.NetworkMode, sealedPhone, value.PhoneNumberSource,
		value.LastErrorClass, value.LastError, value.LastReason,
		sealedTunnel, sealedIMSCore, sealedSMSIP, sealedExtra,
		updatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert VoWiFi runtime for device %q: %w", value.DeviceID, err)
	}
	return nil
}

func (s *Store) VoWiFiRuntime(ctx context.Context, deviceID string) (VoWiFiRuntime, error) {
	return s.scanVoWiFiRuntime(s.db.QueryRowContext(ctx, vowifiRuntimeSelect+` WHERE device_id = ?`, deviceID))
}

const vowifiRuntimeSelect = `
	SELECT device_id, phase, dataplane_mode, iccid, imsi, sim_ready,
		access_ready, tunnel_ready, ims_ready, sms_ready, reg_status,
		reg_status_text, network_mode, local_phone, phone_number_source,
		last_error_class, last_error, last_reason, tunnel_json, imscore_json,
		smsip_json, extra_json, updated_at
	FROM vowifi_runtime`

func (s *Store) scanVoWiFiRuntime(row rowScanner) (VoWiFiRuntime, error) {
	var value VoWiFiRuntime
	var simReady, accessReady, tunnelReady, imsReady, smsReady int
	var tunnel, imscore, smsip, extra string
	var updatedAt int64
	err := row.Scan(
		&value.DeviceID, &value.Phase, &value.DataplaneMode, &value.ICCID,
		&value.IMSI, &simReady, &accessReady, &tunnelReady, &imsReady,
		&smsReady, &value.RegStatus, &value.RegStatusText, &value.NetworkMode,
		&value.LocalPhone, &value.PhoneNumberSource, &value.LastErrorClass,
		&value.LastError, &value.LastReason, &tunnel, &imscore, &smsip,
		&extra, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return VoWiFiRuntime{}, ErrNotFound
	}
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	value.SIMReady = simReady != 0
	value.AccessReady = accessReady != 0
	value.TunnelReady = tunnelReady != 0
	value.IMSReady = imsReady != 0
	value.SMSReady = smsReady != 0
	value.IMSI, err = s.openField("vowifi_runtime.imsi", value.IMSI)
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	value.LocalPhone, err = s.openField("vowifi_runtime.local_phone", value.LocalPhone)
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	openTunnel, err := s.openField("vowifi_runtime.tunnel_json", tunnel)
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	openIMSCore, err := s.openField("vowifi_runtime.imscore_json", imscore)
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	openSMSIP, err := s.openField("vowifi_runtime.smsip_json", smsip)
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	openExtra, err := s.openField("vowifi_runtime.extra_json", extra)
	if err != nil {
		return VoWiFiRuntime{}, err
	}
	value.Tunnel = []byte(openTunnel)
	value.IMSCore = []byte(openIMSCore)
	value.SMSIP = []byte(openSMSIP)
	value.Extra = []byte(openExtra)
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func (s *Store) DeleteVoWiFiRuntime(ctx context.Context, deviceID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM vowifi_runtime WHERE device_id = ?`, deviceID)
	if err != nil {
		return fmt.Errorf("delete VoWiFi runtime for device %q: %w", deviceID, err)
	}
	return requireAffected(result)
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func nullIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func nullBoolPointer(value sql.NullInt64) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Int64 != 0
	return &result
}
