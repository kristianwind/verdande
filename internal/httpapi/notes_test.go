package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// A note in a project is the project's; a note in no project is its author's.
// Both answers come from the project's own roles rather than from a rule notes
// invented for themselves, which is the point of filing them there.
func TestANoteIsNotReadableByJustAnybody(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/notes", map[string]any{
		"body": "Noget privat om #Firma",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("no id came back")
	}

	// The tags were read out of the text on the way in, without being asked for.
	links, _ := body["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("the note recorded %v", body["links"])
	}

	// Somebody else gets the same answer as for a note that does not exist, so an
	// id cannot be probed for existence.
	other := ts.newUser(t, "anden@example.dk", "Anden")
	resp, _ = other.do(t, "GET", "/api/v1/notes/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a stranger got %d for somebody else's note, want 404", resp.StatusCode)
	}
}

// Searching must not become a way to read what you cannot open. The filter is
// applied after the index, and this is what says so.
func TestSearchDoesNotLeakNotesYouCannotRead(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	if resp, body := ts.do(t, "POST", "/api/v1/notes", map[string]any{
		"body": "Hemmeligt kodeord til hjemmesiden",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}

	_, mine := ts.do(t, "GET", "/api/v1/notes?q=hemmeligt", nil)
	if got := len(mine["notes"].([]any)); got != 1 {
		t.Fatalf("the author found %d of their own notes", got)
	}

	other := ts.newUser(t, "anden@example.dk", "Anden")
	_, theirs := other.do(t, "GET", "/api/v1/notes?q=hemmeligt", nil)
	if got := len(theirs["notes"].([]any)); got != 0 {
		t.Errorf("a stranger's search returned %d notes", got)
	}
}

// The backwards question, which is what makes notes worth linking at all.
func TestANoteIsFoundFromTheTaskItMentions(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "ring til Anders",
	})
	taskID, _ := task["id"].(string)
	if taskID == "" {
		t.Fatalf("no task id: %v", task)
	}

	if resp, _ := ts.do(t, "POST", "/api/v1/notes", map[string]any{
		"body": "Aftalt at han ringer først, se /opgave/" + taskID,
	}); resp.StatusCode != http.StatusCreated {
		t.Fatal("the note was not written")
	}

	_, found := ts.do(t, "GET", "/api/v1/notes/linking/task/"+taskID, nil)
	notes, _ := found["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("%d notes point at the task, want 1", len(notes))
	}
}

// Editing the text moves the links with it. A link that outlives its mention
// claims a connection that is not there.
func TestChangingANoteChangesWhatItPointsAt(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, note := ts.do(t, "POST", "/api/v1/notes", map[string]any{"body": "Om #Firma"})
	id, _ := note["id"].(string)

	_, before := ts.do(t, "GET", "/api/v1/notes/linking/project/Firma", nil)
	if len(before["notes"].([]any)) != 1 {
		t.Fatal("the first save recorded nothing")
	}

	if resp, _ := ts.do(t, "PATCH", "/api/v1/notes/"+id, map[string]any{
		"body": "Om #Regnskab i stedet",
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("the note was not changed")
	}

	_, after := ts.do(t, "GET", "/api/v1/notes/linking/project/Firma", nil)
	if got := len(after["notes"].([]any)); got != 0 {
		t.Errorf("%d notes still point at Firma", got)
	}
}

// The export is the promise the whole design was arranged around: the note on
// disk is already the file you would export, so nothing is converted on the way
// out and nothing can be lost in the conversion.
func TestNotesExportAsMarkdownFiles(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	bodies := []string{
		"# Møde med Anders\n\nHan vil gerne have **kaffe** hver uge.",
		"Slash/i/titlen\n\nog noget tekst",
		"# Møde med Anders\n\nen anden note med samme titel",
	}
	for _, body := range bodies {
		if resp, _ := ts.do(t, "POST", "/api/v1/notes", map[string]any{"body": body}); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
	}

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/export/notes.zip", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type is %q", ct)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("the archive does not open: %v", err)
	}
	if len(zr.File) != 3 {
		t.Fatalf("the archive holds %d files, want 3", len(zr.File))
	}

	byName := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		text, _ := io.ReadAll(r)
		r.Close()
		byName[f.Name] = string(text)
	}

	// Exactly what was stored, byte for byte. A converted export is an export that
	// can be wrong.
	if got := byName["Møde med Anders.md"]; got != bodies[0] {
		t.Errorf("the note came out as %q", got)
	}
	// A slash in a title would make a directory, and on Windows it is refused
	// outright.
	if _, ok := byName["Slash-i-titlen.md"]; !ok {
		t.Errorf("the awkward title became %v", keysOf(byName))
	}
	// Two notes with the same title are two notes; one entry twice is one lost.
	if _, ok := byName["Møde med Anders (2).md"]; !ok {
		t.Errorf("the second note of the same name is missing: %v", keysOf(byName))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Out and back in again. If an export cannot be imported, it is not an export —
// it is a file somebody hopes will be readable later.
func TestNotesSurviveTheRoundTripThroughAZip(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	files := map[string]string{
		"Møde med Anders.md": "# Møde med Anders\n\nHan vil have **kaffe** hver uge. #Firma",
		"noget andet.md":     "Løse tanker\n\nog en linje til",
		// A zip made on a Mac carries these beside every file. Importing them would
		// double the whole library, with each copy full of binary rubbish.
		"__MACOSX/._Møde med Anders.md": "\x00\x05\x16\x07binært",
		"billede.png":                   "ikke en note",
		"tom.md":                        "   \n  ",
	}
	for name, body := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	part, _ := mw.CreateFormFile("file", "noter.zip")
	part.Write(archive.Bytes())
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/notes/import", &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: status %d", resp.StatusCode)
	}

	var out struct{ Created, Skipped int }
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Created != 2 {
		t.Fatalf("imported %d notes, want the 2 real ones", out.Created)
	}

	// The title came from the first line, not from the filename — which is the same
	// rule as everywhere else, and the reason renaming a file cannot rename a note.
	_, listed := ts.do(t, "GET", "/api/v1/notes", nil)
	titles := map[string]bool{}
	for _, raw := range listed["notes"].([]any) {
		titles[raw.(map[string]any)["title"].(string)] = true
	}
	if !titles["Møde med Anders"] || !titles["Løse tanker"] {
		t.Errorf("the titles came out as %v", titles)
	}

	// And the #tag was read on the way in, so an imported note lands on its project
	// exactly as a typed one does.
	_, linked := ts.do(t, "GET", "/api/v1/notes/linking/project/Firma", nil)
	if len(linked["notes"].([]any)) != 1 {
		t.Errorf("the imported note did not link to its project")
	}
}

// A note from Apple Notes is full of photographs. One that arrives as words alone
// is not that note — it is a summary of it.
func TestAnImportedNoteKeepsItsPictures(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	write := func(name, body string) {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	write("noter/Kvittering.md", "# Kvittering\n\nHer er den:\n\n![](vedhaeftninger/bon.png)\n\nog resten af teksten")
	write("noter/vedhaeftninger/bon.png", "\x89PNG\r\n\x1a\nlad som om")
	// Another note in the same archive, which must not inherit the first one's
	// picture: an archive is one folder for many notes.
	write("noter/Uden billede.md", "Uden billede\n\nbare tekst")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	part, _ := mw.CreateFormFile("file", "noter.zip")
	part.Write(archive.Bytes())
	mw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/notes/import", &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out struct{ Created, Skipped, Files int }
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Created != 2 || out.Files != 1 {
		t.Fatalf("imported %d notes and %d files, want 2 and 1", out.Created, out.Files)
	}

	_, listed := ts.do(t, "GET", "/api/v1/notes?q=kvittering", nil)
	notes := listed["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("found %d notes", len(notes))
	}
	body := notes[0].(map[string]any)["body"].(string)

	// The link points at the file where it now lives, not at a path inside an
	// archive nobody kept.
	if strings.Contains(body, "vedhaeftninger/bon.png") {
		t.Errorf("the link still points into the archive: %q", body)
	}
	if !strings.Contains(body, "/api/v1/attachments/") {
		t.Errorf("the picture was not attached: %q", body)
	}
	// And it is still where it was written, in the middle of the note.
	if !strings.HasPrefix(body, "# Kvittering") || !strings.Contains(body, "og resten af teksten") {
		t.Errorf("the note was rearranged: %q", body)
	}

	// The picture is really there and really readable.
	id := body[strings.Index(body, "/api/v1/attachments/")+len("/api/v1/attachments/"):]
	id = strings.SplitN(id, ")", 2)[0]
	got, _ := ts.do(t, "GET", "/api/v1/attachments/"+id, nil)
	if got.StatusCode != http.StatusOK {
		t.Errorf("the attachment answers %d", got.StatusCode)
	}

	// The other note did not inherit it.
	_, other := ts.do(t, "GET", "/api/v1/notes?q=Uden", nil)
	for _, raw := range other["notes"].([]any) {
		if strings.Contains(raw.(map[string]any)["body"].(string), "attachments/") {
			t.Error("a note that mentioned no picture was given one")
		}
	}
}
