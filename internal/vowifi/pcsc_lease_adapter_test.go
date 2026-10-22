package vowifi

import (
	"context"
	"errors"
	"testing"

	"vonet/internal/pcsc"
)

type countingPCSCIMILeaseSource struct {
	rotations int
}

func (*countingPCSCIMILeaseSource) CurrentPCSCIMEI(context.Context, string) (string, string, error) {
	return "860000000000013", "random", nil
}

func (source *countingPCSCIMILeaseSource) RotatePCSCIMEI(context.Context, string) (string, bool, error) {
	source.rotations++
	return "860000000000012", true, nil
}

func TestPCSCLeaseAdapterProvisioningReadDoesNotRotateIMEI(t *testing.T) {
	base, err := NewPCSCAdapter(
		pcsc.NewWithBackend(&mockPCSCBackend{}),
		func(context.Context, string) (pcsc.Selector, string, error) {
			return pcsc.Selector{}, "", errors.New("stop before card I/O")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	leases := &countingPCSCIMILeaseSource{}
	adapter, err := NewPCSCAdapterWithLeases(base, leases)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := adapter.ReadIdentityWithoutRotation(context.Background(), "reader-1"); err == nil {
		t.Fatal("expected resolver error")
	}
	if leases.rotations != 0 {
		t.Fatalf("provisioning read rotations = %d, want 0", leases.rotations)
	}
	if _, err := adapter.ReadIdentity(context.Background(), "reader-1"); err == nil {
		t.Fatal("expected resolver error")
	}
	if leases.rotations != 1 {
		t.Fatalf("normal read rotations = %d, want 1", leases.rotations)
	}
}
