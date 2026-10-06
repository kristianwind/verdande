package httpapi

import (
	"bytes"
	"fmt"
	"io/fs"
)

// The product name is in the built frontend as a literal in four places, because
// all four are read before any JavaScript runs: the window title and the meta
// description in the HTML shell, and the two names in the web manifest that decide
// what an installed PWA is called on a home screen.
//
// They cannot be filled in by the app at runtime. The shell is parsed before the
// first script executes, the manifest is fetched by the browser rather than by us,
// and the edition is not known until /auth/setup-state has answered — so a frontend
// that renamed itself after loading would show the sibling's name first and correct
// itself a moment later, on every load, in the tab and on the install prompt.
//
// So the server does it, once at startup, on the way out. One frontend build, two
// names: the build is identical in both editions and the bytes served differ.
//
// Each replacement is REQUIRED and the anchors are exact. A rewrite that quietly
// matched nothing is the whole failure mode here — it leaves an Urd instance
// calling itself verdande, which is cosmetic enough that nobody files it and
// nothing else notices.
type rebrand struct {
	old, new string
}

// shellRebrands returns the replacements for index.html.
//
// Note what is NOT in here: `localStorage.getItem('verdande:theme')` and its two
// neighbours in the inline theme script. Those keys are storage, not a name — the
// rest of the app reads the same three, so renaming them here and nowhere else
// would set the theme from one key and read it from another, and the only symptom
// is the white flash that script exists to prevent. This is why the replacements
// are written out as whole anchored strings rather than as a replace of the word
// "verdande".
func shellRebrands(slug, description string) []rebrand {
	return []rebrand{
		{"<title>verdande</title>", "<title>" + slug + "</title>"},
		{
			`<meta name="description" content="verdande — opgaver og projekter, delt." />`,
			`<meta name="description" content="` + description + `" />`,
		},
	}
}

// manifestRebrands returns the replacements for manifest.webmanifest. The
// manifest's own description is a sentence rather than the shell's "name — sentence"
// form, so it is passed separately rather than derived.
func manifestRebrands(slug, description string) []rebrand {
	return []rebrand{
		{`"name": "verdande"`, `"name": "` + slug + `"`},
		{`"short_name": "verdande"`, `"short_name": "` + slug + `"`},
		{`"description": "Opgaver og projekter, delt."`, `"description": "` + description + `"`},
	}
}

// applyRebrands replaces every pair exactly once and fails on the first anchor it
// cannot find, naming it. Failing loudly rather than returning what it managed is
// the point: a partial rename is a page whose tab and whose install prompt disagree
// about which program this is.
func applyRebrands(raw []byte, pairs []rebrand) ([]byte, error) {
	out := raw
	for _, p := range pairs {
		old := []byte(p.old)
		if bytes.Count(out, old) != 1 {
			return nil, fmt.Errorf("found %d occurrences of %q, want exactly 1", bytes.Count(out, old), p.old)
		}
		out = bytes.Replace(out, old, []byte(p.new), 1)
	}
	return out, nil
}

// rebrandedAssets reads index.html and manifest.webmanifest out of the built
// frontend and returns them renamed for this edition.
//
// Both or neither: an instance serving a renamed shell beside the sibling's
// manifest installs under the wrong name on a phone, which is the half nobody looks
// at until it is on their home screen.
func rebrandedAssets(web fs.FS, slug, shellDesc, manifestDesc string) (shell, manifest []byte, err error) {
	raw, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("index.html: %w", err)
	}
	shell, err = applyRebrands(raw, shellRebrands(slug, shellDesc))
	if err != nil {
		return nil, nil, fmt.Errorf("index.html: %w", err)
	}

	raw, err = fs.ReadFile(web, "manifest.webmanifest")
	if err != nil {
		return nil, nil, fmt.Errorf("manifest.webmanifest: %w", err)
	}
	manifest, err = applyRebrands(raw, manifestRebrands(slug, manifestDesc))
	if err != nil {
		return nil, nil, fmt.Errorf("manifest.webmanifest: %w", err)
	}
	return shell, manifest, nil
}

// The descriptions for the notes edition. Kept beside the replacements they feed
// rather than in the locale files, because these two strings are read by a browser
// and a home screen before any locale has been chosen — the shell is in Danish for
// the same reason the built app.html is.
const (
	notesShellDescription    = "urd — noter, delt."
	notesManifestDescription = "Noter, delt."
)

// renamed returns the renamed bytes for `name`, or nil to serve the built file.
//
// Nil for everything in the full edition, which is what keeps that path exactly as
// it was: one comparison per request against a nil slice.
func (s *Server) renamed(name string) []byte {
	switch name {
	case "index.html":
		return s.shell
	case "manifest.webmanifest":
		return s.manifest
	}
	return nil
}

// contentTypeFor names the type for the two assets served from memory.
//
// http.ServeContent sniffs from the extension, and it gets .html right and
// .webmanifest wrong — Go's table does not have it, so the sniffer reads the first
// bytes, sees JSON-ish text and answers text/plain. A manifest served as text/plain
// is ignored by the browser without an error anywhere: the install prompt simply
// does not appear.
func contentTypeFor(name string) string {
	if name == "manifest.webmanifest" {
		return "application/manifest+json"
	}
	return "text/html; charset=utf-8"
}
