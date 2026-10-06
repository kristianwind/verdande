package config

import (
	"testing"
)

// A typo in VERDANDE_EDITION must stop the program, not fall through to the full
// edition. That is the silent direction of this fault: an instance set up to be a
// notes program would serve every task route, and the only way to notice would be
// to spot that something works which should not.
func TestUnknownEditionIsRefused(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string // the edition we expect, or "" when Load must fail
	}{
		{"", EditionFull}, // unset is the full program, as it has always been
		{EditionFull, EditionFull},
		{EditionNotes, EditionNotes},
		{"note", ""},  // a letter short
		{"Notes", ""}, // the right word, the wrong case
		{"none", ""},  // plausible and meaning nothing
	} {
		t.Setenv("VERDANDE_EDITION", tc.value)
		t.Setenv("VERDANDE_DATA_DIR", t.TempDir())

		got, err := Load()
		if tc.want == "" {
			if err == nil {
				t.Errorf("VERDANDE_EDITION=%q was accepted and gave %q", tc.value, got.Edition)
			}
			continue
		}
		if err != nil {
			t.Errorf("VERDANDE_EDITION=%q: %v", tc.value, err)
			continue
		}
		if got.Edition != tc.want {
			t.Errorf("VERDANDE_EDITION=%q gave %q, want %q", tc.value, got.Edition, tc.want)
		}
		if notes := got.NotesOnly(); notes != (tc.want == EditionNotes) {
			t.Errorf("VERDANDE_EDITION=%q: NotesOnly() = %v", tc.value, notes)
		}
	}
}
