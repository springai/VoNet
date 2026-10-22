package ims

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vonet/internal/vowifi"
)

func TestIncomingCallCanRingAndAnswerWithMediaOffer(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	refreshContext, cancelRefresh := context.WithCancel(context.Background())
	defer cancelRefresh()
	session := &Session{
		fromTag: "local-tag", calls: make(map[string]*imsCall), conn: client,
		identity: identitySet{user: "subscriber"}, transport: "udp", refreshContext: refreshContext,
	}
	packet, err := parseSIPPacket([]byte(strings.Join([]string{
		"INVITE sip:subscriber@example.test SIP/2.0",
		"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK-incoming",
		"From: <tel:+447700900001>;tag=remote",
		"To: <sip:subscriber@example.test>",
		"Call-ID: incoming-call@example.test",
		"CSeq: 1 INVITE",
		"Session-Expires: 1800",
		"Record-Route: <sip:proxy1.example.test;lr>",
		"Record-Route: <sip:proxy2.example.test;lr>",
		"Content-Length: 0", "", "",
	}, "\r\n")))
	if err != nil || packet.Request == nil {
		t.Fatalf("parse INVITE: %v", err)
	}
	var responses [][]byte
	session.handleSIPRequest(packet.Request, func(response []byte) error {
		responses = append(responses, append([]byte(nil), response...))
		return nil
	})
	calls := session.Calls()
	if len(calls) != 1 || calls[0].Direction != "incoming" || calls[0].State != "ringing" || calls[0].Number != "+447700900001" {
		t.Fatalf("incoming Calls = %#v", calls)
	}
	if len(responses) != 1 || !strings.HasPrefix(string(responses[0]), "SIP/2.0 180 Ringing") {
		t.Fatalf("ringing response = %q", responses)
	}
	answered, err := session.AnswerCall(context.Background(), calls[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if answered.State != "active" || len(responses) != 2 || !strings.Contains(string(responses[1]), "a=sendrecv") {
		t.Fatalf("answered = %#v, response = %q", answered, responses[1])
	}
	for _, want := range []string{"Allow: INVITE, ACK, CANCEL, BYE, UPDATE, PRACK, INFO, OPTIONS", "Supported: timer"} {
		if !strings.Contains(string(responses[1]), want) {
			t.Fatalf("answer omits %q:\n%s", want, responses[1])
		}
	}
	for _, response := range responses {
		firstRoute := strings.Index(string(response), "Record-Route: <sip:proxy1.example.test;lr>")
		secondRoute := strings.Index(string(response), "Record-Route: <sip:proxy2.example.test;lr>")
		if firstRoute < 0 || secondRoute < 0 || firstRoute > secondRoute {
			t.Fatalf("response omitted or reordered Record-Route:\n%s", response)
		}
	}
	if !strings.Contains(string(responses[1]), "Contact: <sip:subscriber@pipe;transport=udp>") {
		t.Fatalf("answer omitted dialog Contact: %q", responses[1])
	}
	if !strings.Contains(string(responses[1]), "Session-Expires: 1800;refresher=uas") {
		t.Fatalf("answer omitted session refresher:\n%s", responses[1])
	}
}

func TestIncomingReliableProvisionalResponseHandlesPRACK(t *testing.T) {
	session := &Session{
		fromTag: "local-tag",
		calls:   make(map[string]*imsCall),
		provider: &Provider{config: Config{
			SecurityMode: SecurityDisabled,
		}},
		request: vowifi.IMSRequest{Identity: vowifi.SIMIdentity{
			HomeMCC: "234", HomeMNC: "10", IMSI: "234107000000001",
		}},
	}
	invite, err := parseSIPPacket([]byte(strings.Join([]string{
		"INVITE sip:subscriber@example.test SIP/2.0",
		"Via: SIP/2.0/TCP 192.0.2.10:5060;branch=z9hG4bK-reliable",
		"From: <tel:+447700900001>;tag=remote",
		"To: <sip:subscriber@example.test>",
		"Call-ID: reliable-call",
		"CSeq: 1 INVITE",
		"Require: 100rel",
		"Content-Length: 0", "", "",
	}, "\r\n")))
	if err != nil || invite.Request == nil {
		t.Fatalf("parse INVITE: %v", err)
	}
	var ringing []byte
	if handled := session.handleCallRequest(invite.Request, func(response []byte) error {
		ringing = append([]byte(nil), response...)
		return nil
	}); !handled {
		t.Fatal("INVITE was not handled")
	}
	for _, want := range []string{"SIP/2.0 180 Ringing", "Require: 100rel", "RSeq: 1"} {
		if !strings.Contains(string(ringing), want) {
			t.Fatalf("reliable 180 omits %q:\n%s", want, ringing)
		}
	}
	call := session.calls["reliable-call"]
	if call == nil || call.provisionalRSeq != 1 {
		t.Fatalf("provisional state = %#v", call)
	}
	prack, err := parseSIPPacket([]byte(strings.Join([]string{
		"PRACK sip:subscriber@example.test SIP/2.0",
		"Via: SIP/2.0/TCP 192.0.2.10:5060;branch=z9hG4bK-prack",
		"From: <tel:+447700900001>;tag=remote",
		"To: <sip:subscriber@example.test>;tag=local-tag",
		"Call-ID: reliable-call",
		"CSeq: 2 PRACK",
		"RAck: 1 1 INVITE",
		"Content-Length: 0", "", "",
	}, "\r\n")))
	if err != nil || prack.Request == nil {
		t.Fatalf("parse PRACK: %v", err)
	}
	var prackResponse []byte
	if handled := session.handleCallRequest(prack.Request, func(response []byte) error {
		prackResponse = append([]byte(nil), response...)
		return nil
	}); !handled {
		t.Fatal("PRACK was not handled")
	}
	if !strings.HasPrefix(string(prackResponse), "SIP/2.0 200 OK") {
		t.Fatalf("PRACK response = %q", prackResponse)
	}
	_ = call.media.Close()
}

func TestIncomingCallCanBeRejected(t *testing.T) {
	session := &Session{fromTag: "local-tag", calls: make(map[string]*imsCall)}
	packet, err := parseSIPPacket([]byte(strings.Join([]string{
		"INVITE sip:user@example.test SIP/2.0",
		"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK-a",
		"From: <tel:+1>;tag=a", "To: <sip:user@example.test>",
		"Call-ID: reject-call", "CSeq: 1 INVITE", "Content-Length: 0", "", "",
	}, "\r\n")))
	if err != nil || packet.Request == nil {
		t.Fatalf("parse INVITE: %v", err)
	}
	var response []byte
	session.handleCallRequest(packet.Request, func(value []byte) error { response = append([]byte(nil), value...); return nil })
	if err := session.HangupCall(context.Background(), "reject-call"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(response), "SIP/2.0 486 Busy Here") {
		t.Fatalf("reject response = %q", response)
	}
	calls := session.Calls()
	if len(calls) != 1 || calls[0].State != "ended" || calls[0].EndedAt == nil {
		t.Fatalf("terminal call status = %#v", calls)
	}
}

func TestIncomingCallTriggersOnIncomingCallCallback(t *testing.T) {
	var captured ReceivedCall
	called := make(chan struct{}, 1)
	provider := &Provider{
		config: Config{
			OnIncomingCall: func(_ context.Context, call ReceivedCall) error {
				captured = call
				called <- struct{}{}
				return nil
			},
		},
	}
	session := &Session{
		provider: provider,
		fromTag:  "local-tag",
		calls:    make(map[string]*imsCall),
		request: vowifi.IMSRequest{
			DeviceID: "ec20-test",
			Identity: vowifi.SIMIdentity{IMSI: "123456789012345"},
		},
		identity: identitySet{public: "sip:+447700900123@example.test"},
	}
	packet, err := parseSIPPacket([]byte(strings.Join([]string{
		"INVITE sip:user@example.test SIP/2.0",
		"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK-notify",
		"From: <tel:+447700999888>;tag=caller-tag",
		"To: <tel:+447700900123>",
		"Call-ID: notify-call-id",
		"CSeq: 1 INVITE",
		"Content-Length: 0", "", "",
	}, "\r\n")))
	if err != nil || packet.Request == nil {
		t.Fatalf("parse INVITE: %v", err)
	}
	session.handleCallRequest(packet.Request, func([]byte) error { return nil })

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("OnIncomingCall was not invoked within timeout")
	}

	if captured.DeviceID != "ec20-test" || captured.Caller != "+447700999888" || captured.Called != "+447700900123" || captured.CallID != "notify-call-id" {
		t.Fatalf("captured call = %#v", captured)
	}
}

func TestRejectedOutgoingCallRetainsSIPReason(t *testing.T) {
	session := &Session{calls: make(map[string]*imsCall)}
	call := &imsCall{public: vowifi.Call{ID: "rejected", State: "dialing"}}
	session.calls[call.public.ID] = call
	session.finishCall(call.public.ID, "failed", 484, "Address Incomplete\r\nignored")
	calls := session.Calls()
	if len(calls) != 1 || calls[0].State != "failed" || calls[0].SIPCode != 484 ||
		calls[0].Reason != "Address Incomplete ignored" || calls[0].EndedAt == nil {
		t.Fatalf("rejected call = %#v", calls)
	}
}

func TestCancelledOutgoingInviteDoesNotBecomeFailedOn487(t *testing.T) {
	call := &imsCall{
		public:     vowifi.Call{ID: "cancelled", State: "dialing"},
		callID:     "cancelled",
		responses:  make(chan *sipResponse, 1),
		terminated: true,
	}
	session := &Session{
		calls:          map[string]*imsCall{call.callID: call},
		transactions:   make(map[sipTransactionKey]chan *sipResponse),
		refreshContext: context.Background(),
	}
	key := sipTransactionKey{callID: call.callID, cseq: 1, method: "INVITE"}
	go session.watchOutgoingCall(call, key)
	call.responses <- &sipResponse{StatusCode: 487, Reason: "Request Terminated"}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		calls := session.Calls()
		if len(calls) == 1 && calls[0].EndedAt != nil {
			if calls[0].State != "ended" || calls[0].SIPCode != 487 {
				t.Fatalf("cancelled INVITE = %#v", calls[0])
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cancelled INVITE did not reach a terminal state")
}

func TestBodylessFinalOKRetainsEarlyMedia(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	remote, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	if err := media.configureRemote(remote.offerSDP(net.IPv4(127, 0, 0, 1))); err != nil {
		t.Fatal(err)
	}
	call := &imsCall{
		public:       vowifi.Call{ID: "bodyless-200", State: "early_media"},
		callID:       "bodyless-200",
		target:       "sip:remote@example.test",
		inviteTarget: "sip:remote@example.test",
		from:         "<sip:local@example.test>;tag=local",
		to:           "<sip:remote@example.test>",
		branch:       "invite-branch",
		cseq:         7,
		responses:    make(chan *sipResponse, 1),
		media:        media,
	}
	session := &Session{
		conn:           client,
		transport:      "tcp",
		calls:          map[string]*imsCall{call.callID: call},
		transactions:   make(map[sipTransactionKey]chan *sipResponse),
		refreshContext: context.Background(),
	}
	key := sipTransactionKey{callID: call.callID, cseq: call.cseq, method: "INVITE"}
	go session.watchOutgoingCall(call, key)
	ackRead := make(chan struct{})
	go func() {
		buffer := make([]byte, 4096)
		_, _ = peer.Read(buffer)
		close(ackRead)
	}()
	call.responses <- &sipResponse{
		StatusCode: 200,
		Reason:     "OK",
		Headers: map[string][]string{
			"to":      {"<sip:remote@example.test>;tag=remote"},
			"contact": {"<sip:remote@203.0.113.10>"},
			"cseq":    {"7 INVITE"},
		},
	}
	select {
	case <-ackRead:
	case <-time.After(time.Second):
		t.Fatal("2xx ACK was not sent")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current := session.Calls()[0]
		if current.State == "active" && current.MediaReady && current.EndedAt == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("bodyless 200 discarded early media: %#v", session.Calls()[0])
}

func TestIncomingACKConfiguresOfferFromAnswer(t *testing.T) {
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	call := &imsCall{
		public: vowifi.Call{ID: "incoming-offer", Direction: "incoming", State: "active"},
		callID: "incoming-offer",
		media:  media,
	}
	session := &Session{
		fromTag: "local-tag",
		calls:   map[string]*imsCall{call.callID: call},
	}
	sdp := strings.Join([]string{
		"v=0",
		"m=audio 49170 RTP/AVP 99 102",
		"c=IN IP4 192.0.2.20",
		"a=rtpmap:99 AMR/8000",
		"a=rtpmap:102 telephone-event/8000",
		"",
	}, "\r\n")
	packet, err := parseSIPPacket([]byte(strings.Join([]string{
		"ACK sip:subscriber@example.test SIP/2.0",
		"Via: SIP/2.0/TCP 192.0.2.10:5060;branch=z9hG4bK-answer",
		"From: <tel:+447700900001>;tag=remote",
		"To: <sip:subscriber@example.test>;tag=local-tag",
		"Call-ID: incoming-offer",
		"CSeq: 1 ACK",
		"Content-Type: application/sdp",
		"Content-Length: " + strconv.Itoa(len(sdp)), "",
		sdp,
	}, "\r\n")))
	if err != nil || packet.Request == nil {
		t.Fatalf("parse ACK: %v", err)
	}
	if handled := session.handleCallRequest(packet.Request, func([]byte) error { return nil }); !handled {
		t.Fatal("ACK was not handled")
	}
	if !call.media.ready() || call.public.Codec != "AMR" || !call.public.MediaReady {
		t.Fatalf("ACK media = ready=%v call=%#v", call.media.ready(), call.public)
	}
}

func TestDialogContactUsesRegisteredContactWithoutExpires(t *testing.T) {
	session := &Session{
		identity: identitySet{user: "subscriber"},
		conn:     &net.TCPConn{},
		evidence: vowifi.IMSEvidence{
			RegisteredContact: `<sip:registered@[2001:db8::1]:6050;transport=tcp>;+sip.instance="<urn:uuid:test>";expires=600`,
		},
	}
	got := session.dialogContactHeader()
	want := `Contact: <sip:registered@[2001:db8::1]:6050;transport=tcp>;+sip.instance="<urn:uuid:test>"`
	if got != want {
		t.Fatalf("dialog Contact = %q, want %q", got, want)
	}
}

func TestValidCallNumber(t *testing.T) {
	if !validCallNumber("+447700900000") || validCallNumber("12\r\nBYE") {
		t.Fatal("call number validation mismatch")
	}
}

func TestOutgoingLocalNumberUsesIMSPhoneContextAndMMTelHeaders(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	refreshContext, cancelRefresh := context.WithCancel(context.Background())
	defer cancelRefresh()
	session := &Session{
		provider: &Provider{config: Config{
			UserAgent:    "VoNet Test",
			SecurityMode: SecurityDisabled,
		}},
		request: vowifi.IMSRequest{Identity: vowifi.SIMIdentity{
			HomeMCC: "234", HomeMNC: "33", IMSI: "234330000000001", ICCID: "8944300000000000000",
		}},
		identity: identitySet{
			domain: "ims.mnc033.mcc234.3gppnetwork.org",
			public: "sip:234330000000001@ims.mnc033.mcc234.3gppnetwork.org",
			user:   "234330000000001",
		},
		endpoint:       pcscfEndpoint{host: "pcscf.test", port: 5060},
		transport:      "tcp",
		conn:           client,
		fromTag:        "local-tag",
		instanceID:     "urn:uuid:00000000-0000-4000-8000-000000000001",
		cseq:           1,
		transactions:   make(map[sipTransactionKey]chan *sipResponse),
		calls:          make(map[string]*imsCall),
		refreshContext: refreshContext,
		evidence: vowifi.IMSEvidence{
			PAssociatedURI: []string{"<tel:+447700900123>"},
			ServiceRoute:   []string{"<sip:route.ims.test;lr>"},
		},
	}
	wireResult := make(chan string, 1)
	go func() {
		packet, _ := io.ReadAll(peer)
		wireResult <- string(packet)
	}()

	call, err := session.DialCall(context.Background(), "888")
	if err != nil {
		t.Fatal(err)
	}
	session.finishCall(call.ID, "ended", 0, "test complete")
	cancelRefresh()
	_ = client.Close()
	wire := <-wireResult

	for _, expected := range []string{
		"INVITE tel:888;phone-context=ims.mnc033.mcc234.3gppnetwork.org SIP/2.0\r\n",
		"To: <tel:888;phone-context=ims.mnc033.mcc234.3gppnetwork.org>\r\n",
		"From: <sip:+447700900123@ims.mnc033.mcc234.3gppnetwork.org>;tag=local-tag\r\n",
		"P-Preferred-Identity: <tel:+447700900123>\r\n",
		"P-Preferred-Service: " + mmtelServiceURN + "\r\n",
		`Accept-Contact: *;+g.3gpp.icsi-ref="` + mmtelFeatureTag + `"` + "\r\n",
		"P-Access-Network-Info: IEEE-802.11;i-wlan-node-id=" + defaultPANIWLANNode + "\r\n",
		"User-Agent: VoNet Test\r\n",
		"Accept: application/sdp\r\n",
	} {
		if !strings.Contains(wire, expected) {
			t.Fatalf("INVITE omitted %q:\n%s", expected, wire)
		}
	}
}

func TestDialogRequestOmitsPAccessNetworkInfoWhenProfileDisablesPANI(t *testing.T) {
	profileDir := t.TempDir()
	profile := `{"version":1,"profiles":[{"id":"test-pani-disabled","match":{"home_plmns":["00101"]},"ims":{"pani_enabled":false}}]}`
	if err := os.WriteFile(filepath.Join(profileDir, "pani-disabled.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyProfileDir := t.TempDir()
	t.Cleanup(func() {
		if err := vowifi.LoadCarrierProfileDirectory(emptyProfileDir); err != nil {
			t.Errorf("clear external carrier profiles: %v", err)
		}
	})
	if err := vowifi.LoadCarrierProfileDirectory(profileDir); err != nil {
		t.Fatal(err)
	}

	identity := vowifi.SIMIdentity{HomeMCC: "001", HomeMNC: "01", IMSI: "001010123456789"}
	pani := resolveSessionPAccessNetworkInfo(identity, nil)
	if pani != "" {
		t.Fatalf("disabled profile PANI = %q, want empty", pani)
	}
	client, peer := net.Pipe()
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close client connection: %v", err)
		}
	})
	t.Cleanup(func() {
		if err := peer.Close(); err != nil {
			t.Errorf("close peer connection: %v", err)
		}
	})
	session := &Session{
		request:   vowifi.IMSRequest{Identity: identity},
		transport: "tcp",
		conn:      client,
	}
	call := &imsCall{
		target: "sip:callee@example.test",
		from:   "<sip:caller@example.test>;tag=local",
		to:     "<sip:callee@example.test>;tag=remote",
		callID: "pani-disabled-call",
	}

	request := string(session.buildDialogRequest(call, "BYE", 2))
	if strings.Contains(request, "\r\nP-Access-Network-Info:") {
		t.Fatalf("BYE contains disabled P-Access-Network-Info header:\n%s", request)
	}
}

func TestCallOriginatingIdentitiesFallBackToRegisteredIMPU(t *testing.T) {
	session := &Session{
		identity: identitySet{
			domain: "ims.mnc033.mcc234.3gppnetwork.org",
			public: "sip:234330000000001@ims.mnc033.mcc234.3gppnetwork.org",
		},
	}
	from, preferred, source := session.callOriginatingIdentitiesLocked(vowifi.CarrierProfile{})
	if from != session.identity.public || preferred != session.identity.public || source != "registered_impu" {
		t.Fatalf("fallback identities = (%q, %q, %q)", from, preferred, source)
	}
}

func TestCallTargetURIUsesPhoneContextOnlyForLocalNumbers(t *testing.T) {
	domain := "ims.mnc033.mcc234.3gppnetwork.org"
	if got := callTargetURI("888", domain, vowifi.CarrierProfile{IMSDialURIScheme: "tel"}); got != "tel:888;phone-context="+domain {
		t.Fatalf("local target = %q", got)
	}
	if got := callTargetURI("+447700900123", domain, vowifi.CarrierProfile{IMSDialURIScheme: "tel"}); got != "tel:+447700900123" {
		t.Fatalf("global target = %q", got)
	}
	if got := callTargetURI("888", domain, vowifi.CarrierProfile{IMSDialURIScheme: "sip"}); got != "sip:888@"+domain {
		t.Fatalf("SIP target = %q", got)
	}
	if got := callTargetURI("888", domain, vowifi.CarrierProfile{IMSDialURIScheme: "sip", IMSUserEqPhone: true}); got != "sip:888@"+domain+";user=phone" {
		t.Fatalf("SIP user=phone target = %q", got)
	}
}

func TestCallResponseDiagnosticIncludesNetworkReason(t *testing.T) {
	response := &sipResponse{
		StatusCode: 487,
		Reason:     "Request Terminated",
		Headers: map[string][]string{
			"reason": {`Q.850;cause=31;text="Normal, unspecified"`},
		},
	}
	want := `Request Terminated; Reason: Q.850;cause=31;text="Normal, unspecified"`
	if got := callResponseDiagnostic(response); got != want {
		t.Fatalf("diagnostic = %q, want %q", got, want)
	}
}

func TestRejectedInviteACKUsesOriginalTransaction(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	session := &Session{
		provider:  &Provider{config: Config{UserAgent: "VoNet Test"}},
		request:   vowifi.IMSRequest{Identity: vowifi.SIMIdentity{HomeMCC: "234", HomeMNC: "33"}},
		identity:  identitySet{domain: "ims.mnc033.mcc234.3gppnetwork.org"},
		transport: "tcp",
		conn:      client,
	}
	call := &imsCall{
		callID:       "call-1",
		inviteTarget: "sip:888@ims.mnc033.mcc234.3gppnetwork.org",
		from:         "<sip:+447700900123@ims.mnc033.mcc234.3gppnetwork.org>;tag=local",
		to:           "<sip:888@ims.mnc033.mcc234.3gppnetwork.org>",
		branch:       "original-branch",
		cseq:         41,
		routes:       []string{"<sip:pcscf.test;lr>"},
	}
	response := &sipResponse{StatusCode: 487, Headers: map[string][]string{
		"to": {"<sip:888@ims.mnc033.mcc234.3gppnetwork.org>;tag=tas"},
	}}
	ackResult := make(chan string, 1)
	go func() {
		buffer := make([]byte, 4096)
		count, _ := peer.Read(buffer)
		ackResult <- string(buffer[:count])
	}()
	if err := session.sendRejectedInviteACK(call, response); err != nil {
		t.Fatal(err)
	}
	ack := <-ackResult
	for _, expected := range []string{
		"ACK sip:888@ims.mnc033.mcc234.3gppnetwork.org SIP/2.0\r\n",
		"branch=z9hG4bKoriginal-branch;rport",
		"To: <sip:888@ims.mnc033.mcc234.3gppnetwork.org>;tag=tas\r\n",
		"CSeq: 41 ACK\r\n",
	} {
		if !strings.Contains(ack, expected) {
			t.Fatalf("rejected INVITE ACK omitted %q:\n%s", expected, ack)
		}
	}
}
