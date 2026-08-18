package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/config"
)

// A panel that never answers must not become this server's problem. The container
// is being asked to stop, so the reply may legitimately never arrive — but the
// request has to end either way, or whatever sits in front answers for it.
func TestAPanelThatNeverAnswersStillEndsTheRequest(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drained first. A real server reads what it was sent, and one that does not
		// never learns the caller has hung up — httptest.Server.Close then waits for
		// a handler that will never return, and the whole binary times out instead
		// of the one request.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer panel.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.PanelURL = panel.URL
		cfg.PanelToken = "test-token"
		cfg.PanelServerID = "server-1"
	})
	ts.bootstrap(t)

	// Its own client: the shared one gives up at ten seconds, which is the very
	// budget under test, so it could not tell a pass from a failure.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/panel/restart", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}

	started := time.Now()
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("the request never came back (%v after %s)", err, time.Since(started).Round(time.Second))
	}
	defer resp.Body.Close()
	took := time.Since(started)

	if took > 20*time.Second {
		t.Errorf("the restart took %s; that is long enough for a proxy to answer first",
			took.Round(time.Second))
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status %d, want 202 — a panel that went quiet is the expected case, "+
			"not a failure", resp.StatusCode)
	}
	t.Logf("came back in %s with %d", took.Round(time.Millisecond), resp.StatusCode)
}

// And a panel that says no must say so in this server's own words, not in a proxy's.
func TestAPanelThatRefusesSaysSoAsJSON(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer panel.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.PanelURL = panel.URL
		cfg.PanelToken = "wrong-token"
		cfg.PanelServerID = "server-1"
	})
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/panel/restart", nil)
	if resp.StatusCode != StatusUpstreamRefused {
		t.Errorf("status %d, want 502: %s", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "403") {
		t.Errorf("the reply does not say what the panel answered: %v", body)
	}
}
