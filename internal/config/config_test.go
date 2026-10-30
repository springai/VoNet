package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var configEnvironment = []string{
	"VONET_CONFIG",
	"VONET_ADDR",
	"VONET_DATABASE_PATH",
	"VONET_ADMIN_USERNAME",
	"VONET_ADMIN_PASSWORD",
	"VONET_ADMIN_PASSWORD_B64",
	"VONET_SESSION_TTL",
	"VONET_SECURE_COOKIES",
	"VONET_SHUTDOWN_TIMEOUT",
	"VONET_MAX_REQUEST_BODY_BYTES",
	"VONET_MCP_ENABLED",
	"VONET_PROBE_UNKNOWN_USB",
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range configEnvironment {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Address != "0.0.0.0:7575" {
		t.Fatalf("Address = %q", cfg.Address)
	}
	if cfg.MCPEnabled {
		t.Fatal("MCP must be disabled by default")
	}
	if cfg.ProbeUnknownUSB {
		t.Fatal("unknown USB serial probing must be opt-in")
	}
}

func TestMCPAndUnknownUSBEnvironmentOverrides(t *testing.T) {
	clearConfigEnvironment(t)

	t.Setenv("VONET_MCP_ENABLED", "true")
	t.Setenv("VONET_PROBE_UNKNOWN_USB", "true")
	cfg, err := Load()
	if err != nil || !cfg.MCPEnabled || !cfg.ProbeUnknownUSB {
		t.Fatalf("explicit opt-in: %+v %v", cfg, err)
	}

	t.Setenv("VONET_MCP_ENABLED", "false")
	t.Setenv("VONET_PROBE_UNKNOWN_USB", "false")
	cfg, err = Load()
	if err != nil || cfg.MCPEnabled || cfg.ProbeUnknownUSB {
		t.Fatalf("explicit opt-out: %+v %v", cfg, err)
	}

	t.Setenv("VONET_MCP_ENABLED", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid VONET_MCP_ENABLED accepted")
	}
	t.Setenv("VONET_MCP_ENABLED", "false")
	t.Setenv("VONET_PROBE_UNKNOWN_USB", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid VONET_PROBE_UNKNOWN_USB accepted")
	}
}

func TestUnknownUSBProbingRequiresExplicitConfiguration(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "vonet.json")
	if err := os.WriteFile(path, []byte(`{"probe_unknown_usb":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VONET_CONFIG", path)
	if cfg, err := Load(); err != nil || !cfg.ProbeUnknownUSB {
		t.Fatalf("file opt-in: %+v %v", cfg, err)
	}
	t.Setenv("VONET_PROBE_UNKNOWN_USB", "false")
	if cfg, err := Load(); err != nil || cfg.ProbeUnknownUSB {
		t.Fatalf("environment override: %+v %v", cfg, err)
	}
	t.Setenv("VONET_PROBE_UNKNOWN_USB", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid opt-in accepted")
	}
}

func TestLoadFileThenEnvironmentOverride(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "vonet.json")
	content := []byte(`{
		"address": "127.0.0.1:8000",
		"database_path": "/tmp/from-file.db",
		"session_ttl": "2h",
		"secure_cookies": false,
		"shutdown_timeout": "12s",
		"max_request_body_bytes": 4096
	}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VONET_CONFIG", path)
	t.Setenv("VONET_ADDR", "0.0.0.0:9000")
	t.Setenv("VONET_SECURE_COOKIES", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Address != "0.0.0.0:9000" || !cfg.SecureCookies {
		t.Fatalf("environment override not applied: %+v", cfg)
	}
	if cfg.SessionTTL != 2*time.Hour {
		t.Fatalf("file values not applied: %+v", cfg)
	}
}

func TestLoadRejectsUnknownJSONField(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "vonet.json")
	if err := os.WriteFile(path, []byte(`{"unknown": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VONET_CONFIG", path)

	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly accepted unknown field")
	}
}

func TestLoadIgnoresLegacyAdministratorConfiguration(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "vonet.json")
	if err := os.WriteFile(path, []byte(`{
		"admin_username": "legacy-admin",
		"admin_password": "legacy-password"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VONET_CONFIG", path)
	t.Setenv("VONET_ADMIN_USERNAME", "environment-admin")
	t.Setenv("VONET_ADMIN_PASSWORD", "environment-password")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() rejected ignored legacy credentials: %v", err)
	}
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("VONET_SESSION_TTL", "tomorrow")

	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly accepted invalid duration")
	}
}
