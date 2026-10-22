package pcsc

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"
)

const (
	usimAIDPrefix = "A0000000871002"
	isimAIDPrefix = "A0000000871004"
)

type Service struct {
	mu      sync.Mutex
	backend Backend
}

// Session is an exclusive connection to one smart card. It is used by eUICC
// operations which must keep the same PC/SC transaction and logical channel
// alive across a sequence of APDUs.
type Session struct {
	service *Service
	card    Card
	closed  bool
}

func New() *Service {
	return &Service{backend: newNativeBackend()}
}

func NewWithBackend(backend Backend) *Service {
	return &Service{backend: backend}
}

func DeviceID(reader Reader) string {
	// Use USB serial number only (no interface) for stable device ID.
	// Interface number can change when reader is moved to a different USB port.
	// Format: Reader- + USBSerialNumber
	// Example: Reader-TEST00000002
	serial := strings.TrimSpace(reader.USBSerial)
	if serial != "" {
		return "Reader-" + serial
	}
	return ""
}

func (service *Service) Readers(ctx context.Context) ([]Reader, error) {
	if service == nil || service.backend == nil {
		return nil, ErrUnavailable
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.backend.Readers(ctx)
}

// OpenSession opens one card and holds the service lock until Close. Callers
// must close the returned session; this prevents AKA/identity reads from
// interleaving with a stateful ES10 transaction.
func (service *Service) OpenSession(ctx context.Context, selector Selector) (*Session, error) {
	if service == nil || service.backend == nil {
		return nil, ErrUnavailable
	}
	if err := selector.validate(); err != nil {
		return nil, err
	}
	service.mu.Lock()
	card, err := service.backend.Open(ctx, selector)
	if err != nil {
		service.mu.Unlock()
		return nil, err
	}
	return &Session{service: service, card: card}, nil
}

// Transmit sends one raw APDU. Unlike the ordinary SIM helpers, it leaves
// 61xx continuation handling to the eUICC logical-channel implementation so
// GET RESPONSE uses the correct channel CLA.
func (session *Session) Transmit(ctx context.Context, command []byte) ([]byte, uint16, error) {
	if session == nil || session.card == nil || session.closed {
		return nil, 0, errors.New("pcsc: card session is closed")
	}
	if raw, ok := session.card.(interface {
		TransmitRaw(context.Context, []byte) ([]byte, uint16, error)
	}); ok {
		return raw.TransmitRaw(ctx, command)
	}
	return session.card.Transmit(ctx, command)
}

func (session *Session) Close() error {
	return session.close(false)
}

// CloseWithReset resets the card while releasing the PC/SC connection. eUICC
// EnableProfile requires this refresh boundary before the newly enabled USIM
// application and ICCID become visible to subsequent callers.
func (session *Session) CloseWithReset() error {
	return session.close(true)
}

func (session *Session) close(reset bool) error {
	if session == nil || session.closed {
		return nil
	}
	session.closed = true
	var err error
	if resetter, ok := session.card.(interface{ CloseWithReset() error }); reset && ok {
		err = resetter.CloseWithReset()
	} else {
		err = session.card.Close()
	}
	session.service.mu.Unlock()
	return err
}

func selectorDetail(selector Selector) string {
	return fmt.Sprintf("usb_path=%q reader_name=%q usb_serial=%q",
		strings.TrimSpace(selector.USBPath),
		strings.TrimSpace(selector.ReaderName),
		strings.TrimSpace(selector.USBSerial),
	)
}

func wrapReaderOpenError(err error, selector Selector) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w [selector %s]", err, selectorDetail(selector))
}

func (service *Service) Snapshot(ctx context.Context, selector Selector, pin string) (Snapshot, error) {
	readers, err := service.Readers(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	reader, ok := matchReader(readers, selector)
	if !ok {
		return Snapshot{}, wrapReaderOpenError(ErrReaderNotFound, selector)
	}
	result := Snapshot{Reader: reader}
	if reader.DiscoveryIssue != "" {
		return result, fmt.Errorf("%w: %s", ErrUnavailable, reader.DiscoveryIssue)
	}
	if !reader.CardPresent {
		return result, ErrNoCard
	}
	identity, err := service.ReadIdentity(ctx, selector, pin)
	result.Identity = identity
	return result, err
}

func (service *Service) ReadIdentity(ctx context.Context, selector Selector, pin string) (Identity, error) {
	if service == nil || service.backend == nil {
		return Identity{}, ErrUnavailable
	}
	if err := selector.validate(); err != nil {
		return Identity{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	card, err := service.backend.Open(ctx, selector)
	if err != nil {
		return Identity{}, wrapReaderOpenError(err, selector)
	}
	defer card.Close()
	return readIdentity(ctx, card, pin)
}

func (service *Service) CheckReady(
	ctx context.Context,
	selector Selector,
	expectedICCID string,
	pin string,
) (string, error) {
	if service == nil || service.backend == nil {
		return "", ErrUnavailable
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	card, err := service.backend.Open(ctx, selector)
	if err != nil {
		return "", wrapReaderOpenError(err, selector)
	}
	defer card.Close()
	iccid, err := readICCID(ctx, card)
	if err != nil {
		return "", err
	}
	if expected := strings.TrimSpace(expectedICCID); expected != "" && !strings.EqualFold(expected, iccid) {
		return "", ErrCardChanged
	}
	aid, _, err := selectAKAApplication(ctx, card, "")
	if err != nil {
		return "", err
	}
	if err := verifyPIN(ctx, card, pin); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(aid)), nil
}

func (service *Service) Authenticate(
	ctx context.Context,
	selector Selector,
	expectedICCID string,
	pin string,
	challenge AKAChallenge,
) (AKAResult, error) {
	return service.AuthenticateWithPreference(ctx, selector, expectedICCID, pin, challenge, "")
}

func (service *Service) AuthenticateWithPreference(
	ctx context.Context,
	selector Selector,
	expectedICCID string,
	pin string,
	challenge AKAChallenge,
	preference string,
) (AKAResult, error) {
	if service == nil || service.backend == nil {
		return AKAResult{}, ErrUnavailable
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	card, err := service.backend.Open(ctx, selector)
	if err != nil {
		return AKAResult{}, wrapReaderOpenError(err, selector)
	}
	defer card.Close()
	iccid, err := readICCID(ctx, card)
	if err != nil {
		return AKAResult{}, err
	}
	if expected := strings.TrimSpace(expectedICCID); expected != "" && !strings.EqualFold(expected, iccid) {
		return AKAResult{}, ErrCardChanged
	}
	_, appName, err := selectAKAApplication(ctx, card, preference)
	if err != nil {
		return AKAResult{}, err
	}
	if err := verifyPIN(ctx, card, pin); err != nil {
		return AKAResult{}, err
	}
	apdu := make([]byte, 0, 40)
	apdu = append(apdu, 0x00, 0x88, 0x00, 0x81, 0x22, 0x10)
	apdu = append(apdu, challenge.RAND[:]...)
	apdu = append(apdu, 0x10)
	apdu = append(apdu, challenge.AUTN[:]...)
	apdu = append(apdu, 0x00)
	data, sw, err := card.Transmit(ctx, apdu)
	if err != nil {
		return AKAResult{}, fmt.Errorf("pcsc: %s authentication transport failed", appName)
	}
	if sw == 0x9862 {
		return AKAResult{}, ErrAKARejected
	}
	if sw != 0x9000 {
		return AKAResult{}, fmt.Errorf("pcsc: %s authentication failed with status %04X", appName, sw)
	}
	return parseAKAResponse(data)
}

func matchReader(readers []Reader, selector Selector) (Reader, bool) {
	name := strings.TrimSpace(selector.ReaderName)
	usbSerial := strings.TrimSpace(selector.USBSerial)

	// 1. Exact ReaderName match (serial + interface). Most specific.
	for _, reader := range readers {
		if name != "" && reader.ReaderName == name {
			return reader, true
		}
	}

	// 2. Full PC/SC name match.
	for _, reader := range readers {
		if name != "" && reader.Name == name {
			return reader, true
		}
	}

	// 3. USB serial-only match. The interface number can change when a reader
	//    is moved to a different USB port, so fall back to serial matching.
	//    This ensures the same physical reader is found regardless of port.
	if usbSerial != "" {
		for _, reader := range readers {
			if reader.USBSerial != "" && reader.USBSerial == usbSerial {
				return reader, true
			}
		}
	}
	if name != "" {
		for _, reader := range readers {
			if reader.USBSerial != "" && strings.HasPrefix(name, reader.USBSerial) {
				return reader, true
			}
		}
	}

	// 4. USBPath fallback — weakest signal, only when no name was supplied.
	path := strings.TrimSpace(selector.USBPath)
	if name == "" && usbSerial == "" {
		for _, reader := range readers {
			if path != "" && reader.USBPath == path {
				return reader, true
			}
		}
	}
	return Reader{}, false
}

func readIdentity(ctx context.Context, card Card, pin string) (Identity, error) {
	identity := Identity{PINTries: -1}
	iccid, err := readICCID(ctx, card)
	if err != nil {
		return identity, err
	}
	identity.ICCID = iccid
	aid, err := selectUSIM(ctx, card)
	if err != nil {
		return identity, err
	}
	identity.USIMAID = append([]byte(nil), aid...)
	if err := verifyPIN(ctx, card, pin); err != nil {
		identity.PINRequired = errors.Is(err, ErrPINRequired) || errors.Is(err, ErrPINTriesLow)
		var pinErr *PINError
		if errors.As(err, &pinErr) {
			identity.PINTries = pinErr.Tries
		}
		return identity, err
	}
	if err := selectFile(ctx, card, []byte{0x6F, 0x07}); err != nil {
		return identity, fmt.Errorf("pcsc: select EF_IMSI: %w", err)
	}
	imsiData, err := readBinary(ctx, card, 9)
	if err != nil {
		return identity, fmt.Errorf("pcsc: read EF_IMSI: %w", err)
	}
	identity.IMSI, err = decodeIMSI(imsiData)
	if err != nil {
		return identity, err
	}
	if _, selectErr := selectApplication(ctx, card, aid); selectErr == nil {
		if selectErr = selectFile(ctx, card, []byte{0x6F, 0xAD}); selectErr == nil {
			if data, readErr := readBinary(ctx, card, 4); readErr == nil && len(data) >= 4 {
				length := int(data[3] & 0x0f)
				if length == 2 || length == 3 {
					identity.MNCLength = length
				}
			}
		}
	}
	if _, selectErr := selectApplication(ctx, card, aid); selectErr == nil {
		identity.SPN = readSPN(ctx, card)
	}
	if _, selectErr := selectApplication(ctx, card, aid); selectErr == nil {
		identity.SMSC = readSMSC(ctx, card)
	}
	identity.ProvisionedIMSI = readISIMProvisionedIMSI(ctx, card)
	return identity, nil
}

func (service *Service) ReadSMSCenterPSI(ctx context.Context, selector Selector, pin string) (string, error) {
	session, err := service.OpenSession(ctx, selector)
	if err != nil {
		return "", wrapReaderOpenError(err, selector)
	}
	defer session.Close()
	card := session.card
	if _, err := selectUSIM(ctx, card); err != nil {
		return "", err
	}
	if err := verifyPIN(ctx, card, pin); err != nil {
		return "", err
	}
	var lastErr error
	paths := [][][]byte{
		{{0x7F, 0x10}, {0x6F, 0xE5}},
		{{0x6F, 0xE5}},
	}
	for _, path := range paths {
		if err := selectMF(ctx, card); err != nil {
			lastErr = fmt.Errorf("select MF: %w", err)
			continue
		}
		if err := selectPSIPath(ctx, card, path); err != nil {
			lastErr = err
			continue
		}
		record, err := readRecord(ctx, card, 1, 255)
		if err != nil {
			lastErr = fmt.Errorf("read EF_PSISMSC: %w", err)
			continue
		}
		psi, err := smsCenterPSIFromRecord(record)
		if err != nil {
			lastErr = fmt.Errorf("decode EF_PSISMSC: %w", err)
			continue
		}
		return psi, nil
	}
	if _, err := selectUSIM(ctx, card); err != nil {
		return "", errors.Join(lastErr, err)
	}
	if err := selectFile(ctx, card, []byte{0x6F, 0xE5}); err != nil {
		return "", errors.Join(lastErr, fmt.Errorf("select EF_PSISMSC in USIM: %w", err))
	}
	record, err := readRecord(ctx, card, 1, 255)
	if err != nil {
		return "", errors.Join(lastErr, fmt.Errorf("read EF_PSISMSC in USIM: %w", err))
	}
	psi, err := smsCenterPSIFromRecord(record)
	if err != nil {
		return "", errors.Join(lastErr, fmt.Errorf("decode EF_PSISMSC in USIM: %w", err))
	}
	return psi, nil
}

func selectPSIPath(ctx context.Context, card Card, path [][]byte) error {
	for _, fileID := range path {
		if err := selectFile(ctx, card, fileID); err != nil {
			return fmt.Errorf("select SMS PSI file %X: %w", fileID, err)
		}
	}
	return nil
}

func readISIMProvisionedIMSI(ctx context.Context, card Card) string {
	if _, err := selectApplicationByPrefix(ctx, card, isimAIDPrefix); err != nil {
		return ""
	}
	if err := selectFile(ctx, card, []byte{0x6F, 0x02}); err == nil {
		if data, readErr := readBinary(ctx, card, 256); readErr == nil {
			if imsi := provisionedIMSIFromNAI(data); imsi != "" {
				return imsi
			}
		}
	}
	if err := selectFile(ctx, card, []byte{0x6F, 0x04}); err != nil {
		return ""
	}
	for record := 1; record <= 8; record++ {
		data, err := readRecord(ctx, card, byte(record), 255)
		if err != nil {
			break
		}
		if imsi := provisionedIMSIFromNAI(data); imsi != "" {
			return imsi
		}
	}
	return ""
}

func provisionedIMSIFromNAI(data []byte) string {
	value := strings.Trim(string(data), "\x00\xff \t\r\n")
	if separator := strings.IndexByte(value, ','); separator >= 0 {
		value = value[:separator]
	}
	at := strings.IndexByte(value, '@')
	if at < 0 {
		return ""
	}
	value = value[:at]
	if colon := strings.LastIndexByte(value, ':'); colon >= 0 {
		value = value[colon+1:]
	}
	value = strings.TrimSpace(value)
	if len(value) == 16 && value[0] == '0' {
		value = value[1:]
	}
	if len(value) != 15 || !decimalDigits(value) {
		return ""
	}
	return value
}

func readICCID(ctx context.Context, card Card) (string, error) {
	if err := selectMF(ctx, card); err != nil {
		return "", err
	}
	if err := selectFile(ctx, card, []byte{0x2F, 0xE2}); err != nil {
		return "", fmt.Errorf("pcsc: select EF_ICCID: %w", err)
	}
	data, err := readBinary(ctx, card, 10)
	if err != nil {
		return "", fmt.Errorf("pcsc: read EF_ICCID: %w", err)
	}
	value := decodeSwappedBCD(data, false)
	if len(value) < 18 || len(value) > 22 {
		return "", errors.New("pcsc: card returned an invalid ICCID")
	}
	return value, nil
}

func selectUSIM(ctx context.Context, card Card) ([]byte, error) {
	aid, app, err := selectAKAApplication(ctx, card, "")
	if err != nil {
		return nil, err
	}
	if app != "USIM" {
		return nil, ErrUSIMUnavailable
	}
	return aid, nil
}

func selectApplicationByPrefix(ctx context.Context, card Card, aidPrefix string) ([]byte, error) {
	aids, err := readDIRAIDs(ctx, card)
	if err != nil {
		return nil, err
	}
	for _, aid := range aids {
		if strings.HasPrefix(strings.ToUpper(hex.EncodeToString(aid)), aidPrefix) {
			if _, err := selectApplication(ctx, card, aid); err != nil {
				return nil, err
			}
			return aid, nil
		}
	}
	if aidPrefix == usimAIDPrefix {
		return nil, ErrUSIMUnavailable
	}
	return nil, errors.New("pcsc: requested SIM application was not found")
}

func readDIRAIDs(ctx context.Context, card Card) ([][]byte, error) {
	if err := selectMF(ctx, card); err != nil {
		return nil, err
	}
	if err := selectFile(ctx, card, []byte{0x2F, 0x00}); err != nil {
		return nil, fmt.Errorf("pcsc: select EF_DIR: %w", err)
	}
	var aids [][]byte
	for record := 1; record <= 32; record++ {
		data, sw, err := card.Transmit(ctx, []byte{0x00, 0xB2, byte(record), 0x04, 0x00})
		if err != nil {
			return nil, err
		}
		if sw == 0x6A83 || sw == 0x9402 {
			break
		}
		if sw != 0x9000 {
			continue
		}
		aid := findTLV(data, 0x4F)
		if len(aid) == 0 {
			continue
		}
		aids = append(aids, append([]byte(nil), aid...))
	}
	return aids, nil
}

func selectAKAApplication(ctx context.Context, card Card, preference string) ([]byte, string, error) {
	aids, err := readDIRAIDs(ctx, card)
	if err != nil {
		return nil, "", err
	}
	if strings.EqualFold(strings.TrimSpace(preference), "isim_strict") {
		for _, aid := range aids {
			if strings.HasPrefix(strings.ToUpper(hex.EncodeToString(aid)), isimAIDPrefix) {
				if _, err := selectApplication(ctx, card, aid); err != nil {
					return nil, "", err
				}
				return aid, "ISIM", nil
			}
		}
		return nil, "", ErrISIMUnavailable
	}

	for _, aid := range aids {
		if strings.HasPrefix(strings.ToUpper(hex.EncodeToString(aid)), usimAIDPrefix) {
			if _, err := selectApplication(ctx, card, aid); err != nil {
				return nil, "", err
			}
			return aid, "USIM", nil
		}
	}
	for _, aid := range aids {
		if strings.HasPrefix(strings.ToUpper(hex.EncodeToString(aid)), isimAIDPrefix) {
			if _, err := selectApplication(ctx, card, aid); err != nil {
				return nil, "", err
			}
			return aid, "ISIM", nil
		}
	}
	return nil, "", ErrUSIMUnavailable
}

func selectMF(ctx context.Context, card Card) error {
	// The MF FCP is not consumed here. Request no response data (P2=0C)
	// instead of asking the reader to return a potentially long FCP template.
	// This also avoids the affected native PC/SC path surfacing the template's
	// leading 62 xx bytes as if they were an ISO 7816 warning status.
	_, sw, err := card.Transmit(ctx, []byte{0x00, 0xA4, 0x00, 0x0C, 0x02, 0x3F, 0x00})
	return requireStatus("select MF", sw, err)
}

func selectFile(ctx context.Context, card Card, fileID []byte) error {
	if len(fileID) != 2 {
		return errors.New("pcsc: invalid file identifier")
	}
	apdu := []byte{0x00, 0xA4, 0x00, 0x04, 0x02, fileID[0], fileID[1], 0x00}
	_, sw, err := card.Transmit(ctx, apdu)
	return requireStatus("select file", sw, err)
}

func selectApplication(ctx context.Context, card Card, aid []byte) ([]byte, error) {
	if len(aid) == 0 || len(aid) > 32 {
		return nil, errors.New("pcsc: invalid application AID")
	}
	apdu := []byte{0x00, 0xA4, 0x04, 0x04, byte(len(aid))}
	apdu = append(apdu, aid...)
	apdu = append(apdu, 0x00)
	data, sw, err := card.Transmit(ctx, apdu)
	if err := requireStatus("select application", sw, err); err != nil {
		return nil, err
	}
	return data, nil
}

func readBinary(ctx context.Context, card Card, length int) ([]byte, error) {
	if length <= 0 || length > 256 {
		return nil, errors.New("pcsc: invalid binary read length")
	}
	le := byte(length)
	if length == 256 {
		le = 0
	}
	data, sw, err := card.Transmit(ctx, []byte{0x00, 0xB0, 0x00, 0x00, le})
	if err := requireStatus("read binary", sw, err); err != nil {
		return nil, err
	}
	return data, nil
}

func readRecord(ctx context.Context, card Card, record byte, length int) ([]byte, error) {
	if record == 0 || length <= 0 || length > 255 {
		return nil, errors.New("pcsc: invalid record read parameters")
	}
	data, sw, err := card.Transmit(ctx, []byte{0x00, 0xB2, record, 0x04, byte(length)})
	if err != nil {
		return nil, err
	}
	if sw == 0x6A83 || sw == 0x9402 {
		return nil, errors.New("pcsc: record does not exist")
	}
	if err := requireStatus("read record", sw, nil); err != nil {
		return nil, err
	}
	return data, nil
}

func verifyPIN(ctx context.Context, card Card, pin string) error {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return nil
	}
	if len(pin) < 4 || len(pin) > 8 || !decimalDigits(pin) {
		return errors.New("pcsc: SIM PIN must contain 4 to 8 digits")
	}
	_, sw, err := card.Transmit(ctx, []byte{0x00, 0x20, 0x00, 0x01, 0x00})
	if err != nil {
		return errors.New("pcsc: SIM PIN status check failed")
	}
	if sw == 0x9000 {
		return nil
	}
	tries := -1
	if sw&0xFFF0 == 0x63C0 {
		tries = int(sw & 0x000F)
		if tries <= 2 {
			return &PINError{Kind: ErrPINTriesLow, Tries: tries}
		}
	}
	body := bytes.Repeat([]byte{0xFF}, 8)
	copy(body, []byte(pin))
	apdu := append([]byte{0x00, 0x20, 0x00, 0x01, 0x08}, body...)
	_, sw, err = card.Transmit(ctx, apdu)
	if err != nil {
		return errors.New("pcsc: SIM PIN verification transport failed")
	}
	if sw == 0x9000 {
		return nil
	}
	if sw&0xFFF0 == 0x63C0 {
		return &PINError{Kind: ErrPINRejected, Tries: int(sw & 0x000F)}
	}
	return ErrPINRejected
}

func requireStatus(operation string, sw uint16, err error) error {
	if err != nil {
		return fmt.Errorf("pcsc: %s transport failed", operation)
	}
	if sw == 0x9000 {
		return nil
	}
	if sw == 0x6982 || sw == 0x9804 {
		return &PINError{Kind: ErrPINRequired, Tries: -1}
	}
	return fmt.Errorf("pcsc: %s failed with status %04X", operation, sw)
}

func splitAPDUResponse(response []byte) ([]byte, uint16, error) {
	if len(response) < 2 {
		return nil, 0, errors.New("pcsc: APDU response omitted its status word")
	}
	last := len(response) - 2
	status := uint16(response[last])<<8 | uint16(response[last+1])
	return append([]byte(nil), response[:last]...), status, nil
}

func decodeSwappedBCD(value []byte, dropFirstNibble bool) string {
	var result strings.Builder
	for _, octet := range value {
		for _, nibble := range []byte{octet & 0x0F, octet >> 4} {
			if dropFirstNibble {
				dropFirstNibble = false
				continue
			}
			if nibble == 0x0F {
				return result.String()
			}
			if nibble > 9 {
				return ""
			}
			result.WriteByte('0' + nibble)
		}
	}
	return result.String()
}

func decodeIMSI(data []byte) (string, error) {
	if len(data) < 2 {
		return "", errors.New("pcsc: EF_IMSI is too short")
	}
	length := int(data[0])
	if length <= 0 || length > len(data)-1 {
		return "", errors.New("pcsc: EF_IMSI has an invalid length")
	}
	value := decodeSwappedBCD(data[1:1+length], true)
	if len(value) < 10 || len(value) > 18 || !decimalDigits(value) {
		return "", errors.New("pcsc: card returned an invalid IMSI")
	}
	return value, nil
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func findTLV(data []byte, wanted byte) []byte {
	for len(data) >= 2 {
		tag := data[0]
		data = data[1:]
		length, consumed, ok := decodeTLVLength(data)
		if !ok || consumed+length > len(data) {
			return nil
		}
		value := data[consumed : consumed+length]
		if tag == wanted {
			return append([]byte(nil), value...)
		}
		if tag&0x20 != 0 {
			if nested := findTLV(value, wanted); len(nested) > 0 {
				return nested
			}
		}
		data = data[consumed+length:]
	}
	return nil
}

func decodeTLVLength(data []byte) (length, consumed int, ok bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	if data[0]&0x80 == 0 {
		return int(data[0]), 1, true
	}
	count := int(data[0] & 0x7F)
	if count < 1 || count > 2 || len(data) < 1+count {
		return 0, 0, false
	}
	length = 0
	for _, octet := range data[1 : 1+count] {
		length = length<<8 | int(octet)
	}
	return length, 1 + count, true
}

func parseAKAResponse(data []byte) (AKAResult, error) {
	if len(data) < 2 {
		return AKAResult{}, errors.New("pcsc: USIM returned a short AKA response")
	}
	switch data[0] {
	case 0xDB:
		res, rest, ok := takeLV(data[1:])
		if !ok || len(res) < 4 || len(res) > 16 {
			return AKAResult{}, errors.New("pcsc: USIM returned an invalid AKA RES")
		}
		ck, rest, ok := takeLV(rest)
		if !ok || len(ck) != 16 {
			return AKAResult{}, errors.New("pcsc: USIM returned an invalid AKA CK")
		}
		ik, rest, ok := takeLV(rest)
		if !ok || len(ik) != 16 {
			return AKAResult{}, errors.New("pcsc: USIM returned an invalid AKA IK")
		}
		if len(rest) > 0 {
			kc, tail, valid := takeLV(rest)
			if !valid || len(kc) != 8 || len(tail) != 0 {
				return AKAResult{}, errors.New("pcsc: USIM returned invalid trailing AKA material")
			}
		}
		return AKAResult{RES: append([]byte(nil), res...), CK: append([]byte(nil), ck...), IK: append([]byte(nil), ik...)}, nil
	case 0xDC:
		auts, tail, ok := takeLV(data[1:])
		if !ok || len(auts) != 14 || len(tail) != 0 {
			return AKAResult{}, errors.New("pcsc: USIM returned invalid AKA synchronization evidence")
		}
		return AKAResult{AUTS: append([]byte(nil), auts...), SynchronizationFailure: true}, nil
	default:
		return AKAResult{}, errors.New("pcsc: USIM returned an unsupported AKA response")
	}
}

func takeLV(data []byte) (value, rest []byte, ok bool) {
	if len(data) == 0 || int(data[0]) > len(data)-1 {
		return nil, data, false
	}
	length := int(data[0])
	return data[1 : 1+length], data[1+length:], true
}

func readSPN(ctx context.Context, card Card) string {
	if err := selectFile(ctx, card, []byte{0x6F, 0x46}); err != nil {
		return ""
	}
	data, err := readBinary(ctx, card, 17)
	if err != nil || len(data) < 2 {
		return ""
	}
	return decodeSPN(data)
}

func decodeSPN(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	alpha := data[1:]
	for len(alpha) > 0 && (alpha[len(alpha)-1] == 0xff || alpha[len(alpha)-1] == 0x00) {
		alpha = alpha[:len(alpha)-1]
	}
	if len(alpha) == 0 {
		return ""
	}
	if alpha[0] == 0x80 {
		ucs2 := alpha[1:]
		if len(ucs2)%2 != 0 {
			ucs2 = ucs2[:len(ucs2)-1]
		}
		units := make([]uint16, 0, len(ucs2)/2)
		for index := 0; index+1 < len(ucs2); index += 2 {
			unit := uint16(ucs2[index])<<8 | uint16(ucs2[index+1])
			if unit != 0xffff && unit != 0 {
				units = append(units, unit)
			}
		}
		return strings.TrimSpace(string(utf16.Decode(units)))
	}
	printable := make([]byte, 0, len(alpha))
	for _, value := range alpha {
		if value >= 0x20 && value <= 0x7e {
			printable = append(printable, value)
		}
	}
	return strings.TrimSpace(string(printable))
}

func readSMSC(ctx context.Context, card Card) string {
	if err := selectFile(ctx, card, []byte{0x6F, 0x42}); err != nil {
		return ""
	}
	data, sw, err := card.Transmit(ctx, []byte{0x00, 0xB2, 0x01, 0x04, 0x00})
	if err != nil || sw != 0x9000 || len(data) < 15 {
		return ""
	}
	sca := data[len(data)-15 : len(data)-3]
	if len(sca) < 2 || sca[0] < 2 || int(sca[0]) > len(sca)-1 {
		return ""
	}
	digits := decodeSwappedBCD(sca[2:1+int(sca[0])], false)
	if !decimalDigits(digits) {
		return ""
	}
	if sca[1]&0x70 == 0x10 {
		return "+" + digits
	}
	return digits
}

func smsCenterPSIFromRecord(record []byte) (string, error) {
	value := findTLV(record, 0x80)
	value = bytes.Trim(value, "\x00\xff \t\r\n")
	if len(value) == 0 {
		return "", errors.New("SMS center PSI record has no URI value")
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return "", errors.New("SMS center PSI record is not visible ASCII")
		}
	}
	return string(value), nil
}
