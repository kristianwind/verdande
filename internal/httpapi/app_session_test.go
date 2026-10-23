package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The iOS app loads its interface from itself — capacitor://localhost — so it
// never shares an origin with the server and never gets a cookie back. It signs
// in like a browser does and carries the same kind of session, as a bearer token.

// appDo is a request the way the app makes it: no cookie jar, the token in a
// header, and the browser engine reporting the request as cross-site, because to
// WKWebView it is.
func (ts *testServer) appDo(t *testing.T, token, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Origin", "capacitor://localhost")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	var decoded map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp, decoded
}

func appLogin(t *testing.T, ts *testServer) (*http.Response, map[string]any) {
	t.Helper()
	return ts.appDo(t, "", "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
		"client": "app", "device_name": "Kristians iPhone",
	})
}

func TestAppLoginReturnsATokenAndNoCookie(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := appLogin(t, ts)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("app login: %d %v", resp.StatusCode, body)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("an app login set a cookie: %v", resp.Cookies())
	}
	token, _ := body["session_token"].(string)
	if token == "" {
		t.Fatalf("no session_token in %v", body)
	}
	if body["user"] == nil {
		t.Error("no user in the response")
	}

	// The token is the whole credential, from a foreign origin, reads and writes.
	resp, me := ts.appDo(t, token, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK || me["email"] != "kristian@example.dk" {
		t.Fatalf("me with the app token: %d %v", resp.StatusCode, me)
	}
	resp, body = ts.appDo(t, token, "POST", "/api/v1/projects", map[string]string{"name": "Fra telefonen"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("a write from the app was refused: %d %v", resp.StatusCode, body)
	}

	// It is a session, not an API token: it reaches what a token may not, and it
	// is in the list of devices under the name the app gave itself.
	resp, body = ts.appDo(t, token, "GET", "/api/v1/auth/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sessions from the app: %d %v", resp.StatusCode, body)
	}
	var found bool
	for _, raw := range body["sessions"].([]any) {
		s := raw.(map[string]any)
		if s["current"] == true {
			found = true
			if s["device"] != "Kristians iPhone" || s["client"] != "app" {
				t.Errorf("the app's session reads %v / %v", s["device"], s["client"])
			}
		}
	}
	if !found {
		t.Error("the app's own session is not marked current")
	}

	// And signing out ends it.
	if resp, _ = ts.appDo(t, token, "POST", "/api/v1/auth/logout", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if resp, _ = ts.appDo(t, token, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the token still works after logout: %d", resp.StatusCode)
	}
}

// A browser's session never leaves its httpOnly cookie. Its value replayed as a
// bearer must not work: that would turn the header into a second way past the
// SameSite and CSRF locks the cookie sits behind.
func TestABrowserSessionIsNotABearer(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var cookie string
	for _, c := range ts.client.Jar.Cookies(mustURL(t, ts.URL)) {
		if c.Name == sessionCookieInsecure {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("bootstrap left no session cookie")
	}
	if resp, _ := ts.appDo(t, cookie, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a browser session worked as a bearer: %d", resp.StatusCode)
	}
	// Control: the same value is still a perfectly good cookie.
	if resp, _ := ts.do(t, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("control: the cookie itself: %d", resp.StatusCode)
	}
}

func TestAppLoginWithASecondFactor(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	_, body := ts.do(t, "POST", "/api/v1/auth/totp/setup", map[string]string{"password": "et langt kodeord"})
	secret, _ := body["secret"].(string)
	if resp, body := ts.do(t, "POST", "/api/v1/auth/totp/confirm", map[string]string{"code": codeFor(t, secret)}); resp.StatusCode != http.StatusOK {
		t.Fatalf("totp confirm: %d %v", resp.StatusCode, body)
	}

	resp, body := appLogin(t, ts)
	token, _ := body["session_token"].(string)
	if resp.StatusCode != http.StatusOK || body["totp_required"] != true || token == "" {
		t.Fatalf("app login should hand over a token and ask for a code: %d %v", resp.StatusCode, body)
	}
	if body["user"] != nil {
		t.Error("the user came back before the code was given")
	}

	// Half a login reaches nothing, as in a browser, and does not slide yet.
	resp, body = ts.appDo(t, token, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized || body["code"] != CodeTOTPRequired {
		t.Errorf("a pending app session reached /me: %d %v", resp.StatusCode, body)
	}
	session, _, err := ts.db.SessionByToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if session.IdleTTL != 0 || time.Until(session.ExpiresAt) > 11*time.Minute {
		t.Errorf("a pending app session lives %v and slides by %v", time.Until(session.ExpiresAt), session.IdleTTL)
	}

	resp, body = ts.appDo(t, token, "POST", "/api/v1/auth/login/totp", map[string]string{"code": codeFor(t, secret)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("totp from the app: %d %v", resp.StatusCode, body)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("finishing an app login set a cookie: %v", resp.Cookies())
	}
	if resp, _ = ts.appDo(t, token, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("me after the code: %d", resp.StatusCode)
	}
	session, _, _ = ts.db.SessionByToken(t.Context(), token)
	if session.IdleTTL != 7*24*time.Hour {
		t.Errorf("a finished app session slides by %v, want the app TTL", session.IdleTTL)
	}
}

// A phone in daily use must not sign itself out; one left in a drawer must. The
// end moves forward each time the session is used, and a browser's does not.
func TestAnAppSessionSlides(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	_, body := appLogin(t, ts)
	token := body["session_token"].(string)

	// Pretend it was last used a while ago and is a day from ending.
	soon := time.Now().Add(24 * time.Hour).Unix()
	if _, err := ts.db.ExecContext(t.Context(),
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE client = 'app'`,
		time.Now().Add(-time.Hour).Unix(), soon); err != nil {
		t.Fatal(err)
	}
	if resp, _ := ts.appDo(t, token, "GET", "/api/v1/auth/me", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("me: %d", resp.StatusCode)
	}
	var expires int64
	if err := ts.db.QueryRowContext(t.Context(),
		`SELECT expires_at FROM sessions WHERE client = 'app'`).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(expires, 0)); left < 6*24*time.Hour {
		t.Errorf("using the app session left %v on it, want about the app TTL", left)
	}

	// Control: a browser session used the same way keeps its fixed end.
	if _, err := ts.db.ExecContext(t.Context(),
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE client = 'web'`,
		time.Now().Add(-time.Hour).Unix(), soon); err != nil {
		t.Fatal(err)
	}
	ts.do(t, "GET", "/api/v1/auth/me", nil)
	if err := ts.db.QueryRowContext(t.Context(),
		`SELECT expires_at FROM sessions WHERE client = 'web'`).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires != soon {
		t.Errorf("a browser session's end moved from %d to %d", soon, expires)
	}
}

func TestAppLoginRefusesWhatItDoesNotKnow(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, tc := range []struct {
		name  string
		body  map[string]string
		field string
	}{
		{"unknown client", map[string]string{"client": "android", "device_name": "x"}, "client"},
		{"app without a name", map[string]string{"client": "app", "device_name": "  "}, "device_name"},
	} {
		tc.body["email"] = "kristian@example.dk"
		tc.body["password"] = "et langt kodeord"
		resp, body := ts.appDo(t, "", "POST", "/api/v1/auth/login", tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", tc.name, resp.StatusCode)
			continue
		}
		if fields, _ := body["fields"].(map[string]any); fields[tc.field] == nil {
			t.Errorf("%s: no error on %q in %v", tc.name, tc.field, body)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
