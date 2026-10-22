package nodeadapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ParsedLink holds the result of parsing a share link.
type ParsedLink struct {
	Name           string          `json:"name"`
	Protocol       string          `json:"protocol"`
	NodeConfigJSON json.RawMessage `json:"node_config_json"`
	ServerAddr     string          `json:"server_addr"`
	ServerPort     int             `json:"server_port"`
}

// PingResult holds TCP ping result.
type PingResult struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Reachable  bool   `json:"reachable"`
	LatencyMs  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
}

// TCPPing tests TCP connectivity to host:port.
func TCPPing(host string, port int) PingResult {
	addr := fmt.Sprintf("%s:%d", host, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return PingResult{Host: host, Port: port, Reachable: false, LatencyMs: elapsed, Error: err.Error()}
	}
	conn.Close()
	return PingResult{Host: host, Port: port, Reachable: true, LatencyMs: elapsed}
}

// DetectLocalIPv4 returns all non-loopback IPv4 addresses on this machine.
func DetectLocalIPv4() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ipv4 := ip.To4(); ipv4 != nil {
				ips = append(ips, ipv4.String())
			}
		}
	}
	return ips
}

// ParseShareLink parses a VLESS/VMess/Trojan share link into a ParsedLink.
func ParseShareLink(raw string) (ParsedLink, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParsedLink{}, fmt.Errorf("empty link")
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(lower, "vmess://"):
		return parseVMess(raw)
	case strings.HasPrefix(lower, "trojan://"):
		return parseTrojan(raw)
	default:
		return ParsedLink{}, fmt.Errorf("unsupported protocol (supported: vless, vmess, trojan)")
	}
}

// ParseSubscriptionLinks parses content that may be a single link, multiple links (one per line),
// or base64-encoded subscription content. Same approach as miaomiaowu.
func ParseSubscriptionLinks(content string) ([]ParsedLink, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty content")
	}

	// Try base64 decode first
	decoded := tryBase64Decode(content)
	if decoded != "" && containsSupportedScheme(decoded) {
		content = decoded
	}

	lines := strings.Split(content, "\n")
	var results []ParsedLink
	var lastErr error
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed, err := ParseShareLink(line)
		if err != nil {
			lastErr = err
			continue
		}
		results = append(results, parsed)
	}
	if len(results) == 0 && lastErr != nil {
		return nil, fmt.Errorf("no valid links found: %w", lastErr)
	}
	return results, nil
}

func tryBase64Decode(s string) string {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(s); err == nil {
			return string(decoded)
		}
	}
	return ""
}

func containsSupportedScheme(s string) bool {
	lower := strings.ToLower(s)
	for _, scheme := range []string{"vless://", "vmess://", "trojan://", "ss://", "hysteria2://", "hy2://"} {
		if strings.Contains(lower, scheme) {
			return true
		}
	}
	return false
}

// parseQueryParams manually parses URL query string into map (same as miaomiaowu proxyparser).
// This avoids url.ParseQuery failing on special characters like "[" in values.
func parseQueryParams(query string) map[string]string {
	params := make(map[string]string)
	if query == "" {
		return params
	}
	pairs := strings.Split(query, "&")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			key, _ := url.QueryUnescape(kv[0])
			value, _ := url.QueryUnescape(kv[1])
			params[key] = value
		} else if len(kv) == 1 {
			key, _ := url.QueryUnescape(kv[0])
			params[key] = ""
		}
	}
	return params
}

// safeDecodeURIComponent safely decodes URI component, returns original on error.
func safeDecodeURIComponent(s string) string {
	if s == "" {
		return s
	}
	decoded, err := url.QueryUnescape(s)
	if err != nil {
		return s
	}
	return decoded
}

// --- VLESS ---

func parseVLESS(raw string) (ParsedLink, error) {
	body := strings.TrimPrefix(raw, "vless://")
	mainPart := body

	// Extract name (use LastIndex like miaomiaowu)
	name := ""
	if idx := strings.LastIndex(body, "#"); idx != -1 {
		mainPart = body[:idx]
		name, _ = url.QueryUnescape(body[idx+1:])
	}

	// Extract query params
	var paramsStr string
	if idx := strings.Index(mainPart, "?"); idx != -1 {
		paramsStr = mainPart[idx+1:]
		mainPart = mainPart[:idx]
	}
	mainPart = strings.TrimSuffix(mainPart, "/")

	// Parse uuid@server:port (use LastIndex like miaomiaowu)
	atIdx := strings.LastIndex(mainPart, "@")
	if atIdx < 0 {
		return ParsedLink{}, fmt.Errorf("invalid vless link: missing @")
	}
	uuid := mainPart[:atIdx]
	serverPart := mainPart[atIdx+1:]

	host, port := parseServerPortWithDefault(serverPart, 443)

	q := parseQueryParams(paramsStr)
	security := q["security"]
	flow := q["flow"]
	sni := safeDecodeURIComponent(q["sni"])
	fp := q["fp"]
	if fp == "" {
		fp = "chrome"
	}
	publicKey := q["pbk"]
	shortID := q["sid"]
	spx := q["spx"]
	tcpType := q["type"] // tcp, grpc, ws

	if name == "" {
		name = fmt.Sprintf("vless-%s:%d", host, port)
	}

	outbound := map[string]any{
		"type":        "vless",
		"tag":         "proxy-out",
		"server":      host,
		"server_port": port,
		"uuid":        uuid,
	}
	if flow != "" {
		outbound["flow"] = flow
	}

	transport := buildTransportFromMap(tcpType, q)
	if transport != nil {
		outbound["transport"] = transport
	}

	switch security {
	case "reality":
		tls := map[string]any{
			"enabled":     true,
			"server_name": sni,
			"reality": map[string]any{
				"enabled":    true,
				"public_key": publicKey,
				"short_id":   shortID,
			},
		}
		if fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if spx != "" {
			tls["reality"].(map[string]any)["spider_name"] = spx
		}
		outbound["tls"] = tls
	case "tls":
		tls := map[string]any{
			"enabled":     true,
			"server_name": sni,
		}
		if fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		alpn := q["alpn"]
		if alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		outbound["tls"] = tls
	}

	cfg, _ := json.Marshal(outbound)
	return ParsedLink{Name: name, Protocol: "vless", NodeConfigJSON: cfg, ServerAddr: host, ServerPort: port}, nil
}

// --- VMess ---

func parseVMess(raw string) (ParsedLink, error) {
	b64 := strings.TrimPrefix(raw, "vmess://")
	b64 = strings.TrimSpace(b64)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			// try URL-safe base64
			decoded, err = base64.URLEncoding.DecodeString(b64)
			if err != nil {
				decoded, err = base64.RawURLEncoding.DecodeString(b64)
				if err != nil {
					return ParsedLink{}, fmt.Errorf("invalid vmess base64: %w", err)
				}
			}
		}
	}
	var vm struct {
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		Aid  any    `json:"aid"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
		FP   string `json:"fp"`
		ALPN string `json:"alpn"`
		Scy  string `json:"scy"`
	}
	if err := json.Unmarshal(decoded, &vm); err != nil {
		return ParsedLink{}, fmt.Errorf("invalid vmess json: %w", err)
	}
	port := toInt(vm.Port)
	if port == 0 {
		port = 443
	}
	alterID := toInt(vm.Aid)
	name := vm.PS
	if name == "" {
		name = fmt.Sprintf("vmess-%s:%d", vm.Add, port)
	}

	outbound := map[string]any{
		"type":        "vmess",
		"tag":         "proxy-out",
		"server":      vm.Add,
		"server_port": port,
		"uuid":        vm.ID,
		"alter_id":    alterID,
	}
	if vm.Scy != "" && vm.Scy != "auto" {
		outbound["security"] = vm.Scy
	}

	transport := buildTransportFromMap(vm.Net, nil)
	if transport != nil {
		if vm.Host != "" {
			switch vm.Net {
			case "ws":
				transport["headers"] = map[string]any{"Host": vm.Host}
			case "h2":
				transport["host"] = []string{vm.Host}
			}
		}
		if vm.Path != "" {
			switch vm.Net {
			case "ws", "http":
				transport["path"] = vm.Path
			case "h2":
				transport["path"] = vm.Path
			case "grpc":
				transport["service_name"] = vm.Path
			}
		}
		outbound["transport"] = transport
	}

	if vm.TLS == "tls" {
		tls := map[string]any{"enabled": true}
		sni := vm.SNI
		if sni == "" && vm.Host != "" && vm.Net == "ws" {
			sni = vm.Host
		}
		if sni != "" {
			tls["server_name"] = sni
		}
		if vm.FP != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": vm.FP}
		}
		if vm.ALPN != "" {
			tls["alpn"] = strings.Split(vm.ALPN, ",")
		}
		outbound["tls"] = tls
	}

	cfg, _ := json.Marshal(outbound)
	return ParsedLink{Name: name, Protocol: "vmess", NodeConfigJSON: cfg, ServerAddr: vm.Add, ServerPort: port}, nil
}

// --- Trojan ---

func parseTrojan(raw string) (ParsedLink, error) {
	body := strings.TrimPrefix(raw, "trojan://")
	mainPart := body

	name := ""
	if idx := strings.LastIndex(body, "#"); idx != -1 {
		mainPart = body[:idx]
		name, _ = url.QueryUnescape(body[idx+1:])
	}

	var paramsStr string
	if idx := strings.Index(mainPart, "?"); idx != -1 {
		paramsStr = mainPart[idx+1:]
		mainPart = mainPart[:idx]
	}
	mainPart = strings.TrimSuffix(mainPart, "/")

	atIdx := strings.LastIndex(mainPart, "@")
	if atIdx < 0 {
		return ParsedLink{}, fmt.Errorf("invalid trojan link: missing @")
	}
	password := mainPart[:atIdx]
	serverPart := mainPart[atIdx+1:]

	host, port := parseServerPortWithDefault(serverPart, 443)

	q := parseQueryParams(paramsStr)
	sni := safeDecodeURIComponent(q["sni"])
	if sni == "" {
		sni = safeDecodeURIComponent(q["peer"])
	}
	fp := q["fp"]
	alpn := q["alpn"]
	tcpType := q["type"]

	if name == "" {
		name = fmt.Sprintf("trojan-%s:%d", host, port)
	}

	outbound := map[string]any{
		"type":        "trojan",
		"tag":         "proxy-out",
		"server":      host,
		"server_port": port,
		"password":    password,
	}

	transport := buildTransportFromMap(tcpType, q)
	if transport != nil {
		outbound["transport"] = transport
	}

	tls := map[string]any{
		"enabled":     true,
		"server_name": sni,
	}
	if fp != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if alpn != "" {
		tls["alpn"] = strings.Split(alpn, ",")
	}
	outbound["tls"] = tls

	cfg, _ := json.Marshal(outbound)
	return ParsedLink{Name: name, Protocol: "trojan", NodeConfigJSON: cfg, ServerAddr: host, ServerPort: port}, nil
}

// --- helpers ---

func parseServerPortWithDefault(serverPart string, defaultPort int) (string, int) {
	var server string
	var port int

	if strings.HasPrefix(serverPart, "[") {
		// IPv6
		closeBracket := strings.Index(serverPart, "]")
		if closeBracket != -1 {
			server = serverPart[1:closeBracket]
			portPart := serverPart[closeBracket+1:]
			if strings.HasPrefix(portPart, ":") {
				port, _ = strconv.Atoi(portPart[1:])
			}
		}
	} else {
		parts := strings.Split(serverPart, ":")
		server = parts[0]
		if len(parts) > 1 {
			port, _ = strconv.Atoi(parts[1])
		}
	}

	if port == 0 {
		port = defaultPort
	}

	return server, port
}

func buildTransportFromMap(netType string, q map[string]string) map[string]any {
	if q == nil {
		q = map[string]string{}
	}
	switch netType {
	case "ws":
		t := map[string]any{"type": "websocket"}
		if path := q["path"]; path != "" {
			t["path"] = path
		}
		if host := q["host"]; host != "" {
			t["headers"] = map[string]any{"Host": host}
		}
		return t
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if sn := q["serviceName"]; sn != "" {
			t["service_name"] = sn
		}
		return t
	case "h2", "http":
		t := map[string]any{"type": "http"}
		if host := q["host"]; host != "" {
			t["host"] = []string{host}
		}
		if path := q["path"]; path != "" {
			t["path"] = path
		}
		return t
	default:
		return nil
	}
}

func toInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return int(n)
	case json.Number:
		n, _ := val.Int64()
		return int(n)
	default:
		return 0
	}
}
