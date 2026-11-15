package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vonet/internal/device"
	"vonet/internal/store"
)

type disconnectingESIMController struct {
	fakeDeviceController
	disconnect      context.CancelFunc
	flightErrors    []error
	flightDeadlines []bool
	activeICCID     string
	activeAID       string
}

func (c *disconnectingESIMController) ESIMSwitchProfile(_ context.Context, _, iccid, aid string) error {
	c.activeICCID = iccid
	c.activeAID = aid
	c.disconnect()
	return nil
}

func (c *disconnectingESIMController) Refresh(context.Context, string) (device.Snapshot, error) {
	return device.Snapshot{
		DeviceID: "dev1", Responsive: true, SIMReady: true, ICCID: c.activeICCID,
	}, nil
}

func (c *disconnectingESIMController) ESIMListProfiles(context.Context, string) (device.EsimInfo, error) {
	return device.EsimInfo{Profiles: []device.EsimProfile{{
		ICCID: c.activeICCID, AID: c.activeAID, State: 1,
	}}}, nil
}

func (c *disconnectingESIMController) ESIMInventory(context.Context, string) ([]device.EsimInventoryEntry, error) {
	return []device.EsimInventoryEntry{{Info: device.EsimInfo{Profiles: []device.EsimProfile{{
		ICCID: c.activeICCID, AID: c.activeAID, State: 1,
	}}}}}, nil
}

func (c *disconnectingESIMController) SetFlight(ctx context.Context, _ string, _ bool) (device.FlightResult, error) {
	_, bounded := ctx.Deadline()
	c.flightErrors = append(c.flightErrors, ctx.Err())
	c.flightDeadlines = append(c.flightDeadlines, bounded)
	return device.FlightResult{}, ctx.Err()
}

func TestESIMSwitchFinalizesAfterClientDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name       string
		oldWiFi    bool
		targetWiFi bool
	}{
		{name: "restore cellular policy", oldWiFi: true},
		{name: "restore WiFi policy", targetWiFi: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(context.Background(), ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.UpsertDevice(context.Background(), store.Device{
				ID: "dev1", Name: "dev1", VoWiFiEnabled: tc.oldWiFi, APN: "old.apn",
			}); err != nil {
				t.Fatal(err)
			}
			const targetICCID = "8900000000000000001"
			if err := db.UpsertSimCard(context.Background(), store.SimCard{ICCID: targetICCID}); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertCardPolicy(context.Background(), store.CardPolicy{
				ICCID: targetICCID, VoWiFiEnabled: tc.targetWiFi, AirplaneEnabled: tc.targetWiFi,
				APN: "target.apn", IPVersion: "IP", Source: "manual",
			}); err != nil {
				t.Fatal(err)
			}
			ctx, disconnect := context.WithCancel(context.Background())
			defer disconnect()
			controller := &disconnectingESIMController{disconnect: disconnect}
			wifi := &fakeEsimVoWiFiController{enabled: tc.oldWiFi}
			server := &Server{store: db, devices: controller, vowifi: wifi,
				logger: regionTestLogger(), maxRequestBodyBytes: 4096}
			request := httptest.NewRequest(http.MethodPost, "/esim/actions/switch",
				strings.NewReader(`{"iccid":"`+targetICCID+`"}`)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			server.handleEsimSwitch(recorder, request, "dev1", "dev1", true)
			if ctx.Err() == nil {
				t.Fatal("client disconnect was not simulated")
			}
			if len(controller.flightErrors) < 2 {
				t.Fatal("post-switch flight mode was not applied")
			}
			for index, err := range controller.flightErrors {
				if err != nil {
					t.Errorf("flight call %d used canceled request: %v", index, err)
				}
				if index > 0 && !controller.flightDeadlines[index] {
					t.Errorf("flight call %d has no finalization deadline", index)
				}
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("finalization failed: status=%d body=%s", recorder.Code, recorder.Body)
			}
			config, err := db.Device(context.Background(), "dev1")
			if err != nil {
				t.Fatal(err)
			}
			if config.APN != "target.apn" || config.VoWiFiEnabled != tc.targetWiFi || config.NetworkEnabled || wifi.enabled != tc.targetWiFi {
				t.Fatalf("target policy not restored: config=%+v wifi=%v", config, wifi.enabled)
			}
			if server.cellularDataRuntime().status("dev1", false).Phase != "disabled" {
				t.Fatal("data runtime remains in failed or switching phase")
			}
		})
	}
}
