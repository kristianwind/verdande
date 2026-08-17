package update

import (
	"context"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.2.2", false},
		{"v2.0.0", "v1.9.9", false},
		// The comparison is numeric, not lexical: 10 is after 9.
		{"v1.9.0", "v1.10.0", true},
		{"v1.10.0", "v1.9.0", false},
		// Without the v.
		{"1.0.0", "1.0.1", true},
		// Pre-release suffixes compare as their base version.
		{"v1.0.0-rc1", "v1.0.0", false},
		{"v1.0.0", "v1.0.1-rc1", true},

		// A development build is ahead of every release, not behind it. Telling
		// somebody running their own tree that they are out of date every time
		// they open the app teaches them to ignore the notice.
		{"dev", "v1.0.0", false},
		{"v1.0.0-dirty", "v2.0.0", false},

		// Anything unparseable reports nothing rather than guessing.
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
		{"v1.0.0", "latest", false},
		{"v1.0", "v1.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.current+"→"+tc.latest, func(t *testing.T) {
			if got := IsNewer(tc.current, tc.latest); got != tc.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

// Off unless asked for, and when off it must not reach out at all.
func TestDisabledCheckerNeverCallsOut(t *testing.T) {
	c := New("v1.0.0", false)

	status := c.Status(context.Background())
	if !status.Disabled {
		t.Error("a disabled checker does not report itself as disabled")
	}
	if status.Available {
		t.Error("a disabled checker reported an update")
	}
	if status.Current != "v1.0.0" {
		t.Errorf("Current = %q", status.Current)
	}
	// CheckedAt stays empty, which is how the interface tells "no update" apart
	// from "we never looked".
	if status.CheckedAt != "" {
		t.Errorf("CheckedAt = %q on a disabled checker", status.CheckedAt)
	}
}
