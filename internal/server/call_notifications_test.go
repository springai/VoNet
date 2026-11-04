package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/store"
)

type pollCLCCCapabilityController struct {
	fakeDeviceController
	entries          []device.Device
	supported        map[string]bool
	capabilityChecks map[string]int
	commands         map[string][]string
}

func (controller *pollCLCCCapabilityController) List() []device.Device {
	return append([]device.Device(nil), controller.entries...)
}

func (controller *pollCLCCCapabilityController) Get(id string) (device.Device, error) {
	for _, entry := range controller.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return device.Device{}, device.ErrNotFound
}

func (controller *pollCLCCCapabilityController) SupportsCLCC(_ context.Context, physicalID string) (bool, error) {
	controller.capabilityChecks[physicalID]++
	return controller.supported[physicalID], nil
}

func (controller *pollCLCCCapabilityController) ExecuteAT(_ context.Context, physicalID, command string) (modem.Response, error) {
	controller.commands[physicalID] = append(controller.commands[physicalID], command)
	return modem.Response{Command: command, Final: "OK"}, nil
}

func TestPollCellularCallsSkipsUnsupportedCLCCAndPollsOtherDevices(t *testing.T) {
	server := regionServer(t, "310260123456789")
	controller := &pollCLCCCapabilityController{
		entries: []device.Device{
			{ID: "unsupported", Discovered: true},
			{ID: "supported", Discovered: true},
		},
		supported: map[string]bool{
			"unsupported": false,
			"supported":   true,
		},
		capabilityChecks: make(map[string]int),
		commands:         make(map[string][]string),
	}
	server.devices = controller
	for _, config := range []store.Device{
		{
			ID:             "unsupported",
			Name:           "A Unsupported",
			DeviceType:     store.DeviceTypePCIeEC20EC25,
			NetworkEnabled: true,
		},
		{
			ID:             "supported",
			Name:           "B Supported",
			DeviceType:     store.DeviceTypePCIeEC20EC25,
			NetworkEnabled: true,
		},
	} {
		if err := server.store.UpsertDevice(context.Background(), config); err != nil {
			t.Fatal(err)
		}
	}

	server.pollCellularCalls(context.Background())

	if got := controller.commands["unsupported"]; len(got) != 0 {
		t.Fatalf("unsupported device commands = %v, want none", got)
	}
	got := controller.commands["supported"]
	if len(got) != 1 || got[0] != "AT+CLCC" {
		t.Fatalf("supported device commands = %v, want one AT+CLCC", got)
	}
	if controller.capabilityChecks["unsupported"] != 1 ||
		controller.capabilityChecks["supported"] != 1 {
		t.Fatalf("capability checks = %#v, want one check per device", controller.capabilityChecks)
	}
}

func TestIncomingCallNotificationTextFormatting(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 30, 0, 0, time.UTC)
	notification := IncomingCallNotification{
		DeviceID:    "ec20-1",
		DeviceName:  "Main Router",
		DeviceLabel: "Main Router",
		Caller:      "+8613800138000",
		Called:      "+8613900139000",
		Time:        now,
		Environment: "vowifi",
	}

	if notification.Title() != "收到来电" {
		t.Errorf("Title() = %q, want '收到来电'", notification.Title())
	}

	text := notification.Text()
	for _, want := range []string{
		"📞 收到来电",
		"设备  Main Router",
		"来电号码  +861380138000"[:10],
		"被呼号码  +8613900139000",
		"网络  VoWiFi",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Text() omitted %q:\n%s", want, text)
		}
	}

	notification.Environment = "cellular"
	if !strings.Contains(notification.Text(), "网络  基站直连") {
		t.Errorf("Text() in cellular mode omitted '网络  基站直连':\n%s", notification.Text())
	}
}

func TestIncomingCallDeduplication(t *testing.T) {
	now := time.Now()
	key := "test-device:+8613800000000"

	// First call should not be suppressed
	if shouldSuppressDuplicateCall(key, now, time.Minute) {
		t.Fatal("first call unexpectedly suppressed")
	}

	// Immediate duplicate should be suppressed
	if !shouldSuppressDuplicateCall(key, now.Add(5*time.Second), time.Minute) {
		t.Fatal("duplicate call within window was not suppressed")
	}

	// Call after window should be allowed
	if shouldSuppressDuplicateCall(key, now.Add(70*time.Second), time.Minute) {
		t.Fatal("call after window was suppressed")
	}
}

func TestIncomingVoiceCLCCIgnoresDataSessions(t *testing.T) {
	tests := []struct {
		name string
		call map[string]any
		want bool
	}{
		{
			name: "incoming voice ringing",
			call: map[string]any{"direction": 1, "state": 4, "mode": 0},
			want: true,
		},
		{
			name: "incoming voice active",
			call: map[string]any{"direction": 1, "state": 0, "mode": 0},
			want: true,
		},
		{
			name: "incoming packet data active",
			call: map[string]any{"direction": 1, "state": 0, "mode": 1},
			want: false,
		},
		{
			name: "outgoing voice alerting",
			call: map[string]any{"direction": 0, "state": 3, "mode": 0},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isIncomingVoiceCLCC(test.call); got != test.want {
				t.Fatalf("isIncomingVoiceCLCC() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestParseCLCCDropsActiveIncomingEmptyNumberPhantom(t *testing.T) {
	calls := parseCLCC(modem.Response{
		Lines: []string{`+CLCC: 1,1,0,1,0,"",128`},
		Final: "OK",
	})
	if len(calls) != 0 {
		t.Fatalf("parseCLCC() returned %d phantom calls, want 0: %#v", len(calls), calls)
	}
}

func TestRenderCallWebhookTemplate(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 30, 0, 0, time.UTC)
	message := IncomingCallNotification{
		DeviceID:    "dev-1",
		DeviceName:  "Living Room",
		DeviceLabel: "EC20",
		Caller:      "+8613800000000",
		Called:      "+8613900000000",
		Time:        now,
		Environment: "vowifi",
	}

	got := renderCallWebhookTemplate("{{event}}|{{device_id}}|{{device_name}}|{{device_label}}|{{caller}}|{{called}}|{{environment}}", message)
	want := "call.received|dev-1|Living Room|EC20|+8613800000000|+8613900000000|vowifi"
	if got != want {
		t.Fatalf("renderCallWebhookTemplate() = %q, want %q", got, want)
	}
}

func TestWecomAndLarkCallValues(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 8, 20, 18, 0, 0, 0, location)
	message := IncomingCallNotification{
		DeviceID:    "dev-1",
		DeviceName:  "Office",
		DeviceLabel: "EC20-Office",
		Caller:      "+8613800138000",
		Called:      "+8613900139000",
		Time:        now,
		Environment: "cellular",
	}

	wecom := wecomCallValues(message)
	if wecom["event"] != "call.received" || wecom["title"] != "收到来电" || wecom["number"] != "+8613800138000" {
		t.Fatalf("wecomCallValues = %#v", wecom)
	}
	if !strings.Contains(wecom["message"], "网络  基站直连") {
		t.Fatalf("wecomCallValues message omitted network: %s", wecom["message"])
	}

	lark := larkCallValues(message)
	if lark["event"] != "call.received" || lark["title"] != "收到来电" || lark["device_label"] != "EC20-Office" {
		t.Fatalf("larkCallValues = %#v", lark)
	}
}
