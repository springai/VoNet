package ims

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"vonet/internal/device"
	"vonet/internal/vowifi"
)

type smsTestAKA struct{ *recordingAKA }

func (smsTestAKA) ReadSMSCenter(context.Context, string) (string, error) {
	return "+447785016005", nil
}

func (smsTestAKA) ReadSMSCenterPSI(context.Context, string) (string, error) {
	return "+447785016005", nil
}

func TestValidProtectedTCPSourceAllowsAlternatePCSCF(t *testing.T) {
	session := &Session{securityActive: true}
	source := &net.TCPAddr{IP: net.ParseIP("2001:db8::26"), Port: 22042}
	if !session.validProtectedTCPSource(source) {
		t.Fatal("protected TCP source from an alternate P-CSCF was rejected")
	}
	if session.validProtectedTCPSource(&net.UDPAddr{IP: source.IP, Port: source.Port}) {
		t.Fatal("non-TCP protected source was accepted")
	}
	inactive := &Session{securityActive: false}
	if inactive.validProtectedTCPSource(source) {
		t.Fatal("protected TCP source was accepted before security was active")
	}
}

func TestSessionReceivesAndAcknowledgesSMSOverIMS(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second))

	received := make(chan ReceivedSMS, 1)
	serverDone := make(chan error, 1)
	readyForClose := make(chan struct{})
	nonce := base64.StdEncoding.EncodeToString(make([]byte, 32))
	go func() { serverDone <- serveInboundSMS(listener, nonce, readyForClose) }()
	provider, err := NewProvider(
		smsTestAKA{&recordingAKA{result: vowifi.AKAResult{RES: []byte{1, 2, 3, 4}}}},
		Config{
			PCSCF: listener.LocalAddr().String(), LocalAddress: "127.0.0.1",
			Transport: "udp", TransactionTimeout: 3 * time.Second, SecurityMode: SecurityDisabled,
			OnSMS: func(_ context.Context, message ReceivedSMS) error {
				received <- message
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.Start(context.Background(), vowifi.IMSRequest{
		DeviceID: "ec20",
		Identity: vowifi.SIMIdentity{IMSI: "001010123456789", HomeMCC: "001", HomeMNC: "01"},
		Tunnel: evidenceTunnel{evidence: vowifi.TunnelEvidence{
			Established: true, LocalIPv4: "127.0.0.1", PCSCF: []string{listener.LocalAddr().String()},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		if message.From != "+12345" || message.Text != "HELLO" ||
			message.MessageID != "ims:network-deliver-1:42" ||
			message.ServiceCenterTimestamp == nil || message.Timestamp.IsZero() {
			t.Fatalf("received = %#v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for inbound SMS")
	}
	select {
	case <-readyForClose:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the inbound RP-ACK exchange")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSecurityHeaders(t *testing.T) {
	verify := "ipsec-3gpp;alg=hmac-sha-1-96;prot=esp;mod=trans"
	headers := runtimeSecurityHeaders(true, verify)
	want := []string{
		"Security-Verify: " + verify,
		"Require: sec-agree",
		"Proxy-Require: sec-agree",
	}
	if len(headers) != len(want) {
		t.Fatalf("security header count = %d, want %d", len(headers), len(want))
	}
	for index := range want {
		if headers[index] != want[index] {
			t.Fatalf("security header %d = %q, want %q", index, headers[index], want[index])
		}
	}
	if headers := runtimeSecurityHeaders(false, verify); len(headers) != 0 {
		t.Fatalf("disabled security headers = %#v", headers)
	}
}

func TestExtractSMSPayload(t *testing.T) {
	rpdu := []byte{0x01, 0x2a, 0x00, 0x00, 0x03, 0x04, 0x00, 0x00}
	tests := []struct {
		name        string
		request     *sipRequest
		wantSource  string
		wantPayload []byte
	}{
		{
			name: "direct binary",
			request: &sipRequest{Headers: map[string][]string{
				"content-type":              {smsContentType + "; charset=binary"},
				"content-transfer-encoding": {"binary"},
			}, Body: rpdu},
			wantSource:  smsContentType,
			wantPayload: rpdu,
		},
		{
			name:        "multipart base64",
			request:     multipartSMSRequest(t, rpdu),
			wantSource:  "multipart/mixed",
			wantPayload: rpdu,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, source, err := extractSMSPayload(test.request)
			if err != nil {
				t.Fatalf("extractSMSPayload() error = %v", err)
			}
			if source != test.wantSource || !bytes.Equal(payload, test.wantPayload) {
				t.Fatalf("extractSMSPayload() = (%x, %q), want (%x, %q)",
					payload, source, test.wantPayload, test.wantSource)
			}
		})
	}
}

func TestSupportsSMSContentType(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{smsContentType, true},
		{"Application/Vnd.3gpp.Sms; charset=binary", true},
		{`multipart/mixed; boundary="vodafone-boundary"`, true},
		{"multipart/mixed", false},
		{"text/plain", false},
	} {
		if got := supportsSMSContentType(test.value); got != test.want {
			t.Errorf("supportsSMSContentType(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestSupportsUSSIContentType(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{ussiContentType, true},
		{"Application/Vnd.3gpp.Ussd; charset=binary", true},
		{ussiXMLContentType, true},
		{"application/vnd.3gpp.ussd+xml; charset=utf-8", true},
		{"multipart/mixed; boundary=test-boundary", true},
		{smsContentType, false},
		{"text/plain", false},
	} {
		if got := supportsUSSIContentType(test.value); got != test.want {
			t.Errorf("supportsUSSIContentType(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestClassifiesMultipartUSSIWithoutConsumingSMS(t *testing.T) {
	const boundary = "vowifi-boundary"
	request := func(parts ...string) *sipRequest {
		body := ""
		for _, part := range parts {
			body += "--" + boundary + "\r\n" + part + "\r\n"
		}
		body += "--" + boundary + "--\r\n"
		return &sipRequest{Method: "MESSAGE", Body: []byte(body), Headers: map[string][]string{
			"content-type": {"multipart/mixed; boundary=" + boundary},
		}}
	}

	ussiPart := "Content-Type: application/vnd.3gpp.ussd+xml\r\n\r\n<ussd-data><ussd-string>*100#</ussd-string></ussd-data>"
	smsPart := "Content-Type: application/vnd.3gpp.sms\r\n\r\n0100"
	if !isUSSIMessageRequest(request(ussiPart)) {
		t.Fatal("multipart USSI was classified as SMS")
	}
	if isUSSIMessageRequest(request(smsPart)) || isUSSIMessageRequest(request(smsPart, ussiPart)) {
		t.Fatal("multipart SMS must remain on the SMS path")
	}
}

func TestEncodeDecodeUSSDBody(t *testing.T) {
	for _, text := range []string{"*100#", "Main menu 中文"} {
		body, dcs, err := encodeUSSDBody(text)
		if err != nil {
			t.Fatalf("encodeUSSDBody(%q) error = %v", text, err)
		}
		if dcs == nil || *dcs != 0x48 {
			t.Fatalf("encodeUSSDBody(%q) dcs = %v, want 0x48", text, dcs)
		}
		decoded := decodeUSSDBody(body, *dcs)
		if decoded != text {
			t.Fatalf("decodeUSSDBody(%q) = %q, want %q", text, decoded, text)
		}
	}
}

func TestExtractUSSDString(t *testing.T) {
	text := "Main menu"
	encoded, dcs, err := encodeUSSDBody(text)
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte{byte(len(encoded) + 1), byte(*dcs)}, encoded...)
	raw, gotDCS, gotText := extractUSSDString(body)
	if gotText != text || gotDCS == nil || *gotDCS != *dcs || !bytes.Equal(raw, body) {
		t.Fatalf("extractUSSDString(%x) = (%q, %v, %q)", body, raw, gotDCS, gotText)
	}

	// A plain raw body without a length/DCS prefix falls back to DCS 0x0F.
	raw, gotDCS, gotText = extractUSSDString([]byte("fallback"))
	if gotDCS == nil || *gotDCS != 0x0F || gotText != "fallback" || !bytes.Equal(raw, []byte("fallback")) {
		t.Fatalf("extractUSSDString fallback = (%q, %v, %q)", raw, gotDCS, gotText)
	}
}

func TestSMSCenterForIdentityUsesExactPLMN(t *testing.T) {
	config := Config{SMSCenterByPLMN: map[string]string{
		"23410":  "+447802000332",
		"234010": "+447802000332",
		"23415":  "+447785016005",
	}}
	for _, test := range []struct {
		mnc  string
		want string
	}{
		{mnc: "10", want: "+447802000332"},
		{mnc: "010", want: "+447802000332"},
		{mnc: "15", want: "+447785016005"},
		{mnc: "30", want: ""},
	} {
		identity := vowifi.SIMIdentity{HomeMCC: "234", HomeMNC: test.mnc}
		if got := smsCenterForIdentity(config, identity); got != test.want {
			t.Errorf("smsCenterForIdentity(234/%s) = %q, want %q", test.mnc, got, test.want)
		}
	}
}

func TestSMSCenterForIdentityFallsBackToCarrierProfile(t *testing.T) {
	// Explicit SIM SMSC always takes precedence
	explicit := smsCenterForIdentity(Config{}, vowifi.SIMIdentity{
		HomeMCC: "234", HomeMNC: "15", SMSC: "+447785016005",
	})
	if explicit != "+447785016005" {
		t.Fatalf("explicit SMSC = %q, want +447785016005", explicit)
	}

	// Profile fallback when identity has no SMSC
	identity := vowifi.SIMIdentity{
		ICCID: "8944110000000000001", HomeMCC: "234", HomeMNC: "10",
	}
	profile := vowifi.ResolveCarrierProfile(identity)
	if got := smsCenterForIdentity(Config{}, identity); got != profile.SMSCenter {
		t.Errorf("smsCenterForIdentity = %q, want %q", got, profile.SMSCenter)
	}
	if profile.SMSCenter != "+447802092035" {
		t.Fatalf("O2 profile SMSC = %q, want +447802092035", profile.SMSCenter)
	}
}

func TestO2SMSSubmitRecipientUsesUKNationalAddress(t *testing.T) {
	for _, test := range []struct {
		name      string
		recipient string
		mode      string
		want      string
	}{
		{"O2 international", "+447700900001", "uk-national", "07700900001"},
		{"O2 already national", "07700900001", "uk-national", "07700900001"},
		{"other carrier", "+447700900001", "", "+447700900001"},
		{"non-UK international", "+12025550123", "uk-national", "+12025550123"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeSMSSubmitRecipient(test.recipient, test.mode); got != test.want {
				t.Fatalf("normalized recipient = %q, want %q", got, test.want)
			}
		})
	}

	parts, err := device.PrepareSMSSubmitTPDUs(
		normalizeSMSSubmitRecipient("+447700900001", "uk-national"),
		"hello",
	)
	if err != nil {
		t.Fatal(err)
	}
	tpdu := parts[0].TPDU
	wantAddress := []byte{0x70, 0x07, 0x90, 0x00, 0x00, 0xf1}
	if len(tpdu) < 10 || tpdu[0] != 0x21 || tpdu[2] != 0x0b || tpdu[3] != 0x81 ||
		!bytes.Equal(tpdu[4:10], wantAddress) {
		t.Fatalf("O2 TPDU = %x, want national TP-DA %x", tpdu, wantAddress)
	}
}

func TestSMSProfileControlsO2MessageHeaders(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity vowifi.SIMIdentity
		wantO2   bool
	}{
		{
			name: "O2 UK",
			identity: vowifi.SIMIdentity{
				ICCID: "8944110000000000001", IMSI: "234109999999999",
				HomeMCC: "234", HomeMNC: "10",
			},
			wantO2: true,
		},
		{
			name: "standard profile",
			identity: vowifi.SIMIdentity{
				ICCID: "8999990000000000001", IMSI: "999990000000001",
				HomeMCC: "999", HomeMNC: "99",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := &identityTestConn{}
			session := &Session{
				provider: &Provider{config: Config{TransactionTimeout: time.Second}},
				request: vowifi.IMSRequest{
					DeviceID: "reader",
					Identity: test.identity,
				},
				identity: identitySet{
					private:         "234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
					public:          "sip:234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
					user:            "234109999999999",
					domain:          "ims.mnc010.mcc234.3gppnetwork.org",
					temporaryPublic: true,
				},
				evidence: vowifi.IMSEvidence{
					AssociatedIdentities: []string{"tel:+447700900000", "sip:associated-default@example.test"},
				},
				endpoint:     pcscfEndpoint{host: "127.0.0.1", port: 5060},
				transport:    "tcp",
				conn:         conn,
				fromTag:      "test-tag",
				cseq:         1,
				transactions: make(map[sipTransactionKey]chan *sipResponse),
			}
			if !test.wantO2 {
				session.evidence.AssociatedIdentities = []string{"sip:associated-default@example.test"}
			}
			var captured *sipRequest
			conn.onWrite = func(packet []byte) (int, error) {
				parsed, err := parseSIPPacket(packet)
				if err != nil || parsed.Request == nil {
					return 0, fmt.Errorf("parse outgoing MESSAGE: %v", err)
				}
				captured = parsed.Request
				session.dispatchPacket(sipPacket{Response: &sipResponse{
					StatusCode: 202,
					Headers: map[string][]string{
						"call-id": {captured.value("Call-ID")},
						"cseq":    {captured.value("CSeq")},
					},
				}}, nil)
				return len(packet), nil
			}
			const target = "tel:+447802092035"
			_, err := session.sendSIPMessage(
				context.Background(),
				target,
				[]byte{0x00, 0x01},
				"",
			)
			if err != nil {
				t.Fatal(err)
			}
			const associatedDefault = "<sip:associated-default@example.test>"
			if test.wantO2 {
				if !strings.Contains(captured.value("From"), "<tel:+447700900000>") ||
					captured.value("P-Preferred-Identity") != "<tel:+447700900000>" ||
					captured.value("CSeq") != "1 MESSAGE" {
					t.Fatalf("O2 MESSAGE identity = %#v", captured.Headers)
				}
				if captured.value("Contact") != "" || captured.value("Supported") != "" ||
					captured.value("P-Preferred-Service") != "" || len(captured.values("Accept-Contact")) != 0 {
					t.Fatalf("O2 MESSAGE minimal headers = %#v", captured.Headers)
				}
				if captured.value("P-Access-Network-Info") !=
					"IEEE-802.11;country=GB;i-wlan-node-id=820000000001" {
					t.Fatalf("O2 MESSAGE PANI = %q", captured.value("P-Access-Network-Info"))
				}
				if captured.value("Authorization") != "" {
					t.Fatalf("O2 MESSAGE Authorization = %q", captured.value("Authorization"))
				}
			} else if captured.value("From") != associatedDefault+";tag=test-tag" ||
				captured.value("P-Preferred-Identity") != associatedDefault ||
				captured.value("P-Access-Network-Info") !=
					"IEEE-802.11;i-wlan-node-id=ffffffffffff" ||
				captured.value("Authorization") != "" {
				t.Fatalf("standard MESSAGE headers = %#v", captured.Headers)
			}
		})
	}
}

func TestSMSPreauthorizationUsesCachedAKAState(t *testing.T) {
	const target = "sip:+447802092035@ims.mnc010.mcc234.3gppnetwork.org;user=phone"
	session := &Session{
		securityActive: true,
		identity: identitySet{
			private: "234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
			domain:  "ims.mnc010.mcc234.3gppnetwork.org",
		},
		auth: &authenticationState{
			challenge: digestChallenge{
				Realm:     "ims.mnc010.mcc234.3gppnetwork.org",
				Nonce:     "cached-nonce",
				Algorithm: "AKAv1-MD5",
			},
			response: []byte{1, 2, 3, 4},
		},
	}
	header, authorization := session.smsPreauthorization(target)
	if header != "Authorization" || authorization == "" {
		t.Fatalf("cached preauthorization = %q, %q", header, authorization)
	}
	scheme, parameters, found := strings.Cut(authorization, " ")
	if !found || scheme != "Digest" {
		t.Fatalf("cached preauthorization = %q", authorization)
	}
	directives, err := parseAuthDirectives(parameters)
	if err != nil {
		t.Fatal(err)
	}
	if directives["uri"] != target ||
		directives["nonce"] != "cached-nonce" ||
		directives["algorithm"] != "AKAv1-MD5" ||
		directives["response"] == "" ||
		directives["integrity-protected"] != "yes" {
		t.Fatalf("cached digest directives = %#v", directives)
	}
}

func multipartSMSRequest(t *testing.T, payload []byte) *sipRequest {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("vodafone-boundary"); err != nil {
		t.Fatal(err)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", smsContentType)
	header.Set("Content-Transfer-Encoding", "base64")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(base64.StdEncoding.EncodeToString(payload))); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &sipRequest{
		Headers: map[string][]string{
			"content-type": {`multipart/mixed; boundary="vodafone-boundary"`},
		},
		Body: body.Bytes(),
	}
}

func TestSessionSendsSMSOverIMS(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second))
	serverDone := make(chan error, 1)
	readyForClose := make(chan struct{})
	statusReceived := make(chan ReceivedSMSStatus, 1)
	nonce := base64.StdEncoding.EncodeToString(make([]byte, 32))
	go func() { serverDone <- serveOutboundSMS(listener, nonce, readyForClose) }()
	provider, err := NewProvider(
		smsTestAKA{&recordingAKA{result: vowifi.AKAResult{RES: []byte{1, 2, 3, 4}}}},
		Config{
			PCSCF: listener.LocalAddr().String(), LocalAddress: "127.0.0.1",
			Transport: "udp", TransactionTimeout: 3 * time.Second, SecurityMode: SecurityDisabled,
			OnSMSStatus: func(_ context.Context, status ReceivedSMSStatus) error {
				statusReceived <- status
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.Start(context.Background(), vowifi.IMSRequest{
		DeviceID: "ec20",
		Identity: vowifi.SIMIdentity{IMSI: "001010123456789", HomeMCC: "001", HomeMNC: "01"},
		Tunnel: evidenceTunnel{evidence: vowifi.TunnelEvidence{
			Established: true, LocalIPv4: "127.0.0.1", PCSCF: []string{listener.LocalAddr().String()},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.(vowifi.SMSSender).SendSMS(context.Background(), vowifi.SMSSubmitRequest{
		Recipient: "+12345", Text: "HELLO",
	})
	if err != nil || !result.AllPartsAccepted || result.PartsAccepted != 1 || result.PartResults[0].SIPCode != 202 {
		t.Fatalf("SendSMS = (%#v, %v)", result, err)
	}
	select {
	case status := <-statusReceived:
		if status.To != "+12345" || status.MessageReference != result.PartResults[0].Reference ||
			status.StatusCode != 0 || status.DeliveryStatus != "delivered" ||
			status.ServiceCenterTimestamp == nil || status.DischargeTimestamp == nil {
			t.Fatalf("SMS status = %#v", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SMS delivery status")
	}
	select {
	case <-readyForClose:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the status-report RP-ACK exchange")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func serveInboundSMS(listener *net.UDPConn, nonce string, readyForClose chan<- struct{}) error {
	packet := make([]byte, 65535)
	count, remote, err := listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err := parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	registerPANI := headers["p-access-network-info"]
	if err := validateTestPANI(registerPANI); err != nil {
		return fmt.Errorf("initial REGISTER PANI: %w", err)
	}
	callID := headers["call-id"]
	if _, err = listener.WriteToUDP(testResponse(401, "Unauthorized", callID, headers["cseq"], []string{
		`WWW-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="` + nonce + `", algorithm=AKAv1-MD5, qop="auth"`,
	}), remote); err != nil {
		return err
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if headers["p-access-network-info"] != registerPANI {
		return errors.New("authenticated REGISTER changed PANI")
	}
	if _, err = listener.WriteToUDP(testResponse(200, "OK", callID, headers["cseq"], []string{
		"Contact: " + headers["contact"] + ";expires=600",
	}), remote); err != nil {
		return err
	}

	tpdu := []byte{
		0x04, 0x05, 0x91, 0x21, 0x43, 0xf5, 0x00, 0x00,
		0x42, 0x10, 0x20, 0x30, 0x40, 0x50, 0x00, 0x05,
		0xc8, 0x22, 0x93, 0xf9, 0x04,
	}
	rpdu := []byte{0x01, 0x2a, 0x00, 0x00, byte(len(tpdu))}
	rpdu = append(rpdu, tpdu...)
	var messageBody bytes.Buffer
	mimeWriter := multipart.NewWriter(&messageBody)
	if err = mimeWriter.SetBoundary("vodafone-delivery"); err != nil {
		return err
	}
	mimeHeader := make(textproto.MIMEHeader)
	mimeHeader.Set("Content-Type", smsContentType)
	mimeHeader.Set("Content-Transfer-Encoding", "binary")
	mimePart, createErr := mimeWriter.CreatePart(mimeHeader)
	if createErr != nil {
		return createErr
	}
	if _, err = mimePart.Write(rpdu); err != nil {
		return err
	}
	if err = mimeWriter.Close(); err != nil {
		return err
	}
	request := []byte(strings.Join([]string{
		"MESSAGE sip:001010123456789@ims.mnc001.mcc001.3gppnetwork.org SIP/2.0",
		"Via: SIP/2.0/UDP " + listener.LocalAddr().String() + ";branch=z9hG4bKdeliver",
		"From: <sip:ipsmgw@example.test>;tag=gw",
		"To: <sip:001010123456789@ims.mnc001.mcc001.3gppnetwork.org>",
		"P-Asserted-Identity: <sip:ipsmgw@example.test>",
		"Call-ID: network-deliver-1",
		"CSeq: 1 MESSAGE",
		`Content-Type: multipart/mixed; boundary="vodafone-delivery"`,
		fmt.Sprintf("Content-Length: %d", messageBody.Len()), "", "",
	}, "\r\n"))
	request = append(request, messageBody.Bytes()...)
	if _, err = listener.WriteToUDP(request, remote); err != nil {
		return err
	}

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	response, err := parseSIPResponse(packet[:count])
	if err != nil || response.StatusCode != 200 {
		return fmt.Errorf("delivery SIP response = (%#v, %v)", response, err)
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	report, err := parseSIPPacket(packet[:count])
	if err != nil || report.Request == nil {
		return fmt.Errorf("delivery report parse: %v", err)
	}
	if report.Request.Method != "MESSAGE" || report.Request.value("In-Reply-To") != "network-deliver-1" ||
		len(report.Request.Body) != 2 || report.Request.Body[0] != 0x02 || report.Request.Body[1] != 0x2a {
		return fmt.Errorf("unexpected delivery report %#v", report.Request)
	}
	if report.Request.value("P-Access-Network-Info") != registerPANI {
		return errors.New("inbound SMS RP-ACK did not reuse REGISTER PANI")
	}
	if _, err = listener.WriteToUDP(testResponse(200, "OK", report.Request.value("Call-ID"), report.Request.value("CSeq"), nil), remote); err != nil {
		return err
	}
	close(readyForClose)

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if headers["expires"] != "0" {
		return errors.New("expected deregistration")
	}
	if headers["p-access-network-info"] != registerPANI {
		return errors.New("deregistration changed PANI")
	}
	_, err = listener.WriteToUDP(testResponse(200, "OK", callID, headers["cseq"], nil), remote)
	return err
}

func serveOutboundSMS(listener *net.UDPConn, nonce string, readyForClose chan<- struct{}) error {
	packet := make([]byte, 65535)
	count, remote, err := listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err := parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	registerPANI := headers["p-access-network-info"]
	if err := validateTestPANI(registerPANI); err != nil {
		return fmt.Errorf("initial REGISTER PANI: %w", err)
	}
	registerCallID := headers["call-id"]
	if _, err = listener.WriteToUDP(testResponse(401, "Unauthorized", registerCallID, headers["cseq"], []string{
		`WWW-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="` + nonce + `", algorithm=AKAv1-MD5, qop="auth"`,
	}), remote); err != nil {
		return err
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if headers["p-access-network-info"] != registerPANI {
		return errors.New("authenticated REGISTER changed PANI")
	}
	if _, err = listener.WriteToUDP(testResponse(200, "OK", registerCallID, headers["cseq"], []string{
		"Contact: " + headers["contact"] + ";expires=600",
		"P-Associated-URI: <sip:12025550100@msg.example.test>",
	}), remote); err != nil {
		return err
	}

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	firstMessage := append([]byte(nil), packet[:count]...)
	firstRemote := remote.String()
	// Exercise the RFC SIP/UDP non-INVITE transaction retransmission path by
	// deliberately dropping the first MESSAGE request.
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	if remote.String() != firstRemote || !bytes.Equal(packet[:count], firstMessage) {
		return errors.New("outbound MESSAGE retransmission changed transaction bytes or source")
	}
	message, err := parseSIPPacket(packet[:count])
	if err != nil || message.Request == nil {
		return fmt.Errorf("outbound MESSAGE parse: %v", err)
	}
	if message.Request.Method != "MESSAGE" || message.Request.URI != "tel:+447785016005" ||
		strings.ToLower(message.Request.value("Content-Type")) != smsContentType ||
		message.Request.value("Request-Disposition") != "no-fork" ||
		message.Request.value("Allow") != "MESSAGE" {
		return fmt.Errorf("unexpected outbound MESSAGE %#v", message.Request)
	}
	if message.Request.value("P-Access-Network-Info") != registerPANI {
		return errors.New("outbound SMS MESSAGE did not reuse REGISTER PANI")
	}
	rpdu, err := parseRPDU(message.Request.Body)
	if err != nil || rpdu.messageType != 0 || len(rpdu.tpdu) != 0 {
		// parseRPDU intentionally decodes only network-to-MS RP-DATA; inspect
		// the mandatory MO prefix and TPDU length directly below.
		if err != nil {
			return err
		}
	}
	body := message.Request.Body
	if len(body) < 8 || body[0] != 0x00 || body[2] != 0x00 {
		return fmt.Errorf("invalid MO RP-DATA %x", body)
	}
	destinationLength := int(body[3])
	userLengthIndex := 4 + destinationLength
	if userLengthIndex >= len(body) || int(body[userLengthIndex]) != len(body)-userLengthIndex-1 {
		return fmt.Errorf("invalid MO RP-DATA lengths %x", body)
	}
	tpdu := body[userLengthIndex+1:]
	if len(tpdu) < 2 || tpdu[0]&0x03 != 1 || tpdu[0]&0x20 == 0 || tpdu[1] != body[1] {
		return fmt.Errorf("SMS-SUBMIT did not request a trackable status report: %x", tpdu)
	}
	messageCallID := message.Request.value("Call-ID")
	messageCSeq := message.Request.value("CSeq")
	messageFrom := message.Request.value("From")
	messageTo := message.Request.value("To")
	messageBody := append([]byte(nil), body...)
	messageNonce := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	if _, err = listener.WriteToUDP(testResponse(401, "Unauthorized", messageCallID, messageCSeq, []string{
		`Proxy-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="` + messageNonce + `", algorithm=AKAv1-MD5`,
	}), remote); err != nil {
		return err
	}

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	authenticatedMessage, err := parseSIPPacket(packet[:count])
	if err != nil || authenticatedMessage.Request == nil {
		return fmt.Errorf("authenticated MESSAGE parse: %v", err)
	}
	authenticatedRequest := authenticatedMessage.Request
	if authenticatedRequest.Method != "MESSAGE" ||
		authenticatedRequest.URI != message.Request.URI ||
		authenticatedRequest.value("Call-ID") != messageCallID ||
		authenticatedRequest.value("From") != messageFrom ||
		authenticatedRequest.value("To") != messageTo ||
		!bytes.Equal(authenticatedRequest.Body, messageBody) {
		return fmt.Errorf("authenticated MESSAGE changed the original request: %#v", authenticatedRequest)
	}
	if authenticatedRequest.value("CSeq") == messageCSeq {
		return errors.New("authenticated MESSAGE reused the challenged CSeq")
	}
	if err := verifySIPMessageAuthorization(
		authenticatedRequest.value("Proxy-Authorization"),
		messageNonce,
		message.Request.URI,
		"",
		"",
	); err != nil {
		return fmt.Errorf("MESSAGE Proxy-Authorization: %w", err)
	}
	if authenticatedRequest.value("Authorization") != "" {
		return errors.New("401 Proxy-Authenticate retry unexpectedly used Authorization")
	}

	proxyMessageNonce := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x43}, 32))
	if _, err = listener.WriteToUDP(testResponse(407, "Proxy Authentication Required", messageCallID, authenticatedRequest.value("CSeq"), []string{
		`Proxy-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="` + proxyMessageNonce + `", algorithm=AKAv1-MD5, qop="auth"`,
	}), remote); err != nil {
		return err
	}

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	proxyAuthenticatedMessage, err := parseSIPPacket(packet[:count])
	if err != nil || proxyAuthenticatedMessage.Request == nil {
		return fmt.Errorf("proxy-authenticated MESSAGE parse: %v", err)
	}
	proxyRequest := proxyAuthenticatedMessage.Request
	if proxyRequest.Method != "MESSAGE" ||
		proxyRequest.URI != message.Request.URI ||
		proxyRequest.value("Call-ID") != messageCallID ||
		proxyRequest.value("From") != messageFrom ||
		proxyRequest.value("To") != messageTo ||
		!bytes.Equal(proxyRequest.Body, messageBody) {
		return fmt.Errorf("proxy-authenticated MESSAGE changed the original request: %#v", proxyRequest)
	}
	if proxyRequest.value("CSeq") == authenticatedRequest.value("CSeq") {
		return errors.New("proxy-authenticated MESSAGE reused the challenged CSeq")
	}
	if err := verifySIPMessageAuthorization(
		proxyRequest.value("Proxy-Authorization"),
		proxyMessageNonce,
		message.Request.URI,
		"auth",
		"00000002",
	); err != nil {
		return fmt.Errorf("MESSAGE Proxy-Authorization: %w", err)
	}
	if proxyRequest.value("Authorization") != "" {
		return errors.New("407 MESSAGE retry unexpectedly used Authorization")
	}

	if _, err = listener.WriteToUDP(testResponse(202, "Accepted", messageCallID, proxyRequest.value("CSeq"), nil), remote); err != nil {
		return err
	}

	statusTPDU := []byte{
		0x02, tpdu[1], 0x05, 0x91, 0x21, 0x43, 0xf5,
		0x42, 0x10, 0x20, 0x30, 0x40, 0x50, 0x00,
		0x42, 0x10, 0x20, 0x30, 0x50, 0x50, 0x00,
		0x00,
	}
	statusRPDU := []byte{0x01, 0x2b, 0x00, 0x00, byte(len(statusTPDU))}
	statusRPDU = append(statusRPDU, statusTPDU...)
	statusRequest := []byte(strings.Join([]string{
		"MESSAGE sip:001010123456789@ims.mnc001.mcc001.3gppnetwork.org SIP/2.0",
		"Via: SIP/2.0/UDP " + listener.LocalAddr().String() + ";branch=z9hG4bKstatus",
		"From: <sip:ipsmgw@example.test>;tag=gw",
		"To: <sip:001010123456789@ims.mnc001.mcc001.3gppnetwork.org>",
		"P-Asserted-Identity: <sip:ipsmgw@example.test>",
		"Call-ID: network-status-1",
		"CSeq: 2 MESSAGE",
		"Content-Type: application/vnd.3gpp.sms",
		fmt.Sprintf("Content-Length: %d", len(statusRPDU)), "", "",
	}, "\r\n"))
	statusRequest = append(statusRequest, statusRPDU...)
	if _, err = listener.WriteToUDP(statusRequest, remote); err != nil {
		return err
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	statusResponse, err := parseSIPResponse(packet[:count])
	if err != nil || statusResponse.StatusCode != 200 {
		return fmt.Errorf("status SIP response = (%#v, %v)", statusResponse, err)
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	statusACK, err := parseSIPPacket(packet[:count])
	if err != nil || statusACK.Request == nil || statusACK.Request.value("In-Reply-To") != "network-status-1" ||
		len(statusACK.Request.Body) != 2 || statusACK.Request.Body[0] != 0x02 || statusACK.Request.Body[1] != 0x2b {
		return fmt.Errorf("unexpected status RP-ACK %#v (%v)", statusACK.Request, err)
	}
	if statusACK.Request.value("P-Access-Network-Info") != registerPANI {
		return errors.New("status-report RP-ACK did not reuse REGISTER PANI")
	}
	if _, err = listener.WriteToUDP(testResponse(200, "OK", statusACK.Request.value("Call-ID"), statusACK.Request.value("CSeq"), nil), remote); err != nil {
		return err
	}
	close(readyForClose)

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if headers["expires"] != "0" {
		return errors.New("expected deregistration")
	}
	if headers["p-access-network-info"] != registerPANI {
		return errors.New("deregistration changed PANI")
	}
	_, err = listener.WriteToUDP(testResponse(200, "OK", registerCallID, headers["cseq"], nil), remote)
	return err
}

func TestSessionReceivesUSSIOverIMS(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second))

	received := make(chan ReceivedUSSD, 1)
	serverDone := make(chan error, 1)
	readyForClose := make(chan struct{})
	nonce := base64.StdEncoding.EncodeToString(make([]byte, 32))
	go func() { serverDone <- serveInboundUSSI(listener, nonce, readyForClose) }()
	provider, err := NewProvider(
		smsTestAKA{&recordingAKA{result: vowifi.AKAResult{RES: []byte{1, 2, 3, 4}}}},
		Config{
			PCSCF: listener.LocalAddr().String(), LocalAddress: "127.0.0.1",
			Transport: "udp", TransactionTimeout: 3 * time.Second, SecurityMode: SecurityDisabled,
			OnUSSD: func(_ context.Context, message ReceivedUSSD) error {
				received <- message
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.Start(context.Background(), vowifi.IMSRequest{
		DeviceID: "ec20",
		Identity: vowifi.SIMIdentity{IMSI: "001010123456789", HomeMCC: "001", HomeMNC: "01"},
		Tunnel: evidenceTunnel{evidence: vowifi.TunnelEvidence{
			Established: true, LocalIPv4: "127.0.0.1", PCSCF: []string{listener.LocalAddr().String()},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		if message.Text != "Main menu" || message.From != "sip:ussd@example.test" || message.CallID != "network-ussd-1" {
			t.Fatalf("received = %#v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for inbound USSI")
	}
	select {
	case <-readyForClose:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for USSI MESSAGE acceptance")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func serveInboundUSSI(listener *net.UDPConn, nonce string, readyForClose chan<- struct{}) error {
	packet := make([]byte, 65535)
	count, remote, err := listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err := parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	callID := headers["call-id"]
	if _, err = listener.WriteToUDP(testResponse(401, "Unauthorized", callID, headers["cseq"], []string{
		`WWW-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="` + nonce + `", algorithm=AKAv1-MD5, qop="auth"`,
	}), remote); err != nil {
		return err
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if _, err = listener.WriteToUDP(testResponse(200, "OK", callID, headers["cseq"], []string{
		"Contact: " + headers["contact"] + ";expires=600",
	}), remote); err != nil {
		return err
	}

	body := buildUSSDBody("Main menu")
	request := []byte(strings.Join([]string{
		"MESSAGE sip:001010123456789@ims.mnc001.mcc001.3gppnetwork.org SIP/2.0",
		"Via: SIP/2.0/UDP " + listener.LocalAddr().String() + ";branch=z9hG4bKussd",
		"From: <sip:ussd@example.test>;tag=gw",
		"To: <sip:001010123456789@ims.mnc001.mcc001.3gppnetwork.org>",
		"P-Asserted-Identity: <sip:ussd@example.test>",
		"Call-ID: network-ussd-1",
		"CSeq: 1 MESSAGE",
		"Content-Type: application/vnd.3gpp.ussd",
		"Content-Transfer-Encoding: binary",
		fmt.Sprintf("Content-Length: %d", len(body)), "", "",
	}, "\r\n"))
	request = append(request, body...)
	if _, err = listener.WriteToUDP(request, remote); err != nil {
		return err
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	response, err := parseSIPResponse(packet[:count])
	if err != nil || response.StatusCode != 200 {
		return fmt.Errorf("USSI MESSAGE response = (%#v, %v)", response, err)
	}
	close(readyForClose)

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if headers["expires"] != "0" {
		return errors.New("expected deregistration")
	}
	_, err = listener.WriteToUDP(testResponse(200, "OK", callID, headers["cseq"], nil), remote)
	return err
}

func TestSessionReceivesMalformedSMSBestEffort(t *testing.T) {
	request := &sipRequest{
		Headers: map[string][]string{
			"content-type":              {smsContentType},
			"content-transfer-encoding": {"binary"},
			"call-id":                   {"malformed-test"},
			"p-asserted-identity":       {"<sip:ipsmgw@example.test>"},
		},
		Body: []byte{0x01, 0x2a, 0x00, 0x00, 0x03, 0xff, 0xff, 0xff},
	}

	received := make(chan ReceivedSMS, 1)
	session := &Session{
		provider: &Provider{config: Config{
			Logger: slog.Default(),
			OnSMS: func(_ context.Context, message ReceivedSMS) error {
				received <- message
				return nil
			},
		}},
		request:         vowifi.IMSRequest{DeviceID: "ec20", Identity: vowifi.SIMIdentity{IMSI: "001010123456789", HomeMCC: "001", HomeMNC: "01"}},
		conn:            &fakeConn{},
		transactions:    make(map[sipTransactionKey]chan *sipResponse),
		fromTag:         "tag",
		nextRPReference: 1,
	}

	session.processSMSMessage(request)

	select {
	case message := <-received:
		if message.DecodeError == "" {
			t.Fatal("expected DecodeError to be set")
		}
		if message.RawRPDU == "" || message.RawTPDU == "" {
			t.Fatalf("expected raw payloads to be preserved, got %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for best-effort SMS callback")
	}
}

func TestSessionSuppressesSIMDataDownloadFromSMSInbox(t *testing.T) {
	tpdu, err := hex.DecodeString("440C914477009001237FF6250101000000002703700000AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte{0x01, 0x62, 0x00, 0x00, byte(len(tpdu))}
	body = append(body, tpdu...)
	called := false
	uiccCalled := false
	var reportBody []byte
	var session *Session
	conn := &fakeConn{}
	conn.onWrite = func(source []byte) {
		packet, parseErr := parseSIPPacket(source)
		if parseErr != nil || packet.Request == nil {
			return
		}
		cseq, method, parseErr := cseqNumber(packet.Request.value("CSeq"))
		if parseErr != nil || method != "MESSAGE" {
			return
		}
		reportBody = append([]byte(nil), packet.Request.Body...)
		session.dispatchPacket(sipPacket{Response: &sipResponse{StatusCode: 200, Headers: map[string][]string{
			"call-id": {packet.Request.value("Call-ID")},
			"cseq":    {fmt.Sprintf("%d MESSAGE", cseq)},
		}}}, nil)
	}
	session = &Session{
		provider: &Provider{config: Config{
			Logger:             slog.Default(),
			TransactionTimeout: time.Second,
			OnSIMDataDownload: func(_ context.Context, download SIMDataDownload) error {
				uiccCalled = true
				if download.DeviceID != "ec20" || download.PID != 0x7f || download.DCS != 0xf6 ||
					!bytes.Equal(download.TPDU, tpdu) {
					t.Fatalf("SIM data download = %#v", download)
				}
				return nil
			},
			OnSMS: func(context.Context, ReceivedSMS) error {
				called = true
				return nil
			},
		}},
		request:      vowifi.IMSRequest{DeviceID: "ec20"},
		conn:         conn,
		transactions: make(map[sipTransactionKey]chan *sipResponse),
	}
	// The write hook above feeds a synthetic SIP 200 response into this session.
	_ = session
	session.processSMSMessage(&sipRequest{
		Headers: map[string][]string{
			"content-type":              {smsContentType},
			"content-transfer-encoding": {"binary"},
			"call-id":                   {"sim-download-test"},
			"from":                      {"<sip:network@example.com>"},
		},
		Body: body,
	})
	if called {
		t.Fatal("SIM data download was delivered to the SMS inbox callback")
	}
	if !uiccCalled {
		t.Fatal("SIM data download was not delivered to the UICC callback")
	}
	if !bytes.Equal(reportBody, []byte{0x02, 0x62}) {
		t.Fatalf("SIM data download RP-ACK body = %X, want 0262", reportBody)
	}
}

func TestSessionAcknowledgesSIMDataDownloadWhenCallbackNil(t *testing.T) {
	tpdu, err := hex.DecodeString("440C914477009001237FF6250101000000002703700000AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte{0x01, 0x62, 0x00, 0x00, byte(len(tpdu))}
	body = append(body, tpdu...)
	called := false
	var reportBody []byte
	var session *Session
	conn := &fakeConn{}
	conn.onWrite = func(source []byte) {
		packet, parseErr := parseSIPPacket(source)
		if parseErr != nil || packet.Request == nil {
			return
		}
		cseq, method, parseErr := cseqNumber(packet.Request.value("CSeq"))
		if parseErr != nil || method != "MESSAGE" {
			return
		}
		reportBody = append([]byte(nil), packet.Request.Body...)
		session.dispatchPacket(sipPacket{Response: &sipResponse{StatusCode: 200, Headers: map[string][]string{
			"call-id": {packet.Request.value("Call-ID")},
			"cseq":    {fmt.Sprintf("%d MESSAGE", cseq)},
		}}}, nil)
	}
	session = &Session{
		provider: &Provider{config: Config{
			Logger:             slog.Default(),
			TransactionTimeout: time.Second,
			OnSIMDataDownload:  nil, // 模拟未注册 UICC 回调的场景
			OnSMS: func(context.Context, ReceivedSMS) error {
				called = true
				return nil
			},
		}},
		request:      vowifi.IMSRequest{DeviceID: "ec20"},
		conn:         conn,
		transactions: make(map[sipTransactionKey]chan *sipResponse),
	}
	session.processSMSMessage(&sipRequest{
		Headers: map[string][]string{
			"content-type":              {smsContentType},
			"content-transfer-encoding": {"binary"},
			"call-id":                   {"sim-download-nil-test"},
			"from":                      {"<sip:network@example.com>"},
		},
		Body: body,
	})
	if called {
		t.Fatal("SIM data download was delivered to the SMS inbox callback")
	}
	// 校验必须回执 RP-ACK (0x02 = RP-ACK, 0x62 = RP-Message Reference)
	if !bytes.Equal(reportBody, []byte{0x02, 0x62}) {
		t.Fatalf("SIM data download RP-ACK body = %X, want 0262", reportBody)
	}
}

func TestSessionAllowsSMSWhenContactConfirmed(t *testing.T) {
	session := &Session{
		provider: &Provider{config: Config{Logger: slog.Default()}},
		request: vowifi.IMSRequest{
			Identity: vowifi.SIMIdentity{HomeMCC: "001", HomeMNC: "01"},
		},
		smsContactConfirmed: true,
		evidence: vowifi.IMSEvidence{
			Registered:        true,
			RegistrationState: "registered",
		},
		expiresAt: time.Now().Add(time.Hour),
	}

	evidence, err := session.EnableSMS(context.Background())
	if err != nil || !evidence.Ready {
		t.Fatalf("EnableSMS() = (%#v, %v), want ready when contact confirmed", evidence, err)
	}
}

func TestSessionRequiresSMSContactConfirmationByDefault(t *testing.T) {
	session := &Session{
		provider: &Provider{config: Config{Logger: slog.Default()}},
		request: vowifi.IMSRequest{
			Identity: vowifi.SIMIdentity{HomeMCC: "001", HomeMNC: "01"},
		},
		evidence: vowifi.IMSEvidence{
			Registered:        true,
			RegistrationState: "registered",
		},
		expiresAt: time.Now().Add(time.Hour),
	}

	evidence, err := session.EnableSMS(context.Background())
	if !errors.Is(err, ErrSMSCapabilityNotConfirmed) || evidence.Ready {
		t.Fatalf("EnableSMS() = (%#v, %v), want not-ready", evidence, err)
	}
}

func buildUSSDBody(text string) []byte {
	encoded, dcs, err := encodeUSSDBody(text)
	if err != nil {
		panic(err)
	}
	return append([]byte{byte(len(encoded) + 1), byte(*dcs)}, encoded...)
}

func TestSessionSendsUSSIOverIMS(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(10 * time.Second))
	serverDone := make(chan error, 1)
	readyForClose := make(chan struct{})
	nonce := base64.StdEncoding.EncodeToString(make([]byte, 32))
	go func() { serverDone <- serveOutboundUSSI(listener, nonce, readyForClose) }()
	provider, err := NewProvider(
		smsTestAKA{&recordingAKA{result: vowifi.AKAResult{RES: []byte{1, 2, 3, 4}}}},
		Config{
			PCSCF: listener.LocalAddr().String(), LocalAddress: "127.0.0.1",
			Transport: "udp", TransactionTimeout: 3 * time.Second, SecurityMode: SecurityDisabled,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.Start(context.Background(), vowifi.IMSRequest{
		DeviceID: "ec20",
		Identity: vowifi.SIMIdentity{IMSI: "001010123456789", HomeMCC: "001", HomeMNC: "01"},
		Tunnel: evidenceTunnel{evidence: vowifi.TunnelEvidence{
			Established: true, LocalIPv4: "127.0.0.1", PCSCF: []string{listener.LocalAddr().String()},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.(vowifi.USSISender).SendUSSI(context.Background(), vowifi.USSISubmitRequest{Code: "*100#"})
	if err != nil {
		t.Fatalf("SendUSSI error = %v", err)
	}
	if result.Status != "final" || result.Text != "Reply" || result.SIPCode != 200 {
		t.Fatalf("SendUSSI result = %#v", result)
	}
	select {
	case <-readyForClose:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for USSI transaction to complete")
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func serveOutboundUSSI(listener *net.UDPConn, nonce string, readyForClose chan<- struct{}) error {
	packet := make([]byte, 65535)
	count, remote, err := listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err := parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	registerCallID := headers["call-id"]
	if _, err = listener.WriteToUDP(testResponse(401, "Unauthorized", registerCallID, headers["cseq"], []string{
		`WWW-Authenticate: Digest realm="ims.mnc001.mcc001.3gppnetwork.org", nonce="` + nonce + `", algorithm=AKAv1-MD5, qop="auth"`,
	}), remote); err != nil {
		return err
	}
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if _, err = listener.WriteToUDP(testResponse(200, "OK", registerCallID, headers["cseq"], []string{
		"Contact: " + headers["contact"] + ";expires=600",
	}), remote); err != nil {
		return err
	}

	// 1. Expect outbound INVITE for USSI dialog initiation (3GPP TS 24.390)
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	message, err := parseSIPPacket(packet[:count])
	if err != nil || message.Request == nil {
		return fmt.Errorf("outbound INVITE parse: %v", err)
	}
	if message.Request.Method != "INVITE" ||
		!strings.HasPrefix(message.Request.URI, "sip:") ||
		!strings.Contains(message.Request.URI, "user=dialstring") ||
		message.Request.value("Recv-Info") != ussiInfoPackage {
		return fmt.Errorf("unexpected outbound INVITE %#v", message.Request)
	}
	text, _, _, _, err := extractUSSDPayloadFromSIP(message.Request.Body, message.Request.value("Content-Type"))
	if err != nil || text != "*100#" {
		return fmt.Errorf("extracted USSI text = %q (err=%v), want *100#", text, err)
	}

	// 2. Network responds 200 OK to INVITE with SDP port 0
	inviteCallID := message.Request.value("Call-ID")
	inviteCSeq := message.Request.value("CSeq")
	from := message.Request.value("From")
	to := message.Request.value("To") + ";tag=server-tag-1"
	contact := "<sip:as@127.0.0.1:5060>"
	sdp := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 0 RTP/AVP 96\r\n"
	invite200 := []byte(strings.Join([]string{
		"SIP/2.0 200 OK",
		"Via: " + message.Request.value("Via"),
		"From: " + from,
		"To: " + to,
		"Call-ID: " + inviteCallID,
		"CSeq: " + inviteCSeq,
		"Contact: " + contact,
		"Content-Type: application/sdp",
		fmt.Sprintf("Content-Length: %d", len(sdp)),
		"", "",
	}, "\r\n") + sdp)
	if _, err = listener.WriteToUDP(invite200, remote); err != nil {
		return err
	}

	// 3. UE sends ACK for 200 OK
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	ackMsg, err := parseSIPPacket(packet[:count])
	if err != nil || ackMsg.Request == nil || ackMsg.Request.Method != "ACK" {
		return fmt.Errorf("expected ACK, got %#v", ackMsg)
	}

	// 4. Network sends BYE with application/vnd.3gpp.ussd+xml containing result
	byeXML := `<?xml version="1.0" encoding="UTF-8"?><ussd-data xmlns="urn:oma:xml:ussd:ussd-data"><language>en</language><ussd-string>Reply</ussd-string></ussd-data>`
	clientContact := headerURI(message.Request.value("Contact"))
	if clientContact == "" {
		clientContact = "sip:ec20@127.0.0.1:5060"
	}
	byeReq := []byte(strings.Join([]string{
		"BYE " + clientContact + " SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKbyesrv1",
		"Max-Forwards: 70",
		"From: " + to,
		"To: " + from,
		"Call-ID: " + inviteCallID,
		"CSeq: 1 BYE",
		"Content-Type: application/vnd.3gpp.ussd+xml",
		fmt.Sprintf("Content-Length: %d", len(byeXML)),
		"", "",
	}, "\r\n") + byeXML)
	if _, err = listener.WriteToUDP(byeReq, remote); err != nil {
		return err
	}

	// 5. UE responds 200 OK to BYE
	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	byeResp, err := parseSIPPacket(packet[:count])
	if err != nil || byeResp.Response == nil || byeResp.Response.StatusCode != 200 {
		return fmt.Errorf("expected 200 OK for BYE, got %#v", byeResp)
	}

	close(readyForClose)

	count, remote, err = listener.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	_, headers, err = parseTestRequest(packet[:count])
	if err != nil {
		return err
	}
	if headers["expires"] != "0" {
		return errors.New("expected deregistration")
	}
	_, err = listener.WriteToUDP(testResponse(200, "OK", registerCallID, headers["cseq"], nil), remote)
	return err
}

func verifySIPMessageAuthorization(
	value string,
	nonce string,
	uri string,
	wantQOP string,
	wantNC string,
) error {
	scheme, parameters, found := strings.Cut(value, " ")
	if !found || scheme != "Digest" {
		return errors.New("invalid SIP Digest scheme")
	}
	directives, err := parseAuthDirectives(parameters)
	if err != nil {
		return err
	}
	expected := digestResponse(
		"001010123456789@ims.mnc001.mcc001.3gppnetwork.org",
		"ims.mnc001.mcc001.3gppnetwork.org",
		[]byte{1, 2, 3, 4},
		"MESSAGE",
		uri,
		nonce,
		directives["nc"],
		directives["cnonce"],
		directives["qop"],
	)
	if directives["username"] != "001010123456789@ims.mnc001.mcc001.3gppnetwork.org" ||
		directives["realm"] != "ims.mnc001.mcc001.3gppnetwork.org" ||
		directives["nonce"] != nonce ||
		directives["uri"] != uri ||
		directives["algorithm"] != "AKAv1-MD5" ||
		directives["response"] != expected {
		return fmt.Errorf("digest directives = %#v", directives)
	}
	if directives["qop"] != wantQOP || directives["nc"] != wantNC {
		return fmt.Errorf("digest qop/nc = %q/%q, want %q/%q",
			directives["qop"], directives["nc"], wantQOP, wantNC)
	}
	if wantQOP == "" && directives["cnonce"] != "" {
		return errors.New("digest without qop unexpectedly included cnonce")
	}
	if wantQOP != "" && directives["cnonce"] == "" {
		return errors.New("digest with qop omitted cnonce")
	}
	return nil
}

func TestO2SMSBareUnauthorizedFallbackVariants(t *testing.T) {
	session := &Session{
		request: vowifi.IMSRequest{
			Identity: vowifi.SIMIdentity{
				ICCID: "8944110000000000001", IMSI: "234109999999999",
				HomeMCC: "234", HomeMNC: "10",
			},
		},
		identity: identitySet{
			user:    "234109999999999",
			private: "234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
			public:  "sip:234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
			domain:  "ims.mnc010.mcc234.3gppnetwork.org",
		},
	}
	target := "sip:+447802092035@o2.co.uk;user=phone"
	variants := session.smsMessageVariants(target, "+447802092035")
	if len(variants) != 5 {
		t.Fatalf("O2 fallback variant count = %d, want 5: %#v", len(variants), variants)
	}
	for index, variant := range variants {
		if variant.target != target {
			t.Fatalf("O2 fallback target[%d] = %q, want %q", index, variant.target, target)
		}
		if index > 0 && (variant.options.headerMode != smsMessageHeadersMinimal ||
			!variant.options.useContactVia ||
			variant.options.authorizationMode != smsAuthorizationNone ||
			variant.options.ppiMode != smsMessagePPIFromIdentity) {
			t.Fatalf("O2 fallback options[%d] = %#v", index, variant.options)
		}
	}
	wantPANI := []string{
		"IEEE-802.11;country=GB;i-wlan-node-id=820000000001",
		"IEEE-802.11;i-wlan-node-id=ffffffffffff",
		"IEEE-802.11;country=GB;i-wlan-node-id=ffffffffffff",
		"IEEE-802.11",
		"3GPP-WLAN",
	}
	for index, want := range wantPANI {
		if variants[index].options.paniOverride != want {
			t.Fatalf("O2 PANI[%d] = %q, want %q", index, variants[index].options.paniOverride, want)
		}
	}

	standard := &Session{request: vowifi.IMSRequest{Identity: vowifi.SIMIdentity{
		ICCID: "8999990000000000001", IMSI: "999990000000001",
		HomeMCC: "999", HomeMNC: "99",
	}}}
	if got := standard.smsMessageVariants(target, "+447802092035"); len(got) != 1 {
		t.Fatalf("standard profile fallback variants = %#v, want one", got)
	}
}

func TestO2SMSBareUnauthorizedFallbackStopsAfterAcceptedTarget(t *testing.T) {
	session := &Session{
		provider: &Provider{config: Config{TransactionTimeout: time.Second}},
		request: vowifi.IMSRequest{
			DeviceID: "reader",
			Identity: vowifi.SIMIdentity{
				ICCID: "8944110000000000001", IMSI: "234109999999999",
				HomeMCC: "234", HomeMNC: "10",
			},
		},
		identity: identitySet{
			user:    "234109999999999",
			private: "234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
			public:  "sip:234109999999999@ims.mnc010.mcc234.3gppnetwork.org",
			domain:  "ims.mnc010.mcc234.3gppnetwork.org",
		},
		evidence:     vowifi.IMSEvidence{AssociatedIdentities: []string{"tel:+447700900000"}},
		endpoint:     pcscfEndpoint{host: "127.0.0.1", port: 5060},
		transport:    "tcp",
		fromTag:      "fallback-tag",
		cseq:         1,
		transactions: make(map[sipTransactionKey]chan *sipResponse),
	}
	conn := &identityTestConn{}
	session.conn = conn
	var requests []*sipRequest
	conn.onWrite = func(packet []byte) (int, error) {
		parsed, err := parseSIPPacket(packet)
		if err != nil || parsed.Request == nil {
			return 0, fmt.Errorf("parse outgoing MESSAGE: %v", err)
		}
		requests = append(requests, parsed.Request)
		response := &sipResponse{StatusCode: 202, Headers: map[string][]string{
			"call-id": {parsed.Request.value("Call-ID")},
			"cseq":    {parsed.Request.value("CSeq")},
		}}
		if len(requests) == 1 {
			response = &sipResponse{StatusCode: 403, Headers: map[string][]string{
				"call-id": {parsed.Request.value("Call-ID")},
				"cseq":    {parsed.Request.value("CSeq")},
			}}
		}
		session.dispatchPacket(sipPacket{Response: response}, nil)
		return len(packet), nil
	}

	response, err := session.sendSMSMessageWithFallbacks(
		context.Background(),
		"sip:+447802092035@o2.co.uk;user=phone",
		"+447802092035",
		[]byte{0x00, 0x01},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.StatusCode != 202 {
		t.Fatalf("fallback response = %#v, want 202", response)
	}
	if len(requests) != 2 {
		t.Fatalf("MESSAGE request count = %d, want 2", len(requests))
	}
	if requests[1].URI != "sip:+447802092035@o2.co.uk;user=phone" {
		t.Fatalf("fallback URI = %q", requests[1].URI)
	}
	if requests[0].value("From") == requests[1].value("From") {
		t.Fatal("fallback reused the previous MESSAGE From tag")
	}
	if requests[1].value("Authorization") != "" ||
		!strings.Contains(requests[1].value("From"), "<tel:+447700900000>") ||
		requests[1].value("P-Preferred-Identity") != "<tel:+447700900000>" {
		t.Fatalf("tel-no-auth fallback headers = %#v", requests[1].Headers)
	}
}

// fakeConn is a minimal net.Conn useful for tests that only need LocalAddr
// to succeed and do not care about the actual SIP MESSAGE delivery report.
type fakeConn struct {
	onWrite func([]byte)
}

func (*fakeConn) Read([]byte) (int, error) { return 0, errors.New("fakeConn: closed") }
func (conn *fakeConn) Write(source []byte) (int, error) {
	if conn.onWrite != nil {
		conn.onWrite(source)
	}
	return len(source), nil
}
func (*fakeConn) Close() error { return nil }
func (*fakeConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 5060}
}
func (*fakeConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 20), Port: 5060}
}
func (*fakeConn) SetDeadline(time.Time) error      { return nil }
func (*fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (*fakeConn) SetWriteDeadline(time.Time) error { return nil }
