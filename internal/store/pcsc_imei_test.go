package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"vonet/internal/pcscimei"
)

func pcscTestDevice(id, serial string) Device {
	return Device{
		ID:             id,
		Name:           id,
		DeviceType:     DeviceTypeUSBSIMReader,
		ControlDevice:  serial,
		USBPath:        "usb-" + serial,
		DeviceBackend:  "pcsc",
		ESIMTransport:  "pcsc",
		SMSEnabled:     true,
		VoWiFiEnabled:  true,
		NetworkEnabled: false,
	}
}

func TestUpsertDeviceAllocatesAndPreservesPCSCIMEILease(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	reader := pcscTestDevice("Reader-upsert", "SER-upsert")
	if err := database.UpsertDevice(ctx, reader); err != nil {
		t.Fatal(err)
	}
	lease, err := database.PCSCIMEILease(ctx, reader.ID)
	if err != nil {
		t.Fatalf("missing automatic lease: %v", err)
	}
	if lease.Mode != "random" || lease.IMEI == "" {
		t.Fatalf("automatic lease = %+v", lease)
	}

	reader.Name = "Updated reader"
	if err := database.UpsertDevice(ctx, reader); err != nil {
		t.Fatal(err)
	}
	next, err := database.PCSCIMEILease(ctx, reader.ID)
	if err != nil || next != lease {
		t.Fatalf("lease after update = %+v, %v, want preserved %+v", next, err, lease)
	}
}

func TestEnsurePCSCIMEILeaseRepairsMissingLease(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	reader := pcscTestDevice("Reader-repair", "SER-repair")
	if err := database.UpsertDevice(ctx, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM pcsc_imei_leases WHERE device_id = ?`, reader.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := database.EnsurePCSCIMEILease(ctx, reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Mode != "random" || lease.IMEI == "" {
		t.Fatalf("repaired lease = %+v", lease)
	}
}

func TestMigration28And29RepairPCSCDataAndExpandPool(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pcsc-repair.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 27; version++ {
		for _, statement := range migrationStatements(version) {
			if _, err := raw.ExecContext(ctx, statement); err != nil {
				t.Fatalf("create v%d schema: %v", version, err)
			}
		}
	}
	_, err = raw.ExecContext(ctx, `
		INSERT INTO devices (
			id, name, device_type, control_device, usb_path, modem_imei, extra_json, created_at, updated_at
		) VALUES
			('Reader-A', 'Reader A', 'usb_sim_reader', 'SER-A', '1-1', '111111111111111', '{"legacy":true}', 100, 100),
			('Reader-B', 'Reader B', 'usb_sim_reader', 'SER-B', '1-2', '', '{}', 100, 100),
			('Reader-fixed', 'Reader fixed', 'usb_sim_reader', 'SER-fixed', '1-3', '', '{}', 100, 100),
			('modem-1', 'Modem', 'pcie_ec20_ec25', '/dev/ttyUSB2', '2-2', '860000000000013', '{}', 100, 100);
		INSERT INTO pcsc_imei_leases (device_id, imei, mode, updated_at)
		VALUES ('Reader-fixed', '860000000000012', 'fixed', 100);
		PRAGMA user_version = 27;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database := openTestStore(t, path)
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	poolByIMEI := make(map[string]struct{}, len(pool))
	for _, entry := range pool {
		poolByIMEI[entry.IMEI] = struct{}{}
	}
	for _, imei := range pcscimei.DefaultPool {
		if _, ok := poolByIMEI[imei]; !ok {
			t.Fatalf("migrated IMEI pool is missing default value %s", imei)
		}
	}
	seen := map[string]string{}
	for _, id := range []string{"Reader-A", "Reader-B", "Reader-fixed"} {
		lease, err := database.PCSCIMEILease(ctx, id)
		if err != nil {
			t.Fatalf("lease %s: %v", id, err)
		}
		if other, duplicate := seen[lease.IMEI]; duplicate {
			t.Fatalf("leases %s and %s share IMEI %s", other, id, lease.IMEI)
		}
		seen[lease.IMEI] = id
	}
	if seen["860000000000012"] != "Reader-fixed" || seen["860000000000013"] != "" {
		t.Fatalf("migration lease allocation = %#v", seen)
	}
	for _, id := range []string{"Reader-A", "Reader-B"} {
		config, err := database.Device(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if config.ModemIMEI != "" || !strings.Contains(string(config.Extra), fmt.Sprintf(`"usb_serial_number":"%s"`, config.ControlDevice)) {
			t.Fatalf("repaired config %s = %+v", id, config)
		}
	}
}

func TestPCSCIMEILeaseLifecycle(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) < 2 {
		t.Fatalf("default pool has %d values, want at least 2", len(pool))
	}

	firstIMEI := pool[0].IMEI
	if _, _, err := database.CreateDeviceWithPCSCIMEILease(
		ctx,
		pcscTestDevice("Reader-A", "SER-A"),
		PCSCIMEILeaseRequest{Mode: "fixed", IMEI: firstIMEI},
	); err != nil {
		t.Fatalf("CreateDeviceWithPCSCIMEILease() error = %v", err)
	}

	_, _, err = database.CreateDeviceWithPCSCIMEILease(
		ctx,
		pcscTestDevice("Reader-B", "SER-B"),
		PCSCIMEILeaseRequest{Mode: "fixed", IMEI: firstIMEI},
	)
	if !errors.Is(err, ErrPCSCIMEIUnavailable) {
		t.Fatalf("duplicate fixed lease error = %v, want %v", err, ErrPCSCIMEIUnavailable)
	}
	if _, err := database.Device(ctx, "Reader-B"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed duplicate create left device behind: %v", err)
	}

	config, err := database.Device(ctx, "Reader-A")
	if err != nil {
		t.Fatal(err)
	}
	_, lease, err := database.UpdateDeviceWithPCSCIMEILease(
		ctx,
		config,
		PCSCIMEILeaseRequest{Mode: "random"},
	)
	if err != nil {
		t.Fatalf("fixed-to-random update error = %v", err)
	}
	if lease.Mode != "random" || lease.IMEI != firstIMEI {
		t.Fatalf("fixed-to-random lease = %+v, want current IMEI %s retained", lease, firstIMEI)
	}

	rotated, changed, err := database.RotatePCSCIMEILease(ctx, config.ID)
	if err != nil {
		t.Fatalf("RotatePCSCIMEILease() error = %v", err)
	}
	if !changed || rotated.IMEI == firstIMEI {
		t.Fatalf("random rotation = %+v, changed = %v, want a different IMEI", rotated, changed)
	}

	fixedIMEI := pool[1].IMEI
	_, lease, err = database.UpdateDeviceWithPCSCIMEILease(
		ctx,
		config,
		PCSCIMEILeaseRequest{Mode: "fixed", IMEI: fixedIMEI},
	)
	if err != nil {
		t.Fatalf("random-to-fixed update error = %v", err)
	}
	if lease.Mode != "fixed" || lease.IMEI != fixedIMEI {
		t.Fatalf("random-to-fixed lease = %+v", lease)
	}

	if _, err := database.UpdatePCSCIMEIPool(ctx, fixedIMEI, fixedIMEI); !errors.Is(err, ErrPCSCIMEIInUse) {
		t.Fatalf("same-value leased pool update error = %v, want %v", err, ErrPCSCIMEIInUse)
	}
	if err := database.DeletePCSCIMEIPool(ctx, fixedIMEI); !errors.Is(err, ErrPCSCIMEIInUse) {
		t.Fatalf("leased pool delete error = %v, want %v", err, ErrPCSCIMEIInUse)
	}

	if err := database.DeleteDevice(ctx, config.ID); err != nil {
		t.Fatalf("DeleteDevice() error = %v", err)
	}
	if _, err := database.PCSCIMEILease(ctx, config.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lease should cascade on device deletion, got %v", err)
	}
}

func TestPCSCDeviceCreateRollsBackWhenLeaseCannotBeAllocated(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range pool {
		if err := database.DeletePCSCIMEIPool(ctx, entry.IMEI); err != nil {
			t.Fatal(err)
		}
	}

	_, _, err = database.CreateDeviceWithPCSCIMEILease(
		ctx,
		pcscTestDevice("Reader-empty", "SER-empty"),
		PCSCIMEILeaseRequest{Mode: "random"},
	)
	if !errors.Is(err, ErrPCSCIMEIPoolExhausted) {
		t.Fatalf("pool exhausted create error = %v, want %v", err, ErrPCSCIMEIPoolExhausted)
	}
	if _, err := database.Device(ctx, "Reader-empty"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed create left device behind: %v", err)
	}
}

func TestPCSCRandomRotationSkipsWhenPoolIsExhausted(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) < 2 {
		t.Fatal("default pool should contain at least two values")
	}
	_, _, err = database.CreateDeviceWithPCSCIMEILease(
		ctx,
		pcscTestDevice("Reader-random", "SER-random"),
		PCSCIMEILeaseRequest{Mode: "random"},
	)
	if err != nil {
		t.Fatal(err)
	}
	initialLease, err := database.PCSCIMEILease(ctx, "Reader-random")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range pool {
		if entry.IMEI == initialLease.IMEI {
			continue
		}
		if err := database.DeletePCSCIMEIPool(ctx, entry.IMEI); err != nil {
			t.Fatal(err)
		}
	}

	lease, changed, err := database.RotatePCSCIMEILease(ctx, "Reader-random")
	if err != nil {
		t.Fatalf("RotatePCSCIMEILease() error = %v", err)
	}
	if changed || lease.IMEI != initialLease.IMEI {
		t.Fatalf("rotation = %+v, changed = %v, want current IMEI retained", lease, changed)
	}
}

func TestPCSCIMEIValidationRequiresFifteenDigits(t *testing.T) {
	database := openTestStore(t, ":memory:")
	_, err := database.AddPCSCIMEIPool(context.Background(), "123")
	if !errors.Is(err, pcscimei.ErrInvalidIMEI) {
		t.Fatalf("invalid IMEI error = %v, want %v", err, pcscimei.ErrInvalidIMEI)
	}
}

func TestPCSCIMEIAllocationSkipsModemIMEIAndClearsReaderModemIMEI(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t, ":memory:")
	pool, err := database.ListPCSCIMEIPool(ctx)
	if err != nil || len(pool) < 2 {
		t.Fatalf("ListPCSCIMEIPool() = %v, %v", pool, err)
	}
	if err := database.UpsertDevice(ctx, Device{
		ID: "modem-1", Name: "Modem", DeviceType: DeviceTypePCIeEC20EC25,
		ModemIMEI: pool[0].IMEI, DeviceBackend: "at", ESIMTransport: "at",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateDeviceWithPCSCIMEILease(ctx, pcscTestDevice("Reader-modem", "SER-modem"), PCSCIMEILeaseRequest{
		Mode: "fixed", IMEI: pool[0].IMEI,
	}); !errors.Is(err, ErrPCSCIMEIUnavailable) {
		t.Fatalf("fixed allocation over modem IMEI error = %v, want %v", err, ErrPCSCIMEIUnavailable)
	}
	reader := pcscTestDevice("Reader-random-modem", "SER-random-modem")
	reader.ModemIMEI = pool[0].IMEI
	_, lease, err := database.CreateDeviceWithPCSCIMEILease(ctx, reader, PCSCIMEILeaseRequest{Mode: "random"})
	if err != nil {
		t.Fatal(err)
	}
	if lease.IMEI == pool[0].IMEI {
		t.Fatalf("random allocation reused modem IMEI %s", lease.IMEI)
	}
	stored, err := database.Device(ctx, reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ModemIMEI != "" {
		t.Fatalf("reader modem_imei = %q, want empty", stored.ModemIMEI)
	}
}
