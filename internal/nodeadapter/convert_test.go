package nodeadapter

import (
	"testing"
)

func TestParseVlessRealityWithSpecialChars(t *testing.T) {
	// The problematic link with [ in sni
	link := "vless://11111111-1111-4111-8111-111111111111@192.0.2.10:30000?security=reality&type=tcp&allowInsecure=1&sni=[www.example.com&fp=chrome&flow=xtls-rprx-vision&sid=000000000000&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&encryption=none&udp=1#test-reality-node"

	parsed, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("ParseShareLink failed: %v", err)
	}

	t.Logf("Name: %s", parsed.Name)
	t.Logf("Protocol: %s", parsed.Protocol)
	t.Logf("ServerAddr: %s", parsed.ServerAddr)
	t.Logf("ServerPort: %d", parsed.ServerPort)
	t.Logf("NodeConfigJSON: %s", string(parsed.NodeConfigJSON))

	if parsed.Name != "test-reality-node" {
		t.Errorf("expected name 'test-reality-node', got '%s'", parsed.Name)
	}
	if parsed.Protocol != "vless" {
		t.Errorf("expected protocol 'vless', got '%s'", parsed.Protocol)
	}
	if parsed.ServerAddr != "192.0.2.10" {
		t.Errorf("expected server '192.0.2.10', got '%s'", parsed.ServerAddr)
	}
	if parsed.ServerPort != 30000 {
		t.Errorf("expected port 30000, got %d", parsed.ServerPort)
	}
}
