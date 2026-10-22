package ike

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"vonet/internal/vowifi"
)

var errFirstAuthObserved = errors.New("test: first IKE_AUTH observed")

type constantReader struct{ value byte }

func TestLegacyProposalFallbackIsLimitedToNegotiationFailures(t *testing.T) {
	for _, err := range []error{
		errNoProposalChosen,
		&invalidKEPayloadError{},
		&invalidKEPayloadError{group: dhMODP1024},
	} {
		if !retryableLegacyProposal(err) {
			t.Errorf("negotiation failure %v was not retryable", err)
		}
	}
	for _, err := range []error{
		&invalidKEPayloadError{group: dhMODP2048},
		errors.New("ike: authentication failed"),
		vowifi.ErrEAPAuthenticationRejected,
	} {
		if retryableLegacyProposal(err) {
			t.Errorf("unsafe failure %v enabled legacy retry", err)
		}
	}
}

func TestRejectFatalNotificationsClassifiesAuthenticationFailed(t *testing.T) {
	err := rejectFatalNotifications([]payload{makeNotify(notifyAuthenticationFailed, nil)})
	if !errors.Is(err, vowifi.ErrEAPAuthenticationRejected) {
		t.Fatalf("rejectFatalNotifications() error = %v", err)
	}
	if !strings.Contains(err.Error(), "AUTHENTICATION_FAILED (Notify 24)") {
		t.Fatalf("rejectFatalNotifications() error = %q", err)
	}
}

func (reader constantReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = reader.value
	}
	return len(destination), nil
}

type firstAuthCaptureTransport struct {
	t               *testing.T
	wantEAPOnly     bool
	wantGroup       uint16
	calls           int
	suite           negotiatedSuite
	keys            ikeKeys
	spii            [8]byte
	spir            [8]byte
	nonceI          []byte
	nonceR          []byte
	floated         bool
	cookieChallenge []byte
	cookieSeen      bool
	cookieLoop      bool
}

func (transport *firstAuthCaptureTransport) LocalAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 500}
}

func (transport *firstAuthCaptureTransport) RemoteAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(198, 51, 100, 20), Port: 500}
}

func (transport *firstAuthCaptureTransport) Float(context.Context) error {
	transport.floated = true
	return nil
}

func (transport *firstAuthCaptureTransport) RoundTrip(ctx context.Context, packet []byte) ([]byte, error) {
	transport.calls++
	if transport.cookieLoop {
		return transport.answerIKECookie(packet)
	}
	if len(transport.cookieChallenge) > 0 {
		switch transport.calls {
		case 1:
			return transport.answerIKECookie(packet)
		case 2:
			if err := transport.verifyIKECookie(packet); err != nil {
				return nil, err
			}
			if transport.cookieLoop {
				return transport.answerIKECookie(packet)
			}
			return transport.answerIKEInit(packet)
		case 3:
			return nil, transport.observeFirstAuth(packet)
		default:
			return nil, errors.New("test: unexpected exchange")
		}
	}
	switch transport.calls {
	case 1:
		return transport.answerIKEInit(packet)
	case 2:
		return nil, transport.observeFirstAuth(packet)
	default:
		return nil, errors.New("test: unexpected exchange")
	}
}

func (transport *firstAuthCaptureTransport) RoundTripExchange(ctx context.Context, packets [][]byte) ([][]byte, error) {
	if len(packets) == 0 {
		return nil, errors.New("test: empty outbound packets")
	}
	resp, err := transport.RoundTrip(ctx, packets[0])
	if err != nil {
		return nil, err
	}
	return [][]byte{resp}, nil
}

func (transport *firstAuthCaptureTransport) answerIKECookie(packet []byte) ([]byte, error) {
	header, _, err := parseIKEPacket(packet)
	if err != nil {
		return nil, err
	}
	first, body, err := marshalPayloadChain([]payload{
		makeNotify(notifyCookie, transport.cookieChallenge),
	})
	if err != nil {
		return nil, err
	}
	return ikeHeader{
		InitiatorSPI: header.InitiatorSPI,
		NextPayload:  first,
		Exchange:     exchangeIKEInit,
		Flags:        flagResponse,
		MessageID:    0,
	}.marshal(body), nil
}

func (transport *firstAuthCaptureTransport) verifyIKECookie(packet []byte) error {
	header, body, err := parseIKEPacket(packet)
	if err != nil {
		return err
	}
	if header.Exchange != exchangeIKEInit || header.MessageID != 0 || header.Flags != flagInitiator {
		return errors.New("test: invalid retried IKE_SA_INIT header")
	}
	payloads, err := parsePayloadChain(header.NextPayload, body)
	if err != nil {
		return err
	}
	if len(payloads) == 0 || payloads[0].Type != payloadNotify {
		return errors.New("test: retried IKE_SA_INIT did not put COOKIE first")
	}
	firstKind, firstData, err := parseNotify(payloads[0])
	if err != nil || firstKind != notifyCookie || !bytes.Equal(firstData, transport.cookieChallenge) {
		return errors.New("test: first retried IKE_SA_INIT payload is not the expected COOKIE")
	}
	cookies := payloadsOfType(payloads, payloadNotify)
	if len(cookies) != 4 {
		return fmt.Errorf("test: retried IKE_SA_INIT has %d notify payloads, want 4", len(cookies))
	}
	found := false
	for _, item := range cookies {
		kind, data, err := parseNotify(item)
		if err != nil {
			return err
		}
		if kind == notifyCookie {
			if !bytes.Equal(data, transport.cookieChallenge) {
				return fmt.Errorf("test: cookie = %x, want %x", data, transport.cookieChallenge)
			}
			found = true
		}
	}
	if !found {
		return errors.New("test: retried IKE_SA_INIT did not carry COOKIE")
	}
	transport.cookieSeen = true
	return nil
}

func (transport *firstAuthCaptureTransport) answerIKEInit(packet []byte) ([]byte, error) {
	header, body, err := parseIKEPacket(packet)
	if err != nil {
		return nil, err
	}
	payloads, err := parsePayloadChain(header.NextPayload, body)
	if err != nil {
		return nil, err
	}
	ke, err := onePayload(payloads, payloadKE)
	if err != nil {
		return nil, err
	}
	nonce, err := onePayload(payloads, payloadNonce)
	if err != nil {
		return nil, err
	}
	group := uint16(ke.Body[0])<<8 | uint16(ke.Body[1])
	wantGroup := transport.wantGroup
	if wantGroup == 0 {
		wantGroup = dhMODP2048
	}
	wantKELength := 128
	if wantGroup == dhMODP2048 {
		wantKELength = 256
	}
	if group != wantGroup || len(ke.Body[4:]) != wantKELength {
		transport.t.Fatalf("init KE = group %d length %d, want group %d length %d", group, len(ke.Body[4:]), wantGroup, wantKELength)
	}
	serverDH, err := newDHExchange(group, constantReader{value: 0x77})
	if err != nil {
		return nil, err
	}
	shared, err := serverDH.shared(ke.Body[4:])
	if err != nil {
		return nil, err
	}
	transport.suite = legacyTestSuite()
	if group == dhMODP2048 {
		transport.suite = negotiatedSuite{
			EncryptionID:   encryptionAESCBC,
			EncryptionBits: 128,
			PRFID:          prfHMACSHA256,
			IntegrityID:    integrityHMACSHA256_128,
			DHID:           dhMODP2048,
		}
	}
	transport.spii = header.InitiatorSPI
	transport.spir = [8]byte{0x80, 1, 2, 3, 4, 5, 6, 7}
	transport.nonceI = append([]byte(nil), nonce.Body...)
	transport.nonceR = bytes.Repeat([]byte{0x88}, 32)
	transport.keys, err = deriveIKEKeys(
		transport.suite,
		shared,
		transport.nonceI,
		transport.nonceR,
		transport.spii,
		transport.spir,
	)
	if err != nil {
		return nil, err
	}
	selectedSA, _ := marshalProposals([]proposal{{
		Number:   1,
		Protocol: protocolIKE,
		Transforms: []transform{
			{Type: transformEncryption, ID: encryptionAESCBC, KeyLength: 128},
			{Type: transformPRF, ID: transport.suite.PRFID},
			{Type: transformIntegrity, ID: transport.suite.IntegrityID},
			{Type: transformDH, ID: group},
		},
	}})
	keBody := make([]byte, 4+len(serverDH.Public))
	keBody[1] = byte(group)
	copy(keBody[4:], serverDH.Public)
	first, responseBody, _ := marshalPayloadChain([]payload{
		{Type: payloadSA, Body: selectedSA},
		{Type: payloadKE, Body: keBody},
		{Type: payloadNonce, Body: transport.nonceR},
	})
	return ikeHeader{
		InitiatorSPI: transport.spii,
		ResponderSPI: transport.spir,
		NextPayload:  first,
		Exchange:     exchangeIKEInit,
		Flags:        flagResponse,
	}.marshal(responseBody), nil
}

func (transport *firstAuthCaptureTransport) observeFirstAuth(packet []byte) error {
	header, payloads, err := decryptPayloads(
		packet,
		transport.suite,
		transport.keys.SKei,
		transport.keys.SKai,
	)
	if err != nil {
		return err
	}
	if header.Exchange != exchangeIKEAuth || header.MessageID != 1 || header.Flags != flagInitiator {
		transport.t.Fatalf("first IKE_AUTH header = %#v", header)
	}
	idr, err := onePayload(payloads, payloadIDr)
	if err != nil {
		transport.t.Fatal(err)
	}
	if idr.Body[0] != 2 || string(idr.Body[4:]) != "ims" {
		transport.t.Fatalf("requested IDr = type %d value %q", idr.Body[0], idr.Body[4:])
	}
	foundEAPOnly := false
	for _, item := range payloadsOfType(payloads, payloadNotify) {
		kind, data, err := parseNotify(item)
		if err != nil {
			transport.t.Fatal(err)
		}
		if kind == notifyEAPOnlyAuth && len(data) == 0 {
			foundEAPOnly = true
		}
	}
	if foundEAPOnly != transport.wantEAPOnly {
		transport.t.Fatalf("first IKE_AUTH EAP_ONLY_AUTHENTICATION present=%v, want %v", foundEAPOnly, transport.wantEAPOnly)
	}
	for _, kind := range []uint8{payloadIDi, payloadSA, payloadTSi, payloadTSr, payloadCP} {
		if _, err := onePayload(payloads, kind); err != nil {
			transport.t.Fatalf("first IKE_AUTH payload %d: %v", kind, err)
		}
	}
	return errFirstAuthObserved
}

func (*firstAuthCaptureTransport) SendESP(context.Context, []byte) error {
	return errors.New("test: unused")
}
func (*firstAuthCaptureTransport) ReceiveESP(context.Context, []byte) (int, error) {
	return 0, errors.New("test: unused")
}
func (*firstAuthCaptureTransport) SendSessionPacket(context.Context, []byte, bool) error {
	return errors.New("test: unused")
}
func (*firstAuthCaptureTransport) ReceiveSessionPacket(context.Context, []byte) (int, bool, error) {
	return 0, false, errors.New("test: unused")
}
func (*firstAuthCaptureTransport) Close() error { return nil }

type unusedInstaller struct{}

func (unusedInstaller) Install(context.Context, ChildSAConfig) (ChildSAHandle, error) {
	return nil, errors.New("test: installer must not run")
}

func TestProviderVodafoneFirstAuthIsEAPOnlyAndRequestsIMSAPN(t *testing.T) {
	capture := &firstAuthCaptureTransport{t: t, wantEAPOnly: true}
	provider, err := NewProvider(Config{
		Random:    constantReader{value: 0x42},
		Timeout:   time.Second,
		Installer: unusedInstaller{},
		APN:       "ims",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.transportFactory = func(
		context.Context,
		transportConfig,
		vowifi.ProxyRoute,
		string,
	) (datagramTransport, error) {
		return capture, nil
	}
	aka := &testAKAProvider{}
	_, err = provider.Start(context.Background(), vowifi.TunnelRequest{
		DeviceID: "ec20-1",
		Identity: vowifi.SIMIdentity{
			ICCID:   "8944100000000000000",
			IMSI:    "234150123456789",
			HomeMCC: "234",
			HomeMNC: "15",
		},
		EPDG: "epdg.epc.mnc015.mcc234.pub.3gppnetwork.org",
		AKA:  aka,
	})
	if !errors.Is(err, errFirstAuthObserved) {
		t.Fatalf("Start() error = %v, want capture sentinel", err)
	}
	if capture.calls != 2 || capture.floated || aka.calls != 0 {
		t.Fatalf("capture calls=%d floated=%v AKA calls=%d", capture.calls, capture.floated, aka.calls)
	}
}

func TestProviderRetriesIKEInitAfterCookie(t *testing.T) {
	capture := &firstAuthCaptureTransport{
		t:               t,
		wantEAPOnly:     true,
		cookieChallenge: []byte{0x10, 0x20, 0x30, 0x40},
	}
	provider, err := NewProvider(Config{
		Random:    constantReader{value: 0x42},
		Timeout:   time.Second,
		Installer: unusedInstaller{},
		APN:       "ims",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.transportFactory = func(
		context.Context,
		transportConfig,
		vowifi.ProxyRoute,
		string,
	) (datagramTransport, error) {
		return capture, nil
	}
	aka := &testAKAProvider{}
	_, err = provider.Start(context.Background(), vowifi.TunnelRequest{
		DeviceID: "ec20-cookie",
		Identity: vowifi.SIMIdentity{
			ICCID:   "8944100000000000000",
			IMSI:    "234150123456789",
			HomeMCC: "234",
			HomeMNC: "15",
		},
		EPDG: "epdg.epc.mnc015.mcc234.pub.3gppnetwork.org",
		AKA:  aka,
	})
	if !errors.Is(err, errFirstAuthObserved) {
		t.Fatalf("Start() error = %v, want capture sentinel", err)
	}
	if capture.calls != 3 || !capture.cookieSeen || capture.floated || aka.calls != 0 {
		t.Fatalf("capture calls=%d cookie_seen=%v floated=%v AKA calls=%d", capture.calls, capture.cookieSeen, capture.floated, aka.calls)
	}
}

func TestProviderBoundsRepeatedIKEInitCookieChallenges(t *testing.T) {
	capture := &firstAuthCaptureTransport{
		t:               t,
		wantEAPOnly:     true,
		cookieChallenge: []byte{0x10, 0x20, 0x30, 0x40},
		cookieLoop:      true,
	}
	provider, err := NewProvider(Config{
		Random:    constantReader{value: 0x42},
		Timeout:   time.Second,
		Installer: unusedInstaller{},
		APN:       "ims",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.transportFactory = func(
		context.Context,
		transportConfig,
		vowifi.ProxyRoute,
		string,
	) (datagramTransport, error) {
		return capture, nil
	}
	aka := &testAKAProvider{}
	_, err = provider.Start(context.Background(), vowifi.TunnelRequest{
		DeviceID: "ec20-cookie-loop",
		Identity: vowifi.SIMIdentity{
			ICCID:   "8944100000000000000",
			IMSI:    "234150123456789",
			HomeMCC: "234",
			HomeMNC: "15",
		},
		EPDG: "epdg.epc.mnc015.mcc234.pub.3gppnetwork.org",
		AKA:  aka,
	})
	if err == nil || !strings.Contains(err.Error(), "too many COOKIE challenges") {
		t.Fatalf("Start() error = %v, want bounded COOKIE error", err)
	}
	if capture.calls != maxIKEInitCookieChallenges || aka.calls != 0 {
		t.Fatalf("capture calls=%d AKA calls=%d", capture.calls, aka.calls)
	}
}

var _ io.Reader = constantReader{}
var _ datagramTransport = (*firstAuthCaptureTransport)(nil)

func TestLogIdentityRewriteDiagnosticsRedactsSubscriberValues(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	identity := vowifi.SIMIdentity{
		ICCID:   "8963661234567890123",
		IMSI:    "2040476161234567",
		HomeMCC: "204",
		HomeMNC: "047",
		SPN:     "DITO",
	}
	eapIdentity := []byte("05156610151234567@nai.epc.mnc066.mcc515.3gppnetwork.org")
	logIdentityRewriteDiagnostics(logger, identity, eapIdentity, 3)

	logged := logs.String()
	for _, field := range []string{
		"raw_imsi_prefix=204-047-616",
		"effective_imsi_prefix=515-661-015",
		"imsi_present=true",
		"effective_imsi_length=16",
		"imsi_length=16",
		"iccid_present=true",
		"iccid_prefix=896-366",
		"identity_home_plmn=204-047",
		"profile_route_plmn=515-66",
		"home_mnc_length=3",
		"spn_present=true",
		"spn=DITO",
		"profile_id=ipcc-dito-51566",
		"profile_match_source=hplmn+imsi+iccid",
		"identity_source=carrier_profile_rewrite",
		"rewrite_applied=true",
		"rewrite_rule_present=true",
		"rewrite_from_prefix=204-047-616",
		"rewrite_to_prefix=515-661-015",
		"nai_present=true",
		"nai_realm=nai.epc.mnc066.mcc515.3gppnetwork.org",
		"nai_realm_present=true",
	} {
		if !strings.Contains(logged, field) {
			t.Errorf("diagnostic log missing %q; got %s", field, logged)
		}
	}
	for _, secret := range []string{
		identity.IMSI,
		identity.ICCID,
		string(eapIdentity),
	} {
		if strings.Contains(logged, secret) {
			t.Errorf("diagnostic log exposed full subscriber value %q; got %s", secret, logged)
		}
	}
}

func TestLogIdentityRewriteDiagnosticsReportsProvisionedIdentity(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	identity := vowifi.SIMIdentity{
		ICCID:           "8963661234567890123",
		IMSI:            "204043610123456",
		ProvisionedIMSI: "515661037123459",
		HomeMCC:         "515",
		HomeMNC:         "66",
		SPN:             "DITO",
	}
	logIdentityRewriteDiagnostics(logger, identity, []byte("0515661037123459@nai.epc.mnc066.mcc515.3gppnetwork.org"), 3)

	logged := logs.String()
	for _, field := range []string{
		"identity_source=provisioned_imsi",
		"effective_imsi_prefix=515-661-037",
		"rewrite_applied=true",
	} {
		if !strings.Contains(logged, field) {
			t.Errorf("diagnostic log missing %q; got %s", field, logged)
		}
	}
	for _, secret := range []string{identity.IMSI, identity.ProvisionedIMSI, identity.ICCID} {
		if strings.Contains(logged, secret) {
			t.Errorf("diagnostic log exposed full subscriber value %q", secret)
		}
	}
}

func TestGroupedIdentityPrefixLimitsExposedValues(t *testing.T) {
	if got := groupedIdentityPrefix("2040476161234567", 9); got != "204-047-616" {
		t.Fatalf("groupedIdentityPrefix() = %q", got)
	}
}
