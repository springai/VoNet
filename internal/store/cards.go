package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	CardRuntimeModeAT   = "at"
	CardRuntimeModeQMI  = "qmi"
	CardRuntimeModeMBIM = "mbim"
	CardRuntimeModePCSC = "pcsc"

	CellularIMSModeMBNDefault    = "mbn_default"
	CellularIMSModeForceEnabled  = "force_enabled"
	CellularIMSModeForceDisabled = "force_disabled"

	SimCardCandidatePending = "pending"
	SimCardCandidateStable  = "stable"
	SimCardCandidateOffline = "offline"
)

const cardRuntimeStaleExtraKey = "card_runtime_stale"

type CardRuntimeStaleState struct {
	Binding       bool `json:"binding"`
	DeviceBackend bool `json:"device_backend"`
	CellularIMS   bool `json:"cellular_ims"`
}

func (value Device) CardRuntimeStale() CardRuntimeStaleState {
	var stale CardRuntimeStaleState
	var extra map[string]json.RawMessage
	if len(strings.TrimSpace(string(value.Extra))) == 0 ||
		json.Unmarshal(value.Extra, &extra) != nil {
		return stale
	}
	raw, ok := extra[cardRuntimeStaleExtraKey]
	if !ok {
		return stale
	}
	_ = json.Unmarshal(raw, &stale)
	return stale
}

func (value *Device) SetCardRuntimeStale(state CardRuntimeStaleState) error {
	extra := make(map[string]json.RawMessage)
	if len(strings.TrimSpace(string(value.Extra))) > 0 {
		if err := json.Unmarshal(value.Extra, &extra); err != nil {
			return fmt.Errorf("decode device extra data: %w", err)
		}
	}
	if !state.Binding && !state.DeviceBackend && !state.CellularIMS {
		delete(extra, cardRuntimeStaleExtraKey)
	} else {
		raw, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("encode card runtime stale state: %w", err)
		}
		extra[cardRuntimeStaleExtraKey] = raw
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return fmt.Errorf("encode device extra data: %w", err)
	}
	value.Extra = raw
	return nil
}

func (s *Store) UpdateDeviceCardRuntimeStale(
	ctx context.Context,
	deviceID string,
	state CardRuntimeStaleState,
) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return errors.New("device ID is required")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode card runtime stale state: %w", err)
	}
	value, err := s.Device(ctx, deviceID)
	if err != nil {
		return err
	}
	extra := make(map[string]json.RawMessage)
	if len(value.Extra) > 0 {
		if err := json.Unmarshal(value.Extra, &extra); err != nil {
			return fmt.Errorf("decode device extra data: %w", err)
		}
	}
	if !state.Binding && !state.DeviceBackend && !state.CellularIMS {
		delete(extra, cardRuntimeStaleExtraKey)
	} else {
		extra[cardRuntimeStaleExtraKey] = raw
	}
	value.Extra, err = json.Marshal(extra)
	if err != nil {
		return err
	}
	value.UpdatedAt = time.Now().UTC()
	return s.UpsertDevice(ctx, value)
}

func (s *Store) UpdateDeviceBackend(
	ctx context.Context,
	deviceID string,
	backend string,
) error {
	deviceID = strings.TrimSpace(deviceID)
	backend, err := NormalizeCardRuntimeMode(strings.TrimSpace(backend))
	if err != nil || backend == "" {
		return fmt.Errorf("invalid device backend %q", backend)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE devices SET device_backend = ?, updated_at = ?
		WHERE id = ?
	`, backend, time.Now().UTC().Unix(), deviceID)
	if err != nil {
		return fmt.Errorf("update device backend for %q: %w", deviceID, err)
	}
	return requireAffected(result)
}

type contextQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type contextBindingStore interface {
	contextExecer
	contextQueryRower
}

// NormalizeSimICCID returns the canonical decimal ICCID used by card identity,
// observations, bindings and policy rows.
func NormalizeSimICCID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 18 || len(value) > 22 {
		return "", errors.New("ICCID must contain between 18 and 22 digits")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", errors.New("ICCID must contain only decimal digits")
		}
	}
	return value, nil
}

func NormalizeCardRuntimeMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", CardRuntimeModeAT, CardRuntimeModeQMI, CardRuntimeModeMBIM, CardRuntimeModePCSC:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported card runtime mode %q", value)
	}
}

func NormalizeCellularIMSMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", CellularIMSModeMBNDefault, CellularIMSModeForceEnabled, CellularIMSModeForceDisabled:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported cellular IMS mode %q", value)
	}
}

func normalizeSimCard(value SimCard) (SimCard, error) {
	iccid, err := NormalizeSimICCID(value.ICCID)
	if err != nil {
		return SimCard{}, err
	}
	value.ICCID = iccid
	value.DisplayName = strings.TrimSpace(value.DisplayName)
	value.Notes = strings.TrimSpace(value.Notes)
	value.CardType = strings.ToLower(strings.TrimSpace(value.CardType))
	if value.CardType == "" {
		value.CardType = SimCardTypePhysical
	}
	if value.CardType != SimCardTypePhysical && value.CardType != SimCardTypeESIM {
		return SimCard{}, fmt.Errorf("unsupported SIM card type %q", value.CardType)
	}
	value.IMSI = strings.TrimSpace(value.IMSI)
	value.MSISDN = strings.TrimSpace(value.MSISDN)
	value.Carrier = strings.TrimSpace(value.Carrier)
	value.SPN = strings.TrimSpace(value.SPN)
	value.MCC = strings.TrimSpace(value.MCC)
	value.MNC = strings.TrimSpace(value.MNC)
	value.HomePLMN = strings.TrimSpace(value.HomePLMN)
	value.EID = strings.TrimSpace(value.EID)
	value.ProfileAID = strings.ToUpper(strings.TrimSpace(value.ProfileAID))
	value.GID1 = strings.ToUpper(strings.TrimSpace(value.GID1))
	value.GID2 = strings.ToUpper(strings.TrimSpace(value.GID2))
	value.SMSC = strings.TrimSpace(value.SMSC)
	derived, err := normalizeJSONObject(value.DerivedJSON)
	if err != nil {
		return SimCard{}, fmt.Errorf("normalize SIM derived data: %w", err)
	}
	value.DerivedJSON = derived
	value.HostType = strings.ToLower(strings.TrimSpace(value.HostType))
	value.Source = strings.TrimSpace(value.Source)
	if value.Source == "" {
		value.Source = "manual"
	}
	return value, nil
}

type sealedSimCardFields struct {
	IMSI       string
	MSISDN     string
	EID        string
	ProfileAID string
	GID1       string
	GID2       string
	SMSC       string
	Derived    string
}

func (s *Store) sealSimCardFields(value SimCard) (sealedSimCardFields, error) {
	fields := []struct {
		name      string
		plaintext string
	}{
		{"sim_cards.imsi", value.IMSI},
		{"sim_cards.msisdn", value.MSISDN},
		{"sim_cards.eid", value.EID},
		{"sim_cards.profile_aid", value.ProfileAID},
		{"sim_cards.gid1", value.GID1},
		{"sim_cards.gid2", value.GID2},
		{"sim_cards.smsc", value.SMSC},
		{"sim_cards.derived_json", string(value.DerivedJSON)},
	}
	values := make([]string, len(fields))
	for index, field := range fields {
		sealed, err := s.sealField(field.name, field.plaintext)
		if err != nil {
			return sealedSimCardFields{}, err
		}
		values[index] = sealed
	}
	return sealedSimCardFields{
		IMSI: values[0], MSISDN: values[1], EID: values[2], ProfileAID: values[3],
		GID1: values[4], GID2: values[5], SMSC: values[6], Derived: values[7],
	}, nil
}

func (s *Store) UpsertSimCard(ctx context.Context, value SimCard) error {
	value, err := normalizeSimCard(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	createdAt := value.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := value.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	sealed, err := s.sealSimCardFields(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sim_cards (
			iccid, display_name, notes, card_type, imsi, msisdn, carrier, spn,
			mcc, mnc, home_plmn, eid, profile_aid, gid1, gid2, smsc, derived_json,
			host_type, source, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid) DO UPDATE SET
			display_name = excluded.display_name,
			notes = excluded.notes,
			card_type = excluded.card_type,
			imsi = excluded.imsi,
			msisdn = excluded.msisdn,
			carrier = excluded.carrier,
			spn = excluded.spn,
			mcc = excluded.mcc,
			mnc = excluded.mnc,
			home_plmn = excluded.home_plmn,
			eid = excluded.eid,
			profile_aid = excluded.profile_aid,
			gid1 = excluded.gid1,
			gid2 = excluded.gid2,
			smsc = excluded.smsc,
			derived_json = excluded.derived_json,
			host_type = CASE
				WHEN sim_cards.host_type = '' THEN excluded.host_type
				ELSE sim_cards.host_type
			END,
			source = excluded.source,
			created_at = sim_cards.created_at,
			updated_at = excluded.updated_at
	`,
		value.ICCID, value.DisplayName, value.Notes, value.CardType,
		sealed.IMSI, sealed.MSISDN, value.Carrier, value.SPN,
		value.MCC, value.MNC, value.HomePLMN, sealed.EID, sealed.ProfileAID,
		sealed.GID1, sealed.GID2, sealed.SMSC, sealed.Derived,
		value.HostType, value.Source, createdAt.Unix(), updatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert SIM card %q: %w", value.ICCID, err)
	}
	return nil
}

type SimCardImportResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
}

// ImportSimCards applies a complete card-configuration batch in one database
// transaction. Existing rows keep all identity, binding and metadata fields;
// only display name, notes and card type are replaced.
func (s *Store) ImportSimCards(ctx context.Context, values []SimCard) (SimCardImportResult, error) {
	var result SimCardImportResult
	if len(values) == 0 {
		return result, errors.New("SIM card import batch is empty")
	}
	normalized := make([]SimCard, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		card, err := normalizeSimCard(value)
		if err != nil {
			return result, fmt.Errorf("import SIM card %d: %w", index+1, err)
		}
		if _, exists := seen[card.ICCID]; exists {
			return result, fmt.Errorf("import SIM card %d: duplicate ICCID %q", index+1, card.ICCID)
		}
		seen[card.ICCID] = struct{}{}
		normalized = append(normalized, card)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin SIM card import: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, value := range normalized {
		var exists int
		created := false
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM sim_cards WHERE iccid = ?`, value.ICCID).Scan(&exists)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, `
				UPDATE sim_cards SET
					display_name = ?, notes = ?, card_type = ?, updated_at = ?
				WHERE iccid = ?
			`, value.DisplayName, value.Notes, value.CardType, now.Unix(), value.ICCID)
		case errors.Is(err, sql.ErrNoRows):
			created = true
			_, err = tx.ExecContext(ctx, `
				INSERT INTO sim_cards (
					iccid, display_name, notes, card_type, imsi, msisdn, carrier, spn,
					mcc, mnc, home_plmn, eid, profile_aid, host_type, source,
					created_at, updated_at
				) VALUES (?, ?, ?, ?, '', '', '', '', '', '', '', '', '', '', ?, ?, ?)
			`, value.ICCID, value.DisplayName, value.Notes, value.CardType,
				value.Source, now.Unix(), now.Unix())
		default:
			return result, fmt.Errorf("inspect imported SIM card %q: %w", value.ICCID, err)
		}
		if err != nil {
			return result, fmt.Errorf("import SIM card %q: %w", value.ICCID, err)
		}
		if created {
			result.Created++
		} else {
			result.Updated++
		}
		// Imported configuration must remain actionable even after a prior
		// delete removed the policy row. Existing policies are preserved.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO card_policies (
				iccid, network_enabled, vowifi_enabled, airplane_enabled,
				apn, ip_version, custom_phone_number, mbn_profile,
				cellular_ims_enabled, cellular_ims_managed, runtime_mode,
				cellular_ims_mode, source, created_at, updated_at
			) VALUES (?, 0, 0, 1, '', 'IPV4V6', '', '', 0, 0, '', 'mbn_default', 'import', ?, ?)
			ON CONFLICT(iccid) DO NOTHING
		`, value.ICCID, now.Unix(), now.Unix()); err != nil {
			return result, fmt.Errorf("create imported SIM card policy %q: %w", value.ICCID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit SIM card import: %w", err)
	}
	return result, nil
}

func (s *Store) CreateSimCardWithPolicy(
	ctx context.Context,
	value SimCard,
	policy CardPolicy,
) error {
	value, err := normalizeSimCard(value)
	if err != nil {
		return err
	}
	policy.ICCID = value.ICCID
	policy.CustomPhoneNumber = strings.TrimSpace(policy.CustomPhoneNumber)
	policy.IPVersion = strings.ToUpper(strings.TrimSpace(policy.IPVersion))
	switch policy.IPVersion {
	case "", "IP", "IPV6", "IPV4V6":
	default:
		return fmt.Errorf("unsupported card policy IP version %q", policy.IPVersion)
	}
	policy.MBNProfile = strings.TrimSpace(policy.MBNProfile)
	policy.RuntimeMode, err = NormalizeCardRuntimeMode(policy.RuntimeMode)
	if err != nil {
		return err
	}
	policy.CellularIMSMode, err = NormalizeCellularIMSMode(policy.CellularIMSMode)
	if err != nil {
		return err
	}
	normalizeCardPolicyRFState(&policy)
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	}
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = now
	}
	if policy.Source == "" {
		policy.Source = "manual"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create SIM card %q: %w", value.ICCID, err)
	}
	defer tx.Rollback()
	sealed, err := s.sealSimCardFields(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sim_cards (
			iccid, display_name, notes, card_type, imsi, msisdn, carrier, spn,
			mcc, mnc, home_plmn, eid, profile_aid, gid1, gid2, smsc, derived_json,
			host_type, source, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, value.ICCID, value.DisplayName, value.Notes, value.CardType,
		sealed.IMSI, sealed.MSISDN, value.Carrier, value.SPN,
		value.MCC, value.MNC, value.HomePLMN, sealed.EID, sealed.ProfileAID,
		sealed.GID1, sealed.GID2, sealed.SMSC, sealed.Derived,
		value.HostType, value.Source, value.CreatedAt.Unix(), value.UpdatedAt.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return fmt.Errorf("%w: SIM card %q already exists", ErrAlreadyExists, value.ICCID)
		}
		return fmt.Errorf("create SIM card %q: %w", value.ICCID, err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO card_policies (
			iccid, network_enabled, vowifi_enabled, airplane_enabled,
			apn, ip_version, custom_phone_number, mbn_profile,
			cellular_ims_enabled, cellular_ims_managed, runtime_mode,
			cellular_ims_mode, source, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, policy.ICCID, boolInt(policy.NetworkEnabled), boolInt(policy.VoWiFiEnabled),
		boolInt(policy.AirplaneEnabled), policy.APN, policy.IPVersion,
		policy.CustomPhoneNumber, policy.MBNProfile,
		boolInt(policy.CellularIMSEnabled), boolInt(policy.CellularIMSManaged),
		policy.RuntimeMode, policy.CellularIMSMode, policy.Source,
		policy.CreatedAt.Unix(), policy.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("create initial card policy %q: %w", policy.ICCID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create SIM card %q: %w", value.ICCID, err)
	}
	return nil
}

func (s *Store) TouchSimCardCandidate(
	ctx context.Context,
	value SimCardCandidate,
) (SimCardCandidate, error) {
	iccid, err := NormalizeSimICCID(value.ICCID)
	if err != nil {
		return SimCardCandidate{}, err
	}
	value.ICCID = iccid
	value.DeviceID = strings.TrimSpace(value.DeviceID)
	if value.DeviceID == "" {
		return SimCardCandidate{}, errors.New("SIM card candidate device ID is required")
	}
	value.PhysicalDeviceID = strings.TrimSpace(value.PhysicalDeviceID)
	value.HostType = strings.ToLower(strings.TrimSpace(value.HostType))
	value.CardType = strings.ToLower(strings.TrimSpace(value.CardType))
	if value.CardType == "" {
		value.CardType = SimCardTypePhysical
	}
	if value.CardType != SimCardTypePhysical && value.CardType != SimCardTypeESIM {
		return SimCardCandidate{}, fmt.Errorf("unsupported SIM card type %q", value.CardType)
	}
	value.IMSI = strings.TrimSpace(value.IMSI)
	value.Carrier = strings.TrimSpace(value.Carrier)
	value.SPN = strings.TrimSpace(value.SPN)
	now := time.Now().UTC()
	if value.FirstSeenAt.IsZero() {
		value.FirstSeenAt = now
	}
	if value.LastSeenAt.IsZero() {
		value.LastSeenAt = now
	}
	if value.State == "" {
		if now.Sub(value.FirstSeenAt) >= 3*time.Second {
			value.State = SimCardCandidateStable
		} else {
			value.State = SimCardCandidatePending
		}
	}
	if value.State != SimCardCandidatePending &&
		value.State != SimCardCandidateStable &&
		value.State != SimCardCandidateOffline {
		return SimCardCandidate{}, fmt.Errorf("unsupported SIM card candidate state %q", value.State)
	}
	storedIMSI, err := s.sealField("sim_card_candidates.imsi", value.IMSI)
	if err != nil {
		return SimCardCandidate{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO sim_card_candidates (
			iccid, device_id, physical_device_id, host_type, card_type,
			imsi, carrier, spn, state, first_seen_at, last_seen_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid, device_id, physical_device_id) DO UPDATE SET
			host_type = CASE
				WHEN sim_card_candidates.host_type = '' THEN excluded.host_type
				ELSE sim_card_candidates.host_type
			END,
			card_type = excluded.card_type,
			imsi = CASE WHEN excluded.imsi <> '' THEN excluded.imsi ELSE sim_card_candidates.imsi END,
			carrier = CASE WHEN excluded.carrier <> '' THEN excluded.carrier ELSE sim_card_candidates.carrier END,
			spn = CASE WHEN excluded.spn <> '' THEN excluded.spn ELSE sim_card_candidates.spn END,
			state = CASE
				WHEN excluded.state <> 'pending' THEN excluded.state
				WHEN MIN(sim_card_candidates.first_seen_at, excluded.first_seen_at)
					<= MAX(sim_card_candidates.last_seen_at, excluded.last_seen_at) - 3
					THEN 'stable'
				ELSE excluded.state
			END,
			first_seen_at = MIN(sim_card_candidates.first_seen_at, excluded.first_seen_at),
			last_seen_at = MAX(sim_card_candidates.last_seen_at, excluded.last_seen_at),
			updated_at = excluded.updated_at
		RETURNING iccid, device_id, physical_device_id, host_type, card_type,
			imsi, carrier, spn, state, first_seen_at, last_seen_at,
			created_at, updated_at
	`, value.ICCID, value.DeviceID, value.PhysicalDeviceID, value.HostType,
		value.CardType, storedIMSI, value.Carrier, value.SPN, value.State,
		value.FirstSeenAt.Unix(), value.LastSeenAt.Unix(), now.Unix(), now.Unix())
	return s.scanSimCardCandidate(row)
}

func (s *Store) ListSimCardCandidates(ctx context.Context) ([]SimCardCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		simCardCandidateSelect+` ORDER BY iccid, device_id, physical_device_id`)
	if err != nil {
		return nil, fmt.Errorf("list SIM card candidates: %w", err)
	}
	defer rows.Close()
	values := make([]SimCardCandidate, 0)
	for rows.Next() {
		value, err := s.scanSimCardCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan SIM card candidate: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SIM card candidates: %w", err)
	}
	return values, nil
}

func (s *Store) ClearSimCardCandidatesForDevice(ctx context.Context, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return errors.New("device ID is required")
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sim_card_candidates WHERE device_id = ?`, deviceID,
	); err != nil {
		return fmt.Errorf("clear SIM card candidates for device %q: %w", deviceID, err)
	}
	return nil
}

func (s *Store) DeleteSimCardCandidates(ctx context.Context, iccid string) error {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sim_card_candidates WHERE iccid = ?`, iccid,
	); err != nil {
		return fmt.Errorf("delete SIM card candidates for %q: %w", iccid, err)
	}
	return nil
}

func (s *Store) CreateSimCard(ctx context.Context, value SimCard) error {
	value, err := normalizeSimCard(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = now
	}
	sealed, err := s.sealSimCardFields(value)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO sim_cards (
			iccid, display_name, notes, card_type, imsi, msisdn, carrier, spn,
			mcc, mnc, home_plmn, eid, profile_aid, gid1, gid2, smsc, derived_json,
			host_type, source, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, value.ICCID, value.DisplayName, value.Notes, value.CardType,
		sealed.IMSI, sealed.MSISDN, value.Carrier, value.SPN,
		value.MCC, value.MNC, value.HomePLMN, sealed.EID, sealed.ProfileAID,
		sealed.GID1, sealed.GID2, sealed.SMSC, sealed.Derived,
		value.HostType, value.Source, value.CreatedAt.Unix(), value.UpdatedAt.Unix(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create SIM card %q: %w", value.ICCID, err)
	}
	return requireAffected(result)
}

func (s *Store) UpdateSimCard(ctx context.Context, value SimCard) error {
	value, err := normalizeSimCard(value)
	if err != nil {
		return err
	}
	value.UpdatedAt = time.Now().UTC()
	sealed, err := s.sealSimCardFields(value)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE sim_cards SET
			display_name = ?, notes = ?, card_type = ?, imsi = ?, msisdn = ?,
			carrier = ?, spn = ?, mcc = ?, mnc = ?, home_plmn = ?, eid = ?,
			profile_aid = ?, gid1 = ?, gid2 = ?, smsc = ?, derived_json = ?,
			host_type = CASE WHEN ? <> '' THEN ? ELSE sim_cards.host_type END,
			source = ?, updated_at = ?
		WHERE iccid = ?
	`, value.DisplayName, value.Notes, value.CardType, sealed.IMSI, sealed.MSISDN,
		value.Carrier, value.SPN, value.MCC, value.MNC, value.HomePLMN, sealed.EID,
		sealed.ProfileAID, sealed.GID1, sealed.GID2, sealed.SMSC, sealed.Derived,
		value.HostType, value.HostType, value.Source,
		value.UpdatedAt.Unix(), value.ICCID,
	)
	if err != nil {
		return fmt.Errorf("update SIM card %q: %w", value.ICCID, err)
	}
	return requireAffected(result)
}

func (s *Store) SimCard(ctx context.Context, iccid string) (SimCard, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return SimCard{}, err
	}
	return s.scanSimCard(s.db.QueryRowContext(ctx, simCardSelect+` WHERE iccid = ?`, iccid))
}

func (s *Store) ListSimCards(ctx context.Context) ([]SimCard, error) {
	rows, err := s.db.QueryContext(ctx, simCardSelect+` ORDER BY iccid`)
	if err != nil {
		return nil, fmt.Errorf("list SIM cards: %w", err)
	}
	defer rows.Close()
	values := make([]SimCard, 0)
	for rows.Next() {
		value, err := s.scanSimCard(rows)
		if err != nil {
			return nil, fmt.Errorf("scan SIM card: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SIM cards: %w", err)
	}
	return values, nil
}

func (s *Store) DeleteSimCard(ctx context.Context, iccid string) error {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete SIM card %q: %w", iccid, err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM device_proxy_route_state WHERE binding_group_id IN (
			SELECT id FROM device_proxy_binding_groups WHERE iccid = ?
		)`,
		`DELETE FROM device_proxy_binding_routes WHERE binding_group_id IN (
			SELECT id FROM device_proxy_binding_groups WHERE iccid = ?
		)`,
		`DELETE FROM device_proxy_binding_groups WHERE iccid = ?`,
		`DELETE FROM device_proxy_bindings WHERE iccid = ?`,
		`DELETE FROM automatic_tasks WHERE profile_iccid = ?`,
		`DELETE FROM call_history WHERE iccid = ?`,
		`DELETE FROM sim_card_candidates WHERE iccid = ?`,
		`DELETE FROM sim_card_bindings WHERE iccid = ?`,
		`DELETE FROM sim_card_observations WHERE iccid = ?`,
		`DELETE FROM phone_associations WHERE iccid = ?`,
		`DELETE FROM card_policies WHERE iccid = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, iccid); err != nil {
			return fmt.Errorf("delete SIM card %q related rows: %w", iccid, err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM sim_cards WHERE iccid = ?`, iccid)
	if err != nil {
		return fmt.Errorf("delete SIM card %q: %w", iccid, err)
	}
	if err := requireAffected(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete SIM card %q: %w", iccid, err)
	}
	return nil
}

func (s *Store) UpsertSimCardObservation(
	ctx context.Context,
	value SimCardObservation,
) (SimCardObservation, error) {
	iccid, err := NormalizeSimICCID(value.ICCID)
	if err != nil {
		return SimCardObservation{}, err
	}
	value.ICCID = iccid
	value.DeviceID = strings.TrimSpace(value.DeviceID)
	if value.DeviceID == "" {
		return SimCardObservation{}, errors.New("SIM card observation device ID is required")
	}
	value.PhysicalDeviceID = strings.TrimSpace(value.PhysicalDeviceID)
	value.EID = strings.TrimSpace(value.EID)
	value.ProfileAID = strings.ToUpper(strings.TrimSpace(value.ProfileAID))
	value.Source = strings.TrimSpace(value.Source)
	if value.Source == "" {
		value.Source = "live"
	}
	now := time.Now().UTC()
	if value.FirstSeenAt.IsZero() {
		value.FirstSeenAt = now
	}
	if value.LastSeenAt.IsZero() {
		value.LastSeenAt = now
	}
	sealedEID, err := s.sealStoredField("sim_card_observations", "eid", value.EID)
	if err != nil {
		return SimCardObservation{}, err
	}
	sealedAID, err := s.sealStoredField("sim_card_observations", "profile_aid", value.ProfileAID)
	if err != nil {
		return SimCardObservation{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO sim_card_observations (
			iccid, device_id, physical_device_id, eid, profile_aid, source,
			first_seen_at, last_seen_at, last_enabled_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid, device_id, profile_aid) DO UPDATE SET
			physical_device_id = excluded.physical_device_id,
			eid = CASE WHEN excluded.eid <> '' THEN excluded.eid ELSE sim_card_observations.eid END,
			source = excluded.source,
			first_seen_at = CASE
				WHEN sim_card_observations.physical_device_id <> excluded.physical_device_id
					THEN excluded.first_seen_at
				ELSE MIN(sim_card_observations.first_seen_at, excluded.first_seen_at)
			END,
			last_seen_at = MAX(sim_card_observations.last_seen_at, excluded.last_seen_at),
			last_enabled_at = CASE
				WHEN excluded.last_enabled_at > sim_card_observations.last_enabled_at
					THEN excluded.last_enabled_at
				ELSE sim_card_observations.last_enabled_at
			END
		RETURNING id, iccid, device_id, physical_device_id, eid, profile_aid,
			source, first_seen_at, last_seen_at, last_enabled_at
	`,
		value.ICCID, value.DeviceID, value.PhysicalDeviceID, sealedEID,
		sealedAID, value.Source, value.FirstSeenAt.Unix(),
		value.LastSeenAt.Unix(), value.LastEnabledAt.Unix(),
	)
	return s.scanSimCardObservation(row)
}

func (s *Store) SimCardObservation(ctx context.Context, id int64) (SimCardObservation, error) {
	return s.scanSimCardObservation(s.db.QueryRowContext(ctx, simCardObservationSelect+` WHERE id = ?`, id))
}

func (s *Store) ListSimCardObservations(ctx context.Context, iccid string) ([]SimCardObservation, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		simCardObservationSelect+` WHERE iccid = ? ORDER BY last_seen_at DESC, id DESC`, iccid)
	if err != nil {
		return nil, fmt.Errorf("list SIM card observations: %w", err)
	}
	defer rows.Close()
	values := make([]SimCardObservation, 0)
	for rows.Next() {
		value, err := s.scanSimCardObservation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan SIM card observation: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SIM card observations: %w", err)
	}
	return values, nil
}

// TouchSimCardBinding atomically records a fresh observation and advances the
// current binding generation when its device or eSIM profile identity changes.
func (s *Store) TouchSimCardBinding(ctx context.Context, value SimCardBinding) error {
	iccid, err := NormalizeSimICCID(value.ICCID)
	if err != nil {
		return err
	}
	value.ICCID = iccid
	value.DeviceID = strings.TrimSpace(value.DeviceID)
	value.PhysicalDeviceID = strings.TrimSpace(value.PhysicalDeviceID)
	value.EID = strings.TrimSpace(value.EID)
	value.ProfileAID = strings.ToUpper(strings.TrimSpace(value.ProfileAID))
	value.State = strings.ToLower(strings.TrimSpace(value.State))
	if value.State == "" {
		if value.DeviceID != "" {
			value.State = SimCardBindingActive
		} else {
			value.State = SimCardBindingConflict
		}
	}
	switch value.State {
	case SimCardBindingPending, SimCardBindingActive:
		if value.DeviceID == "" || value.PhysicalDeviceID == "" {
			return errors.New("pending and active SIM card bindings require device and physical device IDs")
		}
	case SimCardBindingConflict, SimCardBindingOffline:
		value.ObservationID = 0
		value.DeviceID = ""
		value.PhysicalDeviceID = ""
		value.EID = ""
		value.ProfileAID = ""
		value.StableSince = time.Time{}
		value.BoundAt = time.Time{}
	default:
		return fmt.Errorf("unsupported SIM card binding state %q", value.State)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin touch SIM card binding %q: %w", value.ICCID, err)
	}
	defer tx.Rollback()
	var cardExists int
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM sim_cards WHERE iccid = ?`, value.ICCID).Scan(&cardExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check SIM card %q: %w", value.ICCID, err)
	}

	now := time.Now().UTC()
	if value.DeviceID != "" {
		observation := SimCardObservation{
			ICCID:            value.ICCID,
			DeviceID:         value.DeviceID,
			PhysicalDeviceID: value.PhysicalDeviceID,
			EID:              value.EID,
			ProfileAID:       value.ProfileAID,
			Source:           "live",
			FirstSeenAt:      now,
			LastSeenAt:       value.LastSeenAt,
			LastEnabledAt:    value.LastSeenAt,
		}
		if observation.LastSeenAt.IsZero() {
			observation.LastSeenAt = now
		}
		if value.State != SimCardBindingActive {
			observation.LastEnabledAt = time.Time{}
		} else if observation.LastEnabledAt.IsZero() {
			observation.LastEnabledAt = now
		}
		observation, err = s.upsertSimCardObservation(ctx, tx, observation)
		if err != nil {
			return err
		}
		value.ObservationID = observation.ID
	}

	existing, existingErr := s.simCardBindingByICCID(ctx, tx, value.ICCID)
	if existingErr != nil && !errors.Is(existingErr, ErrNotFound) {
		return existingErr
	}
	identityChanged := existingErr == nil &&
		(existing.ObservationID != value.ObservationID ||
			existing.DeviceID != value.DeviceID ||
			existing.PhysicalDeviceID != value.PhysicalDeviceID ||
			existing.ProfileAID != value.ProfileAID)
	minimumGeneration := int64(1)
	if existingErr == nil {
		minimumGeneration = existing.Generation
		if identityChanged {
			minimumGeneration++
		}
	}
	if value.Generation < minimumGeneration {
		value.Generation = minimumGeneration
	}
	if value.Generation < 1 {
		value.Generation = 1
	}

	if value.LastSeenAt.IsZero() {
		value.LastSeenAt = now
	}
	if value.State == SimCardBindingOffline && value.ClearedAt.IsZero() {
		value.ClearedAt = now
	}
	if value.State == SimCardBindingActive {
		if value.BoundAt.IsZero() {
			if existingErr == nil && !identityChanged && !existing.BoundAt.IsZero() {
				value.BoundAt = existing.BoundAt
			} else {
				value.BoundAt = now
			}
		}
		if value.StableSince.IsZero() {
			if existingErr == nil && !identityChanged && !existing.StableSince.IsZero() {
				value.StableSince = existing.StableSince
			} else {
				value.StableSince = now
			}
		}
	}
	if existingErr == nil {
		value.CreatedAt = existing.CreatedAt
	} else {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	sealedBindingEID, err := s.sealStoredField("sim_card_bindings", "eid", value.EID)
	if err != nil {
		return err
	}
	sealedBindingAID, err := s.sealStoredField("sim_card_bindings", "profile_aid", value.ProfileAID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sim_card_bindings (
			iccid, observation_id, device_id, physical_device_id, eid, profile_aid,
			generation, state, stable_since, bound_at, last_seen_at, cleared_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid) DO UPDATE SET
			observation_id = excluded.observation_id,
			device_id = excluded.device_id,
			physical_device_id = excluded.physical_device_id,
			eid = excluded.eid,
			profile_aid = excluded.profile_aid,
			generation = excluded.generation,
			state = excluded.state,
			stable_since = excluded.stable_since,
			bound_at = excluded.bound_at,
			last_seen_at = MAX(sim_card_bindings.last_seen_at, excluded.last_seen_at),
			cleared_at = excluded.cleared_at,
			updated_at = excluded.updated_at
	`,
		value.ICCID, nullableObservationID(value.ObservationID), value.DeviceID,
		value.PhysicalDeviceID, sealedBindingEID, sealedBindingAID, value.Generation,
		value.State, value.StableSince.Unix(), value.BoundAt.Unix(),
		value.LastSeenAt.Unix(), value.ClearedAt.Unix(), value.CreatedAt.Unix(),
		value.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("touch SIM card binding %q: %w", value.ICCID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit touch SIM card binding %q: %w", value.ICCID, err)
	}
	return nil
}

func (s *Store) SimCardBinding(ctx context.Context, iccid string) (SimCardBinding, error) {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return SimCardBinding{}, err
	}
	return s.simCardBindingByICCID(ctx, s.db, iccid)
}

func (s *Store) ListSimCardBindings(ctx context.Context) ([]SimCardBinding, error) {
	rows, err := s.db.QueryContext(ctx, simCardBindingSelect+` ORDER BY iccid`)
	if err != nil {
		return nil, fmt.Errorf("list SIM card bindings: %w", err)
	}
	defer rows.Close()
	values := make([]SimCardBinding, 0)
	for rows.Next() {
		value, err := s.scanSimCardBinding(rows)
		if err != nil {
			return nil, fmt.Errorf("scan SIM card binding: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SIM card bindings: %w", err)
	}
	return values, nil
}

func (s *Store) ClearSimCardBinding(ctx context.Context, iccid, deviceID string) error {
	iccid, err := NormalizeSimICCID(iccid)
	if err != nil {
		return err
	}
	return clearSimCardBinding(ctx, s, s.db, iccid, strings.TrimSpace(deviceID))
}

func (s *Store) ClearSimCardBindingsForDevice(ctx context.Context, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return errors.New("device ID is required")
	}
	return clearSimCardBindingsForDevice(ctx, s.db, deviceID)
}

func clearSimCardBinding(ctx context.Context, store *Store, executor contextBindingStore, iccid, deviceID string) error {
	binding, err := store.simCardBindingByICCID(ctx, executor, iccid)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if deviceID != "" && binding.DeviceID != "" && binding.DeviceID != deviceID {
		return ErrNotFound
	}
	if binding.State == SimCardBindingOffline && binding.DeviceID == "" {
		return nil
	}
	now := time.Now().UTC()
	_, err = executor.ExecContext(ctx, `
		UPDATE sim_card_bindings SET
			observation_id = NULL,
			device_id = '',
			physical_device_id = '',
			eid = '',
			profile_aid = '',
			generation = generation + 1,
			state = 'offline',
			stable_since = 0,
			bound_at = 0,
			cleared_at = ?,
			updated_at = ?
		WHERE iccid = ?
	`, now.Unix(), now.Unix(), iccid)
	if err != nil {
		return fmt.Errorf("clear SIM card binding %q: %w", iccid, err)
	}
	return nil
}

func clearSimCardBindingsForDevice(ctx context.Context, executor contextBindingStore, deviceID string) error {
	now := time.Now().UTC()
	_, err := executor.ExecContext(ctx, `
		UPDATE sim_card_bindings SET
			observation_id = NULL,
			device_id = '',
			physical_device_id = '',
			eid = '',
			profile_aid = '',
			generation = generation + 1,
			state = 'offline',
			stable_since = 0,
			bound_at = 0,
			cleared_at = ?,
			updated_at = ?
		WHERE device_id = ?
	`, now.Unix(), now.Unix(), deviceID)
	if err != nil {
		return fmt.Errorf("clear SIM card bindings for device %q: %w", deviceID, err)
	}
	return nil
}

func (s *Store) upsertSimCardObservation(
	ctx context.Context,
	executor contextQueryRower,
	value SimCardObservation,
) (SimCardObservation, error) {
	sealedEID, err := s.sealStoredField("sim_card_observations", "eid", value.EID)
	if err != nil {
		return SimCardObservation{}, err
	}
	sealedAID, err := s.sealStoredField("sim_card_observations", "profile_aid", value.ProfileAID)
	if err != nil {
		return SimCardObservation{}, err
	}
	row := executor.QueryRowContext(ctx, `
		INSERT INTO sim_card_observations (
			iccid, device_id, physical_device_id, eid, profile_aid, source,
			first_seen_at, last_seen_at, last_enabled_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(iccid, device_id, profile_aid) DO UPDATE SET
			physical_device_id = excluded.physical_device_id,
			eid = CASE WHEN excluded.eid <> '' THEN excluded.eid ELSE sim_card_observations.eid END,
			source = excluded.source,
			first_seen_at = MIN(sim_card_observations.first_seen_at, excluded.first_seen_at),
			last_seen_at = MAX(sim_card_observations.last_seen_at, excluded.last_seen_at),
			last_enabled_at = MAX(sim_card_observations.last_enabled_at, excluded.last_enabled_at)
		RETURNING id, iccid, device_id, physical_device_id, eid, profile_aid,
			source, first_seen_at, last_seen_at, last_enabled_at
	`,
		value.ICCID, value.DeviceID, value.PhysicalDeviceID, sealedEID,
		sealedAID, value.Source, value.FirstSeenAt.Unix(),
		value.LastSeenAt.Unix(), value.LastEnabledAt.Unix(),
	)
	return s.scanSimCardObservation(row)
}

func (s *Store) simCardBindingByICCID(
	ctx context.Context,
	executor contextQueryRower,
	iccid string,
) (SimCardBinding, error) {
	return s.scanSimCardBinding(executor.QueryRowContext(ctx,
		simCardBindingSelect+` WHERE iccid = ?`, iccid))
}

func nullableObservationID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

const simCardSelect = `
	SELECT iccid, display_name, notes, card_type, imsi, msisdn, carrier, spn,
		mcc, mnc, home_plmn, eid, profile_aid, gid1, gid2, smsc, derived_json,
		host_type, source, created_at, updated_at
	FROM sim_cards`

func (s *Store) scanSimCard(row rowScanner) (SimCard, error) {
	var value SimCard
	var derived string
	var createdAt, updatedAt int64
	err := row.Scan(
		&value.ICCID, &value.DisplayName, &value.Notes, &value.CardType,
		&value.IMSI, &value.MSISDN, &value.Carrier, &value.SPN,
		&value.MCC, &value.MNC, &value.HomePLMN, &value.EID,
		&value.ProfileAID, &value.GID1, &value.GID2, &value.SMSC, &derived,
		&value.HostType, &value.Source, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SimCard{}, ErrNotFound
	}
	if err != nil {
		return SimCard{}, err
	}
	opened := []struct {
		field string
		value *string
	}{
		{"sim_cards.imsi", &value.IMSI},
		{"sim_cards.msisdn", &value.MSISDN},
		{"sim_cards.eid", &value.EID},
		{"sim_cards.profile_aid", &value.ProfileAID},
		{"sim_cards.gid1", &value.GID1},
		{"sim_cards.gid2", &value.GID2},
		{"sim_cards.smsc", &value.SMSC},
	}
	for _, item := range opened {
		*item.value, err = s.openField(item.field, *item.value)
		if err != nil {
			return SimCard{}, err
		}
	}
	openDerived, err := s.openField("sim_cards.derived_json", derived)
	if err != nil {
		return SimCard{}, err
	}
	value.DerivedJSON = []byte(openDerived)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

const simCardCandidateSelect = `
	SELECT iccid, device_id, physical_device_id, host_type, card_type,
		imsi, carrier, spn, state, first_seen_at, last_seen_at,
		created_at, updated_at
	FROM sim_card_candidates`

func (s *Store) scanSimCardCandidate(row rowScanner) (SimCardCandidate, error) {
	var value SimCardCandidate
	var firstSeenAt, lastSeenAt, createdAt, updatedAt int64
	err := row.Scan(
		&value.ICCID, &value.DeviceID, &value.PhysicalDeviceID,
		&value.HostType, &value.CardType, &value.IMSI, &value.Carrier,
		&value.SPN, &value.State, &firstSeenAt, &lastSeenAt,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SimCardCandidate{}, ErrNotFound
	}
	if err != nil {
		return SimCardCandidate{}, err
	}
	value.IMSI, err = s.openField("sim_card_candidates.imsi", value.IMSI)
	if err != nil {
		return SimCardCandidate{}, err
	}
	value.FirstSeenAt = time.Unix(firstSeenAt, 0).UTC()
	value.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

const simCardObservationSelect = `
	SELECT id, iccid, device_id, physical_device_id, eid, profile_aid, source,
		first_seen_at, last_seen_at, last_enabled_at
	FROM sim_card_observations`

func (s *Store) scanSimCardObservation(row rowScanner) (SimCardObservation, error) {
	var value SimCardObservation
	var firstSeenAt, lastSeenAt, lastEnabledAt int64
	err := row.Scan(
		&value.ID, &value.ICCID, &value.DeviceID, &value.PhysicalDeviceID,
		&value.EID, &value.ProfileAID, &value.Source,
		&firstSeenAt, &lastSeenAt, &lastEnabledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SimCardObservation{}, ErrNotFound
	}
	if err != nil {
		return SimCardObservation{}, err
	}
	value.EID, err = s.openField("sim_card_observations.eid", value.EID)
	if err != nil {
		return SimCardObservation{}, err
	}
	value.ProfileAID, err = s.openField("sim_card_observations.profile_aid", value.ProfileAID)
	if err != nil {
		return SimCardObservation{}, err
	}
	value.FirstSeenAt = time.Unix(firstSeenAt, 0).UTC()
	value.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	if lastEnabledAt != 0 {
		value.LastEnabledAt = time.Unix(lastEnabledAt, 0).UTC()
	}
	return value, nil
}

const simCardBindingSelect = `
	SELECT iccid, COALESCE(observation_id, 0), device_id, physical_device_id,
		eid, profile_aid, generation, state, stable_since, bound_at,
		last_seen_at, cleared_at, created_at, updated_at
	FROM sim_card_bindings`

func (s *Store) scanSimCardBinding(row rowScanner) (SimCardBinding, error) {
	var value SimCardBinding
	var stableSince, boundAt, lastSeenAt, clearedAt, createdAt, updatedAt int64
	err := row.Scan(
		&value.ICCID, &value.ObservationID, &value.DeviceID,
		&value.PhysicalDeviceID, &value.EID, &value.ProfileAID,
		&value.Generation, &value.State, &stableSince, &boundAt,
		&lastSeenAt, &clearedAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SimCardBinding{}, ErrNotFound
	}
	if err != nil {
		return SimCardBinding{}, err
	}
	value.EID, err = s.openField("sim_card_bindings.eid", value.EID)
	if err != nil {
		return SimCardBinding{}, err
	}
	value.ProfileAID, err = s.openField("sim_card_bindings.profile_aid", value.ProfileAID)
	if err != nil {
		return SimCardBinding{}, err
	}
	value.StableSince = simCardTimeFromUnix(stableSince)
	value.BoundAt = simCardTimeFromUnix(boundAt)
	value.LastSeenAt = simCardTimeFromUnix(lastSeenAt)
	value.ClearedAt = simCardTimeFromUnix(clearedAt)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func simCardTimeFromUnix(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}
