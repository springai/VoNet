package integration

import (
	"context"
	"testing"

	"vonet/internal/store"
	"vonet/internal/vowifi"
)

func TestResolveRoutesPrefersOrderedManualBindingAndIgnoresMCC(t *testing.T) {
	database := testStore(t)
	ctx := context.Background()
	if err := database.UpsertDevice(ctx, store.Device{ID: "reader-1", Name: "Reader"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"manual-a", "manual-b", "country"} {
		if err := database.UpsertUpstreamProxy(ctx, store.UpstreamProxy{ID: id, Name: id, Addr: "127.0.0.1:1080", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpsertCountryRule(ctx, store.CountryRule{CountryCode: "GB", UpstreamProxyID: "country", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	group := store.DeviceProxyBindingGroup{DeviceID: "reader-1", ICCID: "8944100000000000001", ProfileName: "Profile"}
	if err := database.SaveDeviceProxyBindingGroup(ctx, group, []string{"manual-a", "manual-b"}); err != nil {
		t.Fatal(err)
	}

	plan, err := (ProxyResolver{Store: database}).ResolveRoutes(ctx, vowifi.ProxyRequest{
		DeviceID: group.DeviceID,
		ICCID:    group.ICCID,
		HomeMCC:  "234",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Manual || len(plan.Routes) != 2 || plan.Routes[0].ID != "manual-a" || plan.Routes[1].ID != "manual-b" {
		t.Fatalf("manual route plan = %#v", plan)
	}
}

func TestResolveRoutesUsesMCCOnlyWithoutManualBinding(t *testing.T) {
	database := testStore(t)
	ctx := context.Background()
	if err := database.UpsertUpstreamProxy(ctx, store.UpstreamProxy{ID: "country", Name: "Country", Addr: "127.0.0.1:1080", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertCountryRule(ctx, store.CountryRule{CountryCode: "GB", UpstreamProxyID: "country", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	plan, err := (ProxyResolver{Store: database}).ResolveRoutes(ctx, vowifi.ProxyRequest{
		DeviceID: "reader-1",
		ICCID:    "8944100000000000001",
		HomeMCC:  "234",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Manual || len(plan.Routes) != 1 || plan.Routes[0].ID != "country" {
		t.Fatalf("MCC route plan = %#v", plan)
	}
}

func TestResolveRoutesManualDisabledDoesNotFallBackToMCC(t *testing.T) {
	database := testStore(t)
	ctx := context.Background()
	if err := database.UpsertDevice(ctx, store.Device{ID: "reader-1", Name: "Reader"}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []store.UpstreamProxy{
		{ID: "manual", Name: "Manual", Addr: "127.0.0.1:1080", Enabled: false},
		{ID: "country", Name: "Country", Addr: "127.0.0.1:1081", Enabled: true},
	} {
		if err := database.UpsertUpstreamProxy(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpsertCountryRule(ctx, store.CountryRule{CountryCode: "GB", UpstreamProxyID: "country", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	group := store.DeviceProxyBindingGroup{DeviceID: "reader-1", ICCID: "8944100000000000001", ProfileName: "Profile"}
	if err := database.SaveDeviceProxyBindingGroup(ctx, group, []string{"manual"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (ProxyResolver{Store: database}).ResolveRoutes(ctx, vowifi.ProxyRequest{
		DeviceID: group.DeviceID,
		ICCID:    group.ICCID,
		HomeMCC:  "234",
	}); err == nil {
		t.Fatal("disabled manual route unexpectedly fell back to MCC")
	}
}
