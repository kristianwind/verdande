package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// doRaw sender en krop, der ikke er JSON — en genvej på en telefon sender ren
// tekst, og en webhook-formular sender felter. Det er halvdelen af det, der
// prøves her.
func (ts *testServer) doRaw(t *testing.T, method, path, contentType, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	var decoded map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp, decoded
}

// hookURL henter personens egen krog-adresse, som fladen viser den.
func (ts *testServer) hookURL(t *testing.T) string {
	t.Helper()
	resp, out := ts.do(t, "GET", "/api/v1/hook-url", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hent krog-URL: %d", resp.StatusCode)
	}
	url, _ := out["url"].(string)
	if !strings.Contains(url, "/inbound/hook/") {
		t.Fatalf("url = %q", url)
	}
	return url
}

// hookPath er stien, testserveren kan kaldes på — URL'en er absolut, fordi den
// skal kunne kopieres ind i en genvej på en telefon.
func hookPath(url string) string {
	return url[strings.Index(url, "/inbound/hook/"):]
}

// En linje ren tekst, skubbet ind udefra, bliver til en opgave i indbakken — og
// betyder det samme, som den ville betyde i feltet øverst i fladen.
func TestAPushFromAnotherAppLandsInTheInbox(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	path := hookPath(ts.hookURL(t))

	anon := newSignedOut(t, ts)
	resp, out := anon.doRaw(t, "POST", path, "text/plain",
		"Ring til Anders p1\nhan ringede i går")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("skub: %d, %v", resp.StatusCode, out)
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks", nil)
	tasks, _ := list["tasks"].([]any)
	var found map[string]any
	for _, raw := range tasks {
		task := raw.(map[string]any)
		if task["content"] == "Ring til Anders" {
			found = task
		}
	}
	if found == nil {
		t.Fatalf("opgaven kom ikke i indbakken: %v", tasks)
	}
	// Parseren er den samme som fladens: p1 er en prioritet og ikke en del af
	// teksten, og resten af kroppen står under opgaven.
	if found["priority"] != float64(1) {
		t.Errorf("prioritet = %v, want 1", found["priority"])
	}
	if found["description"] != "han ringede i går" {
		t.Errorf("beskrivelse = %v", found["description"])
	}
}

// JSON fra en tjeneste, med de feltnavne folk faktisk sender.
func TestTheHookReadsJSON(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	path := hookPath(ts.hookURL(t))
	anon := newSignedOut(t, ts)

	for _, body := range []string{
		`{"text": "Køb mælk"}`,
		`{"title": "Køb mælk"}`,
		`{"content": "Køb mælk"}`,
	} {
		resp, out := anon.doRaw(t, "POST", path, "application/json", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s: %d, %v", body, resp.StatusCode, out)
		}
		if out["content"] != "Køb mælk" {
			t.Errorf("%s gav %v", body, out["content"])
		}
	}

	// En krop, der siger JSON og ikke er det, læses ikke som tekst bagefter: en
	// opgave, der hedder `{"txt": …}`, er en dårligere fejlmeddelelse end et 422.
	resp, _ := anon.doRaw(t, "POST", path, "application/json", `{"txt": "skrevet forkert"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("forkert JSON gav %d, want 422", resp.StatusCode)
	}
}

// Et token, der ikke findes, er det samme svar som et, der aldrig har eksisteret.
// Alt andet er en maskine til at gætte tokens med.
func TestAnUnknownHookTokenIsNotFound(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	anon := newSignedOut(t, ts)

	resp, _ := anon.doRaw(t, "POST", "/inbound/hook/deteringentingogsaaslet", "text/plain", "hej")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ukendt token gav %d, want 404", resp.StatusCode)
	}
}

// Den gamle URL holder op med at virke, når man skifter den. Det er den eneste vej
// tilbage: adressen står i en genvej på en telefon eller i et script.
func TestRotatingTheHookRetiresTheOldURL(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	old := hookPath(ts.hookURL(t))

	resp, out := ts.do(t, "POST", "/api/v1/hook-url/rotate", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("skift: %d", resp.StatusCode)
	}
	fresh := hookPath(out["url"].(string))
	if fresh == old {
		t.Fatal("den nye URL er den gamle")
	}

	anon := newSignedOut(t, ts)
	if resp, _ := anon.doRaw(t, "POST", old, "text/plain", "gammel"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("den gamle URL virker stadig: %d", resp.StatusCode)
	}
	if resp, _ := anon.doRaw(t, "POST", fresh, "text/plain", "ny"); resp.StatusCode != http.StatusCreated {
		t.Errorf("den nye URL virker ikke: %d", resp.StatusCode)
	}
}

// Ingen tekst er ikke en opgave. En tom opgave i indbakken er værre end en fejl,
// fordi den ser ud som om integrationen virker.
func TestAnEmptyPushIsRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	path := hookPath(ts.hookURL(t))
	anon := newSignedOut(t, ts)

	resp, _ := anon.doRaw(t, "POST", path, "text/plain", "   \n  ")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("tom tekst gav %d, want 422", resp.StatusCode)
	}
}

// Et token kan én ting. Det ligger i et script på en anden maskine, og hvad der
// ellers kan nås med det, er hvad der er sluppet ud sammen med det.
func TestTheHookTokenIsNotAnAPIToken(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	url := ts.hookURL(t)
	token := url[strings.LastIndex(url, "/")+1:]

	anon := newSignedOut(t, ts)
	req, err := http.NewRequest("GET", anon.URL+"/api/v1/tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := anon.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("krog-tokenet gav adgang til API'et")
	}
}
