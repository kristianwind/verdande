package httpapi

import (
	"github.com/kristianwind/verdande/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/config"
)

// A Gmail failure has to survive being reported.
//
// It used to be hidden twice over. The handler answered 502 with the code
// `internal_error`, which the interface maps to "something went wrong" — so
// Google's own words, the only thing that says what to do, were replaced by a
// sentence that says nothing. And `writeError` records nothing, so the error log
// was empty: a person clicked Fetch now, read a generic failure, and there was
// nowhere to look it up afterwards.
func TestAGmailFailureSaysWhatGmailSaidAndIsRecorded(t *testing.T) {
	// A Gmail that refuses, the way an expired grant does.
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	defer google.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.GmailClientID = "test-client"
		cfg.GmailClientSecret = "test-secret"
		cfg.GoogleTokenURL = google.URL + "/token"
		cfg.GmailAPIURL = google.URL
	})
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	// Connected, with a refresh token that will be refused.
	if err := ts.db.SaveMailbox(t.Context(), &store.Mailbox{
		UserID: user.ID, Kind: "gmail", RefreshToken: "spent", Trigger: "starred",
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := ts.do(t, "POST", "/api/v1/gmail/sync", nil)
	if resp.StatusCode != StatusUpstreamRefused {
		t.Fatalf("sync: status %d, want 502 — this is Gmail saying no, not this server breaking",
			resp.StatusCode)
	}
	if body["code"] != "gmail_failed" {
		t.Errorf("code = %v, want gmail_failed — `internal_error` is mapped to a generic "+
			"sentence, which throws the diagnosis away", body["code"])
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Error("the message is empty; Gmail's own words are the whole diagnosis")
	}

	// And it is in the error log, which is the point of having one: a sync that has
	// been failing for a week is invisible otherwise.
	_, logged := ts.do(t, "GET", "/api/v1/errors", nil)
	rows, _ := logged["errors"].([]any)
	if len(rows) == 0 {
		t.Fatal("nothing was recorded; the failure is as invisible as it was before")
	}
	row := rows[0].(map[string]any)
	if row["what"] != "sync gmail" {
		t.Errorf("what = %v, want \"sync gmail\"", row["what"])
	}
	if row["status"] != float64(StatusUpstreamRefused) {
		t.Errorf("status = %v, want 502", row["status"])
	}
}

// A slow mailbox must not hold the request open until something in front of this
// server gives up on it.
//
// This is what was actually happening in production: each Gmail call has its own
// thirty-second timeout, and the sync makes up to twenty-five of them one after the
// other. Cloudflare stopped waiting at a hundred seconds and answered with its own
// 502 page — HTML, so the browser got no code, no message and no error-log row, and
// showed "something went wrong". The handler had never returned.
func TestASlowMailboxDoesNotHangTheRequest(t *testing.T) {
	// A Gmail that never answers.
	blocked := make(chan struct{})
	defer close(blocked)
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	}))
	defer google.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.GmailClientID = "test-client"
		cfg.GmailClientSecret = "test-secret"
		cfg.GoogleTokenURL = google.URL + "/token"
		cfg.GmailAPIURL = google.URL
		// A second rather than the real twenty-five: what is being tested is that a
		// budget is applied at all, and spending the production one on every run
		// would put half a minute into the suite to learn the same thing.
		cfg.GmailSyncBudget = time.Second
	})
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	// A token that has not expired, so the run goes straight to the message list
	// rather than stopping at a refresh.
	if err := ts.db.SaveMailbox(t.Context(), &store.Mailbox{
		UserID: user.ID, Kind: "gmail", RefreshToken: "r", AccessToken: "a",
		Trigger: "starred", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// Its own client, because the shared one gives up after ten seconds — which is
	// shorter than the budget being tested and would prove nothing either way.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gmail/sync", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}

	started := time.Now()
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("the request never came back (%v after %s); the sync has no budget of "+
			"its own, and whatever sits in front of this server answers for it",
			err, time.Since(started).Round(time.Second))
	}
	defer resp.Body.Close()
	took := time.Since(started)

	// Cloudflare stops waiting at a hundred seconds. Anything close to that is the
	// bug, whatever status eventually arrives.
	// Comfortably over the one-second budget and far under any proxy's patience.
	if took > 10*time.Second {
		t.Errorf("the sync took %s; the budget is not being applied",
			took.Round(time.Second))
	}
	if resp.StatusCode >= 500 && resp.StatusCode != StatusUpstreamRefused {
		t.Errorf("status %d — a slow mailbox is not this server breaking", resp.StatusCode)
	}
	t.Logf("came back in %s with %d", took.Round(time.Millisecond), resp.StatusCode)
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
