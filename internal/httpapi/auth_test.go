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
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{
		BaseURL:    "http://127.0.0.1",
		DataDir:    t.TempDir(),
		SessionTTL: 24 * time.Hour,
		InviteTTL:  7 * 24 * time.Hour,
		ResetTTL:   time.Hour,
		Dev:        true,
	}
	// Discard: a passing test should print nothing, and these handlers log freely.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := httptest.NewServer(New(cfg, db, log, nil))
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
	resp, body := ts.do(t, "POST", "/api/v1/auth/totp/setup", nil)
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

	_, body := ts.do(t, "POST", "/api/v1/auth/totp/setup", nil)
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
