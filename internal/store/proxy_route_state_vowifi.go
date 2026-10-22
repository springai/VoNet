package store

import (
	"context"
	"errors"
	"time"

	"vonet/internal/vowifi"
)

func (s *Store) LoadProxyRouteState(ctx context.Context, groupID string) (vowifi.ProxyRouteStateSnapshot, error) {
	state, err := s.DeviceProxyRouteState(ctx, groupID)
	if errors.Is(err, ErrNotFound) {
		return vowifi.ProxyRouteStateSnapshot{}, err
	}
	if err != nil {
		return vowifi.ProxyRouteStateSnapshot{}, err
	}
	return vowifi.ProxyRouteStateSnapshot{
		CurrentRouteID: state.CurrentRouteID,
		StickyRouteID:  state.StickyRouteID,
		RouteFailures:  state.RouteFailures,
		LastSuccessAt:  state.LastSuccessAt,
	}, nil
}

func (s *Store) SaveProxyRouteState(ctx context.Context, groupID string, value vowifi.ProxyRouteStateSnapshot) error {
	lastSuccess := value.LastSuccessAt
	if lastSuccess.IsZero() {
		lastSuccess = time.Unix(0, 0).UTC()
	}
	return s.SaveDeviceProxyRouteState(ctx, DeviceProxyRouteState{
		BindingGroupID: groupID,
		CurrentRouteID: value.CurrentRouteID,
		StickyRouteID:  value.StickyRouteID,
		RouteFailures:  value.RouteFailures,
		LastSuccessAt:  lastSuccess,
		UpdatedAt:      time.Now().UTC(),
	})
}
