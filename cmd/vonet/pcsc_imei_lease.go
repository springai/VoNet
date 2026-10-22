package main

import (
	"context"
	"fmt"

	"vonet/internal/store"
)

type storePCSCIMEILeaseSource struct {
	store *store.Store
}

func (source storePCSCIMEILeaseSource) CurrentPCSCIMEI(
	ctx context.Context,
	deviceID string,
) (string, string, error) {
	lease, err := source.store.PCSCIMEILease(ctx, deviceID)
	if err != nil {
		return "", "", err
	}
	return lease.IMEI, lease.Mode, nil
}

func (source storePCSCIMEILeaseSource) RotatePCSCIMEI(
	ctx context.Context,
	deviceID string,
) (string, bool, error) {
	lease, rotated, err := source.store.RotatePCSCIMEILease(ctx, deviceID)
	if err != nil {
		return "", false, fmt.Errorf("rotate PCSC IMEI lease: %w", err)
	}
	return lease.IMEI, rotated, nil
}
