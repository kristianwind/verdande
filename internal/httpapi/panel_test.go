package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kristianwind/verdande/internal/config"
)

// Restarting is administrators only, and sessions only. A leaked API token that
// could restart the server is a denial of service in one request.
func TestRestartingIsAdminsOnlyAndSessionsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	for _, path := range []string{"/api/v1/panel", "/api/v1/panel/restart"} {
		method := "GET"
		if path != "/api/v1/panel" {
			method = "POST"
		}
		if resp, _ := other.do(t, method, path, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as an ordinary user: status %d, want 403", path, resp.StatusCode)
		}
	}

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, admin.ID)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/panel/restart", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("restart with an admin's API token: status %d, want 403", resp.StatusCode)
	}
}

// Unconfigured, it says which of the three settings are missing rather than
// "not configured" — an operator should not have to go and find out which.
func TestAnUnconfiguredPanelSaysWhatIsMissing(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, status := ts.do(t, "GET", "/api/v1/panel", nil)
	if status["configured"] != false {
		t.Fatalf("configured = %v on an instance with no panel settings", status["configured"])
	}
	missing, _ := status["missing"].([]any)
	if len(missing) != 3 {
		t.Errorf("missing = %v, want all three settings named", missing)
	}

	resp, body := ts.do(t, "POST", "/api/v1/panel/restart", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("restart with nothing configured: status %d, want 503", resp.StatusCode)
	}
	if body["error"] == nil {
		t.Error("the refusal says nothing about why")
	}
}

// Configured, it calls the panel with a bearer token and reports what came back.
func TestARestartCallsThePanelWithItsToken(t *testing.T) {
	var gotPath, gotAuth string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer panel.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.PanelURL = panel.URL
		cfg.PanelToken = "hemmelig"
		cfg.PanelServerID = "abc-123"
	})
	ts.bootstrap(t)

	_, status := ts.do(t, "GET", "/api/v1/panel", nil)
	if status["configured"] != true {
		t.Fatalf("configured = %v with all three set", status["configured"])
	}
	// The token is a credential and must never reach a browser.
	for key, value := range status {
		if s, ok := value.(string); ok && s == "hemmelig" {
			t.Errorf("the panel token was sent to the client in %q", key)
		}
	}

	resp, _ := ts.do(t, "POST", "/api/v1/panel/restart", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart: status %d, want 202", resp.StatusCode)
	}
	if gotPath != "/api/servers/abc-123/restart" {
		t.Errorf("called %q", gotPath)
	}
	if gotAuth != "Bearer hemmelig" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

// A panel that refuses is reported as the panel refusing, not as this server
// breaking: the fix is a token with control of that server, and saying "internal
// error" would send somebody looking in the wrong place.
func TestAPanelThatRefusesIsReportedAsSuch(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer panel.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.PanelURL = panel.URL
		cfg.PanelToken = "forkert"
		cfg.PanelServerID = "abc-123"
	})
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/panel/restart", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("a refusing panel: status %d, want 502", resp.StatusCode)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Error("the refusal does not say what to check")
	}
}
