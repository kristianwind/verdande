package gmail

import (
	"strings"
	"testing"
)

// The scope is the one thing verdande chooses about the connection, and it is the
// one the person reads on the consent screen. The flow itself is exercised in
// internal/google; what belongs here is that Gmail still asks for read-only mail.
func TestTheScopeIsReadOnlyMail(t *testing.T) {
	if !strings.HasSuffix(Scope, "gmail.readonly") {
		t.Errorf("scope = %q; verdande never needs to send or modify", Scope)
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
