package vowifi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type routeSelectorResolver struct {
	orchestrator *Orchestrator
	inner        ProxyResolver
}

func (resolver *routeSelectorResolver) Resolve(ctx context.Context, request ProxyRequest) (ProxyRoute, error) {
	return resolver.orchestrator.selectProxyRoute(ctx, request, resolver.inner)
}

func (orchestrator *Orchestrator) selectProxyRoute(ctx context.Context, request ProxyRequest, inner ProxyResolver) (ProxyRoute, error) {
	var plan ProxyRoutePlan
	if multi, ok := inner.(ProxyRouteResolver); ok {
		resolved, err := multi.ResolveRoutes(ctx, request)
		if err != nil {
			return ProxyRoute{}, err
		}
		plan = resolved
	} else {
		route, err := inner.Resolve(ctx, request)
		if err != nil {
			return ProxyRoute{}, err
		}
		plan = ProxyRoutePlan{Routes: []ProxyRoute{route}}
	}
	if len(plan.Routes) == 0 {
		return ProxyRoute{}, errors.New("vowifi proxy resolver returned an empty route plan")
	}

	requestICCID := strings.TrimSpace(request.ICCID)
	sameICCID := orchestrator.routeICCID == requestICCID
	planChanged := proxyRoutePlanSignature(orchestrator.routePlan) != proxyRoutePlanSignature(plan)
	selectionReset := planChanged || !sameICCID
	var persisted *ProxyRouteStateSnapshot
	if plan.Manual && plan.BindingGroupID != "" && orchestrator.routeICCID != requestICCID && orchestrator.deps.RouteStates != nil {
		if state, err := orchestrator.deps.RouteStates.LoadProxyRouteState(ctx, plan.BindingGroupID); err == nil {
			persisted = &state
		}
	}
	orchestrator.mu.Lock()
	previousRouteID := ""
	if len(orchestrator.routePlan.Routes) > 0 && orchestrator.routeIndex >= 0 && orchestrator.routeIndex < len(orchestrator.routePlan.Routes) {
		previousRouteID = orchestrator.routePlan.Routes[orchestrator.routeIndex].ID
	}
	orchestrator.routeICCID = requestICCID
	orchestrator.routePlan = plan
	if selectionReset {
		orchestrator.routeIndex = 0
		orchestrator.routeFailures = 0
		orchestrator.stickyProxyID = ""
		orchestrator.proxyHadSuccess = false
		orchestrator.proxyLastSuccessAt = time.Time{}
	} else if previousRouteID != "" {
		for index, route := range plan.Routes {
			if route.ID == previousRouteID {
				orchestrator.routeIndex = index
				break
			}
		}
	}
	if persisted != nil {
		orchestrator.routeFailures = persisted.RouteFailures
		orchestrator.stickyProxyID = persisted.StickyRouteID
		orchestrator.proxyLastSuccessAt = persisted.LastSuccessAt
		orchestrator.proxyHadSuccess = persisted.StickyRouteID != ""
		for index, route := range plan.Routes {
			if route.ID == persisted.CurrentRouteID {
				orchestrator.routeIndex = index
				break
			}
		}
	}
	if orchestrator.proxyHadSuccess && orchestrator.stickyProxyID != "" {
		for index, route := range plan.Routes {
			if route.ID == orchestrator.stickyProxyID {
				orchestrator.routeIndex = index
				break
			}
		}
	}
	if orchestrator.routeIndex < 0 || orchestrator.routeIndex >= len(plan.Routes) {
		orchestrator.routeIndex = 0
	}
	route := plan.Routes[orchestrator.routeIndex]
	orchestrator.mu.Unlock()
	return route, nil
}

func (orchestrator *Orchestrator) recordProxyFailure(stage Phase) {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	defer orchestrator.persistProxyRouteStateLocked()
	if !orchestrator.proxyHadSuccess && orchestrator.routePlan.Manual && stage == PhaseSIMReady {
		return
	}
	if len(orchestrator.routePlan.Routes) == 0 {
		return
	}
	switch stage {
	case PhaseSIMReady, PhaseAccessReady:
		return
	case PhaseTunnelReady, PhaseIMSReady, PhaseSMSReady, PhaseFailed:
	default:
		return
	}
	orchestrator.routeFailures++
	if orchestrator.routeFailures < 3 {
		return
	}
	orchestrator.routeFailures = 0
	orchestrator.routeIndex = (orchestrator.routeIndex + 1) % len(orchestrator.routePlan.Routes)
	orchestrator.stickyProxyID = ""
}

func (orchestrator *Orchestrator) markProxySuccess() {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	defer orchestrator.persistProxyRouteStateLocked()
	if len(orchestrator.routePlan.Routes) == 0 || orchestrator.routeIndex < 0 || orchestrator.routeIndex >= len(orchestrator.routePlan.Routes) {
		return
	}
	orchestrator.proxyHadSuccess = true
	orchestrator.proxyLastSuccessAt = time.Now().UTC()
	orchestrator.routeFailures = 0
	orchestrator.stickyProxyID = orchestrator.routePlan.Routes[orchestrator.routeIndex].ID
}

func (orchestrator *Orchestrator) persistProxyRouteStateLocked() {
	if orchestrator.deps.RouteStates == nil || !orchestrator.routePlan.Manual || orchestrator.routePlan.BindingGroupID == "" {
		return
	}
	currentRouteID := ""
	if orchestrator.routeIndex >= 0 && orchestrator.routeIndex < len(orchestrator.routePlan.Routes) {
		currentRouteID = orchestrator.routePlan.Routes[orchestrator.routeIndex].ID
	}
	_ = orchestrator.deps.RouteStates.SaveProxyRouteState(context.Background(), orchestrator.routePlan.BindingGroupID, ProxyRouteStateSnapshot{
		CurrentRouteID: currentRouteID,
		StickyRouteID:  orchestrator.stickyProxyID,
		RouteFailures:  orchestrator.routeFailures,
		LastSuccessAt:  orchestrator.proxyLastSuccessAt,
	})
}

func (orchestrator *Orchestrator) proxyRouteSnapshot() (string, string, uint) {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	current := ""
	if len(orchestrator.routePlan.Routes) > 0 && orchestrator.routeIndex >= 0 && orchestrator.routeIndex < len(orchestrator.routePlan.Routes) {
		current = orchestrator.routePlan.Routes[orchestrator.routeIndex].ID
	}
	return current, orchestrator.stickyProxyID, orchestrator.routeFailures
}

var _ ProxyResolver = (*routeSelectorResolver)(nil)

func proxyRoutePlanSignature(plan ProxyRoutePlan) string {
	parts := []string{fmt.Sprintf("manual=%t;group=%s", plan.Manual, plan.BindingGroupID)}
	for _, route := range plan.Routes {
		parts = append(parts, fmt.Sprintf("%s;%s;%s", route.ID, route.Mode, route.Address))
	}
	return strings.Join(parts, "\x00")
}

func normalizeProxyPlan(plan ProxyRoutePlan) (ProxyRoutePlan, error) {
	if len(plan.Routes) == 0 {
		return ProxyRoutePlan{}, fmt.Errorf("proxy route plan is empty")
	}
	for _, route := range plan.Routes {
		if route.Mode == "" {
			return ProxyRoutePlan{}, errors.New("proxy route mode is empty")
		}
	}
	return plan, nil
}
