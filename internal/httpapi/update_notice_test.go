package httpapi

import (
	"testing"

	"github.com/kristianwind/verdande/internal/update"
)

// En ny version siger til af sig selv. Uden det stod svaret nederst på en
// indstillingsside, som man kun åbner, hvis man i forvejen havde mistanke om, at
// der var noget at se efter.
func TestANewVersionTellsTheAdministrator(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	status := update.Status{
		Current: "v0.44.0", Latest: "v0.45.0", Available: true,
		Notes: "Nyt:\n\n- noget godt", URL: "https://example.dk/v0.45.0",
	}
	if err := ts.api.notifyOfUpdate(t.Context(), status); err != nil {
		t.Fatalf("besked om ny version: %v", err)
	}

	list, unread := notifications(t, ts)
	if len(list) != 1 || unread != 1 {
		t.Fatalf("beskeder = %d (%v ulæste), want 1", len(list), unread)
	}
	n := firstOf(t, list)
	if n["kind"] != "update.available" {
		t.Errorf("besked = %v", n)
	}
	// Titlen bærer versionen, fordi den også bliver til en push-besked, og push
	// har ingen ordbog at slå sætningen op i.
	if title, _ := n["title"].(string); title != "verdande v0.45.0 er klar" {
		t.Errorf("titel = %q", title)
	}

	// Én besked pr. version, ikke én pr. time. En påmindelse hver time om det
	// samme er den slags besked, folk slår fra — og så er den næste, der betyder
	// noget, også slået fra.
	if err := ts.api.notifyOfUpdate(t.Context(), status); err != nil {
		t.Fatal(err)
	}
	if list, _ := notifications(t, ts); len(list) != 1 {
		t.Errorf("den samme version gav %d beskeder, want 1", len(list))
	}

	// Og den næste version er en ny ting at få at vide.
	status.Latest = "v0.46.0"
	if err := ts.api.notifyOfUpdate(t.Context(), status); err != nil {
		t.Fatal(err)
	}
	if list, _ := notifications(t, ts); len(list) != 2 {
		t.Errorf("en ny version gav %d beskeder, want 2", len(list))
	}
}

// Kun administratorer. En besked om, at serveren er bagud, er en besked om en
// andens arbejde, hvis man ikke selv kan gøre noget ved den.
func TestOnlyAdministratorsHearAboutANewVersion(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "sofie@example.dk", "Sofie")

	err := ts.api.notifyOfUpdate(t.Context(), update.Status{
		Current: "v0.44.0", Latest: "v0.45.0", Available: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if list, _ := notifications(t, other); len(list) != 0 {
		t.Errorf("en almindelig bruger fik %d beskeder om en opdatering", len(list))
	}
	if list, _ := notifications(t, ts); len(list) != 1 {
		t.Errorf("administratoren fik %d beskeder, want 1", len(list))
	}
}

// Et tjek, operatøren har slået fra, må ikke blive til en besked ad bagvejen.
func TestNoUpdateNoticeWhenTheCheckIsOff(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, status := range []update.Status{
		{Current: "v0.44.0", Disabled: true},
		{Current: "v0.44.0", Available: false},
		{Current: "v0.44.0", Available: true, Latest: ""},
	} {
		if err := ts.api.notifyOfUpdate(t.Context(), status); err != nil {
			t.Fatalf("%v: %v", status, err)
		}
	}
	if list, _ := notifications(t, ts); len(list) != 0 {
		t.Errorf("der blev sagt til om en opdatering, der ikke var: %d beskeder", len(list))
	}
}

// Beskeden fører hen, hvor knappen sidder — ikke ud af programmet. Prøvet på det,
// fladen læser: en besked uden note, opgave eller projekt skal stadig kunne
// klikkes.
func TestAnUpdateNoticeCarriesNoTarget(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	err := ts.api.notifyOfUpdate(t.Context(), update.Status{
		Current: "v0.44.0", Latest: "v0.45.0", Available: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	n := firstOf(t, mustNotifications(t, ts))
	for _, key := range []string{"note_id", "task_id", "project_id"} {
		if v, ok := n[key]; ok && v != "" {
			t.Errorf("%s = %v, want ingenting", key, v)
		}
	}
}

func mustNotifications(t *testing.T, ts *testServer) []any {
	t.Helper()
	list, _ := notifications(t, ts)
	if len(list) == 0 {
		t.Fatal("ingen beskeder")
	}
	return list
}
