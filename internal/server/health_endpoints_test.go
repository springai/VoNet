package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/crypto/bcrypt"

	"vonet/internal/auth"
	"vonet/internal/sso"
	"vonet/internal/store"
)

type ssoHealthApplication struct {
	server  *httptest.Server
	client  *http.Client
	cleanup *sso.Service
}

func newSSOHealthApplication(t *testing.T) ssoHealthApplication {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	authService, err := auth.New(database, auth.Options{
		SessionTTL: time.Hour,
		BcryptCost: bcrypt.MinCost,
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupService := sso.New(database, sso.Options{})
	handler, err := New(Options{
		Store: database,
		Auth:  authService,
		SSO:   cleanupService,
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html>SPA shell</html>")},
		},
		MaxRequestBodyBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	return ssoHealthApplication{
		server:  httpServer,
		client:  httpServer.Client(),
		cleanup: cleanupService,
	}
}

func TestOperationalHealthEndpointsAreAnonymousAndNonIdentifying(t *testing.T) {
	app := newTestApplication(t)

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/healthz", contentType: "application/json", contains: `"status":"ok"`},
		{path: "/readyz", contentType: "application/json", contains: `"status":"ready"`},
		{path: "/metrics", contentType: "text/plain", contains: "vonet_ready 1"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response, err := app.client.Get(app.server.URL + test.path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.StatusCode, body)
			}
			if !strings.Contains(response.Header.Get("Content-Type"), test.contentType) {
				t.Fatalf("Content-Type = %q", response.Header.Get("Content-Type"))
			}
			if !strings.Contains(string(body), test.contains) {
				t.Fatalf("body = %q, want %q", body, test.contains)
			}
			for _, forbidden := range []string{"imsi", "iccid", "msisdn", "proxy", "device_id"} {
				if strings.Contains(strings.ToLower(string(body)), forbidden) {
					t.Fatalf("body exposes forbidden label %q: %s", forbidden, body)
				}
			}
		})
	}
}

func TestOperationalHealthEndpointsRejectPOST(t *testing.T) {
	app := newTestApplication(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		request, err := http.NewRequest(http.MethodPost, app.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := app.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want %d", path, response.StatusCode, http.StatusMethodNotAllowed)
		}
	}
}

func TestSSOCleanupHealthControlsReadinessAndMetrics(t *testing.T) {
	app := newSSOHealthApplication(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.cleanup.Cleanup(ctx); err == nil {
		t.Fatal("cleanup with a cancelled context unexpectedly succeeded")
	}
	if health := app.cleanup.CleanupHealth(); health.Healthy {
		t.Fatalf("failed cleanup health = %+v", health)
	}

	response, err := app.client.Get(app.server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy /readyz status = %d, body = %s", response.StatusCode, body)
	}
	var readiness struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &readiness); err != nil {
		t.Fatalf("decode /readyz: %v", err)
	}
	if readiness.Status != "degraded" || readiness.Reason != "sso_cleanup" {
		t.Fatalf("/readyz = %#v, want degraded/sso_cleanup", readiness)
	}

	response, err = app.client.Get(app.server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "vonet_sso_cleanup_healthy 0") {
		t.Fatalf("/metrics omitted unhealthy SSO cleanup gauge:\n%s", body)
	}

	if err := app.cleanup.Cleanup(context.Background()); err != nil {
		t.Fatalf("recover cleanup: %v", err)
	}
	response, err = app.client.Get(app.server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recovered /readyz status = %d, body = %s", response.StatusCode, body)
	}
	var recovered struct {
		Status            string `json:"status"`
		SSOCleanupHealthy bool   `json:"sso_cleanup_healthy"`
	}
	if err := json.Unmarshal(body, &recovered); err != nil {
		t.Fatalf("decode recovered /readyz: %v", err)
	}
	if recovered.Status != "ready" || !recovered.SSOCleanupHealthy {
		t.Fatalf("recovered /readyz = %#v, want ready with healthy SSO cleanup", recovered)
	}

	response, err = app.client.Get(app.server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "vonet_sso_cleanup_healthy 1") {
		t.Fatalf("/metrics omitted recovered SSO cleanup gauge:\n%s", body)
	}
}
