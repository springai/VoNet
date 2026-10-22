package pcsc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type missingReaderBackend struct{}

func (missingReaderBackend) Readers(context.Context) ([]Reader, error) {
	return nil, nil
}

func (missingReaderBackend) Open(context.Context, Selector) (Card, error) {
	return nil, ErrReaderNotFound
}

func TestReaderOpenErrorIncludesSelector(t *testing.T) {
	service := NewWithBackend(missingReaderBackend{})
	_, err := service.ReadIdentity(context.Background(), Selector{
		USBPath:    "1-1",
		ReaderName: "Reader-TEST00000001",
		USBSerial:  "TEST00000001",
	}, "")
	if !errors.Is(err, ErrReaderNotFound) {
		t.Fatalf("ReadIdentity error = %v, want ErrReaderNotFound", err)
	}
	for _, detail := range []string{
		`usb_path="1-1"`,
		`reader_name="Reader-TEST00000001"`,
		`usb_serial="TEST00000001"`,
	} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("ReadIdentity error %q missing %s", err, detail)
		}
	}
}
