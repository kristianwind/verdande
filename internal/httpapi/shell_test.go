package httpapi

import (
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/store"
)

// serverWithWeb builds the real router around a frontend, which newTestServerWith
// cannot do — it passes nil for the web filesystem, because every other test here
// is about the API. The renaming only happens when there is something to rename.
func serverWithWeb(t *testing.T, edition string, web fs.FS) *Server {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{
		BaseURL:    "http://localhost",
		DataDir:    t.TempDir(),
		SessionTTL: 24 * time.Hour,
		Edition:    edition,
	}
	return New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)), web)
}

// The shell as the frontend build produces it, cut down to the parts that matter
// here: the two anchors that get renamed, and the inline theme script that must
// not be touched. TestAppHTMLStillCarriesTheAnchors below is what keeps this
// honest against the real file.
const builtShell = `<!doctype html>
<html lang="da" data-theme="dark">
	<head>
		<meta name="description" content="verdande — opgaver og projekter, delt." />
		<link rel="manifest" href="/manifest.webmanifest" />
		<title>verdande</title>
		<script>
			try {
				const stored = localStorage.getItem('verdande:theme');
				const look = localStorage.getItem('verdande:look');
				if (look && look !== 'verdande') document.documentElement.dataset.look = look;
			} catch (e) {}
		</script>
	</head>
	<body></body>
</html>
`

const builtManifest = `{
  "name": "verdande",
  "short_name": "verdande",
  "description": "Opgaver og projekter, delt.",
  "start_url": "/"
}
`

func builtWeb() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte(builtShell)},
		"manifest.webmanifest":  {Data: []byte(builtManifest)},
		"_app/immutable/app.js": {Data: []byte("// app")},
	}
}

// TestRenamingTheShellLeavesTheStorageKeysAlone is the one this file exists for.
//
// The product name and the localStorage prefix are the same word in app.html, and
// they are not the same thing: the prefix is read back by the app itself, so
// renaming it here and nowhere else sets the theme from one key and reads it from
// another. The symptom is the white flash on load that the inline script exists to
// prevent — which is to say, no error, nothing in a log, and nothing a test that
// merely checked the title would see.
func TestRenamingTheShellLeavesTheStorageKeysAlone(t *testing.T) {
	shell, manifest, err := rebrandedAssets(builtWeb(), "urd", "urd — noter, delt.", "Noter, delt.")
	if err != nil {
		t.Fatalf("rebrandedAssets: %v", err)
	}

	got := string(shell)
	for _, want := range []string{
		"<title>urd</title>",
		`content="urd — noter, delt."`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the shell does not contain %q", want)
		}
	}
	// Named values rather than "no occurrences of verdande": the three keys are
	// what has to survive, so the assertion says the three keys.
	for _, want := range []string{
		`localStorage.getItem('verdande:theme')`,
		`localStorage.getItem('verdande:look')`,
		`look !== 'verdande'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renaming the shell changed a storage key: %q is gone", want)
		}
	}
	if strings.Contains(got, "urd:theme") || strings.Contains(got, "urd:look") {
		t.Error("renaming the shell rewrote a localStorage prefix; the app reads the verdande: ones")
	}

	gotManifest := string(manifest)
	for _, want := range []string{`"name": "urd"`, `"short_name": "urd"`, `"description": "Noter, delt."`} {
		if !strings.Contains(gotManifest, want) {
			t.Errorf("the manifest does not contain %q", want)
		}
	}
}

// TestRenamingTheShellKeepsTheCSPHashValid pins the relationship between the two
// files that is easy to break and impossible to notice: the policy names a hash of
// the inline script, and the renamed shell is what gets served. If a replacement
// ever reached inside the <script>, the hash would change, the browser would refuse
// to run it, and the page would be blank — in production only, because the policy
// is not enforced for a page opened from a file.
func TestRenamingTheShellKeepsTheCSPHashValid(t *testing.T) {
	shell, _, err := rebrandedAssets(builtWeb(), "urd", "urd — noter, delt.", "Noter, delt.")
	if err != nil {
		t.Fatalf("rebrandedAssets: %v", err)
	}

	before := scriptHashesIn([]byte(builtShell))
	after := scriptHashesIn(shell)
	if len(before) != 1 {
		t.Fatalf("the fixture should have exactly one inline script, found %d", len(before))
	}
	if len(after) != len(before) || after[0] != before[0] {
		t.Errorf("renaming changed the inline script: policy had %v, served shell hashes to %v", before, after)
	}
}

// TestRenamingRefusesAMissingAnchor is the direction that matters more than the
// happy path. A replacement that silently matched nothing leaves an Urd instance
// calling itself verdande, which is cosmetic enough that nobody reports it — so the
// rewrite has to say so rather than return what it managed.
func TestRenamingRefusesAMissingAnchor(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"no title": {
			"index.html":           {Data: []byte(strings.Replace(builtShell, "<title>verdande</title>", "<title>something else</title>", 1))},
			"manifest.webmanifest": {Data: []byte(builtManifest)},
		},
		"no description": {
			"index.html":           {Data: []byte(strings.Replace(builtShell, `content="verdande — opgaver og projekter, delt."`, `content="other"`, 1))},
			"manifest.webmanifest": {Data: []byte(builtManifest)},
		},
		"no manifest name": {
			"index.html":           {Data: []byte(builtShell)},
			"manifest.webmanifest": {Data: []byte(strings.Replace(builtManifest, `"name": "verdande"`, `"name": "other"`, 1))},
		},
		"no index.html at all": {
			"manifest.webmanifest": {Data: []byte(builtManifest)},
		},
		"no manifest at all": {
			"index.html": {Data: []byte(builtShell)},
		},
	}
	for name, web := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := rebrandedAssets(web, "urd", "d", "m"); err == nil {
				t.Error("renaming reported success over a shell it could not rename")
			}
		})
	}
}

// TestAppHTMLStillCarriesTheAnchors is what stops the fixture above from becoming a
// frozen copy of a file that has moved on.
//
// The anchors are exact strings in web/src/app.html, and that file is edited for
// unrelated reasons — a meta tag, a new theme attribute. An edit that reformats the
// title line or rewrites the description breaks the rename and nothing in Go would
// know, because the Go build graph has never heard of app.html.
//
// Note the caching trap that comes with reading a non-Go file from a test: `go
// test` will serve a cached green over a changed app.html. CI passes -count=1 in
// both workflows; a local run needs it typed.
func TestAppHTMLStillCarriesTheAnchors(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/app.html")
	if err != nil {
		t.Skipf("app.html is not reachable from here: %v", err)
	}
	src := string(raw)

	for _, pair := range shellRebrands("urd", "d") {
		if n := strings.Count(src, pair.old); n != 1 {
			t.Errorf("web/src/app.html contains %d occurrences of the anchor %q, want exactly 1 — "+
				"the notes edition renames the shell by replacing it", n, pair.old)
		}
	}
	// And the keys the rename must not touch, asserted here too: app.html is where
	// somebody would rename them, and if they are renamed there they must be
	// renamed in stores.svelte.js in the same commit.
	if !strings.Contains(src, `localStorage.getItem('verdande:theme')`) {
		t.Error("web/src/app.html no longer reads verdande:theme; check that the app reads the same key")
	}
	t.Logf("app.html: %d bytes, %d anchors found", len(raw), len(shellRebrands("urd", "d")))
}

// TestTheFullEditionServesTheBuiltFilesUnchanged is the control. Everything above
// is about the renamed path; this says the other edition does not go through it.
func TestTheFullEditionServesTheBuiltFilesUnchanged(t *testing.T) {
	for _, edition := range []string{"full", "notes"} {
		t.Run(edition, func(t *testing.T) {
			srv := serverWithWeb(t, edition, builtWeb())
			if edition == "full" {
				if srv.renamed("index.html") != nil || srv.renamed("manifest.webmanifest") != nil {
					t.Error("the full edition renamed the shell")
				}
				return
			}
			if srv.renamed("index.html") == nil || srv.renamed("manifest.webmanifest") == nil {
				t.Fatal("the notes edition did not rename the shell")
			}
			if !strings.Contains(string(srv.renamed("index.html")), "<title>urd</title>") {
				t.Error("the notes edition's shell does not say urd")
			}
			if srv.renamed("_app/immutable/app.js") != nil {
				t.Error("a build asset was served from memory; only the shell and the manifest are renamed")
			}
		})
	}
}

// webWithIcons is builtWeb plus both editions' icons, as a real build has them.
func webWithIcons() fstest.MapFS {
	web := builtWeb()
	for _, name := range []string{
		"icon.svg", "icon-192.png", "icon-512.png", "apple-touch-icon.png",
		"urd-icon.svg", "urd-icon-192.png", "urd-icon-512.png", "urd-apple-touch-icon.png",
	} {
		web[name] = &fstest.MapFile{Data: []byte("bytes of " + name)}
	}
	return web
}

// TestEachEditionServesItsOwnMark.
//
// Four paths, named one by one rather than counted: the favicon in app.html, the
// manifest's two, and the one Safari looks for by name. The last is the reason
// this is an alias rather than a rewritten manifest — Safari does not read the
// manifest at all.
func TestEachEditionServesItsOwnMark(t *testing.T) {
	for _, tc := range []struct {
		edition string
		want    map[string]string
	}{
		{"notes", map[string]string{
			"icon.svg":             "urd-icon.svg",
			"icon-192.png":         "urd-icon-192.png",
			"icon-512.png":         "urd-icon-512.png",
			"apple-touch-icon.png": "urd-apple-touch-icon.png",
		}},
		// The control, and it is the half that stops this being a swap rather than
		// a choice: the full edition must resolve nothing.
		{"full", map[string]string{}},
	} {
		t.Run(tc.edition, func(t *testing.T) {
			srv := serverWithWeb(t, tc.edition, webWithIcons())
			if len(srv.assets) != len(tc.want) {
				t.Errorf("%s edition aliases %d paths, want %d: %v", tc.edition, len(srv.assets), len(tc.want), srv.assets)
			}
			for path, alias := range tc.want {
				if srv.assets[path] != alias {
					t.Errorf("%s: %s resolves to %q, want %q", tc.edition, path, srv.assets[path], alias)
				}
			}
			// And nothing else: an alias on a build asset would serve one file's
			// bytes under another's hashed, immutable name.
			for path := range srv.assets {
				if _, ok := tc.want[path]; !ok {
					t.Errorf("%s edition aliases %s, which is not an icon", tc.edition, path)
				}
			}
			t.Logf("%s: %d aliased paths", tc.edition, len(srv.assets))
		})
	}
}

// TestAMissingMarkIsReportedAndFallsBack.
//
// The fallback is deliberate and it is the only place in this file that prefers
// carrying on to failing: an icon's absence is a broken-image placeholder on the
// one screen somebody reads before deciding whether to press update, so the wrong
// mark beats no mark. What must not happen is it being quiet.
func TestAMissingMarkIsReportedAndFallsBack(t *testing.T) {
	web := webWithIcons()
	delete(web, "urd-icon-512.png")

	var reported []string
	got := resolveNotesAssets(web, func(name, alias string) {
		reported = append(reported, name+" → "+alias)
	})

	if len(reported) != 1 || reported[0] != "icon-512.png → urd-icon-512.png" {
		t.Errorf("the missing icon was reported as %v, want exactly one naming both paths", reported)
	}
	if _, ok := got["icon-512.png"]; ok {
		t.Error("a path with no file behind it was aliased anyway")
	}
	if got["icon.svg"] != "urd-icon.svg" {
		t.Error("one missing icon took the other three with it")
	}
}
