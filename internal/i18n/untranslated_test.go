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
// The same words, inside the code rather than the markup.
//
// `confirm()`, `alert()` and `app.toast()` take a string argument, and a string
// argument lives in the script block where the scanner above never looks. Six of
// them survived the whole i18n sweep that way — every destructive confirmation in
// the app, which is the worst possible set to leave in one language, because it is
// the text somebody reads in the half-second before they agree to lose something.
func TestNoDanishProseInDialogsAndToasts(t *testing.T) {
	// Every string literal in the argument list of one of the three, not just one
	// sitting flush against the paren. A toast written as a ternary over two lines
	// hid Danish from the earlier version of this guard for a whole release.
	// Deliberately still narrow: a general scan of every literal in a script block
	// would drown in class names, keys and query fragments.
	call := regexp.MustCompile(`(?:confirm|alert|app\.toast)\(`)
	danish := regexp.MustCompile(`(?i)\b(og|er|ikke|det|den|der|til|kan|som|har|med|af|en|et|du|din|dine|vises|ingen|nyere|fundet|ude|valgt|slettet|gemt|gemmes|hentet|hentes|beskeder|opgave|opgaver|projekt|indstillinger|fjern|opret|vælg|luk|tilføj|ryd|gem|slet|omdøb|bliver|holder|alle)\b`)

	var found []string
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".svelte") && !strings.HasSuffix(path, ".js") {
			return nil
		}
		if strings.Contains(path, "locales") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		src := string(body)
		for _, loc := range call.FindAllStringIndex(src, -1) {
			for _, lit := range literalsInCall(src[loc[1]:]) {
				if danish.MatchString(lit) {
					found = append(found, rel+": "+lit)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if len(found) > 0 {
		t.Errorf("%d dialog or toast string(s) are written in Danish inside the code. "+
			"They are what somebody reads before agreeing to lose something, and an "+
			"English interface shows them in Danish:\n  %s",
			len(found), strings.Join(found, "\n  "))
	}
}

// literalsInCall returns the string literals in one argument list, given the text
// just past its opening paren. It stops at the matching close paren so the next
// statement's strings are somebody else's problem.
func literalsInCall(src string) []string {
	var out []string
	depth := 1
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return out
			}
		case '\'', '"', '`':
			end := strings.IndexByte(src[i+1:], c)
			if end < 0 {
				return out
			}
			if lit := src[i+1 : i+1+end]; len(lit) >= 4 {
				out = append(out, lit)
			}
			i += end + 1
		}
	}
	return out
}

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
