package pcsc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (service *Service) PINStatus(ctx context.Context, selector Selector) (PINState, error) {
	return service.withPINCard(ctx, selector, func(card Card) (PINState, error) {
		if _, err := selectUSIM(ctx, card); err != nil {
			return PINState{}, err
		}
		return pinStatus(ctx, card)
	})
}

func (service *Service) VerifyPIN(ctx context.Context, selector Selector, pin string) (PINState, error) {
	return service.withPINCard(ctx, selector, func(card Card) (PINState, error) {
		if _, err := selectUSIM(ctx, card); err != nil {
			return PINState{}, err
		}
		if err := verifyPIN(ctx, card, pin); err != nil {
			state, statusErr := pinStatus(ctx, card)
			if statusErr != nil {
				return PINState{}, err
			}
			return state, err
		}
		return PINState{Required: false, Tries: -1}, nil
	})
}

func (service *Service) ChangePIN(ctx context.Context, selector Selector, current, next string) (PINState, error) {
	return service.changePIN(ctx, selector, current, next, false)
}

func (service *Service) UnblockPIN(ctx context.Context, selector Selector, puk, next string) (PINState, error) {
	return service.changePIN(ctx, selector, puk, next, true)
}

func (service *Service) changePIN(ctx context.Context, selector Selector, current, next string, puk bool) (PINState, error) {
	if err := validatePIN(current, puk); err != nil {
		return PINState{}, err
	}
	if err := validatePIN(next, false); err != nil {
		return PINState{}, err
	}
	return service.withPINCard(ctx, selector, func(card Card) (PINState, error) {
		if _, err := selectUSIM(ctx, card); err != nil {
			return PINState{}, err
		}
		state, err := pinStatus(ctx, card)
		if err != nil {
			return PINState{}, err
		}
		if !puk && state.Required && state.Tries >= 0 && state.Tries <= 2 {
			return state, &PINError{Kind: ErrPINTriesLow, Tries: state.Tries}
		}
		body := make([]byte, 16)
		for index := range body {
			body[index] = 0xff
		}
		copy(body[:8], current)
		copy(body[8:], next)
		command := []byte{0x00, 0x24, 0x00, 0x01, 0x10}
		if puk {
			command = []byte{0x00, 0x2C, 0x00, 0x00, 0x10}
		}
		command = append(command, body...)
		_, sw, transmitErr := card.Transmit(ctx, command)
		if transmitErr != nil {
			return PINState{}, errors.New("pcsc: PIN change transport failed")
		}
		if sw == 0x9000 {
			return PINState{Required: false, Tries: -1}, nil
		}
		if sw&0xFFF0 == 0x63C0 {
			tries := int(sw & 0x000F)
			kind := ErrPINRejected
			if puk {
				kind = errors.New("pcsc: SIM PUK was rejected")
			}
			return PINState{Required: true, Tries: tries, PUKRequired: puk}, &PINError{Kind: kind, Tries: tries}
		}
		return PINState{Required: true, Tries: -1, PUKRequired: puk}, ErrPINRejected
	})
}

func (service *Service) withPINCard(
	ctx context.Context,
	selector Selector,
	fn func(Card) (PINState, error),
) (PINState, error) {
	if service == nil || service.backend == nil {
		return PINState{}, ErrUnavailable
	}
	if err := selector.validate(); err != nil {
		return PINState{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	card, err := service.backend.Open(ctx, selector)
	if err != nil {
		return PINState{}, wrapReaderOpenError(err, selector)
	}
	defer card.Close()
	return fn(card)
}

func validatePIN(value string, puk bool) error {
	value = strings.TrimSpace(value)
	minimum, label := 4, "SIM PIN"
	if puk {
		minimum, label = 8, "SIM PUK"
	}
	if len(value) < minimum || len(value) > 8 || !decimalDigits(value) {
		return fmt.Errorf("pcsc: %s must contain %d digits", label, minimum)
	}
	return nil
}

func pinStatus(ctx context.Context, card Card) (PINState, error) {
	_, sw, err := card.Transmit(ctx, []byte{0x00, 0x20, 0x00, 0x01, 0x00})
	if err != nil {
		return PINState{}, errors.New("pcsc: SIM PIN status check failed")
	}
	if sw == 0x9000 {
		return PINState{Required: false, Tries: -1}, nil
	}
	if sw&0xFFF0 == 0x63C0 {
		tries := int(sw & 0x000F)
		return PINState{Required: true, Tries: tries, PUKRequired: tries == 0}, nil
	}
	if sw == 0x6983 || sw == 0x6984 {
		return PINState{Required: true, Tries: 0, PUKRequired: true}, nil
	}
	return PINState{Required: true, Tries: -1}, ErrPINRequired
}
