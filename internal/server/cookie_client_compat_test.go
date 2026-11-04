package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"vonet/internal/store"
)

// newCookieCompatibilityFixture creates a local server with a disabled SSO draft for client compatibility tests.
func newCookieCompatibilityFixture(t *testing.T, origin string) *ssoHTTPFixture {
	t.Helper()
	f := newSSOHTTPFixture(t)
	if err := f.auth.ResetAdminCredentials(context.Background(), "admin", "review-local-password"); err != nil {
		t.Fatal(err)
	}
	secret := "review-client-secret"
	_, _, err := f.store.SaveSSOConfiguration(context.Background(), store.SSOSaveRequest{
		Config: store.SSOConfig{
			Kind: "generic", PublicOrigin: origin, ClientID: "review-client",
			AuthURL: "https://id.example/authorize", TokenURL: "https://id.example/token",
			UserInfoURL: "https://id.example/user", UserIDField: "id",
			Scopes: []string{"profile"}, TokenAuthMethod: "client_secret_post", PKCEMode: "s256",
		},
		ClientSecret: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Preserve an existing password+Cookie API client even when a disabled SSO
// draft names HTTPS for the same authority. All network traffic is loopback.
func TestPasswordCookieClientIgnoresSSODraft(t *testing.T) {
	for _, entry := range []string{"http://192.168.111.27:7575", "http://vonet.example"} {
		t.Run(entry, func(t *testing.T) {
			f := newCookieCompatibilityFixture(t, "https"+entry[len("http"):])
			upstream, _ := url.Parse(f.server.URL)
			transport := &http.Transport{Proxy: nil}
			t.Cleanup(transport.CloseIdleConnections)
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, Transport: ssoTestTransport(func(r *http.Request) (*http.Response, error) {
				clone := r.Clone(r.Context())
				clone.URL.Scheme = upstream.Scheme
				clone.URL.Host = upstream.Host
				clone.Host = r.URL.Host
				return transport.RoundTrip(clone)
			})}
			response, err := client.Post(
				entry+"/api/auth/login",
				"application/json",
				bytes.NewBufferString(`{"username":"admin","password":"review-local-password"}`),
			)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("login=%d", response.StatusCode)
			}
			secure := false
			for _, cookie := range response.Cookies() {
				if cookie.Name == sessionCookieName {
					secure = cookie.Secure
				}
			}
			response, err = client.Get(entry + "/api/auth/session")
			if err != nil {
				t.Fatal(err)
			}
			var session struct {
				Data struct {
					CSRF string `json:"csrf_token"`
				}
			}
			if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			t.Logf("entry=%s, SSO draft disabled, login=200, Secure=%v", entry, secure)
			if response.StatusCode != 200 {
				t.Fatalf("password Cookie client regressed: session=%d, want 200", response.StatusCode)
			}
			for _, operation := range []struct{ method, path, body string }{
				{"PUT", "/api/settings/security", `{"mode":"internal"}`},
				{"POST", "/api/auth/logout", `{}`},
			} {
				request, err := http.NewRequest(
					operation.method,
					entry+operation.path,
					bytes.NewBufferString(operation.body),
				)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set(csrfHeaderName, session.Data.CSRF)
				response, err = client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("%s: %d", operation.path, response.StatusCode)
				}
			}
			response, err = client.Get(entry + "/api/auth/session")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 401 {
				t.Fatalf("logout left an authenticated session: %d", response.StatusCode)
			}
		})
	}
}

// TestBearerEntriesRemainIndependentOfSSO checks bearer authorization and read-only enforcement across entries.
func TestBearerEntriesRemainIndependentOfSSO(t *testing.T) {
	f := newCookieCompatibilityFixture(t, "https://vonet.example")
	readWrite, err := f.auth.CreateAPIToken(context.Background(), "review-rw", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := f.auth.CreateAPIToken(context.Background(), "review-ro", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{
		"http://192.168.111.27:7575",
		"http://127.0.0.1:7575",
		"https://vonet.example",
	} {
		for _, tc := range []struct {
			name, method, token string
			want                int
		}{
			{"read-write read", "GET", readWrite.Token, http.StatusOK},
			{"read-write mutation", "PUT", readWrite.Token, http.StatusOK},
			{"read-only read", "GET", readOnly.Token, http.StatusOK},
			{"read-only mutation", "PUT", readOnly.Token, http.StatusForbidden},
			{"invalid bearer", "GET", "vonet_invalid", http.StatusUnauthorized},
		} {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				body, _ := json.Marshal(map[string]any{"mode": "internal"})
				request := httptest.NewRequest(tc.method, entry+"/api/settings/security", bytes.NewReader(body))
				request.RemoteAddr = "192.168.111.3:12345"
				request.Header.Set("Authorization", "Bearer "+tc.token)
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				f.handler.ServeHTTP(recorder, request)
				if recorder.Code != tc.want {
					t.Fatalf("status=%d, want=%d: %s", recorder.Code, tc.want, recorder.Body)
				}
				if len(recorder.Result().Cookies()) != 0 {
					t.Fatal("Bearer request changed browser cookies")
				}
			})
		}
	}
}
