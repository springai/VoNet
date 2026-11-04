package vowifi

import "context"

// ReadSMSCenterPSI preserves the historical EC20 entry point while the
// command implementation lives on the shared AT adapter core.
func (adapter *EC20Adapter) ReadSMSCenterPSI(ctx context.Context, deviceID string) (string, error) {
	return adapter.atModemAdapter.ReadSMSCenterPSI(ctx, deviceID)
}
