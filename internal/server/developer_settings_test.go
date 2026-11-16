package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vonet/internal/developer"
	"vonet/internal/httpsmode"
	"vonet/internal/store"
)

func TestAdvancedSettingsAreAvailableWithoutDeveloperMode(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	manager, err := httpsmode.New(ctx, database, t.TempDir(), "127.0.0.1:17575")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		store: database, logger: regionTestLogger(), https: manager, maxRequestBodyBytes: 4096,
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request){
		server.handleSystemLimits,
		server.handleHTTPSSettings,
		server.handleHTTPSCertificate,
	} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("advanced endpoint status = %d, want 200; body=%s", response.Code, response.Body.String())
		}
	}
}

func TestSystemLimitsUpdatesGlobalSMSLimit(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server := &Server{store: database, logger: regionTestLogger(), maxRequestBodyBytes: 4096}
	request := httptest.NewRequest(http.MethodPut, "/api/settings/developer", strings.NewReader(`{"sms_hourly_limit":21}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleSystemLimits(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if got := developer.SMSHourlyLimit(ctx, database); got != 21 {
		t.Fatalf("SMS hourly limit = %d, want 21", got)
	}
}

func TestSystemLimitsValidatesSMSHourlyLimit(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server := &Server{store: database, logger: regionTestLogger(), maxRequestBodyBytes: 4096}
	for _, body := range []string{
		`{"sms_hourly_limit":0}`,
		`{"sms_hourly_limit":-1}`,
		`{"sms_hourly_limit":1.5}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/settings/developer", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.handleSystemLimits(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, response.Code)
		}
	}
}

func TestSystemLimitsUpdatesAutoClearModemStorage(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server := &Server{store: database, logger: regionTestLogger(), maxRequestBodyBytes: 4096}
	request := httptest.NewRequest(http.MethodPut, "/api/settings/developer", strings.NewReader(`{"auto_clear_modem_storage":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleSystemLimits(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if developer.AutoClearModemStorage(ctx, database) {
		t.Fatal("auto-clear should be disabled")
	}
}
