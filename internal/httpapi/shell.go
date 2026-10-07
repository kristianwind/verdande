package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
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
	// Any .webmanifest, not the one name — there are two of them now, and the
	// second one was served as HTML for about four minutes because this read
	// `name == "manifest.webmanifest"`. Nothing would have reported it: a manifest
	// with the wrong type is ignored silently, and the only symptom is an install
	// prompt that does not appear.
	if strings.HasSuffix(name, ".webmanifest") {
		return "application/manifest+json"
	}
	return "text/html; charset=utf-8"
}

// notesAssetAliases maps the icon paths the shell and the manifest already point
// at to urd's own files. Both sets are in the binary; the server decides.
//
// Aliased rather than renamed in the manifest, and the reason is the third entry.
// Safari ignores a manifest's icons entirely and looks for `apple-touch-icon.png`
// BY NAME — so pointing the manifest at urd-icon-192.png would have left iOS with
// Verdande's mark, or, if that file were renamed too, with a screenshot of the page
// as its home-screen icon. Keeping the paths and swapping the bytes behind them
// fixes all four at once, including the favicon in `app.html`, which is not in the
// manifest either.
var notesAssetAliases = map[string]string{
	"icon.svg":             "urd-icon.svg",
	"icon-192.png":         "urd-icon-192.png",
	"icon-512.png":         "urd-icon-512.png",
	"apple-touch-icon.png": "urd-apple-touch-icon.png",
}

// resolveNotesAssets returns the aliases that have a file behind them, and reports
// the ones that do not.
//
// Resolved once at startup rather than per request, so a missing file is one loud
// line in the log instead of a silent fallback on every page load. The fallback is
// still to Verdande's icon rather than to nothing: an icon is the one asset whose
// absence turns into a broken-image placeholder on the Updates screen — which is
// exactly how four releases of nolimit-views shipped with no icon — so serving the
// wrong mark beats serving none.
func resolveNotesAssets(web fs.FS, missing func(name, alias string)) map[string]string {
	out := map[string]string{}
	for name, alias := range notesAssetAliases {
		if _, err := fs.Stat(web, alias); err != nil {
			missing(name, alias)
			continue
		}
		out[name] = alias
	}
	return out
}

// ---------------------------------------------------------------------------
// The second door.
//
// One instance, one database, one login — and two things you can install. urd is
// reached at /urd, which serves the same single-page app under its own identity:
// its own title, its own mark, and its own web manifest.
//
// The manifest is the part that matters and the part that is easy to get wrong.
// An operating system does not install a URL, it installs a *manifest*, and the
// identity of an installed app is the manifest's `id`. Two manifests on one origin
// with different ids are two installable apps; two URLs sharing one manifest are
// one app with a bookmark.
//
// And the shell has to be served per door rather than patched by JavaScript,
// because iOS reads the page's <head> when somebody taps Add to Home Screen —
// Safari looks for apple-touch-icon BY NAME and has historically ignored the
// manifest's icons entirely. A head rewritten after load is a head that may or may
// not be the one iOS read.
// ---------------------------------------------------------------------------

// urdDoorPath is the entry point, and it is also the manifest's id. One constant,
// because those two being the same string is the whole mechanism.
const urdDoorPath = "/urd"

// urdShellRebrands rewrites the four things in the shell's <head> that decide what
// this door is: what the tab says, what a crawler reads, which manifest the browser
// fetches, and which icons it finds without the manifest.
//
// Note what is NOT in here, for the same reason as in shellRebrands above: the
// three `verdande:` localStorage keys in the inline theme script. They are storage
// rather than a name, the rest of the app reads them, and — now more than before —
// the two doors share an origin and therefore share that storage. Renaming them
// here would give the two apps different themes by accident and lose everybody's
// setting on the way.
func urdShellRebrands() []rebrand {
	return []rebrand{
		{"<title>verdande</title>", "<title>urd</title>"},
		{
			`<meta name="description" content="verdande — opgaver og projekter, delt." />`,
			`<meta name="description" content="` + notesShellDescription + `" />`,
		},
		{
			`<link rel="manifest" href="/manifest.webmanifest" />`,
			`<link rel="manifest" href="/urd.webmanifest" />`,
		},
		{
			`<link rel="icon" href="/icon.svg" type="image/svg+xml" />`,
			`<link rel="icon" href="/urd-icon.svg" type="image/svg+xml" />`,
		},
		{
			`<link rel="apple-touch-icon" href="/apple-touch-icon.png" />`,
			`<link rel="apple-touch-icon" href="/urd-apple-touch-icon.png" />`,
		},
	}
}

// urdManifest builds the second door's manifest from the one the frontend build
// produced, rather than writing a second one by hand.
//
// Transformed as JSON rather than by string replacement, which the shell above
// does: a manifest needs keys the built one does not have — `id` and `start_url`
// — so there is nothing to anchor a replacement on, and a hand-written copy would
// drift on the fields the two genuinely share (the colours, the display mode, the
// language).
//
// `id` is what makes this a second app rather than a bookmark. `scope` is spelled
// out even though "/" is what the spec would default to from a start_url of
// "/urd": an app whose scope did not cover the whole origin would open its own
// links in a browser tab the first time somebody navigated out of /urd, and that
// is a rule nobody should have to remember while reading this.
func urdManifest(raw []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("the built manifest is not JSON: %w", err)
	}

	m["id"] = urdDoorPath
	m["start_url"] = urdDoorPath
	m["scope"] = "/"
	m["name"] = "urd"
	m["short_name"] = "urd"
	m["description"] = notesManifestDescription

	icons, ok := m["icons"].([]any)
	if !ok || len(icons) == 0 {
		return nil, fmt.Errorf("the built manifest has no icons to rewrite")
	}
	for i, entry := range icons {
		icon, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("icon %d is not an object", i)
		}
		src, _ := icon["src"].(string)
		// Every icon must be one this door has a file for. Refusing an unknown one
		// is the point: an icon added to the manifest and not to this map would
		// otherwise leave urd's install prompt showing Verdande's mark, which is
		// cosmetic enough that nobody files it.
		swapped, ok := map[string]string{
			"/icon.svg":     "/urd-icon.svg",
			"/icon-192.png": "/urd-icon-192.png",
			"/icon-512.png": "/urd-icon-512.png",
		}[src]
		if !ok {
			return nil, fmt.Errorf("icon %d is %q, which urd has no file for", i, src)
		}
		icon["src"] = swapped
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// urdDoorAssets builds the second door from the files the frontend build produced.
//
// Both or neither, and loudly: a door whose shell points at /urd.webmanifest while
// that manifest is not being served is an install prompt that silently falls back
// to the other app's identity.
func urdDoorAssets(web fs.FS) (shell, manifest []byte, err error) {
	raw, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("index.html: %w", err)
	}
	shell, err = applyRebrands(raw, urdShellRebrands())
	if err != nil {
		return nil, nil, fmt.Errorf("index.html: %w", err)
	}

	raw, err = fs.ReadFile(web, "manifest.webmanifest")
	if err != nil {
		return nil, nil, fmt.Errorf("manifest.webmanifest: %w", err)
	}
	manifest, err = urdManifest(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest.webmanifest: %w", err)
	}
	return shell, manifest, nil
}

// door returns the bytes for the second door's two paths, or nil.
//
// Keyed on the requested path rather than on the resolved file, unlike `renamed`
// below: /urd is not a file and never will be, so it has to be answered before the
// fallback to index.html gets to it — which would serve the other door's shell
// under this door's address, with no error anywhere.
func (s *Server) door(name string) []byte {
	switch name {
	case "urd":
		return s.urdShell
	case "urd.webmanifest":
		return s.urdManifest
	}
	return nil
}
