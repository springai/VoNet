package device

import (
	"context"
	"errors"

	"vonet/internal/pcsc"
)

func (manager *Manager) PINStatus(ctx context.Context, id string) (pcsc.PINState, error) {
	selector, err := manager.pcscSelector(id)
	if err != nil {
		return pcsc.PINState{}, err
	}
	return manager.cardReaders.PINStatus(ctx, selector)
}

func (manager *Manager) VerifyPIN(ctx context.Context, id, pin string) (pcsc.PINState, error) {
	selector, err := manager.pcscSelector(id)
	if err != nil {
		return pcsc.PINState{}, err
	}
	return manager.cardReaders.VerifyPIN(ctx, selector, pin)
}

func (manager *Manager) ChangePIN(ctx context.Context, id, current, next string) (pcsc.PINState, error) {
	selector, err := manager.pcscSelector(id)
	if err != nil {
		return pcsc.PINState{}, err
	}
	return manager.cardReaders.ChangePIN(ctx, selector, current, next)
}

func (manager *Manager) UnblockPIN(ctx context.Context, id, puk, next string) (pcsc.PINState, error) {
	selector, err := manager.pcscSelector(id)
	if err != nil {
		return pcsc.PINState{}, err
	}
	return manager.cardReaders.UnblockPIN(ctx, selector, puk, next)
}

func (manager *Manager) pcscSelector(id string) (pcsc.Selector, error) {
	if manager.cardReaders == nil {
		return pcsc.Selector{}, pcsc.ErrUnavailable
	}
	entry, err := manager.Get(id)
	if err != nil {
		return pcsc.Selector{}, err
	}
	if entry.Candidate.HardwareKind != pcsc.HardwareKind {
		return pcsc.Selector{}, errors.New("device is not a PC/SC reader")
	}
	return pcsc.Selector{
		ReaderName: entry.Candidate.ReaderName,
		USBSerial:  entry.Candidate.SerialNumber,
		USBPath:    entry.Candidate.USBPath,
	}, nil
}
