package vowifi

import (
	"context"
	"errors"
	"testing"

	"vonet/internal/agentproto"
)

type remoteAgentProfileStub struct {
	profile  *agentproto.CardProfile
	commands []*agentproto.AgentCommand
}

func (stub *remoteAgentProfileStub) Execute(
	_ context.Context,
	_ string,
	command *agentproto.AgentCommand,
) (*agentproto.AgentEvent, error) {
	stub.commands = append(stub.commands, command)
	return &agentproto.AgentEvent{Event: &agentproto.AgentEvent_CardProfile{
		CardProfile: stub.profile,
	}}, nil
}

type remoteAgentIMEILeaseStub struct {
	imei      string
	mode      string
	rotated   string
	rotations int
}

func (stub *remoteAgentIMEILeaseStub) CurrentPCSCIMEI(context.Context, string) (string, string, error) {
	if stub.imei == "" {
		return "", "", errors.New("missing lease")
	}
	return stub.imei, stub.mode, nil
}

func (stub *remoteAgentIMEILeaseStub) RotatePCSCIMEI(context.Context, string) (string, bool, error) {
	stub.rotations++
	return stub.rotated, true, nil
}

func TestRemoteSIMIdentityMatchesPCSCAdapterRouting(t *testing.T) {
	executor := &remoteAgentProfileStub{profile: &agentproto.CardProfile{
		Iccid: "8944305293606904114", Imsi: "234336570994346", MncLength: 2,
		Smsc: "+447700000999", Spn: "CTExcel", UsimAid: []byte{0xa0, 0x00},
	}}
	leases := &remoteAgentIMEILeaseStub{imei: "866069053452247", mode: "random", rotated: "866069050999893"}
	adapter, err := NewRemoteSIMAdapter(executor, func(context.Context, string) (RemoteDeviceBinding, error) {
		return RemoteDeviceBinding{AgentID: "agent-1", DeviceID: "Reader-1", ReaderID: "Reader-1"}, nil
	}, leases)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := adapter.ReadIdentity(context.Background(), "configured-reader")
	if err != nil {
		t.Fatal(err)
	}
	if identity.IMEI != "866069050999893" || identity.HomeMCC != "234" || identity.HomeMNC != "33" {
		t.Fatalf("identity=%+v", identity)
	}
	if leases.rotations != 1 {
		t.Fatalf("rotations=%d, want 1", leases.rotations)
	}
}

func TestRemoteSIMIdentityWithoutRotationPreservesLease(t *testing.T) {
	executor := &remoteAgentProfileStub{profile: &agentproto.CardProfile{
		Iccid: "8944305293606904114", Imsi: "234336570994346", MncLength: 2,
	}}
	leases := &remoteAgentIMEILeaseStub{imei: "866069053452247", mode: "random", rotated: "866069050999893"}
	adapter, err := NewRemoteSIMAdapter(executor, func(context.Context, string) (RemoteDeviceBinding, error) {
		return RemoteDeviceBinding{AgentID: "agent-1", DeviceID: "Reader-1"}, nil
	}, leases)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := adapter.ReadIdentityWithoutRotation(context.Background(), "configured-reader")
	if err != nil {
		t.Fatal(err)
	}
	if identity.IMEI != "866069053452247" || leases.rotations != 0 {
		t.Fatalf("identity=%+v rotations=%d", identity, leases.rotations)
	}
}
