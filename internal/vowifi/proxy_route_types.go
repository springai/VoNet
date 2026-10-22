package vowifi

import (
	"context"
	"time"
)

// ProxyRoutePlan is the ordered candidate set for one device and ICCID.
type ProxyRoutePlan struct {
	Routes         []ProxyRoute
	Manual         bool
	BindingGroupID string
}

// ProxyRouteResolver is implemented by resolvers that support ordered manual routes.
type ProxyRouteStateSnapshot struct {
	CurrentRouteID string
	StickyRouteID  string
	RouteFailures  uint
	LastSuccessAt  time.Time
}

type ProxyRouteStateStore interface {
	LoadProxyRouteState(context.Context, string) (ProxyRouteStateSnapshot, error)
	SaveProxyRouteState(context.Context, string, ProxyRouteStateSnapshot) error
}

type ProxyRouteResolver interface {
	ProxyResolver
	ResolveRoutes(context.Context, ProxyRequest) (ProxyRoutePlan, error)
}
