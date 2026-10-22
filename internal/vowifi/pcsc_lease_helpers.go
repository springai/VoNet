package vowifi

import "strings"

func rememberPCSCIMEI(adapter *PCSCAdapter, deviceID, imei string) {
	adapter.mu.Lock()
	adapter.imeiMap[strings.TrimSpace(deviceID)] = strings.TrimSpace(imei)
	adapter.mu.Unlock()
}
