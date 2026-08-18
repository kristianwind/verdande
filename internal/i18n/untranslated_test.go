package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Danish prose left in a component is the failure the dictionaries cannot see.
//
// `i18n_test.go` checks that the two dictionaries agree. It cannot check the thing
// that actually went wrong: a string that was never put in either, because nobody
// noticed it. Three survived the sweep — "Ingen nyere version fundet.", "Ikke mere
// i dag." and a token hint split across two lines — and they survived it for the
// same reason. The sweep grepped for æøå, and none of them contain one.
//
// So this reads the templates instead and looks for the words that give Danish
// away whatever letters it happens to use. A word list rather than a language
// detector: this repository ships one static binary, and a list of thirty common
// words catches the sentences a person would actually write.
func TestNoDanishProseOutsideTheDictionaries(t *testing.T) {
	// Text nodes of three characters or more, and the three attributes a person
	// reads. Anything inside {…} is already an expression — usually a t() call —
	// and is not what this is looking for.
	text := regexp.MustCompile(`>\s*([^<>{}\n][^<>{}]{3,})\s*<`)
	attrs := regexp.MustCompile(`(?:aria-label|title|placeholder)="([^"{][^"]{3,})"`)
	// Words that are Danish and are not also English. "is", "for" and "to" would
	// match English prose and are deliberately absent.
	danish := regexp.MustCompile(`(?i)\b(og|er|ikke|det|den|der|til|kan|som|har|med|af|en|et|du|din|dine|vises|ingen|nyere|fundet|ude|valgt|slettet|gemt|opgave|projekt|indstillinger|fjern|opret|vælg|luk|tilføj|ryd|gem|slet|omdøb)\b`)
	comments := regexp.MustCompile(`(?s)<!--.*?-->`)
	styles := regexp.MustCompile(`(?s)<style>.*`)

	var found []string
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".svelte") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		markup := string(body)
		if at := strings.Index(markup, "</script>"); at > 0 {
			markup = markup[at:]
		}
		markup = styles.ReplaceAllString(comments.ReplaceAllString(markup, ""), "")

		rel, _ := filepath.Rel(root, path)
		for _, m := range append(text.FindAllStringSubmatch(markup, -1),
			attrs.FindAllStringSubmatch(markup, -1)...) {
			phrase := strings.TrimSpace(m[1])
			if danish.MatchString(phrase) {
				found = append(found, rel+": "+strings.Join(strings.Fields(phrase), " "))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if len(found) > 0 {
		t.Errorf("%d Danish string(s) are written into a component instead of going through "+
			"t(). An English interface shows them in Danish, and neither dictionary knows "+
			"they exist:\n  %s", len(found), strings.Join(found, "\n  "))
	}
}
