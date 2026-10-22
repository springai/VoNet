//go:build !linux

package device

import (
	"context"

	"vonet/internal/modem"
)

func readPlatformRegistration(context.Context, modem.Candidate) (platformRegistration, bool) {
	return platformRegistration{}, false
}
