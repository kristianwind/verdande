package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The palette is written down three times: here, in web/src/lib/colors.js for the
// picker, and in web/src/app.css as the tokens that actually paint. Three copies
// is two chances to drift, and the failure is quiet — a colour the server accepts
// and the interface cannot draw renders as the default, which looks like a colour
// that did not save.
//
// So this reads the other two. A small scanner rather than a parser, the same
// approach openapi_test.go takes to the spec: it needs the ids out of a list this
// repository also owns, and that is worth more than a dependency in a project
// whose whole shape is "one static binary".
func TestThePaletteIsTheSameInGoAndInTheFrontend(t *testing.T) {
	js := readFrontendColors(t, filepath.Join("..", "..", "web", "src", "lib", "colors.js"))
	css := readColorTokens(t, filepath.Join("..", "..", "web", "src", "app.css"))

	for _, name := range Colors {
		if !js[name] {
			t.Errorf("%q is accepted by the server and missing from colors.js — the picker cannot offer it", name)
		}
		if !css[name] {
			t.Errorf("--color-%s is missing from app.css — the server accepts a colour nothing can paint", name)
		}
	}
	for name := range js {
		if !ValidColor(name) {
			t.Errorf("colors.js offers %q, which the server refuses — picking it would fail to save", name)
		}
	}
}

// readFrontendColors pulls the ids out of the COLORS array: lines shaped
// `{ id: 'tomato', name: 'Tomat' },`.
func readFrontendColors(t *testing.T, path string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read colors.js: %v", err)
	}

	found := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		id, ok := between(line, "{ id: '", "'")
		if ok {
			found[id] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("no colours were read out of colors.js — the scanner and the file disagree")
	}
	return found
}

// readColorTokens pulls the names out of `--color-tomato: #d9614f;`.
func readColorTokens(t *testing.T, path string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}

	found := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		name, ok := between(line, "--color-", ":")
		if ok {
			found[name] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("no --color- tokens were read out of app.css")
	}
	return found
}

func between(line, open, close string) (string, bool) {
	start := strings.Index(line, open)
	if start < 0 {
		return "", false
	}
	rest := line[start+len(open):]
	end := strings.Index(rest, close)
	if end <= 0 {
		return "", false
	}
	return rest[:end], true
}
