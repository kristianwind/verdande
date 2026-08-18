// Package i18n exists only to hold this test.
//
// The interface's strings live in the frontend, where they are read; nothing in Go
// needs them. What Go can do is fail the build when the two dictionaries drift,
// which is the failure mode a translation always has and which nothing else here
// would notice: a key present in Danish and missing from English renders as Danish
// inside an English screen, and a key present only in English is a string nobody
// will ever see.
//
// A scanner rather than a parser, the same approach `colors_test.go` and
// `openapi_test.go` take: it needs the keys out of two lists this repository owns,
// and that is worth more than a dependency in a project whose whole shape is "one
// static binary".
package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestTheTwoDictionariesHaveTheSameKeys(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "src", "lib", "locales")
	da := readKeys(t, filepath.Join(dir, "da.js"))
	en := readKeys(t, filepath.Join(dir, "en.js"))

	if len(da) == 0 {
		t.Fatal("no keys found in da.js — the scanner is looking at the wrong shape")
	}

	var missing, extra []string
	for key := range da {
		if !en[key] {
			missing = append(missing, key)
		}
	}
	for key := range en {
		if !da[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("%d keys are in da.js and not in en.js — they render as Danish inside an "+
			"English screen:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("%d keys are in en.js and not in da.js — nothing reads them, because Danish "+
			"is the source:\n  %s", len(extra), strings.Join(extra, "\n  "))
	}
}

// The placeholders have to match too. `t()` fills `{name}` from what the caller
// passes, so a translation that spells one differently leaves `{navn}` on the
// screen — which is worse than an untranslated sentence, because it looks like a
// bug in the app rather than a gap in the dictionary.
func TestTheTwoDictionariesUseTheSamePlaceholders(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "src", "lib", "locales")
	da := readEntries(t, filepath.Join(dir, "da.js"))
	en := readEntries(t, filepath.Join(dir, "en.js"))

	for key, danish := range da {
		english, ok := en[key]
		if !ok {
			continue // reported by the test above
		}
		want := placeholders(danish)
		got := placeholders(english)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			t.Errorf("%s: Danish fills %v, English fills %v", key, want, got)
		}
	}
}

var (
	// `'some.key': 'the string',` — single-quoted keys and values, which is what
	// prettier writes for these files.
	reEntry       = regexp.MustCompile(`(?m)^\s*'([a-zA-Z0-9._]+)':\s*(.*?),?\s*$`)
	rePlaceholder = regexp.MustCompile(`\{(\w+)\}`)
)

func readKeys(t *testing.T, path string) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for key := range readEntries(t, path) {
		keys[key] = true
	}
	return keys
}

func readEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	out := map[string]string{}
	for _, m := range reEntry.FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = m[2]
	}
	return out
}

func placeholders(s string) []string {
	var found []string
	for _, m := range rePlaceholder.FindAllStringSubmatch(s, -1) {
		found = append(found, m[1])
	}
	sort.Strings(found)
	return found
}
