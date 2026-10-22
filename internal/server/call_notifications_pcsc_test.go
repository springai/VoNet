package server

import (
	"context"
	"testing"

	"vonet/internal/device"
	"vonet/internal/modem"
	"vonet/internal/pcsc"
	"vonet/internal/store"
)

func TestPollCellularCallsSkipsPCSCReaderForAllNetworkStates(t *testing.T) {
	for _, networkEnabled := range []bool{false, true} {
		name := "network_disabled"
		if networkEnabled {
			name = "network_enabled"
		}
		t.Run(name, func(t *testing.T) {
			server := regionServer(t, "310260123456789")
			var atCommands []string
			server.devices = fakeDeviceController{
				entry: device.Device{
					ID:         "dev1",
					Discovered: true,
					Candidate: modem.Candidate{
						HardwareKind: pcsc.HardwareKind,
						SerialNumber: "TEST00000001",
					},
					Snapshot: &device.Snapshot{DeviceID: "dev1", IMSI: "310260123456789"},
				},
				atHandler: func(command string) (modem.Response, error) {
					atCommands = append(atCommands, command)
					return modem.Response{Final: "OK"}, nil
				},
			}
			config := store.Device{
				ID:             "dev1",
				Name:           "Reader",
				DeviceType:     store.DeviceTypeUSBSIMReader,
				ControlDevice:  "TEST00000001",
				NetworkEnabled: networkEnabled,
			}
			if err := server.store.UpsertDevice(context.Background(), config); err != nil {
				t.Fatal(err)
			}

			server.pollCellularCalls(context.Background())

			if len(atCommands) != 0 {
				t.Fatalf("PCSC reader executed AT commands: %#v", atCommands)
			}
		})
	}
}

func TestPollCellularCallsStillPollsCellularWhenNetworkDisabled(t *testing.T) {
	server := regionServer(t, "310260123456789")
	var atCommands []string
	server.devices = fakeDeviceController{
		entry: device.Device{
			ID:         "dev1",
			Discovered: true,
			Candidate: modem.Candidate{
				HardwareKind: "modem",
				SerialNumber: "ec20-serial",
			},
			Snapshot: &device.Snapshot{DeviceID: "dev1", IMSI: "310260123456789"},
		},
		atHandler: func(command string) (modem.Response, error) {
			atCommands = append(atCommands, command)
			return modem.Response{Final: "OK"}, nil
		},
	}
	config := store.Device{
		ID:             "dev1",
		Name:           "EC20",
		DeviceType:     store.DeviceTypePCIeEC20EC25,
		ControlDevice:  "ec20-serial",
		NetworkEnabled: false,
	}
	if err := server.store.UpsertDevice(context.Background(), config); err != nil {
		t.Fatal(err)
	}

	server.pollCellularCalls(context.Background())

	if len(atCommands) != 1 || atCommands[0] != "AT+CLCC" {
		t.Fatalf("cellular commands = %#v, want one AT+CLCC", atCommands)
	}
}
