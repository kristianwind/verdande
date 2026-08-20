package beacon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Hvad der bliver sendt, tegn for tegn.
//
// Prøven er skrevet imod *hele* kroppen og ikke imod enkelte felter, fordi det er
// den, der er løftet: siden siger "præcis det her og ikke mere", og et felt lagt
// til senere skal vælte noget. En prøve, der kun så efter, at instance_id var med,
// ville lade en ny nøgle glide igennem uden at nogen opdagede det.
func TestSendsExactlyTwoFields(t *testing.T) {
	var got map[string]any
	var contentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("kroppen er ikke JSON: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	err := Send(context.Background(), srv.Client(), srv.URL, Payload{
		InstanceID: "01a01b16-4e54-7000-9eaa-2f8eb0629c3a",
		Version:    "v0.26.3",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if contentType != "application/json" {
		t.Errorf("Content-Type = %q", contentType)
	}
	if len(got) != 2 {
		t.Fatalf("der blev sendt %d felter, ikke to: %v", len(got), got)
	}
	if got["instance_id"] != "01a01b16-4e54-7000-9eaa-2f8eb0629c3a" {
		t.Errorf("instance_id = %v", got["instance_id"])
	}
	if got["version"] != "v0.26.3" {
		t.Errorf("version = %v", got["version"])
	}
}

// En collector, der er nede, er ikke denne installations problem.
func TestSendReportsAFailureRatherThanRetrying(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := Send(context.Background(), srv.Client(), srv.URL, Payload{
		InstanceID: "01a01b16-4e54-7000-9eaa-2f8eb0629c3a", Version: "v1.0.0",
	})
	if err == nil {
		t.Fatal("en 500 skal give en fejl")
	}
	if calls != 1 {
		t.Errorf("der blev prøvet %d gange; en telemetri-ping må ikke hamre på en server, der er nede", calls)
	}
}

// Et id, der ikke er id-formet, sendes slet ikke — så en fejl her ikke bliver til
// noget, en fremmed collector skal filtrere fra.
func TestSendRefusesAMalformedID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("der blev sendt noget, som ikke skulle sendes")
	}))
	defer srv.Close()

	if err := Send(context.Background(), srv.Client(), srv.URL, Payload{
		InstanceID: "; DROP TABLE beacon_installs", Version: "v1.0.0",
	}); err == nil {
		t.Fatal("et misformet id skal afvises før det forlader maskinen")
	}
}

func TestShapes(t *testing.T) {
	ok := []string{"01a01b16-4e54-7000-9eaa-2f8eb0629c3a", "89df44d7-3742-4611-9a43-db0b07733c6d"}
	for _, s := range ok {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false", s)
		}
	}
	bad := []string{"", "kort", "../../etc/passwd", "01a01b16-4e54-7000-9eaa-2f8eb0629c3a'; --",
		"<script>alert(1)</script>"}
	for _, s := range bad {
		if ValidID(s) {
			t.Errorf("ValidID(%q) = true", s)
		}
	}

	for _, s := range []string{"v0.26.3", "0.26.3", "v1.2.3-rc.1", "dev"} {
		if !ValidVersion(s) {
			t.Errorf("ValidVersion(%q) = false", s)
		}
	}
	for _, s := range []string{"", "v<script>", "v" + string(make([]byte, 200))} {
		if ValidVersion(s) {
			t.Errorf("ValidVersion(%q) = true", s)
		}
	}
}
