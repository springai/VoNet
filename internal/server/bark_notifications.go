package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
)

// sendBarkPayload isolates failures between independent Bark destinations.
// Runtime notifications are complete when at least one destination accepts the
// payload; retrying the whole list after a partial success would duplicate
// notifications on the destinations that already succeeded. Test sends are
// strict so the settings page reports a partially broken list.
func sendBarkPayload(
	ctx context.Context,
	client *http.Client,
	destinations []string,
	payload []byte,
	userAgent string,
	requireAll bool,
) (int, error) {
	return sendBarkPayloadWith(
		destinations,
		requireAll,
		func(destination string) error {
			return sendBarkDestination(ctx, client, destination, payload, userAgent)
		},
	)
}

func sendBarkPayloadWith(
	destinations []string,
	requireAll bool,
	send func(string) error,
) (int, error) {
	delivered := 0
	failures := make([]error, 0, len(destinations))
	for index, destination := range destinations {
		err := send(destination)
		if err != nil {
			failures = append(failures, sanitizeBarkDeliveryError(index+1, err))
			continue
		}
		delivered++
	}
	if requireAll || delivered == 0 {
		return delivered, errors.Join(failures...)
	}
	return delivered, nil
}

func sendBarkDestination(
	ctx context.Context,
	client *http.Client,
	destination string,
	payload []byte,
	userAgent string,
) error {
	parsed, err := validateOutboundURL(ctx, destination, false)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		parsed.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create Bark notification request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("User-Agent", userAgent)
	return performNotificationRequest(client, request, false)
}

// Bark keys are commonly encoded in the destination path. Proxy and HTTP
// errors can echo the full URL, so callers only receive a destination ordinal
// and safe error classification.
func sanitizeBarkDeliveryError(index int, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("Bark destination %d: %w", index, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Bark destination %d: %w", index, context.DeadlineExceeded)
	case errors.Is(err, errUnsafeDestination):
		return fmt.Errorf("Bark destination %d: %w", index, errUnsafeDestination)
	case errors.Is(err, errProviderRejected):
		return fmt.Errorf("Bark destination %d: %w", index, errProviderRejected)
	default:
		return fmt.Errorf("Bark destination %d failed", index)
	}
}
