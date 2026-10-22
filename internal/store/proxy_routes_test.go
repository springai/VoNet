package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeviceProxyBindingGroupsPreserveOrderedRoutesAndCascade(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := database.UpsertDevice(ctx, Device{ID: "reader-1", Name: "Reader"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"proxy-a", "proxy-b", "proxy-c"} {
		if err := database.UpsertUpstreamProxy(ctx, UpstreamProxy{ID: id, Name: id, Addr: "127.0.0.1:1080", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	group := DeviceProxyBindingGroup{
		DeviceID:    "reader-1",
		ICCID:       "8944100000000000001",
		ProfileName: "Profile",
	}
	if err := database.SaveDeviceProxyBindingGroup(ctx, group, []string{"proxy-a", "proxy-b", "proxy-c"}); err != nil {
		t.Fatal(err)
	}
	saved, err := database.DeviceProxyBindingGroup(ctx, group.DeviceID, group.ICCID)
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"proxy-a", "proxy-b", "proxy-c"} {
		if saved.Routes[index].UpstreamProxyID != id || saved.Routes[index].Priority != index {
			t.Fatalf("route %d = %+v, want %s priority %d", index, saved.Routes[index], id, index)
		}
	}

	if err := database.SaveDeviceProxyBindingGroup(ctx, group, []string{"proxy-c", "proxy-a"}); err != nil {
		t.Fatal(err)
	}
	saved, err = database.DeviceProxyBindingGroup(ctx, group.DeviceID, group.ICCID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Routes) != 2 || saved.Routes[0].UpstreamProxyID != "proxy-c" || saved.Routes[1].UpstreamProxyID != "proxy-a" {
		t.Fatalf("reordered routes = %+v", saved.Routes)
	}

	if err := database.DeleteDevice(ctx, group.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DeviceProxyBindingGroup(ctx, group.DeviceID, group.ICCID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding group after device delete error = %v, want ErrNotFound", err)
	}
}
