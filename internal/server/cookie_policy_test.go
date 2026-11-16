package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vonet/internal/store"
)

// TestBrowserEntryOrigin checks entry selection without trusting arbitrary proxy headers.
func TestBrowserEntryOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, host, saved, forwarded, want string
		tls, trust, reject                 bool
	}{
		{name: "HTTPS reverse proxy", host: "vo.example", saved: "https://vo.example", forwarded: "http", want: "https://vo.example"},
		{name: "legacy expanded public IPv6 behind proxy", host: "[2001:db8::1]:8443", saved: "https://[2001:0DB8:0:0:0:0:0:1]:8443", want: "https://[2001:db8::1]:8443"},
		{name: "legacy mapped IPv6 behind proxy", host: "[::ffff:c0a8:114]:17575", saved: "https://[::ffff:192.168.1.20]:17575", want: "https://[::ffff:c0a8:114]:17575"},
		{name: "different public IPv6 host", host: "[2001:db8::2]:8443", saved: "https://[2001:0DB8:0:0:0:0:0:1]:8443", reject: true},
		{name: "different public IPv6 port", host: "[2001:db8::1]:8444", saved: "https://[2001:0DB8:0:0:0:0:0:1]:8443", reject: true},
		{name: "private HTTP", host: "192.168.111.27:17575", saved: "https://vo.example", want: "http://192.168.111.27:17575"},
		{name: "loopback HTTP", host: "127.0.0.1:17575", saved: "https://vo.example", want: "http://127.0.0.1:17575"},
		{name: "direct TLS", host: "192.168.111.27:17575", saved: "https://vo.example", tls: true, want: "https://192.168.111.27:17575"},
		{name: "ignore forged proxy header", host: "192.168.111.27:17575", saved: "https://vo.example", forwarded: "https", want: "http://192.168.111.27:17575"},
		{name: "trusted proxy", host: "192.168.111.27:17575", saved: "https://vo.example", trust: true, forwarded: "https", want: "https://192.168.111.27:17575"},
		{name: "unknown public Host", host: "evil.example", saved: "https://vo.example", reject: true},
		{name: "metadata address", host: "169.254.169.254", saved: "https://vo.example", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{access: parsedAccessConfig{trustProxy: tc.trust}}
			r := httptest.NewRequest("GET", "http://"+tc.host+"/", nil)
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			got, err := s.ssoRequestOrigin(r, tc.saved)
			if (err != nil) != tc.reject || (!tc.reject && got != tc.want) {
				t.Fatalf("origin=%q err=%v want=%q reject=%v", got, err, tc.want, tc.reject)
			}
		})
	}
}

// TestPasswordCookieTransportPolicy keeps password sessions independent of SSO.
func TestPasswordCookieTransportPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, proto             string
		tls, trust, force, want bool
	}{
		{name: "plain HTTP"},
		{name: "direct TLS", tls: true, want: true},
		{name: "trusted HTTPS proxy", trust: true, proto: "https", want: true},
		{name: "untrusted proxy header", proto: "https"},
		{name: "ambiguous proxy header", trust: true, proto: "https,http"},
		{name: "explicit legacy override", force: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{secureCookies: tc.force, access: parsedAccessConfig{trustProxy: tc.trust}}
			request := httptest.NewRequest("POST", "http://vonet.example/api/auth/login", nil)
			request.Header.Set("X-Forwarded-Proto", tc.proto)
			if tc.tls {
				request.TLS = &tls.ConnectionState{}
			}
			if got := server.secureCookiesForRequest(request); got != tc.want {
				t.Fatalf("secure=%v want=%v", got, tc.want)
			}
		})
	}
}

// TestBrowserHTTPSCookieHints accepts only unambiguous same-authority HTTPS hints.
func TestBrowserHTTPSCookieHints(t *testing.T) {
	for _, tc := range []struct {
		name, host        string
		origins, referers []string
		want              bool
	}{
		{name: "HTTPS Origin", host: "vonet.example", origins: []string{"https://vonet.example"}, want: true},
		{name: "default HTTPS port", host: "vonet.example:443", origins: []string{"https://vonet.example"}, want: true},
		{name: "HTTPS Referer", host: "vonet.example", referers: []string{"https://vonet.example/settings?tab=security"}, want: true},
		{name: "HTTP Origin", host: "vonet.example", origins: []string{"http://vonet.example"}, referers: []string{"https://vonet.example/settings"}},
		{name: "cross host", host: "vonet.example", origins: []string{"https://evil.example"}},
		{name: "cross port", host: "vonet.example:17575", origins: []string{"https://vonet.example"}},
		{name: "opaque Origin", host: "vonet.example", origins: []string{"null"}, referers: []string{"https://vonet.example/"}},
		{name: "duplicate Origin", host: "vonet.example", origins: []string{"https://vonet.example", "https://vonet.example"}},
		{name: "Origin with path", host: "vonet.example", origins: []string{"https://vonet.example/settings"}},
		{name: "Origin with query", host: "vonet.example", origins: []string{"https://vonet.example?"}},
		{name: "credentials", host: "vonet.example", origins: []string{"https://user@vonet.example"}},
		{name: "fragment", host: "vonet.example", referers: []string{"https://vonet.example/#fragment"}},
		{name: "no browser headers", host: "vonet.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "http://"+tc.host+"/api/auth/session", nil)
			request.Header["Origin"] = tc.origins
			request.Header["Referer"] = tc.referers
			if got := browserHTTPSCookieHint(request); got != tc.want {
				t.Fatalf("HTTPS hint=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSecurityHeadersHSTSRequiresTrustedTLS(t *testing.T) {
	for _, tc := range []struct {
		name          string
		tls, trust    bool
		forwarded     string
		secureCookies bool
		want          bool
	}{
		{name: "plain HTTP with legacy override", secureCookies: true},
		{name: "direct TLS", tls: true, want: true},
		{name: "trusted TLS termination", trust: true, forwarded: "https", want: true},
		{name: "forged forwarded protocol", forwarded: "https"},
		{name: "ambiguous forwarded protocol", trust: true, forwarded: "https,http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{
				secureCookies: tc.secureCookies,
				access:        parsedAccessConfig{trustProxy: tc.trust},
			}
			request := httptest.NewRequest(http.MethodGet, "http://vonet.example/", nil)
			request.Header.Set("X-Forwarded-Proto", tc.forwarded)
			if tc.tls {
				request.TLS = &tls.ConnectionState{}
			}
			recorder := httptest.NewRecorder()
			server.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(recorder, request)
			got := recorder.Header().Get("Strict-Transport-Security") != ""
			if got != tc.want {
				t.Fatalf("HSTS=%v want=%v headers=%v", got, tc.want, recorder.Header())
			}
		})
	}
}

func TestClearAuthCookiesMatchesPersistedPolicyAndClearsLegacyForms(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("raw-session-token"))
	csrfHash := sha256.Sum256([]byte("csrf"))
	if err := database.CreateSession(
		ctx,
		1,
		tokenHash[:],
		csrfHash[:],
		time.Now().Add(time.Hour),
		true,
	); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: database}
	request := httptest.NewRequest(http.MethodPost, "http://vonet.example/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	recorder := httptest.NewRecorder()
	server.clearAuthCookies(recorder, request)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("persisted policy produced %d deletion cookies: %#v", len(cookies), cookies)
	}
	for _, cookie := range cookies {
		if !cookie.Secure || cookie.MaxAge >= 0 {
			t.Fatalf("invalid persisted-policy deletion cookie: %#v", cookie)
		}
	}

	if err := database.DeleteSession(ctx, tokenHash[:]); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	server.clearAuthCookies(recorder, request)
	cookies = recorder.Result().Cookies()
	if len(cookies) != 4 {
		t.Fatalf("unknown policy produced %d deletion cookies: %#v", len(cookies), cookies)
	}
	seen := map[string]bool{}
	for _, cookie := range cookies {
		seen[map[bool]string{true: "secure", false: "insecure"}[cookie.Secure]] = true
	}
	if !seen["secure"] || !seen["insecure"] {
		t.Fatalf("unknown policy did not clear both Cookie forms: %#v", cookies)
	}
}
