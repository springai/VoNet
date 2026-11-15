//go:build !linux

package device

import (
	"context"

	"vonet/internal/modem"
	"vonet/internal/pcsc"
)

func openQMIDataSession(context.Context, string) (qmiDataSession, error) {
	return nil, ErrDataBackendUnavailable
}

func invalidateQMINetworkSession(*managedDevice, modem.Candidate) {}

func (manager *Manager) NetworkStatus(_ context.Context, id string) (NetworkStatus, error) {
	state, err := manager.lookup(id)
	if err != nil {
		return NetworkStatus{}, err
	}
	if manager.candidateFor(state).HardwareKind == pcsc.HardwareKind {
		return NetworkStatus{
			Backend: "pcsc",
			Detail:  "cellular data is unavailable for PCSC readers",
		}, ErrDataBackendUnavailable
	}
	return NetworkStatus{Backend: "at"}, ErrDataBackendUnavailable
}

func (manager *Manager) setQMINetwork(
	context.Context,
	*managedDevice,
	modem.Candidate,
	bool,
	string,
	string,
	string,
	string,
	string,
) (NetworkResult, error) {
	return NetworkResult{}, ErrDataBackendUnavailable
}
