package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"vonet/internal/auth"
	"vonet/internal/store"
)

// TestSSOFlowCookieSecureOverride covers issuance and deletion under the global override.
func TestSSOFlowCookieSecureOverride(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, origin := range []string{"http://192.168.1.20:7575", "https://vonet.example"} {
			for _, clear := range []bool{false, true} {
				server := &Server{secureCookies: force}
				flow := store.SSOFlow{
					ID: "test-flow", RedirectURI: origin + "/api/auth/sso/callback",
					ExpiresAt: time.Now().Add(time.Minute),
				}
				recorder := httptest.NewRecorder()
				server.setSSOFlowCookie(recorder, flow, "browser-token", clear)
				cookies := recorder.Result().Cookies()
				want := force || origin == "https://vonet.example"
				if len(cookies) != 1 {
					t.Fatal("missing flow cookie")
				}
				cookie := cookies[0]
				if cookie.Secure != want || !cookie.HttpOnly ||
					cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/api/auth/sso" {
					t.Fatalf("force=%v origin=%s clear=%v: invalid cookie flags", force, origin, clear)
				}
				if clear && (cookie.Value != "" || cookie.MaxAge != -1) {
					t.Fatal("flow cookie was not cleared")
				}
			}
		}
	}
}

// TestSessionCSRFReplacementRemembersSecureLogin reproduces hint-free proxy refreshes with missing or stale CSRF cookies.
func TestSessionCSRFReplacementRemembersSecureLogin(t *testing.T) {
	for _, tc := range []struct {
		name, entry, origin             string
		directTLS, trust, force, secure bool
	}{
		{name: "HTTPS browser behind default proxy", entry: "https://vonet.example", origin: "https://vonet.example", secure: true},
		{name: "direct TLS", entry: "https://vonet.example", directTLS: true, secure: true},
		{name: "trusted proxy", entry: "http://vonet.example", trust: true, secure: true},
		{name: "force Secure", entry: "http://vonet.example", force: true, secure: true},
		{name: "HTTP API client", entry: "http://vonet.example"},
		{name: "private HTTP browser", entry: "http://192.168.111.27:7575", origin: "http://192.168.111.27:7575"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSSOHTTPFixture(t)
			if err := f.auth.ResetAdminCredentials(context.Background(), "admin", "cookie-policy-fixture"); err != nil {
				t.Fatal(err)
			}
			f.handler.secureCookies = tc.force
			f.handler.access.trustProxy = tc.trust
			request := httptest.NewRequest(
				http.MethodPost,
				tc.entry+"/api/auth/login",
				bytes.NewBufferString(`{"username":"admin","password":"cookie-policy-fixture"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			if !tc.directTLS {
				request.TLS = nil
			}
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			if tc.trust {
				request.Header.Set("X-Forwarded-Proto", "https")
			}
			recorder := httptest.NewRecorder()
			request.RemoteAddr = "127.0.0.1:12345"
			f.handler.ServeHTTP(recorder, request)
			if recorder.Code != 200 {
				t.Fatalf("login=%d", recorder.Code)
			}
			var session *http.Cookie
			var validCSRF string
			for _, cookie := range recorder.Result().Cookies() {
				if cookie.Name == sessionCookieName {
					session = cookie
				}
				if cookie.Name == csrfCookieName {
					validCSRF = cookie.Value
				}
			}
			if session == nil || validCSRF == "" {
				t.Fatal("missing login credentials")
			}
			stored, err := f.store.SessionByTokenHash(context.Background(), auth.TokenHash(session.Value))
			if err != nil || stored.SecureCookies != tc.secure {
				t.Fatalf("stored Secure policy=%v err=%v", stored.SecureCookies, err)
			}
			for _, csrf := range []string{validCSRF, "", "stale-csrf"} {
				// Backend sees HTTP without Origin, Referer or forwarded headers.
				request = httptest.NewRequest("GET", "http://"+request.Host+"/api/auth/session", nil)
				request.AddCookie(session)
				if csrf != "" {
					request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
				}
				recorder = httptest.NewRecorder()
				request.RemoteAddr = "127.0.0.1:12345"
				f.handler.ServeHTTP(recorder, request)
				if recorder.Code != 200 {
					t.Fatalf("refresh=%d", recorder.Code)
				}
				found := false
				for _, cookie := range recorder.Result().Cookies() {
					if cookie.Name == csrfCookieName {
						found = true
						if cookie.Secure != tc.secure {
							t.Fatalf("CSRF replacement Secure=%v, want=%v", cookie.Secure, tc.secure)
						}
					}
				}
				if (tc.secure || csrf != validCSRF) && !found {
					t.Fatal("missing CSRF replacement")
				}
				var body struct {
					Data struct {
						CSRF string `json:"csrf_token"`
					} `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				mutation := httptest.NewRequest(
					http.MethodPut,
					"http://"+request.Host+"/api/settings/security",
					bytes.NewBufferString(`{"mode":"internal"}`),
				)
				mutation.AddCookie(session)
				mutation.AddCookie(&http.Cookie{Name: csrfCookieName, Value: body.Data.CSRF})
				mutation.Header.Set("Content-Type", "application/json")
				mutation.Header.Set(csrfHeaderName, body.Data.CSRF)
				recorder = httptest.NewRecorder()
				mutation.RemoteAddr = "127.0.0.1:12345"
				f.handler.ServeHTTP(recorder, mutation)
				if recorder.Code != 200 {
					t.Fatalf("first CSRF write=%d", recorder.Code)
				}
			}
		})
	}
}

// TestSessionCookiePolicyOnlyUpgrades protects both cookies on an HTTPS upgrade
// and on a hint-free retry whose browser did not receive the first response.
func TestSessionCookiePolicyOnlyUpgrades(t *testing.T) {
	f := newSSOHTTPFixture(t)
	csrf := f.login(t)
	base := f.server.URL
	entry, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	cookies := f.client.Jar.Cookies(entry)
	var originalSession *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == sessionCookieName {
			originalSession = cookie
		}
	}
	if originalSession == nil {
		t.Fatal("missing initial session cookie")
	}
	stored, err := f.store.SessionByTokenHash(context.Background(), auth.TokenHash(originalSession.Value))
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range []string{"https://vonet.example/settings", ""} {
		request := httptest.NewRequest("GET", "http://vonet.example/api/auth/session", nil)
		if hint != "" {
			request.Header.Set("Referer", hint)
		}
		for _, cookie := range cookies {
			if cookie.Name == sessionCookieName {
				request.AddCookie(cookie)
			}
		}
		request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
		recorder := httptest.NewRecorder()
		request.RemoteAddr = "127.0.0.1:12345"
		f.handler.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("session=%d", recorder.Code)
		}
		found := map[string]bool{}
		for _, cookie := range recorder.Result().Cookies() {
			if cookie.Name == sessionCookieName || cookie.Name == csrfCookieName {
				found[cookie.Name] = true
				if !cookie.Secure ||
					cookie.HttpOnly != (cookie.Name == sessionCookieName) ||
					cookie.SameSite != http.SameSiteStrictMode ||
					cookie.Path != "/" || cookie.Domain != "" {
					t.Fatalf("invalid upgraded cookie attributes for %s", cookie.Name)
				}
				if !cookie.Expires.Equal(stored.ExpiresAt) {
					t.Fatalf("cookie upgrade changed the original expiry for %s", cookie.Name)
				}
				if cookie.Name == sessionCookieName && cookie.Value != originalSession.Value ||
					cookie.Name == csrfCookieName && cookie.Value != csrf {
					t.Fatalf("cookie upgrade unexpectedly rotated %s", cookie.Name)
				}
			}
		}
		if !found[sessionCookieName] || !found[csrfCookieName] {
			t.Fatal("upgrade or retry did not issue both Secure authentication cookies")
		}
	}
}

// TestSessionPolicyFailurePreservesCSRF checks server-side credential retention and retry with the saved session cookie.
func TestSessionPolicyFailurePreservesCSRF(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		status        int
	}{
		{"database error", "SELECT RAISE(FAIL, 'injected policy failure');", http.StatusInternalServerError},
		{"session not found", "SELECT RAISE(IGNORE);", http.StatusUnauthorized},
	} {
		for _, stale := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stale=%v", tc.name, stale), func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "failure.db")
				f := newSSOHTTPFixtureWithPath(t, path)
				csrf := f.login(t)
				entry, err := url.Parse(f.server.URL)
				if err != nil {
					t.Fatal(err)
				}
				var session *http.Cookie
				for _, cookie := range f.client.Jar.Cookies(entry) {
					if cookie.Name == sessionCookieName {
						session = cookie
					}
				}
				if session == nil {
					t.Fatal("missing session")
				}
				before, err := f.store.SessionByTokenHash(ctx, auth.TokenHash(session.Value))
				if err != nil {
					t.Fatal(err)
				}
				connection, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { connection.Close() })
				trigger := `CREATE TRIGGER fail_cookie_policy BEFORE UPDATE OF secure_cookies ON sessions BEGIN ` +
					tc.failure + ` END`
				if _, err := connection.ExecContext(ctx, trigger); err != nil {
					t.Fatal(err)
				}
				refresh := func() *httptest.ResponseRecorder {
					request := httptest.NewRequest("GET", "http://vonet.example/api/auth/session", nil)
					request.RemoteAddr = "127.0.0.1:12345"
					request.Header.Set("Referer", "https://vonet.example/settings")
					request.AddCookie(session)
					if stale {
						request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "stale-csrf"})
					}
					recorder := httptest.NewRecorder()
					f.handler.ServeHTTP(recorder, request)
					return recorder
				}
				recorder := refresh()
				if recorder.Code != tc.status {
					t.Fatalf("failed refresh=%d, want=%d", recorder.Code, tc.status)
				}
				for _, cookie := range recorder.Result().Cookies() {
					if cookie.Name == csrfCookieName && cookie.MaxAge >= 0 {
						t.Fatal("failed refresh issued a new CSRF cookie")
					}
				}
				if tc.status == http.StatusUnauthorized {
					expired := false
					for _, cookie := range recorder.Result().Cookies() {
						if cookie.Name == sessionCookieName && cookie.MaxAge < 0 {
							expired = true
						}
					}
					if !expired {
						t.Fatal("unauthorized refresh did not expire the session cookie")
					}
				}
				after, err := f.store.SessionByTokenHash(ctx, auth.TokenHash(session.Value))
				if err != nil {
					t.Fatal(err)
				}
				if after.SecureCookies || !bytes.Equal(before.CSRFHash, after.CSRFHash) {
					t.Fatal("failed policy update changed the session or CSRF hash")
				}
				if _, err := f.auth.ValidateCSRF(ctx, session.Value, csrf); err != nil {
					t.Fatalf("previous CSRF credential invalidated: %v", err)
				}
				if _, err := connection.ExecContext(ctx, `DROP TRIGGER fail_cookie_policy`); err != nil {
					t.Fatal(err)
				}
				recorder = refresh()
				if recorder.Code != http.StatusOK {
					t.Fatalf("retry=%d", recorder.Code)
				}
				found := false
				for _, cookie := range recorder.Result().Cookies() {
					if cookie.Name == csrfCookieName {
						found = true
						if !cookie.Secure {
							t.Fatal("retry downgraded cookie protection")
						}
						if _, err := f.auth.ValidateCSRF(ctx, session.Value, cookie.Value); err != nil {
							t.Fatalf("replacement CSRF credential invalid: %v", err)
						}
					}
				}
				if !found {
					t.Fatal("retry did not return a CSRF cookie")
				}
			})
		}
	}
}
