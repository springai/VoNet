package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"vonet/internal/device"
	"vonet/internal/store"
)

const (
	cardGateCodeInvalidDevice       = "invalid_device"
	cardGateCodeIdentityUnavailable = "card_identity_unavailable"
	cardGateCodeNotRegistered       = "card_not_registered"
	cardGateCodeConflict            = "card_iccid_conflict"
	cardGateCodeOffline             = "card_offline"
	cardGateCodeBindingStale        = "card_binding_stale"
	cardGateCodeUnavailable         = "card_gate_unavailable"
	cardGateCodePhysicalMissing     = "physical_device_missing"
)

var (
	ErrCardNotRegistered = errors.New("card is not registered")
	ErrCardICCIDConflict = errors.New("ICCID is active on multiple devices")
	ErrCardOffline       = errors.New("registered card is offline")
	ErrCardBindingStale  = errors.New("card binding does not match the active device")
)

// RegisteredCard is the stable identity stored by the card registry.
type RegisteredCard struct {
	ICCID       string
	DisplayName string
	CardType    string
}

// CardBinding is a current device attachment. Historical observations must
// not be returned here because more than one current binding is a conflict.
type CardBinding struct {
	ICCID      string
	DeviceID   string
	PhysicalID string
	Generation int64
	Active     bool
	State      string
}

// CardExecutionContext is the validated identity and hardware context for an
// operation that changes network or communication state.
type CardExecutionContext struct {
	ICCID      string
	DeviceID   string
	PhysicalID string
	Snapshot   device.Snapshot
	Card       RegisteredCard
	Binding    CardBinding
}

// CardRegistry is the narrow store boundary expected from store.cards.
type CardRegistry interface {
	RegisteredCard(ctx context.Context, iccid string) (RegisteredCard, error)
	CardBindings(ctx context.Context, iccid string) ([]CardBinding, error)
}

type CardDeviceState struct {
	DeviceID   string
	PhysicalID string
	Snapshot   device.Snapshot
	Present    bool
}

// CardDeviceLookup resolves the configured device to its current physical
// device and reports all live devices currently presenting an ICCID.
type CardDeviceLookup interface {
	CurrentCardDevice(ctx context.Context, deviceID string) (CardDeviceState, error)
	MatchingCardDevices(ctx context.Context, iccid string) ([]CardDeviceState, error)
}

type CardGateError struct {
	Code       string
	Message    string
	HTTPStatus int
	Cause      error
}

func (err *CardGateError) Error() string {
	if err.Cause != nil {
		return fmt.Sprintf("%s: %v", err.Message, err.Cause)
	}
	return err.Message
}

func (err *CardGateError) Unwrap() error {
	return err.Cause
}

func cardGateError(code, message string, status int, cause error) *CardGateError {
	return &CardGateError{Code: code, Message: message, HTTPStatus: status, Cause: cause}
}

func normalizeCardGateICCID(value string) string {
	return strings.TrimSpace(value)
}

func normalizeCardGateDeviceID(value string) string {
	return strings.TrimSpace(value)
}

func resolveRegisteredCard(
	ctx context.Context,
	registry CardRegistry,
	devices CardDeviceLookup,
	deviceID string,
	expectedICCID string,
) (CardExecutionContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deviceID = normalizeCardGateDeviceID(deviceID)
	expectedICCID = normalizeCardGateICCID(expectedICCID)
	if !validDeviceID(deviceID) {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeInvalidDevice,
			"device ID is invalid",
			http.StatusBadRequest,
			nil,
		)
	}
	if registry == nil || devices == nil {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeUnavailable,
			"card registry or device lookup is unavailable",
			http.StatusServiceUnavailable,
			nil,
		)
	}

	current, err := devices.CurrentCardDevice(ctx, deviceID)
	if err != nil {
		return CardExecutionContext{}, err
	}
	if !current.Present {
		return CardExecutionContext{}, cardGateError(
			cardGateCodePhysicalMissing,
			"the configured device is not present",
			http.StatusServiceUnavailable,
			nil,
		)
	}

	liveICCID := normalizeCardGateICCID(current.Snapshot.ICCID)
	if liveICCID == "" {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeIdentityUnavailable,
			"the device has not produced a stable ICCID",
			http.StatusConflict,
			nil,
		)
	}
	if _, err := store.NormalizeSimICCID(liveICCID); err != nil {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeIdentityUnavailable,
			"the device produced an invalid ICCID",
			http.StatusConflict,
			err,
		)
	}
	if expectedICCID != "" {
		if _, err := store.NormalizeSimICCID(expectedICCID); err != nil {
			return CardExecutionContext{}, cardGateError(
				cardGateCodeIdentityUnavailable,
				"the expected ICCID is invalid",
				http.StatusBadRequest,
				err,
			)
		}
	}
	if expectedICCID != "" && !strings.EqualFold(liveICCID, expectedICCID) {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeBindingStale,
			"the device is no longer using the expected ICCID",
			http.StatusConflict,
			ErrCardBindingStale,
		)
	}
	if !current.Snapshot.Responsive || !current.Snapshot.SIMReady {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeOffline,
			"the current card is not ready",
			http.StatusConflict,
			ErrCardOffline,
		)
	}

	matches, err := devices.MatchingCardDevices(ctx, liveICCID)
	if err != nil {
		return CardExecutionContext{}, err
	}
	uniqueMatches := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		key := normalizeCardGateDeviceID(match.PhysicalID)
		if key == "" {
			key = normalizeCardGateDeviceID(match.DeviceID)
		}
		if key == "" {
			continue
		}
		uniqueMatches[key] = struct{}{}
	}
	if len(uniqueMatches) > 1 {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeConflict,
			"the same ICCID is currently present on multiple devices",
			http.StatusConflict,
			ErrCardICCIDConflict,
		)
	}

	card, err := registry.RegisteredCard(ctx, liveICCID)
	if err != nil {
		if errors.Is(err, ErrCardNotRegistered) || errors.Is(err, store.ErrNotFound) {
			return CardExecutionContext{}, cardGateError(
				cardGateCodeNotRegistered,
				"the active ICCID is not registered in card management",
				http.StatusConflict,
				ErrCardNotRegistered,
			)
		}
		return CardExecutionContext{}, cardGateError(
			cardGateCodeUnavailable,
			"could not read the registered card",
			http.StatusServiceUnavailable,
			err,
		)
	}
	if normalizeCardGateICCID(card.ICCID) == "" {
		card.ICCID = liveICCID
	}
	if !strings.EqualFold(normalizeCardGateICCID(card.ICCID), liveICCID) {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeBindingStale,
			"the card registry returned a different ICCID",
			http.StatusConflict,
			ErrCardBindingStale,
		)
	}

	bindings, err := registry.CardBindings(ctx, liveICCID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			bindings = nil
		} else {
			return CardExecutionContext{}, cardGateError(
				cardGateCodeUnavailable,
				"could not read card bindings",
				http.StatusServiceUnavailable,
				err,
			)
		}
	}
	if len(bindings) > 1 {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeConflict,
			"the ICCID has more than one current device binding",
			http.StatusConflict,
			ErrCardICCIDConflict,
		)
	}
	if len(bindings) == 0 {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeOffline,
			"the registered card has no current device binding",
			http.StatusConflict,
			ErrCardOffline,
		)
	}

	binding := bindings[0]
	if binding.State == store.SimCardBindingConflict {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeConflict,
			"the registered card binding is marked as conflicting",
			http.StatusConflict,
			ErrCardICCIDConflict,
		)
	}
	if !binding.Active {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeOffline,
			"the registered card is not active",
			http.StatusConflict,
			ErrCardOffline,
		)
	}
	bindingID := normalizeCardGateDeviceID(binding.DeviceID)
	bindingPhysicalID := normalizeCardGateDeviceID(binding.PhysicalID)
	currentPhysicalID := normalizeCardGateDeviceID(current.PhysicalID)
	if (bindingID != "" && !strings.EqualFold(bindingID, deviceID)) ||
		(bindingPhysicalID != "" && currentPhysicalID != "" && !strings.EqualFold(bindingPhysicalID, currentPhysicalID)) {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeBindingStale,
			"the stored card binding points to another device",
			http.StatusConflict,
			ErrCardBindingStale,
		)
	}

	return CardExecutionContext{
		ICCID:      liveICCID,
		DeviceID:   deviceID,
		PhysicalID: current.PhysicalID,
		Snapshot:   current.Snapshot,
		Card:       card,
		Binding:    binding,
	}, nil
}

func (s *Server) ResolveRegisteredActiveCard(
	ctx context.Context,
	deviceID string,
	expectedICCID string,
) (CardExecutionContext, error) {
	if s == nil || s.store == nil {
		return CardExecutionContext{}, cardGateError(
			cardGateCodeUnavailable,
			"the card registry API is not available",
			http.StatusServiceUnavailable,
			nil,
		)
	}
	registry := storeCardRegistry{store: s.store}
	return resolveRegisteredCard(ctx, registry, serverCardGateDevices{server: s}, deviceID, expectedICCID)
}

func (s *Server) RequireRegisteredActiveCard(
	w http.ResponseWriter,
	r *http.Request,
	deviceID string,
	expectedICCID string,
) (CardExecutionContext, bool) {
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	execution, err := s.ResolveRegisteredActiveCard(ctx, deviceID, expectedICCID)
	if err != nil {
		writeCardGateError(w, err)
		return CardExecutionContext{}, false
	}
	return execution, true
}

type cardSIMReadLocker interface {
	AcquireSIMRead(context.Context, string) (func(), error)
}

func (s *Server) acquireCardExecution(
	ctx context.Context,
	execution CardExecutionContext,
) (func(), error) {
	release := func() {}
	if locker, ok := s.devices.(cardSIMReadLocker); ok {
		var err error
		release, err = locker.AcquireSIMRead(ctx, execution.PhysicalID)
		if err != nil {
			return nil, err
		}
	}
	verified, err := s.ResolveRegisteredActiveCard(ctx, execution.DeviceID, execution.ICCID)
	if err != nil {
		release()
		return nil, err
	}
	if verified.Binding.Generation != execution.Binding.Generation {
		release()
		return nil, cardGateError(
			cardGateCodeBindingStale,
			"the card binding changed before the operation",
			http.StatusConflict,
			ErrCardBindingStale,
		)
	}
	return release, nil
}

type cardStopExecutionContext struct {
	ICCID      string
	PhysicalID string
}

func (s *Server) acquireCardStopExecution(
	ctx context.Context,
	config store.Device,
	expectedICCID string,
) (cardStopExecutionContext, func(), error) {
	entry, physicalID, present := s.physicalForConfig(config)
	if !present || entry.Snapshot == nil {
		expectedICCID = strings.TrimSpace(expectedICCID)
		if expectedICCID != "" {
			binding, err := s.store.SimCardBinding(ctx, expectedICCID)
			if err != nil || binding.State != store.SimCardBindingActive ||
				!strings.EqualFold(strings.TrimSpace(binding.DeviceID), strings.TrimSpace(config.ID)) {
				return cardStopExecutionContext{}, nil, cardGateError(
					cardGateCodeBindingStale,
					"the stop request belongs to a different ICCID",
					http.StatusConflict,
					ErrCardBindingStale,
				)
			}
		}
		return cardStopExecutionContext{ICCID: expectedICCID, PhysicalID: physicalID}, func() {}, nil
	}
	release := func() {}
	if locker, ok := s.devices.(cardSIMReadLocker); ok {
		var err error
		release, err = locker.AcquireSIMRead(ctx, physicalID)
		if err != nil {
			return cardStopExecutionContext{}, nil, err
		}
	}
	if expectedICCID = strings.TrimSpace(expectedICCID); expectedICCID != "" &&
		!s.callStopMatchesICCID(config, expectedICCID) {
		release()
		return cardStopExecutionContext{}, nil, cardGateError(
			cardGateCodeBindingStale,
			"the stop request belongs to a different ICCID",
			http.StatusConflict,
			ErrCardBindingStale,
		)
	}
	return cardStopExecutionContext{
		ICCID:      strings.TrimSpace(entry.Snapshot.ICCID),
		PhysicalID: physicalID,
	}, release, nil
}

// AllowCardStop authorizes fail-safe cleanup independently of registration.
// It must only be used for operations that stop work, such as disabling data,
// entering airplane mode, hanging up, or cancelling a session.
func AllowCardStop(deviceID string) bool {
	return validDeviceID(normalizeCardGateDeviceID(deviceID))
}

func writeCardGateError(w http.ResponseWriter, err error) {
	var gateErr *CardGateError
	if errors.As(err, &gateErr) {
		status := gateErr.HTTPStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		code := gateErr.Code
		if code == "" {
			code = cardGateCodeUnavailable
		}
		message := gateErr.Message
		if message == "" {
			message = "card operation is not authorized"
		}
		writeError(w, status, code, message)
		return
	}
	writeError(
		w,
		http.StatusServiceUnavailable,
		cardGateCodeUnavailable,
		"card operation authorization is unavailable",
	)
}

type serverCardGateDevices struct {
	server *Server
}

type storeCardRegistry struct {
	store *store.Store
}

func (registry storeCardRegistry) RegisteredCard(
	ctx context.Context,
	iccid string,
) (RegisteredCard, error) {
	if registry.store == nil {
		return RegisteredCard{}, cardGateError(
			cardGateCodeUnavailable,
			"the card registry API is not available",
			http.StatusServiceUnavailable,
			nil,
		)
	}
	value, err := registry.store.SimCard(ctx, iccid)
	if err != nil {
		return RegisteredCard{}, err
	}
	return RegisteredCard{
		ICCID:       value.ICCID,
		DisplayName: value.DisplayName,
		CardType:    value.CardType,
	}, nil
}

func (registry storeCardRegistry) CardBindings(
	ctx context.Context,
	iccid string,
) ([]CardBinding, error) {
	if registry.store == nil {
		return nil, cardGateError(
			cardGateCodeUnavailable,
			"the card registry API is not available",
			http.StatusServiceUnavailable,
			nil,
		)
	}
	value, err := registry.store.SimCardBinding(ctx, iccid)
	if err != nil {
		return nil, err
	}
	return []CardBinding{{
		ICCID:      value.ICCID,
		DeviceID:   value.DeviceID,
		PhysicalID: value.PhysicalDeviceID,
		Generation: value.Generation,
		Active:     value.State == store.SimCardBindingActive,
		State:      value.State,
	}}, nil
}

func (lookup serverCardGateDevices) CurrentCardDevice(
	ctx context.Context,
	deviceID string,
) (CardDeviceState, error) {
	if lookup.server == nil || lookup.server.store == nil || lookup.server.devices == nil {
		return CardDeviceState{}, cardGateError(
			cardGateCodeUnavailable,
			"device lookup is unavailable",
			http.StatusServiceUnavailable,
			nil,
		)
	}
	config, err := lookup.server.store.Device(ctx, deviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return CardDeviceState{}, cardGateError(
				cardGateCodePhysicalMissing,
				"the configured device was not found",
				http.StatusServiceUnavailable,
				err,
			)
		}
		return CardDeviceState{}, cardGateError(
			cardGateCodeUnavailable,
			"could not read the configured device",
			http.StatusServiceUnavailable,
			err,
		)
	}
	entry, physicalID, present := lookup.server.physicalForConfig(config)
	snapshot := device.Snapshot{}
	if entry.Snapshot != nil {
		snapshot = *entry.Snapshot
	}
	return CardDeviceState{
		DeviceID:   deviceID,
		PhysicalID: physicalID,
		Snapshot:   snapshot,
		Present:    present,
	}, nil
}

func (lookup serverCardGateDevices) MatchingCardDevices(
	_ context.Context,
	iccid string,
) ([]CardDeviceState, error) {
	if lookup.server == nil || lookup.server.devices == nil {
		return nil, cardGateError(
			cardGateCodeUnavailable,
			"device lookup is unavailable",
			http.StatusServiceUnavailable,
			nil,
		)
	}
	iccid = normalizeCardGateICCID(iccid)
	result := make([]CardDeviceState, 0, 1)
	for _, entry := range lookup.server.devices.List() {
		if !entry.Discovered || entry.Snapshot == nil ||
			!strings.EqualFold(normalizeCardGateICCID(entry.Snapshot.ICCID), iccid) {
			continue
		}
		snapshot := *entry.Snapshot
		result = append(result, CardDeviceState{
			DeviceID:   entry.ID,
			PhysicalID: entry.ID,
			Snapshot:   snapshot,
			Present:    true,
		})
	}
	return result, nil
}
