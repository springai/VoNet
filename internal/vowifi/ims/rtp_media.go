package ims

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	amrnb "github.com/VoiceBlender/goamr-nb"
	amrwb "github.com/VoiceBlender/goamr-wb"
)

const (
	rtpClockRate     = 8000
	rtpPacketSamples = 160

	// silenceSuppressWindow is the duration after a real audio write during which
	// the silence sender suppresses its packets to avoid interleaving artifacts.
	silenceSuppressWindow = 400 * time.Millisecond
)

type rtpMedia struct {
	conn *net.UDPConn

	mu              sync.RWMutex
	remote          *net.UDPAddr
	codec           string
	payloadType     byte
	sampleRate      int
	frameSamples    int
	octetAligned    bool
	modeSet         string
	selectedFMTP    string
	rtpProfile      string
	rtcpMux         bool
	rtcpRsize       bool
	dtmfPayloadType byte
	dtmfFMTP        string
	encodeFrame     func([]int16) ([]byte, error)
	decodeFrame     func([]byte) ([]int16, error)

	writeMu   sync.Mutex
	pending   []int16
	sequence  uint16
	timestamp uint32
	ssrc      uint32

	downlink           chan []int16
	closed             chan struct{}
	close              sync.Once
	borrowed           bool
	lastRealAudioWrite time.Time
	offerPreferences   []string
}

type sdpAudioCodec struct {
	Name      string
	ClockRate int
	FMTP      string
}

type sdpOffer struct {
	formats    string
	attributes []string
}

func newRTPMedia(local net.IP, sipConn net.Conn, codecPreferences ...string) (*rtpMedia, error) {
	var connection *net.UDPConn
	borrowed := false
	if sipConn != nil {
		if udpConn, ok := sipConn.(*net.UDPConn); ok {
			connection = udpConn
			borrowed = true
		}
	}
	if connection == nil {
		address := &net.UDPAddr{IP: local, Port: 0}
		var err error
		connection, err = net.ListenUDP("udp", address)
		if err != nil {
			return nil, fmt.Errorf("ims: open RTP socket: %w", err)
		}
	}
	seed := make([]byte, 10)
	if _, err := io.ReadFull(cryptorand.Reader, seed); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("ims: initialize RTP state: %w", err)
	}
	media := &rtpMedia{
		conn: connection, sequence: binary.BigEndian.Uint16(seed[:2]),
		timestamp: binary.BigEndian.Uint32(seed[2:6]), ssrc: binary.BigEndian.Uint32(seed[6:]),
		downlink: make(chan []int16, 64), closed: make(chan struct{}),
		borrowed: borrowed, offerPreferences: append([]string(nil), codecPreferences...),
	}
	go media.receive()
	return media, nil
}

func (media *rtpMedia) Codec() string {
	media.mu.RLock()
	defer media.mu.RUnlock()
	return media.codec
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func offerCodecValues(preferences []string) sdpOffer {
	allowed := map[string]bool{"AMR-WB": true, "AMR": true, "PCMA": true, "PCMU": true}
	selected := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	for _, value := range preferences {
		name := strings.ToUpper(strings.TrimSpace(value))
		if name == "AMR-WB" || name == "AMR" || name == "PCMA" || name == "PCMU" {
			if allowed[name] && !seen[name] {
				selected = append(selected, name)
				seen[name] = true
			}
		}
	}
	if len(selected) == 0 {
		selected = []string{"PCMA", "PCMU"}
	}

	formats := make([]string, 0, 8)
	attributes := make([]string, 0, 12)
	for _, name := range selected {
		switch name {
		case "AMR-WB":
			formats = append(formats, "97", "98")
			attributes = append(attributes,
				"a=rtpmap:97 AMR-WB/16000",
				"a=fmtp:97 octet-align=0",
				"a=rtpmap:98 AMR-WB/16000",
				"a=fmtp:98 octet-align=1",
			)
		case "AMR":
			formats = append(formats, "99", "100")
			attributes = append(attributes,
				"a=rtpmap:99 AMR/8000",
				"a=fmtp:99 octet-align=0",
				"a=rtpmap:100 AMR/8000",
				"a=fmtp:100 octet-align=1",
			)
		case "PCMA":
			formats = append(formats, "8")
			attributes = append(attributes, "a=rtpmap:8 PCMA/8000")
		case "PCMU":
			formats = append(formats, "0")
			attributes = append(attributes, "a=rtpmap:0 PCMU/8000")
		}
	}
	if seen["AMR-WB"] {
		formats = append(formats, "101")
		attributes = append(attributes, "a=rtpmap:101 telephone-event/16000", "a=fmtp:101 0-16")
	}
	if seen["AMR"] || !seen["AMR-WB"] {
		payload := 102
		if !seen["AMR"] && !seen["AMR-WB"] {
			payload = 100
		}
		formats = append(formats, strconv.Itoa(payload))
		attributes = append(attributes,
			fmt.Sprintf("a=rtpmap:%d telephone-event/8000", payload),
			fmt.Sprintf("a=fmtp:%d 0-16", payload),
		)
	}
	return sdpOffer{formats: strings.Join(formats, " "), attributes: attributes}
}

func (media *rtpMedia) ready() bool {
	media.mu.RLock()
	defer media.mu.RUnlock()
	return media.remote != nil && media.codec != ""
}

func (media *rtpMedia) offerSDP(local net.IP, preferences ...string) []byte {
	if len(preferences) == 0 {
		preferences = media.offerPreferences
	}
	values := offerCodecValues(preferences)
	return media.buildSDP(local, values.formats, values.attributes)
}

func (media *rtpMedia) answerSDP(local net.IP) []byte {
	media.mu.RLock()
	codec, payload := media.codec, media.payloadType
	sampleRate, octetAligned, modeSet := media.sampleRate, media.octetAligned, media.modeSet
	selectedFMTP := media.selectedFMTP
	dtmfPayloadType := media.dtmfPayloadType
	dtmfFMTP := media.dtmfFMTP
	media.mu.RUnlock()
	if codec == "" {
		return media.offerSDP(local)
	}
	attributes := []string{fmt.Sprintf("a=rtpmap:%d %s/%d", payload, codec, sampleRate)}
	if codec == "AMR-WB" || codec == "AMR" {
		fmtp := selectedFMTP
		if fmtp == "" {
			fmtp = fmt.Sprintf("octet-align=%d", boolToInt(octetAligned))
			if modeSet != "" {
				fmtp += ";mode-set=" + modeSet
			}
		}
		attributes = append(attributes, fmt.Sprintf("a=fmtp:%d %s", payload, fmtp))
	}
	if dtmfPayloadType != 0 {
		attributes = append(attributes, fmt.Sprintf("a=rtpmap:%d telephone-event/%d", dtmfPayloadType, sampleRate))
		if strings.TrimSpace(dtmfFMTP) != "" {
			attributes = append(attributes, fmt.Sprintf("a=fmtp:%d %s", dtmfPayloadType, dtmfFMTP))
		}
	}
	formats := strconv.Itoa(int(payload))
	if dtmfPayloadType != 0 {
		formats += " " + strconv.Itoa(int(dtmfPayloadType))
	}
	if media.rtcpMux {
		attributes = append(attributes, "a=rtcp-mux")
	}
	if media.rtcpRsize {
		attributes = append(attributes, "a=rtcp-rsize")
	}
	return media.buildSDPWithProfile(local, formats, attributes, media.rtpProfile)
}

func (media *rtpMedia) buildSDP(local net.IP, formats string, attributes []string) []byte {
	return media.buildSDPWithProfile(local, formats, attributes, "RTP/AVP")
}

func (media *rtpMedia) buildSDPWithProfile(local net.IP, formats string, attributes []string, profile string) []byte {
	if local == nil || local.IsUnspecified() {
		if udp, ok := media.conn.LocalAddr().(*net.UDPAddr); ok {
			local = udp.IP
		}
	}
	if local == nil || local.IsUnspecified() {
		local = net.IPv4zero
	}
	family := "IP4"
	if local.To4() == nil {
		family = "IP6"
	}
	port := media.conn.LocalAddr().(*net.UDPAddr).Port
	if strings.TrimSpace(profile) == "" {
		profile = "RTP/AVP"
	}
	sessionID := time.Now().UnixNano()
	lines := []string{
		"v=0",
		fmt.Sprintf("o=- %d %d IN %s %s", sessionID, sessionID, family, local.String()),
		"s=VoNet",
		fmt.Sprintf("c=IN %s %s", family, local.String()),
		"t=0 0",
		fmt.Sprintf("m=audio %d %s %s", port, profile, formats),
	}
	if attributes == nil {
		lines = append(lines,
			"a=rtpmap:8 PCMA/8000",
			"a=rtpmap:0 PCMU/8000",
			"a=rtpmap:100 telephone-event/8000",
			"a=fmtp:100 0-15",
		)
	} else {
		lines = append(lines, attributes...)
	}
	lines = append(lines, "a=ptime:20", "a=sendrecv", "")
	return []byte(strings.Join(lines, "\r\n"))
}

func (media *rtpMedia) configureRemote(body []byte) error {
	address, port, formats, mappings, err := parseAudioSDP(body)
	if err != nil {
		return err
	}
	var codec string
	var payload byte
	var sampleRate, frameSamples int
	var octetAligned bool
	var selectedFMTP string
	dtmfPayloads := make(map[int]byte)
	for _, value := range formats {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 0 || parsed > 127 {
			continue
		}
		mapping := mappings[parsed]
		name := strings.ToUpper(mapping.Name)
		fmt.Printf("[RTP] offer candidate: payload=%d name=%q rate=%d fmtp=%q\n",
			parsed, mapping.Name, mapping.ClockRate, mapping.FMTP)
		if name == "" {
			switch parsed {
			case 0:
				name = "PCMU"
			case 8:
				name = "PCMA"
			default:
				continue
			}
		}
		if name == "TELEPHONE-EVENT" {
			rate := mapping.ClockRate
			if rate == 0 {
				rate = 8000
			}
			dtmfPayloads[rate] = byte(parsed)
			continue
		}
		octet := parseFMTPBool(mapping.FMTP, "octet-align", false)
		if name != "PCMA" && name != "PCMU" && name != "AMR-WB" && name != "AMR" {
			continue
		}
		if codec != "" && codec != name {
			continue
		}
		if codec == "" {
			codec, payload, octetAligned, selectedFMTP = name, byte(parsed), octet, mapping.FMTP
			if name == "AMR-WB" || name == "AMR" {
				if name == "AMR-WB" {
					sampleRate, frameSamples = amrwb.SampleRate, amrwb.FrameSamples
				} else {
					sampleRate, frameSamples = amrnb.SampleRate, amrnb.FrameSamples
				}
			} else {
				sampleRate, frameSamples = 8000, rtpPacketSamples
			}
			continue
		}
		// Prefer the O2/IMS octet-aligned AMR variant when the offer contains
		// both framing modes. The codec implementation is created below after
		// the final framing choice is known.
		if octet && !octetAligned {
			octetAligned = true
			payload = byte(parsed)
			selectedFMTP = mapping.FMTP
		} else if (name == "AMR-WB" || name == "AMR") && amrPayloadRank(name, parsed) > amrPayloadRank(name, int(payload)) {
			// Android O2 uses fixed dynamic payload families:
			// AMR-WB 97/98 and AMR-NB 99/100. Prefer those over unrelated
			// higher payload numbers even when fmtp is omitted.
			payload = byte(parsed)
			selectedFMTP = mapping.FMTP
		}
	}
	if codec == "" {
		return errors.New("ims: remote SDP has no supported audio format")
	}
	var encodeFrame func([]int16) ([]byte, error)
	var decodeFrame func([]byte) ([]int16, error)
	switch codec {
	case "PCMA", "PCMU":
		encodeFrame, decodeFrame = g711Codec(codec)
	case "AMR-WB":
		mode, ok := selectAMRWBMode(selectedFMTP)
		if !ok {
			return errors.New("ims: remote SDP has no supported AMR-WB mode")
		}
		encoder, encoderErr := amrwb.NewEncoder(amrwb.EncoderConfig{Mode: mode, OctetAligned: octetAligned})
		if encoderErr != nil {
			return fmt.Errorf("ims: initialize AMR-WB codec: %w", encoderErr)
		}
		decoder := amrwb.NewDecoder(amrwb.DecoderConfig{OctetAligned: octetAligned})
		encodeFrame, decodeFrame = encoder.Encode, decoder.Decode
	case "AMR":
		mode, ok := selectAMRNBMode(selectedFMTP)
		if !ok {
			return errors.New("ims: remote SDP has no supported AMR-NB mode")
		}
		encoder, encoderErr := amrnb.NewEncoder(amrnb.EncoderConfig{Mode: mode, OctetAligned: octetAligned})
		if encoderErr != nil {
			return fmt.Errorf("ims: initialize AMR-NB codec: %w", encoderErr)
		}
		decoder := amrnb.NewDecoder(amrnb.DecoderConfig{OctetAligned: octetAligned})
		encodeFrame, decodeFrame = encoder.Encode, decoder.Decode
	}
	dtmfPayload := dtmfPayloads[sampleRate]
	dtmfFMTP := ""
	if dtmfPayload == 0 {
		if sampleRate == 16000 {
			dtmfPayload = 101
		} else {
			dtmfPayload = 102
		}
	} else {
		dtmfFMTP = mappings[int(dtmfPayload)].FMTP
	}
	media.writeMu.Lock()
	media.pending = nil
	media.writeMu.Unlock()
	media.mu.Lock()
	media.remote = &net.UDPAddr{IP: address, Port: port}
	media.codec = codec
	media.payloadType = payload
	media.sampleRate = sampleRate
	media.frameSamples = frameSamples
	media.octetAligned = octetAligned
	media.modeSet = parseFMTPValue(selectedFMTP, "mode-set")
	media.selectedFMTP = selectedFMTP
	media.rtpProfile = sdpAudioProfile(body)
	media.rtcpMux = sdpMediaAttribute(body, "a=rtcp-mux")
	media.rtcpRsize = sdpMediaAttribute(body, "a=rtcp-rsize")
	media.dtmfPayloadType = dtmfPayload
	media.dtmfFMTP = dtmfFMTP
	media.encodeFrame = encodeFrame
	media.decodeFrame = decodeFrame
	media.mu.Unlock()
	fmt.Printf("[RTP] configureRemote: codec=%s payload=%d rate=%d octet-align=%t fmtp=%q dtmf=%d profile=%s rtcp-mux=%t rtcp-rsize=%t remote=%s:%d formats=%v\n",
		codec, payload, sampleRate, octetAligned, selectedFMTP, dtmfPayload,
		media.rtpProfile, media.rtcpMux, media.rtcpRsize, address.String(), port, formats)
	return nil
}

func amrPayloadRank(codec string, payload int) int {
	switch codec {
	case "AMR-WB":
		switch payload {
		case 98:
			return 3
		case 97:
			return 2
		case 96:
			return 1
		}
	case "AMR":
		switch payload {
		case 100:
			return 3
		case 99:
			return 2
		}
	}
	return 0
}

func parseAudioSDP(body []byte) (net.IP, int, []string, map[int]sdpAudioCodec, error) {
	var sessionIP, mediaIP net.IP
	var port int
	var formats []string
	mappings := make(map[int]sdpAudioCodec)
	inAudio := false
	for _, raw := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "m="):
			fields := strings.Fields(strings.TrimPrefix(line, "m="))
			inAudio = len(fields) >= 4 && strings.EqualFold(fields[0], "audio") && strings.HasPrefix(strings.ToUpper(fields[2]), "RTP/AVP")
			if inAudio {
				port, _ = strconv.Atoi(strings.Split(fields[1], "/")[0])
				formats = append([]string(nil), fields[3:]...)
			}
		case strings.HasPrefix(line, "c="):
			fields := strings.Fields(strings.TrimPrefix(line, "c="))
			if len(fields) >= 3 {
				ip := net.ParseIP(strings.Split(fields[2], "/")[0])
				if inAudio {
					mediaIP = ip
				} else {
					sessionIP = ip
				}
			}
		case inAudio && strings.HasPrefix(strings.ToLower(line), "a=rtpmap:"):
			fields := strings.Fields(strings.TrimPrefix(line, "a=rtpmap:"))
			if len(fields) == 2 {
				pt, parseErr := strconv.Atoi(fields[0])
				if parseErr == nil {
					parts := strings.Split(fields[1], "/")
					mapping := mappings[pt]
					mapping.Name = parts[0]
					if len(parts) > 1 {
						mapping.ClockRate, _ = strconv.Atoi(parts[1])
					}
					mappings[pt] = mapping
				}
			}
		case inAudio && strings.HasPrefix(strings.ToLower(line), "a=fmtp:"):
			fields := strings.Fields(strings.TrimPrefix(line, "a=fmtp:"))
			if len(fields) >= 2 {
				pt, parseErr := strconv.Atoi(fields[0])
				if parseErr == nil {
					mapping := mappings[pt]
					mapping.FMTP = strings.Join(fields[1:], " ")
					mappings[pt] = mapping
				}
			}
		}
	}
	if mediaIP == nil {
		mediaIP = sessionIP
	}
	if mediaIP == nil || port < 1 || port > 65535 || len(formats) == 0 {
		return nil, 0, nil, nil, errors.New("ims: remote SDP has no usable audio endpoint")
	}
	return mediaIP, port, formats, mappings, nil
}

func sdpAudioProfile(body []byte) string {
	for _, raw := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "m=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "m="))
		if len(fields) >= 3 && strings.EqualFold(fields[0], "audio") {
			profile := strings.TrimSpace(fields[2])
			if profile != "" {
				return profile
			}
		}
	}
	return "RTP/AVP"
}

func sdpMediaAttribute(body []byte, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, raw := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		if strings.EqualFold(strings.TrimSpace(raw), name) {
			return true
		}
	}
	return false
}

func parseFMTPBool(value, name string, fallback bool) bool {
	for _, item := range strings.Split(value, ";") {
		key, raw, found := strings.Cut(strings.TrimSpace(item), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(raw)) {
		case "1", "true", "yes":
			return true
		case "0", "false", "no":
			return false
		}
	}
	return fallback
}

func parseFMTPValue(value, name string) string {
	for _, item := range strings.Split(value, ";") {
		key, raw, found := strings.Cut(strings.TrimSpace(item), "=")
		if found && strings.EqualFold(strings.TrimSpace(key), name) {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}

func parseFMTPModeSet(value string) ([]int, bool) {
	raw := parseFMTPValue(value, "mode-set")
	if raw == "" {
		return nil, false
	}
	var modes []int
	for _, item := range strings.Split(raw, ",") {
		mode, err := strconv.Atoi(strings.TrimSpace(item))
		if err == nil {
			modes = append(modes, mode)
		}
	}
	return modes, len(modes) > 0
}

func selectAMRWBMode(fmtp string) (amrwb.Mode, bool) {
	modes, constrained := parseFMTPModeSet(fmtp)
	if !constrained {
		return amrwb.Mode1265, true
	}
	preference := []int{2, 8, 7, 6, 5, 4, 3, 1, 0}
	for _, want := range preference {
		for _, mode := range modes {
			if mode == want {
				return amrwb.Mode(mode), true
			}
		}
	}
	return 0, false
}

func selectAMRNBMode(fmtp string) (amrnb.Mode, bool) {
	modes, constrained := parseFMTPModeSet(fmtp)
	if !constrained {
		return amrnb.Mode1220, true
	}
	preference := []int{7, 6, 5, 4, 3, 2, 1, 0}
	for _, want := range preference {
		for _, mode := range modes {
			if mode == want {
				return amrnb.Mode(mode), true
			}
		}
	}
	return 0, false
}

func g711Codec(name string) (func([]int16) ([]byte, error), func([]byte) ([]int16, error)) {
	encode := func(samples []int16) ([]byte, error) {
		payload := make([]byte, len(samples))
		for index, sample := range samples {
			if name == "PCMA" {
				payload[index] = linearToALaw(sample)
			} else {
				payload[index] = linearToMuLaw(sample)
			}
		}
		return payload, nil
	}
	decode := func(payload []byte) ([]int16, error) {
		samples := make([]int16, len(payload))
		for index, value := range payload {
			if name == "PCMA" {
				samples[index] = aLawToLinear(value)
			} else {
				samples[index] = muLawToLinear(value)
			}
		}
		return samples, nil
	}
	return encode, decode
}

func (media *rtpMedia) ReadPCM(ctx context.Context) ([]int16, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-media.closed:
		return nil, io.EOF
	case samples := <-media.downlink:
		return samples, nil
	}
}

func (media *rtpMedia) WritePCM(samples []int16) error {
	media.mu.RLock()
	var remote *net.UDPAddr
	if media.remote != nil {
		copy := *media.remote
		remote = &copy
	}
	ready := media.codec != "" && media.encodeFrame != nil
	sampleRate := media.sampleRate
	media.mu.RUnlock()
	if remote == nil || !ready {
		return errors.New("ims: RTP media is not negotiated")
	}
	media.mu.Lock()
	media.lastRealAudioWrite = time.Now()
	media.mu.Unlock()
	if sampleRate != 0 && sampleRate != 8000 {
		samples = resamplePCM(samples, 8000, sampleRate)
	}
	return media.writeEncodedSamples(samples)
}

func (media *rtpMedia) receive() {
	packet := make([]byte, 2048)
	for {
		count, source, err := media.conn.ReadFromUDP(packet)
		if err != nil {
			return
		}
		media.mu.Lock()
		remote, payload, decodeFrame := media.remote, media.payloadType, media.decodeFrame
		sampleRate := media.sampleRate
		if remote != nil && remote.IP.Equal(source.IP) && remote.Port != source.Port {
			remote.Port = source.Port // symmetric RTP/NAT port learning
		}
		media.mu.Unlock()
		receivedPT := packet[1] & 0x7f
		if receivedPT != payload {
			fmt.Printf("[RTP] payload mismatch: expected=%d received=%d len=%d\n", payload, receivedPT, count)
		}
		if remote == nil || decodeFrame == nil || !remote.IP.Equal(source.IP) || count < 12 || packet[0]>>6 != 2 || receivedPT != payload {
			continue
		}
		header := 12 + int(packet[0]&0x0f)*4
		if packet[0]&0x10 != 0 {
			if count < header+4 {
				continue
			}
			header += 4 + int(binary.BigEndian.Uint16(packet[header+2:header+4]))*4
		}
		if header >= count {
			continue
		}
		samples, err := decodeFrame(packet[header:count])
		if err != nil {
			continue
		}
		if sampleRate != 8000 {
			samples = resamplePCM(samples, sampleRate, 8000)
		}
		select {
		case media.downlink <- samples:
		default:
			// Keep real-time behavior by dropping the oldest queued packet.
			select {
			case <-media.downlink:
			default:
			}
			select {
			case media.downlink <- samples:
			default:
			}
		}
	}
}

// startSilenceSender sends silence RTP packets every 20ms to keep the
// media path alive. Carriers terminate calls when they stop receiving RTP
// (typically after ~10s of media inactivity). When real audio is flowing
// (e.g. from the browser), silence packets are suppressed to avoid
// interleaving artifacts that cause buzzing/static noise.
func (media *rtpMedia) startSilenceSender() {
	if !media.ready() {
		return
	}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		media.mu.RLock()
		frameSamples := media.frameSamples
		media.mu.RUnlock()
		if frameSamples <= 0 {
			frameSamples = rtpPacketSamples
		}
		silence := make([]int16, frameSamples)
		for {
			select {
			case <-media.closed:
				return
			case <-ticker.C:
				media.mu.RLock()
				lastRealAudioWrite := media.lastRealAudioWrite
				media.mu.RUnlock()
				if time.Since(lastRealAudioWrite) < silenceSuppressWindow {
					continue
				}
				if err := media.writePCMInternal(silence); err != nil {
					fmt.Printf("[RTP] silence send error: %v\n", err)
				}
			}
		}
	}()
}

// writePCMInternal writes samples to the RTP stream without updating
// lastRealAudioWrite. Used by the silence sender so that the real-audio
// detection in WritePCM is not contaminated.
func (media *rtpMedia) writePCMInternal(samples []int16) error {
	return media.writeEncodedSamples(samples)
}

func (media *rtpMedia) writeEncodedSamples(samples []int16) error {
	media.mu.RLock()
	var remote *net.UDPAddr
	if media.remote != nil {
		cp := *media.remote
		remote = &cp
	}
	payload, frameSamples, encodeFrame := media.payloadType, media.frameSamples, media.encodeFrame
	media.mu.RUnlock()
	if remote == nil || encodeFrame == nil {
		return errors.New("ims: RTP media is not negotiated")
	}
	if frameSamples <= 0 {
		frameSamples = rtpPacketSamples
	}
	media.writeMu.Lock()
	defer media.writeMu.Unlock()
	media.pending = append(media.pending, samples...)
	for len(media.pending) >= frameSamples {
		encoded, err := encodeFrame(media.pending[:frameSamples])
		if err != nil {
			return fmt.Errorf("ims: encode RTP audio: %w", err)
		}
		packet := make([]byte, 12+len(encoded))
		packet[0], packet[1] = 0x80, payload
		binary.BigEndian.PutUint16(packet[2:4], media.sequence)
		binary.BigEndian.PutUint32(packet[4:8], media.timestamp)
		binary.BigEndian.PutUint32(packet[8:12], media.ssrc)
		copy(packet[12:], encoded)
		if _, err := media.conn.WriteToUDP(packet, remote); err != nil {
			return fmt.Errorf("ims: send RTP: %w", err)
		}
		media.pending = media.pending[frameSamples:]
		media.sequence++
		media.timestamp += uint32(frameSamples)
	}
	return nil
}

// SendDTMF sends an RFC 2833 telephone-event DTMF digit.
// It transmits repeated RTP telephone-event packets so that the far end can
// reliably detect the digit even on lossy networks.
func (media *rtpMedia) SendDTMF(digit byte) error {
	event, ok := dtmfEventCode(digit)
	if !ok {
		return fmt.Errorf("ims: invalid DTMF digit %q", string(digit))
	}
	media.mu.RLock()
	var remote *net.UDPAddr
	if media.remote != nil {
		cp := *media.remote
		remote = &cp
	}
	dtmfPayloadType := media.dtmfPayloadType
	frameSamples := media.frameSamples
	media.mu.RUnlock()
	if remote == nil || dtmfPayloadType == 0 {
		return errors.New("ims: RTP media is not negotiated")
	}
	if frameSamples <= 0 {
		frameSamples = rtpPacketSamples
	}
	media.writeMu.Lock()
	defer media.writeMu.Unlock()
	// Send 3 start packets, then 3 end packets. Each packet advances the event
	// duration by 20 ms at the negotiated telephone-event clock rate.
	const (
		dtmfVolume = 10
		dtmfCount  = 3
	)
	dtmfDuration := frameSamples
	for i := 0; i < dtmfCount; i++ {
		pkt := make([]byte, 16)
		pkt[0] = 0x80
		pkt[1] = dtmfPayloadType
		binary.BigEndian.PutUint16(pkt[2:4], media.sequence)
		binary.BigEndian.PutUint32(pkt[4:8], media.timestamp)
		binary.BigEndian.PutUint32(pkt[8:12], media.ssrc)
		pkt[12] = event
		pkt[13] = dtmfVolume // E=0, R=0, volume
		binary.BigEndian.PutUint16(pkt[14:16], uint16(dtmfDuration*(i+1)))
		if _, err := media.conn.WriteToUDP(pkt, remote); err != nil {
			return fmt.Errorf("ims: send DTMF start: %w", err)
		}
		media.sequence++
	}
	// End packets: set the E bit (0x80 in byte 13).
	for i := 0; i < dtmfCount; i++ {
		pkt := make([]byte, 16)
		pkt[0] = 0x80
		pkt[1] = dtmfPayloadType
		binary.BigEndian.PutUint16(pkt[2:4], media.sequence)
		binary.BigEndian.PutUint32(pkt[4:8], media.timestamp)
		binary.BigEndian.PutUint32(pkt[8:12], media.ssrc)
		pkt[12] = event
		pkt[13] = 0x80 | dtmfVolume // E=1, R=0, volume
		binary.BigEndian.PutUint16(pkt[14:16], uint16(dtmfDuration*(dtmfCount+i+1)))
		if _, err := media.conn.WriteToUDP(pkt, remote); err != nil {
			return fmt.Errorf("ims: send DTMF end: %w", err)
		}
		media.sequence++
	}
	media.timestamp += uint32(dtmfDuration * (dtmfCount*2 + 1))
	return nil
}

func resamplePCM(samples []int16, fromRate, toRate int) []int16 {
	if fromRate <= 0 || toRate <= 0 || fromRate == toRate || len(samples) == 0 {
		return samples
	}
	outputLength := (len(samples)*toRate + fromRate/2) / fromRate
	if outputLength <= 0 {
		return nil
	}
	output := make([]int16, outputLength)
	scale := float64(fromRate) / float64(toRate)
	for index := range output {
		position := float64(index) * scale
		lower := int(position)
		if lower >= len(samples)-1 {
			output[index] = samples[len(samples)-1]
			continue
		}
		fraction := position - float64(lower)
		value := float64(samples[lower])*(1-fraction) + float64(samples[lower+1])*fraction
		if value > 32767 {
			value = 32767
		} else if value < -32768 {
			value = -32768
		}
		output[index] = int16(value)
	}
	return output
}

func dtmfEventCode(digit byte) (byte, bool) {
	switch {
	case digit >= '0' && digit <= '9':
		return digit - '0', true
	case digit == '*':
		return 10, true
	case digit == '#':
		return 11, true
	case digit >= 'A' && digit <= 'D':
		return digit - 'A' + 12, true
	case digit >= 'a' && digit <= 'd':
		return digit - 'a' + 12, true
	default:
		return 0, false
	}
}

func (media *rtpMedia) Close() error {
	media.close.Do(func() {
		close(media.closed)
		if !media.borrowed {
			_ = media.conn.Close()
		}
	})
	return nil
}

func linearToMuLaw(sample int16) byte {
	value := int(sample)
	sign := byte(0)
	if value < 0 {
		sign, value = 0x80, -value
		if value > 32767 {
			value = 32767
		}
	}
	value += 132
	if value > 32635 {
		value = 32635
	}
	exponent := 7
	for mask := 0x4000; exponent > 0 && value&mask == 0; mask >>= 1 {
		exponent--
	}
	mantissa := (value >> (exponent + 3)) & 0x0f
	return ^(sign | byte(exponent<<4) | byte(mantissa))
}

func muLawToLinear(value byte) int16 {
	value = ^value
	magnitude := ((int(value)&0x0f)<<3 + 132) << ((value & 0x70) >> 4)
	magnitude -= 132
	if value&0x80 != 0 {
		return int16(-magnitude)
	}
	return int16(magnitude)
}

func linearToALaw(sample int16) byte {
	value := int(sample)
	mask := byte(0xd5)
	if value < 0 {
		mask, value = 0x55, -value-1
	}
	if value > 32767 {
		value = 32767
	}
	var encoded byte
	if value < 256 {
		encoded = byte(value >> 4)
	} else {
		exponent := 1
		for threshold := 512; exponent < 7 && value >= threshold; threshold <<= 1 {
			exponent++
		}
		encoded = byte(exponent<<4) | byte((value>>(exponent+3))&0x0f)
	}
	return encoded ^ mask
}

func aLawToLinear(value byte) int16 {
	value ^= 0x55
	magnitude := int(value&0x0f)<<4 + 8
	exponent := int((value & 0x70) >> 4)
	if exponent != 0 {
		magnitude = (magnitude + 0x100) << (exponent - 1)
	}
	if value&0x80 == 0 {
		return int16(-magnitude)
	}
	return int16(magnitude)
}
