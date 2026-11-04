package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"vonet/internal/sso"
	"vonet/internal/store"
)

// Exercise the same saved identity over multiple browser entries, including
// callback origin binding, primary cookie refresh and the first CSRF write.
func TestSSOAutomaticBrowserEntries(t *testing.T) {
	f := newSSOHTTPFixture(t)
	csrf := f.login(t)
	base, _ := url.Parse(f.server.URL)
	passwordCookies := f.client.Jar.Cookies(base)
	do := func(
		origin, method, path string,
		body any,
		cookies []*http.Cookie,
		csrf string,
		directTLS bool,
	) *httptest.ResponseRecorder {
		var buffer bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&buffer).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequest(method, origin+path, &buffer)
		request.RemoteAddr = "192.168.111.3:12345"
		// httptest derives TLS from URL; reset it for reverse-proxy cases.
		request.TLS = nil
		if directTLS {
			request.TLS = &tls.ConnectionState{}
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(csrfHeaderName, csrf)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, request)
		return recorder
	}

	config := f.config()
	config["public_origin"] = "https://vonet.example"
	recorder := do(
		"https://vonet.example",
		http.MethodPut,
		"/api/settings/sso",
		map[string]any{
			"expected_revision": 0,
			"config":            config,
			"client_secret":     "fixture-client-secret",
		},
		passwordCookies,
		csrf,
		false,
	)
	if recorder.Code != 200 {
		t.Fatalf("save: %d %s", recorder.Code, recorder.Body)
	}
	recorder = do(
		"https://vonet.example",
		http.MethodPost,
		"/api/auth/sso/bind",
		map[string]any{"expected_revision": 1},
		passwordCookies,
		csrf,
		false,
	)
	if recorder.Code != 200 {
		t.Fatalf("bind: %d %s", recorder.Code, recorder.Body)
	}
	var payload struct {
		Data struct {
			URL string `json:"authorization_url"`
		}
	}
	json.Unmarshal(recorder.Body.Bytes(), &payload)
	authURL, _ := url.Parse(payload.Data.URL)
	callback := sso.CallbackPath + "?code=fixture-code&state=" + url.QueryEscape(authURL.Query().Get("state"))
	recorder = do(
		"https://vonet.example",
		http.MethodGet,
		callback,
		nil,
		recorder.Result().Cookies(),
		"",
		false,
	)
	if recorder.Code != 303 || recorder.Header().Get("Location") != "/settings?sso_result=bound" {
		t.Fatalf("bind callback: %d %s", recorder.Code, recorder.Header().Get("Location"))
	}

	config["enabled"] = true
	recorder = do(
		"https://vonet.example",
		http.MethodPut,
		"/api/settings/sso",
		map[string]any{"expected_revision": 2, "config": config},
		passwordCookies,
		csrf,
		false,
	)
	if recorder.Code != 200 {
		t.Fatalf("enable without Cookie environment override: %d %s", recorder.Code, recorder.Body)
	}
	for _, tc := range []struct {
		origin            string
		secure, directTLS bool
	}{
		{"https://vonet.example", true, false},
		{"http://192.168.111.27:7575", false, false},
		{"http://127.0.0.1:7575", false, false},
		{"http://[::1]:7575", false, false},
		{"http://[::ffff:c0a8:114]:7575", false, false},
		{"https://192.168.111.27:7575", true, true},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			recorder := do(tc.origin, http.MethodGet, "/api/auth/sso/start", nil, nil, "", tc.directTLS)
			if recorder.Code != 302 {
				t.Fatalf("start: %d %s", recorder.Code, recorder.Body)
			}
			flowCookies := recorder.Result().Cookies()
			if len(flowCookies) != 1 ||
				flowCookies[0].Secure != tc.secure ||
				flowCookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("wrong flow Cookie policy")
			}
			authorizationURL, _ := url.Parse(recorder.Header().Get("Location"))
			if authorizationURL.Query().Get("redirect_uri") != tc.origin+sso.CallbackPath {
				t.Fatalf("callback=%s", authorizationURL.Query().Get("redirect_uri"))
			}
			callback := sso.CallbackPath + "?code=fixture-code&state=" +
				url.QueryEscape(authorizationURL.Query().Get("state"))
			wrong := do(
				"http://10.0.0.99:7575",
				http.MethodGet,
				callback,
				nil,
				flowCookies,
				"",
				false,
			)
			if wrong.Header().Get("Location") != "/login?sso_error=invalid_flow" {
				t.Fatal("cross-entry callback accepted")
			}
			recorder = do(tc.origin, http.MethodGet, callback, nil, flowCookies, "", tc.directTLS)
			if recorder.Code != 303 || recorder.Header().Get("Location") != "/" {
				t.Fatalf(
					"login: %d %s",
					recorder.Code,
					recorder.Header().Get("Location"),
				)
			}
			var credentials []*http.Cookie
			var csrf string
			for _, cookie := range recorder.Result().Cookies() {
				if cookie.Name == sessionCookieName || cookie.Name == csrfCookieName {
					if cookie.Secure != tc.secure || cookie.SameSite != http.SameSiteStrictMode {
						t.Fatal("wrong primary Cookie policy")
					}
					credentials = append(credentials, cookie)
					if cookie.Name == csrfCookieName {
						csrf = cookie.Value
					}
				}
			}
			if len(credentials) != 2 {
				t.Fatal("missing login credentials")
			}
			recorder = do(tc.origin, http.MethodGet, "/api/auth/session", nil, credentials, "", tc.directTLS)
			if recorder.Code != 200 {
				t.Fatalf("session: %d %s", recorder.Code, recorder.Body)
			}
			for _, cookie := range recorder.Result().Cookies() {
				if cookie.Name == csrfCookieName && cookie.Secure != tc.secure {
					t.Fatal("session refresh changed transport policy")
				}
			}
			recorder = do(
				tc.origin,
				http.MethodPut,
				"/api/settings/security",
				map[string]any{"mode": "internal"},
				credentials,
				csrf,
				tc.directTLS,
			)
			if recorder.Code != 200 {
				t.Fatalf("first CSRF write: %d %s", recorder.Code, recorder.Body)
			}
		})
	}

	// Simulate a configuration saved by an older version without re-saving it
	// through the new normalization path; the existing binding remains usable.
	snapshot, err := f.store.SSOSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.PublicOrigin = "https://[2001:0DB8:0:0:0:0:0:1]:8443"
	legacy, _, err := f.store.SaveSSOConfiguration(
		context.Background(),
		store.SSOSaveRequest{ExpectedRevision: snapshot.Config.Revision, Config: snapshot.Config},
	)
	if err != nil || legacy.Binding == nil {
		t.Fatalf("prepare legacy configuration: %v", err)
	}
	entry := "https://[2001:db8::1]:8443"
	recorder = do(entry, http.MethodGet, "/api/auth/sso/start", nil, nil, "", false)
	if recorder.Code != http.StatusFound {
		t.Fatalf("legacy IPv6 start: %d %s", recorder.Code, recorder.Body)
	}
	authorizationURL, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil || authorizationURL.Query().Get("redirect_uri") != entry+sso.CallbackPath {
		t.Fatalf("legacy IPv6 callback: %v %v", err, authorizationURL)
	}
	callback = sso.CallbackPath + "?code=fixture-code&state=" +
		url.QueryEscape(authorizationURL.Query().Get("state"))
	recorder = do(entry, http.MethodGet, callback, nil, recorder.Result().Cookies(), "", false)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/" {
		t.Fatalf(
			"legacy IPv6 login: %d %s",
			recorder.Code,
			recorder.Header().Get("Location"),
		)
	}
}
