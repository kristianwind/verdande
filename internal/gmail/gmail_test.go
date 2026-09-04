package gmail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The scope is the one thing verdande chooses about the connection, and it is the
// one the person reads on the consent screen. The flow itself is exercised in
// internal/google; what belongs here is that Gmail asks for modify — read plus the
// one write it makes, taking the star off an imported message — and nothing wider.
func TestTheScopeIsModifyMail(t *testing.T) {
	if !strings.HasSuffix(Scope, "gmail.modify") {
		t.Errorf("scope = %q; want gmail.modify — read, plus removing the star", Scope)
	}
	// Modify is the ceiling. Send, or the full account, would be a different promise
	// to the person on the consent screen than the one this feature needs.
	for _, wider := range []string{"gmail.send", "mail.google.com", "gmail.settings"} {
		if strings.Contains(Scope, wider) {
			t.Errorf("scope = %q; must not include %q", Scope, wider)
		}
	}
}

// Unstar removes the star from a batch in one call, and it addresses the messages
// it was given and only the STARRED label.
func TestUnstarRemovesOnlyTheStar(t *testing.T) {
	var gotPath string
	var gotBody struct {
		IDs            []string `json:"ids"`
		AddLabelIDs    []string `json:"addLabelIds"`
		RemoveLabelIDs []string `json:"removeLabelIds"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusNoContent) // what batchModify answers on success
	}))
	defer srv.Close()

	err := NewClient("token").At(srv.URL).Unstar(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Unstar: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/users/me/messages/batchModify") {
		t.Errorf("path = %q, want the batchModify endpoint", gotPath)
	}
	if strings.Join(gotBody.IDs, ",") != "a,b" {
		t.Errorf("ids = %v, want [a b]", gotBody.IDs)
	}
	if strings.Join(gotBody.RemoveLabelIDs, ",") != "STARRED" {
		t.Errorf("removeLabelIds = %v, want [STARRED]", gotBody.RemoveLabelIDs)
	}
	if len(gotBody.AddLabelIDs) != 0 {
		t.Errorf("addLabelIds = %v, want none — verdande adds no label", gotBody.AddLabelIDs)
	}
}

// An empty batch is not a request: Gmail rejects a batchModify with no ids, and
// there is nothing to say to it anyway.
func TestUnstarOfNothingMakesNoCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	if err := NewClient("token").At(srv.URL).Unstar(context.Background(), nil); err != nil {
		t.Fatalf("Unstar(nil): %v", err)
	}
	if called {
		t.Error("Unstar(nil) made a request; it should make none")
	}
}

func TestQuery(t *testing.T) {
	cases := []struct {
		trigger, label string
		wantContains   []string
		wantEmpty      bool
	}{
		{"starred", "", []string{"is:starred", "newer_than:30d"}, false},
		{"label", "Til handling", []string{`label:"Til handling"`, "newer_than:30d"}, false},
		{"both", "Til handling", []string{"is:starred", `label:"Til handling"`}, false},
		{"both", "", []string{"is:starred"}, false},
		// A label trigger with no label selects nothing, which is right: it would
		// otherwise match the whole mailbox.
		{"label", "", nil, true},
		{"", "", nil, true},
		{"nonsense", "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.trigger+"/"+tc.label, func(t *testing.T) {
			got := Query(tc.trigger, tc.label)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("Query = %q, want empty", got)
				}
				return
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("Query = %q, missing %q", got, want)
				}
			}
			// Without a window, connecting an account with a decade of starred
			// mail would create a decade of tasks in one sweep.
			if !strings.Contains(got, "newer_than:") {
				t.Errorf("Query = %q has no time window", got)
			}
		})
	}
}

func TestSenderName(t *testing.T) {
	cases := map[string]string{
		"Anders Jensen <anders@example.dk>":    "Anders Jensen",
		`"Jensen, Anders" <anders@example.dk>`: "Jensen, Anders",
		"anders@example.dk":                    "anders@example.dk",
		"<anders@example.dk>":                  "anders@example.dk",
		"  Anders <a@b.dk>  ":                  "Anders",
	}
	for in, want := range cases {
		if got := SenderName(in); got != want {
			t.Errorf("SenderName(%q) = %q, want %q", in, got, want)
		}
	}
}
