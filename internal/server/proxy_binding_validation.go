package server

import (
	"fmt"
	"strings"
)

func validateProxyRouteIDs(proxyIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(proxyIDs))
	values := make([]string, 0, len(proxyIDs))
	for _, raw := range proxyIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("proxy route ID cannot be empty")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate proxy route %q", id)
		}
		seen[id] = struct{}{}
		values = append(values, id)
	}
	return values, nil
}
