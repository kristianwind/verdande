package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/kristianwind/verdande/internal/auth"
	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/store"
)

// testServer runs the real router against a real database over a real listener.
// Auth is mostly cookies, status codes and middleware ordering — none of which a
// unit test of the handler functions would exercise.
type testServer struct {
	*httptest.Server
	db     *store.DB
	client *http.Client
	// api er den samme server, routeren hænger på. Et par prøver har brug for at
	// nå ind i den — se `icsFetch` — og uden den her er der kun håndtaget udefra.
	api *Server
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return newTestServerWith(t, nil)
}

// newTestServerWith lets a test vary the configuration — a registered OAuth client,
// update checking on — without every other test paying for it.
func newTestServerWith(t *testing.T, configure func(*config.Config)) *testServer {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{
		// `localhost`, not a loopback IP: passkeys refuse an IP address as a relying
		// party id, and every browser makes the same exception for localhost.
		BaseURL:    "http://localhost",
		DataDir:    t.TempDir(),
		SessionTTL: 24 * time.Hour,
		InviteTTL:  7 * 24 * time.Hour,
		ResetTTL:   time.Hour,
		Dev:        true,
	}
	if configure != nil {
		configure(cfg)
	}

	// Discard: a passing test should print nothing, and these handlers log freely.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	api := New(cfg, db, log, nil)
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testServer{
		Server: srv,
		db:     db,
		// The cookie jar is what makes this a session test rather than a
		// sequence of unrelated requests.
		client: &http.Client{Jar: jar, Timeout: 10 * time.Second},
		api:    api,
	}
}

func (ts *testServer) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Same-origin, as a browser would report for the app's own fetches.
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := ts.client.Do(req)
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

// bootstrap creates the first admin and leaves the client signed in.
func (ts *testServer) bootstrap(t *testing.T) {
	t.Helper()
	resp, _ := ts.do(t, "POST", "/api/v1/auth/setup", map[string]string{
		"email": "kristian@example.dk", "name": "Kristian", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: status %d", resp.StatusCode)
	}
}

func TestSetupCreatesTheFirstAdminOnlyOnce(t *testing.T) {
	ts := newTestServer(t)

	resp, body := ts.do(t, "GET", "/api/v1/auth/setup", nil)
	if resp.StatusCode != http.StatusOK || body["needs_setup"] != true {
		t.Fatalf("a fresh instance should need setup: %d %v", resp.StatusCode, body)
	}

	resp, body = ts.do(t, "POST", "/api/v1/auth/setup", map[string]string{
		"email": "kristian@example.dk", "name": "Kristian", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: status %d, body %v", resp.StatusCode, body)
	}
	user, _ := body["user"].(map[string]any)
	if user == nil || user["is_admin"] != true {
		t.Errorf("the first account is not an admin: %v", body)
	}
	// The password hash must never appear in a response.
	if _, leaked := user["password_hash"]; leaked {
		t.Error("the response carries the password hash")
	}

	// A second attempt must be refused, or anyone could add themselves as admin.
	resp, _ = ts.do(t, "POST", "/api/v1/auth/setup", map[string]string{
		"email": "someone@else.dk", "name": "Someone", "password": "et andet kodeord",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second setup: status %d, want 409", resp.StatusCode)
	}

	resp, body = ts.do(t, "GET", "/api/v1/auth/setup", nil)
	if body["needs_setup"] != false {
		t.Errorf("instance still reports needing setup: %v", body)
	}
}

func TestSetupCreatesAnInbox(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.InboxID(t.Context(), user.ID); err != nil {
		t.Errorf("no Inbox was created for the first user: %v", err)
	}
}

func TestLoginAndLogout(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// Signed in from bootstrap.
	resp, body := ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK || body["email"] != "kristian@example.dk" {
		t.Fatalf("me: %d %v", resp.StatusCode, body)
	}

	resp, _ = ts.do(t, "POST", "/api/v1/auth/logout", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: status %d", resp.StatusCode)
	}
	resp, _ = ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("me after logout: status %d, want 401", resp.StatusCode)
	}

	resp, body = ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %v", resp.StatusCode, body)
	}
	if body["totp_required"] != false {
		t.Errorf("login asked for a code with 2FA off: %v", body)
	}

	resp, _ = ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("me after login: status %d", resp.StatusCode)
	}
}

// The address is normalised, so one account cannot be reached under two spellings
// and, worse, a second account cannot be created that shadows the first.
func TestLoginIgnoresEmailCaseAndSpace(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.do(t, "POST", "/api/v1/auth/logout", nil)

	for _, email := range []string{
		"kristian@example.dk", "KRISTIAN@example.dk", "  Kristian@Example.DK  ",
	} {
		resp, _ := ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
			"email": email, "password": "et langt kodeord",
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("login as %q: status %d", email, resp.StatusCode)
		}
		ts.do(t, "POST", "/api/v1/auth/logout", nil)
	}
}

// A wrong password and an address with no account must be indistinguishable, or the
// login form becomes a way to find out who has an account here.
func TestLoginDoesNotRevealWhichAccountsExist(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.do(t, "POST", "/api/v1/auth/logout", nil)

	respWrong, bodyWrong := ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "forkert kodeord",
	})
	respMissing, bodyMissing := ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "findes-ikke@example.dk", "password": "forkert kodeord",
	})

	if respWrong.StatusCode != respMissing.StatusCode {
		t.Errorf("status differs: %d vs %d", respWrong.StatusCode, respMissing.StatusCode)
	}
	if bodyWrong["error"] != bodyMissing["error"] || bodyWrong["code"] != bodyMissing["code"] {
		t.Errorf("message differs:\n  wrong password: %v\n  no account:     %v", bodyWrong, bodyMissing)
	}
}

func TestSessionCookieIsHardened(t *testing.T) {
	ts := newTestServer(t)

	// Read the cookie off the response, not out of the jar: Go's cookiejar keeps
	// only name and value, so every attribute checked here would come back unset
	// regardless of what the server actually sent.
	resp, _ := ts.do(t, "POST", "/api/v1/auth/setup", map[string]string{
		"email": "kristian@example.dk", "name": "Kristian", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: status %d", resp.StatusCode)
	}

	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieInsecure || c.Name == sessionCookieSecure {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly {
		t.Error("session cookie is readable from JavaScript")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", session.SameSite)
	}
	if session.Path != "/" {
		t.Errorf("Path = %q, want /", session.Path)
	}

	// The stored value must be a hash, never the cookie itself: a database dump
	// must not contain anything that can be pasted into a browser.
	var stored string
	err := ts.db.QueryRow(`SELECT id FROM sessions LIMIT 1`).Scan(&stored)
	if err != nil {
		t.Fatal(err)
	}
	if stored == session.Value {
		t.Error("the session token is stored verbatim; it must be hashed")
	}
}

func TestTOTPEnrolmentAndTwoStepLogin(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// Begin: a secret exists, but 2FA is not on yet — closing the tab here must
	// not lock the account.
	// Med adgangskoden: at slå andenfaktoren til er mindst lige så indgribende som
	// at slå den fra, og den har altid spurgt.
	resp, body := ts.do(t, "POST", "/api/v1/auth/totp/setup", map[string]string{
		"password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("totp setup: %d %v", resp.StatusCode, body)
	}
	secret, _ := body["secret"].(string)
	if secret == "" {
		t.Fatal("no secret returned")
	}
	_, me := ts.do(t, "GET", "/api/v1/auth/me", nil)
	if me["totp_enabled"] != false {
		t.Error("2FA switched on before the code was confirmed")
	}

	// Confirm with a real code.
	resp, body = ts.do(t, "POST", "/api/v1/auth/totp/confirm", map[string]string{
		"code": codeFor(t, secret),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("totp confirm: %d %v", resp.StatusCode, body)
	}
	codes, _ := body["recovery_codes"].([]any)
	if len(codes) == 0 {
		t.Fatal("no recovery codes were issued")
	}

	// Now a login is two steps.
	ts.do(t, "POST", "/api/v1/auth/logout", nil)
	resp, body = ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK || body["totp_required"] != true {
		t.Fatalf("login should ask for a code: %d %v", resp.StatusCode, body)
	}

	// The half-finished session must not reach anything.
	resp, body = ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a pending session reached /me: %d", resp.StatusCode)
	}
	if body["code"] != CodeTOTPRequired {
		t.Errorf("code = %v, want %s", body["code"], CodeTOTPRequired)
	}

	// A wrong code does not finish the login.
	resp, _ = ts.do(t, "POST", "/api/v1/auth/login/totp", map[string]string{"code": "000000"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong code was accepted: %d", resp.StatusCode)
	}

	// The right one does.
	resp, body = ts.do(t, "POST", "/api/v1/auth/login/totp", map[string]string{
		"code": codeFor(t, secret),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("totp login: %d %v", resp.StatusCode, body)
	}
	resp, _ = ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("me after completing 2FA: %d", resp.StatusCode)
	}
}

// A recovery code is accepted in the same field as a TOTP code, and works once.
func TestRecoveryCodeCompletesLoginOnce(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, body := ts.do(t, "POST", "/api/v1/auth/totp/setup", map[string]string{
		"password": "et langt kodeord",
	})
	secret := body["secret"].(string)
	_, body = ts.do(t, "POST", "/api/v1/auth/totp/confirm", map[string]string{
		"code": codeFor(t, secret),
	})
	raw := body["recovery_codes"].([]any)
	recovery := raw[0].(string)

	ts.do(t, "POST", "/api/v1/auth/logout", nil)
	ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
	})
	resp, _ := ts.do(t, "POST", "/api/v1/auth/login/totp", map[string]string{"code": recovery})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recovery code rejected: %d", resp.StatusCode)
	}

	// The same code must not work a second time.
	ts.do(t, "POST", "/api/v1/auth/logout", nil)
	ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
	})
	resp, _ = ts.do(t, "POST", "/api/v1/auth/login/totp", map[string]string{"code": recovery})
	if resp.StatusCode == http.StatusOK {
		t.Error("a recovery code worked twice")
	}
}

func TestChangePasswordRequiresTheCurrentOne(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "POST", "/api/v1/auth/password/change", map[string]string{
		"current_password": "det forkerte", "new_password": "et helt nyt kodeord",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("wrong current password: status %d, want 422", resp.StatusCode)
	}

	resp, _ = ts.do(t, "POST", "/api/v1/auth/password/change", map[string]string{
		"current_password": "et langt kodeord", "new_password": "et helt nyt kodeord",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("change password: status %d", resp.StatusCode)
	}

	// The session that made the change survives; the old password does not.
	resp, _ = ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Error("changing the password logged out the session that did it")
	}
	ts.do(t, "POST", "/api/v1/auth/logout", nil)
	resp, _ = ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
	})
	if resp.StatusCode == http.StatusOK {
		t.Error("the old password still works")
	}
}

func TestPasswordRulesRejectShortOnesOnly(t *testing.T) {
	ts := newTestServer(t)

	resp, body := ts.do(t, "POST", "/api/v1/auth/setup", map[string]string{
		"email": "k@example.dk", "name": "K", "password": "kort",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("short password: status %d", resp.StatusCode)
	}
	fields, _ := body["fields"].(map[string]any)
	if fields["password"] == nil {
		t.Errorf("the error does not name the password field: %v", body)
	}

	// A long passphrase with no digits or symbols must be accepted: composition
	// rules push people towards worse passwords, not better ones.
	resp, _ = ts.do(t, "POST", "/api/v1/auth/setup", map[string]string{
		"email": "k@example.dk", "name": "K", "password": "syv heste løber over marken",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("a long passphrase was rejected: status %d", resp.StatusCode)
	}
}

// Asking to reset a password must not reveal whether the address has an account.
func TestForgotPasswordAnswersTheSameEitherWay(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	respKnown, bodyKnown := ts.do(t, "POST", "/api/v1/auth/password/forgot", map[string]string{
		"email": "kristian@example.dk",
	})
	respUnknown, bodyUnknown := ts.do(t, "POST", "/api/v1/auth/password/forgot", map[string]string{
		"email": "findes-ikke@example.dk",
	})

	if respKnown.StatusCode != respUnknown.StatusCode {
		t.Errorf("status differs: %d vs %d", respKnown.StatusCode, respUnknown.StatusCode)
	}
	if bodyKnown["status"] != bodyUnknown["status"] {
		t.Errorf("message differs: %v vs %v", bodyKnown, bodyUnknown)
	}
}

func TestPasswordResetEndsEverySession(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token, err := ts.db.CreatePasswordReset(t.Context(), user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	resp, _ := ts.do(t, "POST", "/api/v1/auth/password/reset", map[string]string{
		"token": token, "password": "et helt nyt kodeord",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: status %d", resp.StatusCode)
	}

	// Everything is signed out — a reset is what you do when you think somebody
	// else has been in your account.
	resp, _ = ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a session survived a password reset: %d", resp.StatusCode)
	}

	// The link works exactly once.
	resp, _ = ts.do(t, "POST", "/api/v1/auth/password/reset", map[string]string{
		"token": token, "password": "endnu et kodeord",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a reset link worked twice: %d", resp.StatusCode)
	}
}

func TestSignupRequiresAnInvite(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "POST", "/api/v1/auth/signup", map[string]string{
		"token": "opfundet", "name": "Ubuden", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("signup without a valid invite: status %d, want 400", resp.StatusCode)
	}

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := ts.db.CreateInvite(t.Context(), "ny@example.dk", "", store.RoleEditor, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// The address comes from the invite, not from the form — so an invite cannot
	// be redirected to somebody else by editing the request.
	resp, body := ts.do(t, "POST", "/api/v1/auth/signup", map[string]string{
		"token": token, "name": "Ny Bruger", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup: %d %v", resp.StatusCode, body)
	}
	user, _ := body["user"].(map[string]any)
	if user["email"] != "ny@example.dk" {
		t.Errorf("email = %v, want the invited address", user["email"])
	}
	if user["is_admin"] != false {
		t.Error("an invited user was made an admin")
	}

	// And the invite is spent.
	resp, _ = ts.do(t, "POST", "/api/v1/auth/signup", map[string]string{
		"token": token, "name": "Endnu En", "password": "et langt kodeord",
	})
	if resp.StatusCode == http.StatusCreated {
		t.Error("an invite link was used twice")
	}
}

func TestUnauthenticatedRequestsAreRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.do(t, "POST", "/api/v1/auth/logout", nil)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/auth/me"},
		{"POST", "/api/v1/auth/totp/setup"},
		{"POST", "/api/v1/auth/password/change"},
		{"GET", "/api/v1/recovery-codes"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp, _ := ts.do(t, tc.method, tc.path, map[string]string{})
			if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound {
				t.Errorf("status %d, want 401", resp.StatusCode)
			}
		})
	}
}

// A cross-site POST must not be able to act on somebody's session.
func TestCrossSiteWritesAreRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	req, err := http.NewRequest("POST", ts.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site POST: status %d, want 403", resp.StatusCode)
	}
	// And the session survived it.
	if r, _ := ts.do(t, "GET", "/api/v1/auth/me", nil); r.StatusCode != http.StatusOK {
		t.Error("the cross-site request logged the user out anyway")
	}
}

func TestMalformedRequestBodies(t *testing.T) {
	ts := newTestServer(t)

	cases := []struct {
		name, body  string
		contentType string
		wantStatus  int
	}{
		{"empty", "", "application/json", http.StatusBadRequest},
		{"not json", "hejsa", "application/json", http.StatusBadRequest},
		{"wrong field type", `{"email": 42}`, "application/json", http.StatusBadRequest},
		{"unknown field", `{"email":"a@b.dk","sneaky":1}`, "application/json", http.StatusBadRequest},
		{"two objects", `{"email":"a@b.dk"}{"email":"c@d.dk"}`, "application/json", http.StatusBadRequest},
		{"wrong content type", `{"email":"a@b.dk"}`, "text/plain", http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", ts.URL+"/api/v1/auth/login", bytes.NewReader([]byte(tc.body)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			resp, err := ts.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

// Repeated password guesses must stop being answered.
func TestLoginIsRateLimited(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.do(t, "POST", "/api/v1/auth/logout", nil)

	var limited bool
	for i := 0; i < 30; i++ {
		resp, _ := ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
			"email": "kristian@example.dk", "password": "gæt",
		})
		if resp.StatusCode == http.StatusTooManyRequests {
			if resp.Header.Get("Retry-After") == "" {
				t.Error("a rate-limited response does not say when to retry")
			}
			limited = true
			break
		}
	}
	if !limited {
		t.Error("thirty wrong passwords in a row were all answered")
	}
}

func codeFor(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, time.Now(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}

// newJarClient gives a test its own cookie jar, which is what makes two clients
// against one server two different browsers rather than the same session twice.
func newJarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return hash
}

// A database that cannot be read must not look like a failed login.
//
// authenticate() cannot tell the two apart on its own: both come back as an error
// from the session lookup. Answering 401 for both means a disk problem presents as
// every session ending at once — the frontend clears its state and shows the
// sign-in screen to everybody — and the logs record a wave of failed logins rather
// than the fault. Found by the end-to-end suite, where a deleted database file
// turned into exactly that.
func TestDatabaseFailureIsNotALogout(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// Still signed in, and the session cookie is in the jar.
	resp, _ := ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me: status %d", resp.StatusCode)
	}

	// Pull the database out from under the server. Every query now fails with
	// something that is not "no such session".
	if err := ts.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	resp, body := ts.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("a broken database signed the user out instead of reporting a fault")
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", resp.StatusCode)
	}
	if body["code"] != CodeInternal {
		t.Errorf("code = %v, want %q", body["code"], CodeInternal)
	}
}

// The other half of the same contract: no cookie at all is still a plain 401.
func TestMissingCredentialsAreStillUnauthorized(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	anonymous := &testServer{Server: ts.Server, db: ts.db, client: newJarClient(t)}
	resp, body := anonymous.do(t, "GET", "/api/v1/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
	if body["code"] != CodeUnauthorized {
		t.Errorf("code = %v, want %q", body["code"], CodeUnauthorized)
	}
}

// And a cookie that is not a session is a 401 too, not a 500 — an unknown token is
// a wrong credential, which is the client's problem and not the server's.
func TestUnknownSessionTokenIsUnauthorized(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	req, err := http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "verdande_session", Value: "ikke-en-rigtig-token"})
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
}

// last_seen_at has been written on every request since sessions existed, for
// exactly this list, and nothing read it. A session you cannot see is a session
// you cannot end.
func TestSessionsCanBeListedAndEnded(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// A second sign-in from "another device": same account, its own cookie jar.
	other := &testServer{Server: ts.Server, db: ts.db, client: newJarClient(t)}
	resp, _ := other.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": "kristian@example.dk", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second login: status %d", resp.StatusCode)
	}

	resp, body := ts.do(t, "GET", "/api/v1/auth/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list sessions: %d %v", resp.StatusCode, body)
	}
	sessions, _ := body["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("want two sessions, got %v", sessions)
	}

	// Exactly one is this one, or the interface cannot tell somebody which row is
	// the browser they are reading it in.
	current := ""
	for _, raw := range sessions {
		s := raw.(map[string]any)
		if s["current"] == true {
			if current != "" {
				t.Fatal("two sessions both claim to be the current one")
			}
			current = s["id"].(string)
		}
		if s["device"] == "" {
			t.Errorf("no device summary: %v", s)
		}
		if s["last_seen_at"] == nil {
			t.Errorf("no last_seen_at, which is the whole reason the column is written: %v", s)
		}
	}
	if current == "" {
		t.Fatal("none of the sessions is marked as the current one")
	}

	// End the other one.
	var otherID string
	for _, raw := range sessions {
		if s := raw.(map[string]any); s["id"] != current {
			otherID = s["id"].(string)
		}
	}
	if resp, _ := ts.do(t, "DELETE", "/api/v1/auth/sessions/"+otherID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete session: status %d", resp.StatusCode)
	}

	// That device is signed out, and this one is not.
	if r, _ := other.do(t, "GET", "/api/v1/auth/me", nil); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("the ended session still works: status %d", r.StatusCode)
	}
	if r, _ := ts.do(t, "GET", "/api/v1/auth/me", nil); r.StatusCode != http.StatusOK {
		t.Errorf("ending another session logged this one out: status %d", r.StatusCode)
	}
}

// Somebody else's session is not yours to end, even with its id in hand.
func TestASessionCanOnlyBeEndedByItsOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, body := ts.do(t, "GET", "/api/v1/auth/sessions", nil)
	mine := body["sessions"].([]any)[0].(map[string]any)["id"].(string)

	if resp, _ := other.do(t, "DELETE", "/api/v1/auth/sessions/"+mine, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
	// And it still works.
	if r, _ := ts.do(t, "GET", "/api/v1/auth/me", nil); r.StatusCode != http.StatusOK {
		t.Errorf("somebody else ended my session: status %d", r.StatusCode)
	}
}

// An API token is accepted almost everywhere, and must not be accepted here: a
// leaked token that could end its owner's sessions, or even list their devices,
// turns a theft into a lockout.
func TestSessionsAreNotReachableWithAToken(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, user.ID)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/auth/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status %d, want 403", resp.StatusCode)
	}
}

func TestUserAgentsAreSummarisedForAPerson(t *testing.T) {
	cases := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36", "Chrome på macOS"},
		// An iPhone's user agent says Mac OS X too, so the order of the tests is
		// the whole correctness of this function.
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "Safari på iPhone"},
		// And Edge claims to be Chrome, which claims to be Safari.
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0 Safari/537.36 Edg/131.0", "Edge på Windows"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0", "Firefox på Linux"},
		{"", "Ukendt enhed"},
		// Not a browser at all. Its own name beats a word that says the server
		// gave up: a CalDAV client is a real session.
		{"curl/8.4.0", "curl/8.4.0"},
	}
	for _, c := range cases {
		if got := describeUserAgent(c.ua); got != c.want {
			t.Errorf("describeUserAgent(%q) = %q, want %q", c.ua, got, c.want)
		}
	}
}

// Andenfaktoren kan ikke slås til uden adgangskoden.
//
// Døren åbnede før med et token og lukkede kun med et kodeord: `disable` har
// altid spurgt, `setup` spurgte om ingenting. Et stjålet token kunne derfor hente
// hemmeligheden, lægge den i sin egen godkender, bekræfte den og beholde den
// eneste kopi af gendannelseskoderne — hvorefter ejerens næste login stoppede ved
// en kode, de ikke havde, og de ikke kunne slå den fra uden at logge ind først.
func TestTOTPCannotBeSwitchedOnWithoutThePassword(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "POST", "/api/v1/auth/totp/setup", map[string]string{
		"password": "det forkerte kodeord",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, ventede at en forkert adgangskode blev afvist", resp.StatusCode)
	}

	resp, _ = ts.do(t, "POST", "/api/v1/auth/totp/setup", map[string]string{})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("en tom adgangskode blev accepteret")
	}
}

// Ratebegrænseren må ikke nulstilles af adgangskoden alene.
//
// Den blev det, og det gav én, der havde kodeordet, ubegrænsede portioner på ti
// gæt: log ind, få en frisk spand og en frisk ventende session, brug ti gæt på
// koden, log ind igen. Andenfaktoren findes præcis til det tilfælde, hvor
// kodeordet allerede er tabt, så den portion skal betales for hver gang.
func TestAPasswordAloneDoesNotRefillTheCodeAttempts(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, body := ts.do(t, "POST", "/api/v1/auth/totp/setup", map[string]string{
		"password": "et langt kodeord",
	})
	secret := body["secret"].(string)
	ts.do(t, "POST", "/api/v1/auth/totp/confirm", map[string]string{"code": codeFor(t, secret)})
	ts.do(t, "POST", "/api/v1/auth/logout", nil)

	// Kodeordet er rigtigt hver gang; kun koden er forkert. Uden loftet ville det
	// her kunne blive ved i det uendelige.
	refused := 0
	for i := 0; i < 14; i++ {
		resp, _ := ts.do(t, "POST", "/api/v1/auth/login", map[string]string{
			"email": "kristian@example.dk", "password": "et langt kodeord",
		})
		if resp.StatusCode == http.StatusTooManyRequests {
			refused++
			continue
		}
		resp, _ = ts.do(t, "POST", "/api/v1/auth/login/totp", map[string]string{"code": "000000"})
		if resp.StatusCode == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("fjorten runder med forkerte koder blev aldrig afvist; spanden fyldes stadig af adgangskoden")
	}
}
