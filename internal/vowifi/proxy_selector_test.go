package vowifi

import (
	"context"
	"testing"
)

type selectorTestResolver struct {
	plan ProxyRoutePlan
}

func (resolver selectorTestResolver) Resolve(context.Context, ProxyRequest) (ProxyRoute, error) {
	return resolver.plan.Routes[0], nil
}

func (resolver selectorTestResolver) ResolveRoutes(context.Context, ProxyRequest) (ProxyRoutePlan, error) {
	return resolver.plan, nil
}

func TestProxySelectorStartsAtFirstRouteAfterBindingPlanChanges(t *testing.T) {
	orchestrator := &Orchestrator{}
	request := ProxyRequest{DeviceID: "reader-1", ICCID: "8944100000000000001"}
	initial := selectorTestResolver{plan: ProxyRoutePlan{
		Manual: true,
		Routes: []ProxyRoute{
			{Mode: ProxyModeSOCKS5, ID: "proxy-a"},
			{Mode: ProxyModeSOCKS5, ID: "proxy-b"},
		},
	}}

	route, err := orchestrator.selectProxyRoute(context.Background(), request, initial)
	if err != nil || route.ID != "proxy-a" {
		t.Fatalf("initial route = %#v, %v", route, err)
	}
	orchestrator.markProxySuccess()

	reordered := selectorTestResolver{plan: ProxyRoutePlan{
		Manual: true,
		Routes: []ProxyRoute{
			{Mode: ProxyModeSOCKS5, ID: "proxy-b"},
			{Mode: ProxyModeSOCKS5, ID: "proxy-a"},
		},
	}}
	route, err = orchestrator.selectProxyRoute(context.Background(), request, reordered)
	if err != nil || route.ID != "proxy-b" {
		t.Fatalf("route after reorder = %#v, %v, want first reordered route", route, err)
	}
}

func TestProxySelectorAdvancesAfterThreeFailuresAndKeepsStickyRoute(t *testing.T) {
	orchestrator := &Orchestrator{}
	resolver := selectorTestResolver{plan: ProxyRoutePlan{
		Manual: true,
		Routes: []ProxyRoute{
			{Mode: ProxyModeSOCKS5, ID: "proxy-a"},
			{Mode: ProxyModeSOCKS5, ID: "proxy-b"},
			{Mode: ProxyModeSOCKS5, ID: "proxy-c"},
		},
	}}
	request := ProxyRequest{DeviceID: "reader-1", ICCID: "8944100000000000001"}

	route, err := orchestrator.selectProxyRoute(context.Background(), request, resolver)
	if err != nil || route.ID != "proxy-a" {
		t.Fatalf("first route = %#v, %v", route, err)
	}
	for i := 0; i < 2; i++ {
		orchestrator.recordProxyFailure(PhaseTunnelReady)
		route, err = orchestrator.selectProxyRoute(context.Background(), request, resolver)
		if err != nil || route.ID != "proxy-a" {
			t.Fatalf("retry %d route = %#v, %v", i+2, route, err)
		}
	}
	orchestrator.recordProxyFailure(PhaseTunnelReady)
	route, err = orchestrator.selectProxyRoute(context.Background(), request, resolver)
	if err != nil || route.ID != "proxy-b" {
		t.Fatalf("route after three failures = %#v, %v", route, err)
	}

	orchestrator.markProxySuccess()
	for i := 0; i < 2; i++ {
		orchestrator.recordProxyFailure(PhaseFailed)
		route, err = orchestrator.selectProxyRoute(context.Background(), request, resolver)
		if err != nil || route.ID != "proxy-b" {
			t.Fatalf("sticky retry %d route = %#v, %v", i+1, route, err)
		}
	}
	orchestrator.recordProxyFailure(PhaseFailed)
	route, err = orchestrator.selectProxyRoute(context.Background(), request, resolver)
	if err != nil || route.ID != "proxy-c" {
		t.Fatalf("route after sticky failures = %#v, %v", route, err)
	}
}
