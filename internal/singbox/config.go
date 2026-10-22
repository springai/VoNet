package singbox

import (
	"encoding/json"
	"fmt"
)

// BuildConfig generates a complete sing-box JSON configuration.
// outboundJSON is the sing-box outbound config provided by the user.
func BuildConfig(
	outboundJSON json.RawMessage,
	listenHost string,
	listenPort int,
	authEnabled bool,
	username string,
	password string,
) (json.RawMessage, error) {
	if len(outboundJSON) == 0 {
		return nil, fmt.Errorf("outbound config is empty")
	}
	if listenPort < 1 || listenPort > 65535 {
		return nil, fmt.Errorf("invalid listen port %d", listenPort)
	}
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	inbound := map[string]any{
		"type":        "socks",
		"tag":         "socks-in",
		"listen":      listenHost,
		"listen_port": listenPort,
	}
	if authEnabled && username != "" {
		inbound["users"] = []map[string]any{
			{"username": username, "password": password},
		}
	}

	var outbound map[string]any
	if err := json.Unmarshal(outboundJSON, &outbound); err != nil {
		return nil, fmt.Errorf("parse outbound config: %w", err)
	}
	if _, ok := outbound["tag"]; !ok {
		outbound["tag"] = "proxy-out"
	}

	tag := "proxy-out"
	if t, ok := outbound["tag"].(string); ok && t != "" {
		tag = t
	}

	config := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []any{
			inbound,
		},
		"outbounds": []any{
			outbound,
			map[string]any{"type": "block", "tag": "block"},
		},
		"route": map[string]any{
			"rules": []any{},
			"final": tag,
		},
	}

	result, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box config: %w", err)
	}
	return result, nil
}


// BuildHTTPConfig generates a complete sing-box JSON configuration with HTTP inbound.
// This is similar to BuildConfig but uses an HTTP proxy inbound instead of SOCKS5.
// No authentication is enabled for the HTTP proxy (transparent mode).
func BuildHTTPConfig(
	outboundJSON json.RawMessage,
	listenHost string,
	listenPort int,
) (json.RawMessage, error) {
	if len(outboundJSON) == 0 {
		return nil, fmt.Errorf("outbound config is empty")
	}
	if listenPort < 1 || listenPort > 65535 {
		return nil, fmt.Errorf("invalid listen port %d", listenPort)
	}
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	inbound := map[string]any{
		"type":        "http",
		"tag":         "http-in",
		"listen":      listenHost,
		"listen_port": listenPort,
	}

	var outbound map[string]any
	if err := json.Unmarshal(outboundJSON, &outbound); err != nil {
		return nil, fmt.Errorf("parse outbound config: %w", err)
	}
	if _, ok := outbound["tag"]; !ok {
		outbound["tag"] = "proxy-out"
	}

	tag := "proxy-out"
	if t, ok := outbound["tag"].(string); ok && t != "" {
		tag = t
	}

	config := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []any{
			inbound,
		},
		"outbounds": []any{
			outbound,
			map[string]any{"type": "block", "tag": "block"},
		},
		"route": map[string]any{
			"rules": []any{},
			"final": tag,
		},
	}

	result, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box config: %w", err)
	}
	return result, nil
}