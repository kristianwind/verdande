package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeModel er en OpenAI-formet endpoint, der svarer med det, prøven har brug for.
//
// Den rigtige vej hele vejen igennem: indstillingerne, prompten, parsningen af
// svaret og skrivningen bagefter. Det, der er værd at prøve her, er ikke om en
// model kan svare — det er, hvad der sker med svaret, når den har svaret noget.
func fakeModel(t *testing.T, reply string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": reply}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// useModel peger kontoen på den falske model. Kun en administrator må pege AI'en
// på en adresse i det private net, og bootstrap-kontoen er en.
func (ts *testServer) useModel(t *testing.T, url string) {
	t.Helper()
	resp, _ := ts.do(t, "PUT", "/api/v1/ai/settings", map[string]any{
		"provider": "compatible", "base_url": url, "model": "prøvemodel",
	})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("gem ai-indstillinger: %d", resp.StatusCode)
	}
}

// Forslaget peger tilbage på den opgave, det handler om — også når modellen
// springer en over. Uden nummeret ville "sæt en dato på den her" lande på en
// anden opgave end den, den blev skrevet om.
func TestTidyingTheInboxSuggestsPerTask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.useModel(t, fakeModel(t, `[
		{"line": "2. Ring til Anders i morgen p1", "why": "der står et navn og et telefonnummer"}
	]`))

	first := ts.quickTask(t, "noget som helst")
	second := ts.quickTask(t, "anders 12345678")

	resp, out := ts.do(t, "POST", "/api/v1/ai/inbox/tidy", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ryd op: %d %v", resp.StatusCode, out)
	}
	list, _ := out["suggestions"].([]any)
	if len(list) != 1 {
		t.Fatalf("forslag = %v, want ét", list)
	}
	sug := list[0].(map[string]any)
	if sug["task_id"] != second {
		t.Errorf("forslaget peger på %v, want den anden opgave (%v)", sug["task_id"], second)
	}
	if sug["line"] != "Ring til Anders i morgen p1" {
		t.Errorf("nummeret blev ikke taget af linjen: %v", sug["line"])
	}
	if sug["why"] == "" {
		t.Error("forslaget siger ikke hvorfor")
	}
	_ = first
}

// Et ja skriver forslaget ind i opgaven — gennem den samme parser som alt andet,
// så "i morgen" betyder det samme her som i feltet øverst i fladen.
func TestAcceptingASuggestionWritesItIntoTheTask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.useModel(t, fakeModel(t, `[]`))
	taskID := ts.quickTask(t, "anders")

	resp, out := ts.do(t, "POST", "/api/v1/ai/inbox/tidy/apply", map[string]any{
		"task_id": taskID, "line": "Ring til Anders i morgen p1",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accepter: %d %v", resp.StatusCode, out)
	}
	if out["content"] != "Ring til Anders" {
		t.Errorf("indhold = %v", out["content"])
	}
	if out["priority"] != float64(1) {
		t.Errorf("prioritet = %v, want 1", out["priority"])
	}
	if out["due_date"] == nil || out["due_date"] == "" {
		t.Errorf("datoen blev ikke sat: %v", out["due_date"])
	}
}

// Et projekt, modellen har fundet på, flytter ingenting. Den fik listen over dem,
// der findes, og et navn uden for den er et gæt — et gæt må ikke kunne flytte en
// opgave hen, hvor ejeren ikke leder efter den.
func TestAnInventedProjectDoesNotMoveTheTask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.useModel(t, fakeModel(t, `[]`))
	taskID := ts.quickTask(t, "anders")

	_, before := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	resp, after := ts.do(t, "POST", "/api/v1/ai/inbox/tidy/apply", map[string]any{
		"task_id": taskID, "line": "Ring til Anders #EtProjektDerIkkeFindes",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accepter: %d", resp.StatusCode)
	}
	if after["project_id"] != before["project_id"] {
		t.Errorf("opgaven blev flyttet til %v (var %v)", after["project_id"], before["project_id"])
	}
}

// En note bliver ikke rørt, når der bliver lavet opgaver ud af den. En tekst, et
// program skriver i uden at blive bedt om det, er en tekst, man holder op med at
// stole på.
func TestTasksFromANoteLeaveTheNoteAlone(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.useModel(t, fakeModel(t, `[
		{"line": "Send prisliste til Anders på fredag", "why": "“jeg sender prislisten fredag”"}
	]`))

	body := "# Møde med Anders\njeg sender prislisten fredag"
	noteID := ts.createNote(t, body)

	resp, out := ts.do(t, "POST", "/api/v1/ai/notes/"+noteID+"/actions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("find opgaver: %d %v", resp.StatusCode, out)
	}
	list, _ := out["suggestions"].([]any)
	if len(list) != 1 {
		t.Fatalf("forslag = %v, want ét", list)
	}
	line := list[0].(map[string]any)["line"].(string)

	resp, task := ts.do(t, "POST", "/api/v1/ai/notes/"+noteID+"/actions/apply",
		map[string]any{"line": line})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("accepter: %d %v", resp.StatusCode, task)
	}
	if task["content"] != "Send prisliste til Anders" {
		t.Errorf("opgave = %v", task["content"])
	}
	// Opgaven bærer, hvor den kom fra, som et notelink.
	if task["description"] != "[[Møde med Anders]]" {
		t.Errorf("beskrivelse = %v, want et link til noten", task["description"])
	}

	_, note := ts.do(t, "GET", "/api/v1/notes/"+noteID, nil)
	if note["body"] != body {
		t.Errorf("noten blev rettet:\n%v", note["body"])
	}
}

// Uden en model er funktionen slået fra — ikke i stykker. Svaret skal kunne
// kendes fra en fejl, så fladen kan sige "læg din nøgle ind" frem for "der skete
// noget".
func TestSuggestionsAreOffWithoutAModel(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, path := range []string{"/api/v1/ai/inbox/tidy"} {
		if resp, _ := ts.do(t, "POST", path, nil); resp.StatusCode != http.StatusConflict {
			t.Errorf("%s gav %d, want 409", path, resp.StatusCode)
		}
	}
}

// quickTask lægger en linje i indbakken gennem den samme vej som feltet øverst.
func (ts *testServer) quickTask(t *testing.T, text string) string {
	t.Helper()
	resp, out := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{"text": text})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("quickadd %q: %d %v", text, resp.StatusCode, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("quickadd gav intet id: %v", out)
	}
	return id
}

// Svaret på et spørgsmål bærer, hvad det står på — og kun det, det faktisk brugte.
// En liste over alt, der blev kigget i, er ikke en henvisning.
func TestAskingAnswersFromYourOwnNotes(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.useModel(t, fakeModel(t, `{"answer": "Du lovede Anders prislisten fredag.", "sources": [1]}`))

	ts.createNote(t, "# Møde med Anders\njeg sender prislisten fredag")
	ts.createNote(t, "# Noget helt andet\nanders er ikke nævnt her")

	resp, out := ts.do(t, "POST", "/api/v1/ai/ask", map[string]any{"question": "hvad lovede jeg Anders?"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spørg: %d %v", resp.StatusCode, out)
	}
	if out["answer"] != "Du lovede Anders prislisten fredag." {
		t.Errorf("svar = %v", out["answer"])
	}
	sources, _ := out["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("kilder = %v, want én", sources)
	}
	src := sources[0].(map[string]any)
	if src["kind"] != "note" || src["title"] != "Møde med Anders" {
		t.Errorf("kilde = %v", src)
	}
}

// Et spørgsmål, der ikke rammer noget, får ikke et opfundet svar. Man spørger om
// sine egne noter, fordi man ikke selv kan huske det, og kan derfor ikke se, at
// svaret var gættet.
func TestAskingAboutNothingAnswersNothing(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.useModel(t, fakeModel(t, `{"answer": "det burde du huske", "sources": []}`))

	resp, out := ts.do(t, "POST", "/api/v1/ai/ask",
		map[string]any{"question": "kvadratrodenafenbanan"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spørg: %d", resp.StatusCode)
	}
	if out["answer"] != "" {
		t.Errorf("der blev svaret uden noget at svare ud fra: %v", out["answer"])
	}
}
