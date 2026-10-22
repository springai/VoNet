package pcscimei

import "testing"

func TestDefaultPoolIsValidAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(DefaultPool))
	for _, imei := range DefaultPool {
		if err := Validate(imei); err != nil {
			t.Fatalf("DefaultPool contains invalid IMEI %q: %v", imei, err)
		}
		if _, duplicate := seen[imei]; duplicate {
			t.Fatalf("DefaultPool contains duplicate IMEI %q", imei)
		}
		seen[imei] = struct{}{}
	}
}
