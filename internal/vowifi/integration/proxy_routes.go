package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"vonet/internal/device"
	"vonet/internal/store"
	"vonet/internal/vowifi"
)

// ResolveRoutes returns ordered manual routes when a device/ICCID binding
// exists. MCC rules are resolved dynamically only when no manual group exists.
func (resolver ProxyResolver) ResolveRoutes(
	ctx context.Context,
	request vowifi.ProxyRequest,
) (vowifi.ProxyRoutePlan, error) {
	if resolver.Store == nil {
		return vowifi.ProxyRoutePlan{}, errors.New("vowifi proxy resolver: store is nil")
	}
	deviceID := strings.TrimSpace(request.DeviceID)
	iccid := strings.TrimSpace(request.ICCID)
	if deviceID == "" {
		return vowifi.ProxyRoutePlan{Routes: []vowifi.ProxyRoute{{Mode: vowifi.ProxyModeDirect}}}, nil
	}

	if iccid != "" {
		group, err := resolver.Store.DeviceProxyBindingGroup(ctx, deviceID, iccid)
		if err == nil && len(group.Routes) > 0 {
			routes := make([]vowifi.ProxyRoute, 0, len(group.Routes))
			for _, bindingRoute := range group.Routes {
				upstream, loadErr := resolver.Store.UpstreamProxy(ctx, bindingRoute.UpstreamProxyID)
				if loadErr != nil {
					return vowifi.ProxyRoutePlan{}, fmt.Errorf("load manual upstream proxy %q for device %s: %w", bindingRoute.UpstreamProxyID, deviceID, loadErr)
				}
				if !upstream.Enabled {
					continue
				}
				routes = append(routes, proxyRouteFromUpstream(upstream))
			}
			if len(routes) == 0 {
				return vowifi.ProxyRoutePlan{}, fmt.Errorf("all manual upstream proxies for ICCID %s are disabled", iccid)
			}
			return vowifi.ProxyRoutePlan{Routes: routes, Manual: true, BindingGroupID: group.ID}, nil
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return vowifi.ProxyRoutePlan{}, fmt.Errorf("resolve proxy binding group for ICCID %s: %w", iccid, err)
		}

		// Compatibility with databases populated by the old one-proxy API.
		if binding, legacyErr := resolver.Store.DeviceProxyBinding(ctx, iccid); legacyErr == nil &&
			binding.DeviceID == deviceID {
			upstream, loadErr := resolver.Store.UpstreamProxy(ctx, binding.UpstreamProxyID)
			if loadErr != nil {
				return vowifi.ProxyRoutePlan{}, fmt.Errorf("load legacy upstream proxy %q for device %s: %w", binding.UpstreamProxyID, deviceID, loadErr)
			}
			if !upstream.Enabled {
				return vowifi.ProxyRoutePlan{}, fmt.Errorf("legacy upstream proxy %q for device %s is disabled", upstream.ID, deviceID)
			}
			return vowifi.ProxyRoutePlan{Routes: []vowifi.ProxyRoute{proxyRouteFromUpstream(upstream)}, Manual: true}, nil
		} else if legacyErr != nil && !errors.Is(legacyErr, store.ErrNotFound) {
			return vowifi.ProxyRoutePlan{}, fmt.Errorf("resolve legacy proxy binding for ICCID %s: %w", iccid, legacyErr)
		}
	}

	country, found := device.CountryForMCC(strings.TrimSpace(request.HomeMCC))
	if !found {
		country = strings.ToUpper(strings.TrimSpace(request.CountryCode))
		if len(country) != 2 {
			return vowifi.ProxyRoutePlan{Routes: []vowifi.ProxyRoute{{Mode: vowifi.ProxyModeDirect}}}, nil
		}
	}
	rule, err := resolver.Store.CountryRule(ctx, country)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !rule.Enabled) {
		return vowifi.ProxyRoutePlan{Routes: []vowifi.ProxyRoute{{Mode: vowifi.ProxyModeDirect}}}, nil
	}
	if err != nil {
		return vowifi.ProxyRoutePlan{}, fmt.Errorf("resolve proxy country rule for MCC %s: %w", request.HomeMCC, err)
	}
	upstream, err := resolver.Store.UpstreamProxy(ctx, rule.UpstreamProxyID)
	if err != nil {
		return vowifi.ProxyRoutePlan{}, fmt.Errorf("load MCC upstream proxy %q for device %s: %w", rule.UpstreamProxyID, deviceID, err)
	}
	if !upstream.Enabled {
		return vowifi.ProxyRoutePlan{Routes: []vowifi.ProxyRoute{{Mode: vowifi.ProxyModeDirect}}}, nil
	}
	return vowifi.ProxyRoutePlan{Routes: []vowifi.ProxyRoute{proxyRouteFromUpstream(upstream)}}, nil
}

func proxyRouteFromUpstream(upstream store.UpstreamProxy) vowifi.ProxyRoute {
	return vowifi.ProxyRoute{
		Mode:     vowifi.ProxyModeSOCKS5,
		ID:       upstream.ID,
		Address:  upstream.Addr,
		Username: upstream.Username,
		Password: upstream.Password,
	}
}
