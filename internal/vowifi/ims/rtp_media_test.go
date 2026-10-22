package ims

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRTPMediaCarriesPCMOverPCMA(t *testing.T) {
	left, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if err := left.configureRemote(right.offerSDP(net.IPv4(127, 0, 0, 1))); err != nil {
		t.Fatal(err)
	}
	if err := right.configureRemote(left.answerSDP(net.IPv4(127, 0, 0, 1))); err != nil {
		t.Fatal(err)
	}
	want := make([]int16, rtpPacketSamples)
	for index := range want {
		want[index] = int16(9000 * math.Sin(float64(index)*2*math.Pi/40))
	}
	if err := left.WritePCM(want); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := right.ReadPCM(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("received %d samples, want %d", len(got), len(want))
	}
	for index := range got {
		if difference := math.Abs(float64(got[index]) - float64(want[index])); difference > 700 {
			t.Fatalf("sample %d difference %.0f exceeds G.711 tolerance", index, difference)
		}
	}
}

func TestParseAudioSDPRejectsMissingEndpoint(t *testing.T) {
	if _, _, _, _, err := parseAudioSDP([]byte("v=0\r\nm=audio 0 RTP/AVP 8\r\n")); err == nil {
		t.Fatal("expected unusable SDP error")
	}
}

func TestO2OfferUsesAMRDualPayloadAndDTMF(t *testing.T) {
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR-WB", "AMR", "PCMA", "PCMU")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	sdp := string(media.offerSDP(net.IPv4(127, 0, 0, 1)))
	for _, want := range []string{
		"m=audio ",
		"RTP/AVP 97 98 99 100 8 0 101 102",
		"a=rtpmap:97 AMR-WB/16000",
		"a=fmtp:97 octet-align=0",
		"a=rtpmap:98 AMR-WB/16000",
		"a=fmtp:98 octet-align=1",
		"a=rtpmap:99 AMR/8000",
		"a=fmtp:99 octet-align=0",
		"a=rtpmap:100 AMR/8000",
		"a=fmtp:100 octet-align=1",
		"a=rtpmap:101 telephone-event/16000",
		"a=rtpmap:102 telephone-event/8000",
	} {
		if !strings.Contains(sdp, want) {
			t.Fatalf("O2 SDP omits %q:\n%s", want, sdp)
		}
	}
}

func TestRTPMediaCarriesPCMOverAMRWB(t *testing.T) {
	assertAMRRoundTrip(t, "AMR-WB")
}

func TestRTPMediaCarriesPCMOverAMRNB(t *testing.T) {
	assertAMRRoundTrip(t, "AMR")
}

func assertAMRRoundTrip(t *testing.T, codec string) {
	t.Helper()
	left, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if err := left.configureRemote(right.offerSDP(net.IPv4(127, 0, 0, 1))); err != nil {
		t.Fatal(err)
	}
	answer := left.answerSDP(net.IPv4(127, 0, 0, 1))
	wantFormats := "RTP/AVP 100 102"
	if codec == "AMR-WB" {
		wantFormats = "RTP/AVP 98 101"
	}
	if !strings.Contains(string(answer), wantFormats) {
		t.Fatalf("%s answer does not contain %q:\n%s", codec, wantFormats, answer)
	}
	if err := right.configureRemote(answer); err != nil {
		t.Fatal(err)
	}
	if left.Codec() != codec || right.Codec() != codec {
		t.Fatalf("negotiated codecs = %q/%q, want %q", left.Codec(), right.Codec(), codec)
	}

	input := make([]int16, 320)
	for index := range input {
		input[index] = int16(9000 * math.Sin(float64(index)*2*math.Pi/80))
	}
	if err := left.WritePCM(input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output := make([]int16, 0, len(input))
	for len(output) < len(input) {
		chunk, readErr := right.ReadPCM(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		output = append(output, chunk...)
	}
	if len(output) != len(input) {
		t.Fatalf("received %d samples, want %d", len(output), len(input))
	}
	var energy float64
	for _, sample := range output {
		energy += float64(sample) * float64(sample)
	}
	if energy == 0 {
		t.Fatal("AMR round trip returned silence")
	}
}

func TestParseAudioSDPReadsAMRFMTP(t *testing.T) {
	body := []byte("v=0\r\nm=audio 49170 RTP/AVPF 98\r\nc=IN IP4 192.0.2.1\r\na=rtcp-mux\r\na=rtcp-rsize\r\na=rtpmap:98 AMR-WB/16000\r\na=fmtp:98 octet-align=1;mode-set=0,1,2\r\n")
	_, _, formats, mappings, err := parseAudioSDP(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(formats) != 1 || formats[0] != "98" {
		t.Fatalf("formats = %#v", formats)
	}
	mapping := mappings[98]
	if mapping.Name != "AMR-WB" || mapping.ClockRate != 16000 ||
		!parseFMTPBool(mapping.FMTP, "octet-align", false) {
		t.Fatalf("mapping = %#v", mapping)
	}
	if mode, ok := selectAMRWBMode(mapping.FMTP); !ok || mode != 2 {
		t.Fatalf("selected AMR-WB mode = %d/%t, want 2/true", mode, ok)
	}
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR-WB")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	if err := media.configureRemote(body); err != nil {
		t.Fatal(err)
	}
	answer := string(media.answerSDP(net.IPv4(127, 0, 0, 1)))
	for _, want := range []string{"RTP/AVPF", "a=rtcp-mux", "a=rtcp-rsize", "a=fmtp:98 octet-align=1;mode-set=0,1,2"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer omits %q:\n%s", want, answer)
		}
	}
}

func TestConfigureRemoteMatchesDTMFClockAndAMRModeSet(t *testing.T) {
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR", "AMR-WB")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	body := []byte(strings.Join([]string{
		"v=0",
		"m=audio 49170 RTP/AVP 99 101 102",
		"c=IN IP4 192.0.2.1",
		"a=rtpmap:99 AMR/8000",
		"a=fmtp:99 octet-align=1;mode-set=0,2",
		"a=rtpmap:101 telephone-event/16000",
		"a=rtpmap:102 telephone-event/8000",
		"",
	}, "\r\n"))
	if err := media.configureRemote(body); err != nil {
		t.Fatal(err)
	}
	if media.Codec() != "AMR" || media.dtmfPayloadType != 102 || media.modeSet != "0,2" {
		t.Fatalf("negotiated media = codec=%q dtmf=%d mode-set=%q",
			media.Codec(), media.dtmfPayloadType, media.modeSet)
	}
	if mode, ok := selectAMRNBMode("mode-set=" + media.modeSet); !ok || mode != 2 {
		t.Fatalf("selected AMR-NB mode = %d/%t, want 2/true", mode, ok)
	}
	answer := string(media.answerSDP(net.IPv4(127, 0, 0, 1)))
	if !strings.Contains(answer, "a=fmtp:99 octet-align=1;mode-set=0,2") {
		t.Fatalf("answer dropped negotiated AMR FMTP:\n%s", answer)
	}
}

func TestO2AnswerPreservesAMRWBAndDTMFFMTP(t *testing.T) {
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR-WB", "AMR")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	body := []byte(strings.Join([]string{
		"v=0",
		"m=audio 49170 RTP/AVP 96 97 98 101 102 112 99 100",
		"c=IN IP4 192.0.2.1",
		"a=rtpmap:96 AMR-WB/16000",
		"a=fmtp:96 mode-set=0,1,2;mode-change-period=2;mode-change-neighbor=1;mode-change-capability=2;max-red=0",
		"a=rtpmap:97 AMR/8000",
		"a=fmtp:97 mode-set=0,2,4,7;max-red=0",
		"a=rtpmap:98 AMR/8000",
		"a=fmtp:98 mode-set=7;max-red=0",
		"a=rtpmap:101 AMR-WB/16000",
		"a=rtpmap:102 AMR/8000",
		"a=rtpmap:112 EVS/16000",
		"a=rtpmap:99 telephone-event/16000",
		"a=fmtp:99 0-15",
		"a=rtpmap:100 telephone-event/8000",
		"a=fmtp:100 0-15",
		"",
	}, "\r\n"))
	if err := media.configureRemote(body); err != nil {
		t.Fatal(err)
	}
	if media.codec != "AMR-WB" || media.payloadType != 96 ||
		media.dtmfPayloadType != 99 || media.dtmfFMTP != "0-15" {
		t.Fatalf("negotiated O2 media = codec=%q payload=%d dtmf=%d dtmf-fmtp=%q",
			media.codec, media.payloadType, media.dtmfPayloadType, media.dtmfFMTP)
	}
	answer := string(media.answerSDP(net.IPv4(127, 0, 0, 1)))
	for _, want := range []string{
		" RTP/AVP 96 99",
		"a=rtpmap:96 AMR-WB/16000",
		"a=fmtp:96 mode-set=0,1,2;mode-change-period=2;mode-change-neighbor=1;mode-change-capability=2;max-red=0",
		"a=rtpmap:99 telephone-event/16000",
		"a=fmtp:99 0-15",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("O2 answer omits %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "a=fmtp:99 0-16") {
		t.Fatalf("O2 answer changed DTMF event range:\n%s", answer)
	}
}

func TestConfigureRemotePrefersOctetAlignedAMRVariant(t *testing.T) {
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR-WB")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	body := []byte(strings.Join([]string{
		"v=0",
		"m=audio 49170 RTP/AVP 96 98 101",
		"c=IN IP4 192.0.2.1",
		"a=rtpmap:96 AMR-WB/16000",
		"a=fmtp:96 octet-align=0",
		"a=rtpmap:98 AMR-WB/16000",
		"a=fmtp:98 octet-align=1",
		"",
	}, "\r\n"))
	if err := media.configureRemote(body); err != nil {
		t.Fatal(err)
	}
	if media.Codec() != "AMR-WB" || media.payloadType != 98 || !media.octetAligned {
		t.Fatalf("selected AMR variant = codec=%q payload=%d octet=%t",
			media.Codec(), media.payloadType, media.octetAligned)
	}
}

func TestConfigureRemotePrefersHigherO2AMRVariantWithoutFMTP(t *testing.T) {
	media, err := newRTPMedia(net.IPv4(127, 0, 0, 1), nil, "AMR-WB")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	body := []byte(strings.Join([]string{
		"v=0",
		"m=audio 49170 RTP/AVP 96 98",
		"c=IN IP4 192.0.2.1",
		"a=rtpmap:96 AMR-WB/16000",
		"a=rtpmap:98 AMR-WB/16000",
		"a=rtpmap:101 AMR-WB/16000",
		"",
	}, "\r\n"))
	if err := media.configureRemote(body); err != nil {
		t.Fatal(err)
	}
	if media.Codec() != "AMR-WB" || media.payloadType != 98 {
		t.Fatalf("selected O2 AMR variant = codec=%q payload=%d", media.Codec(), media.payloadType)
	}
}
