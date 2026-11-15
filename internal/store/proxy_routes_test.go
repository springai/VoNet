package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeviceProxyBindingGroupsPreserveOrderedRoutesAfterDeviceDelete(t *testing.T) {
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
	saved, err = database.DeviceProxyBindingGroupByICCID(ctx, group.ICCID)
	if err != nil {
		t.Fatalf("binding group after device delete error = %v", err)
	}
	if saved.DeviceID != "" || len(saved.Routes) != 2 {
		t.Fatalf("offline binding group after device delete = %+v", saved)
	}
}

func TestDeviceProxyBindingGroupsAreUniqueByICCIDAndAllowOfflineDevice(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, id := range []string{"reader-1", "reader-2"} {
		if err := database.UpsertDevice(ctx, Device{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpsertUpstreamProxy(ctx, UpstreamProxy{
		ID: "proxy-a", Name: "proxy-a", Addr: "127.0.0.1:1080", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	const iccid = "8944100000000000002"
	for _, deviceID := range []string{"reader-1", "reader-2", ""} {
		if err := database.SaveDeviceProxyBindingGroup(ctx, DeviceProxyBindingGroup{
			DeviceID: deviceID, ICCID: iccid, ProfileName: "Offline profile",
		}, []string{"proxy-a"}); err != nil {
			t.Fatalf("SaveDeviceProxyBindingGroup(device=%q): %v", deviceID, err)
		}
	}
	groups, err := database.ListDeviceProxyBindingGroups(ctx, "")
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	if groups[0].ICCID != iccid || groups[0].DeviceID != "" || len(groups[0].Routes) != 1 {
		t.Fatalf("offline group = %+v", groups[0])
	}
	if _, err := database.DeviceProxyBindingGroup(ctx, "wrong-device", iccid); err != nil {
		t.Fatalf("query by ICCID ignored compatible device argument: %v", err)
	}
	if err := database.DeleteDeviceProxyBindingGroup(ctx, "wrong-device", iccid); err != nil {
		t.Fatalf("delete by ICCID ignored compatible device argument: %v", err)
	}
	if _, err := database.DeviceProxyBindingGroupByICCID(ctx, iccid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("group after ICCID delete error = %v, want ErrNotFound", err)
	}
}

func TestDeviceProxyBindingsAllowOfflineAndSurviveDeviceDelete(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	if err := database.UpsertDevice(ctx, Device{ID: "reader-1", Name: "Reader"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"proxy-a", "proxy-b"} {
		if err := database.UpsertUpstreamProxy(ctx, UpstreamProxy{
			ID: id, Name: id, Addr: "127.0.0.1:1080", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	const iccid = "8944100000000000004"
	if err := database.UpsertDeviceProxyBinding(ctx, DeviceProxyBinding{
		DeviceID: "reader-1", ICCID: iccid, ProfileName: "Profile", UpstreamProxyID: "proxy-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteDevice(ctx, "reader-1"); err != nil {
		t.Fatal(err)
	}
	binding, err := database.DeviceProxyBinding(ctx, iccid)
	if err != nil || binding.DeviceID != "" || binding.UpstreamProxyID != "proxy-a" {
		t.Fatalf("offline binding after device delete = %+v, %v", binding, err)
	}
	if err := database.UpsertDeviceProxyBinding(ctx, DeviceProxyBinding{
		ICCID: iccid, ProfileName: "Offline profile", UpstreamProxyID: "proxy-b",
	}); err != nil {
		t.Fatalf("offline prebind: %v", err)
	}
	binding, err = database.DeviceProxyBinding(ctx, iccid)
	if err != nil || binding.DeviceID != "" || binding.UpstreamProxyID != "proxy-b" {
		t.Fatalf("offline prebind = %+v, %v", binding, err)
	}
	bindings, err := database.ListDeviceProxyBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings = %+v, %v", bindings, err)
	}
}

func TestEnsureProxyRouteSchemaRebuildsDeviceOwnedBindingGroups(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	for _, id := range []string{"reader-1", "reader-2"} {
		if err := database.UpsertDevice(ctx, Device{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"proxy-a", "proxy-b"} {
		if err := database.UpsertUpstreamProxy(ctx, UpstreamProxy{
			ID: id, Name: id, Addr: "127.0.0.1:1080", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`DROP TABLE device_proxy_route_state`,
		`DROP TABLE device_proxy_binding_routes`,
		`DROP TABLE device_proxy_binding_groups`,
		`CREATE TABLE device_proxy_binding_groups (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			iccid TEXT NOT NULL,
			profile_name TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(device_id, iccid),
			FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE device_proxy_binding_routes (
			id TEXT PRIMARY KEY,
			binding_group_id TEXT NOT NULL,
			upstream_proxy_id TEXT NOT NULL,
			priority INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (binding_group_id) REFERENCES device_proxy_binding_groups(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE device_proxy_route_state (
			binding_group_id TEXT PRIMARY KEY,
			current_route_id TEXT,
			sticky_route_id TEXT,
			route_failures INTEGER NOT NULL DEFAULT 0,
			last_success_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (binding_group_id) REFERENCES device_proxy_binding_groups(id) ON DELETE CASCADE
		)`,
		`INSERT INTO device_proxy_binding_groups VALUES
			('binding:reader-1:8944100000000000003', 'reader-1', '8944100000000000003', 'Winner', 100, 200),
			('binding:reader-2:8944100000000000003', 'reader-2', '8944100000000000003', 'Stale', 100, 100)`,
		`INSERT INTO device_proxy_binding_routes VALUES
			('route:winner', 'binding:reader-1:8944100000000000003', 'proxy-a', 0, 100, 200),
			('route:stale', 'binding:reader-2:8944100000000000003', 'proxy-b', 0, 100, 100)`,
		`INSERT INTO device_proxy_route_state VALUES
			('binding:reader-1:8944100000000000003', 'route:winner', 'route:winner', 0, 200, 200)`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	if err := ensureProxyRouteSchema(ctx, database.db); err != nil {
		t.Fatal(err)
	}
	groups, err := database.ListDeviceProxyBindingGroups(ctx, "")
	if err != nil || len(groups) != 1 {
		t.Fatalf("rebuilt groups = %+v, %v", groups, err)
	}
	if groups[0].DeviceID != "reader-1" || groups[0].ProfileName != "Winner" ||
		len(groups[0].Routes) != 1 || groups[0].Routes[0].ID != "route:winner" {
		t.Fatalf("rebuilt winner = %+v", groups[0])
	}
	if needsRebuild, err := proxyBindingGroupSchemaNeedsRebuild(ctx, database.db); err != nil || needsRebuild {
		t.Fatalf("rebuilt schema still marked legacy: %v, %v", needsRebuild, err)
	}
}
