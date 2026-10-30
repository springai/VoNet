package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSendBarkPayloadWithIsolatesDestinationsAndMasksFailures(t *testing.T) {
	const secret = "synthetic-bark-key"
	destinations := []string{
		"https://api.day.app/" + secret + "-a/",
		"https://api.day.app/" + secret + "-b/",
		"https://api.day.app/" + secret + "-c/",
	}
	attempted := make([]string, 0, len(destinations))
	send := func(destination string) error {
		attempted = append(attempted, destination)
		if strings.Contains(destination, "-b/") {
			return nil
		}
		return fmt.Errorf("provider echoed %q: %w", destination, context.DeadlineExceeded)
	}

	delivered, err := sendBarkPayloadWith(destinations, false, send)
	if err != nil || delivered != 1 {
		t.Fatalf("partial delivery = %d, %v; want 1, nil", delivered, err)
	}
	if len(attempted) != len(destinations) {
		t.Fatalf("attempted %d destinations, want all %d", len(attempted), len(destinations))
	}

	attempted = attempted[:0]
	delivered, err = sendBarkPayloadWith(destinations, true, send)
	if delivered != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("strict delivery = %d, %v; want 1 and deadline classification", delivered, err)
	}
	for _, value := range []string{err.Error(), fmt.Sprintf("%+v", err)} {
		if strings.Contains(value, secret) || strings.Contains(value, "api.day.app") {
			t.Fatalf("Bark failure exposed destination: %s", value)
		}
	}

	delivered, err = sendBarkPayloadWith(destinations, false, func(string) error {
		return errors.New("provider response containing " + secret)
	})
	if delivered != 0 || err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("all-failed delivery = %d, %v; want 0 and masked error", delivered, err)
	}
}

func TestSanitizeBarkDeliveryErrorPreservesOnlySafeClassification(t *testing.T) {
	const secret = "synthetic-bark-key"
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"canceled", fmt.Errorf("%s: %w", secret, context.Canceled), context.Canceled},
		{"deadline", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), context.DeadlineExceeded},
		{"provider rejected", fmt.Errorf("%s: %w", secret, errProviderRejected), errProviderRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeBarkDeliveryError(2, test.err)
			if !errors.Is(got, test.want) {
				t.Fatalf("classification = %v, want %v", got, test.want)
			}
			if strings.Contains(got.Error(), secret) {
				t.Fatalf("sanitized error exposed cause: %v", got)
			}
		})
	}
}
