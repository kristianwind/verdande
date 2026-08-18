package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
	})
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	// Connected, with a refresh token that will be refused.
	if err := ts.db.SetUserSettings(t.Context(), user.ID, "gmail", map[string]any{
		"refresh_token": "spent", "trigger": "starred",
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := ts.do(t, "POST", "/api/v1/gmail/sync", nil)
	if resp.StatusCode != http.StatusBadGateway {
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
	if row["status"] != float64(http.StatusBadGateway) {
		t.Errorf("status = %v, want 502", row["status"])
	}
}
