package httpapi

import (
	"net/http"
	"testing"
)

// Beaconet er slået til som udgangspunkt, og det kan kun forsvares, hvis det
// bliver sagt uopfordret. Beskeden venter, indtil nogen har taget stilling — og
// den viser de rigtige værdier, så løftet kan efterprøves frem for tros på.
func TestTheBeaconSaysWhatItSendsBeforeAnybodyAsks(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, notice := ts.do(t, "GET", "/api/v1/beacon/notice", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hent besked: %d", resp.StatusCode)
	}
	if notice["pending"] != true {
		t.Fatalf("beskeden er ikke i vente på en frisk installation: %v", notice)
	}
	if notice["enabled"] != true {
		t.Errorf("beaconet er ikke slået til som udgangspunkt: %v", notice)
	}
	// De tre værdier, beskeden viser frem, er de rigtige — ikke et eksempel.
	settings := beaconSettingsOf(t, ts)
	if notice["instance_id"] != settings["instance_id"] {
		t.Errorf("id i beskeden = %v, i indstillingerne = %v",
			notice["instance_id"], settings["instance_id"])
	}
	if notice["collector_url"] != settings["collector_url"] {
		t.Errorf("adressen i beskeden = %v, i indstillingerne = %v",
			notice["collector_url"], settings["collector_url"])
	}
	if notice["version"] == "" || notice["version"] == nil {
		t.Error("beskeden siger ikke, hvilken version der bliver sendt")
	}
}

// "Behold den" er et svar, og det svar skal huskes: beskeden må ikke komme igen
// ved næste login.
func TestKeepingTheBeaconAnswersTheNoticeForGood(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, out := ts.do(t, "POST", "/api/v1/beacon/notice", map[string]any{"keep": true})
	if resp.StatusCode != http.StatusOK || out["enabled"] != true {
		t.Fatalf("behold: %d %v", resp.StatusCode, out)
	}
	if _, notice := ts.do(t, "GET", "/api/v1/beacon/notice", nil); notice["pending"] != false {
		t.Errorf("beskeden kommer igen efter et svar: %v", notice)
	}
	if s := beaconSettingsOf(t, ts); s["enabled"] != true {
		t.Errorf("beaconet blev slået fra af et ja: %v", s["enabled"])
	}
}

// Og "slå den fra" slår den fra — ét kald, både svaret og virkningen.
func TestTurningTheBeaconOffFromTheNoticeStopsIt(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, out := ts.do(t, "POST", "/api/v1/beacon/notice", map[string]any{"keep": false})
	if resp.StatusCode != http.StatusOK || out["enabled"] != false {
		t.Fatalf("slå fra: %d %v", resp.StatusCode, out)
	}
	s := beaconSettingsOf(t, ts)
	if s["enabled"] != false {
		t.Errorf("beaconet kører stadig efter et nej: %v", s["enabled"])
	}
	if _, notice := ts.do(t, "GET", "/api/v1/beacon/notice", nil); notice["pending"] != false {
		t.Errorf("beskeden kommer igen efter et nej: %v", notice)
	}
}

// En tom krop må ikke kunne betyde "slå det fra". Et fravalg, ingen har truffet,
// er lige så meget et brudt løfte som et tilvalg, ingen har truffet.
func TestAnEmptyAnswerToTheNoticeIsRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "POST", "/api/v1/beacon/notice", map[string]any{})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("tom krop gav %d, want 422", resp.StatusCode)
	}
	if s := beaconSettingsOf(t, ts); s["enabled"] != true {
		t.Errorf("en tom krop slog beaconet fra: %v", s["enabled"])
	}
	if _, notice := ts.do(t, "GET", "/api/v1/beacon/notice", nil); notice["pending"] != true {
		t.Errorf("en tom krop talte som et svar: %v", notice)
	}
}

// Et tal, der falder, kan ikke offentliggøres som en kendsgerning. Under gulvet
// svares der ingenting frem for et lille tal, ingen kan tolke.
func TestThePublishedCountHasAFloorUnderIt(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	if resp, _ := ts.do(t, "PUT", "/api/v1/beacon/settings", map[string]any{
		"is_collector": true, "publish_count": true,
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("bliv collector")
	}

	// Instansen tæller sig selv, så der er én. Gulvet er højere.
	resp, out := ts.do(t, "GET", "/api/v1/beacon/count", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tælling: %d", resp.StatusCode)
	}
	if out["installs"] != nil {
		t.Errorf("et tal under gulvet blev offentliggjort: %v", out["installs"])
	}

	// Sættes gulvet ned, står tallet der.
	if resp, _ := ts.do(t, "PUT", "/api/v1/beacon/settings",
		map[string]any{"publish_min": 0}); resp.StatusCode != http.StatusOK {
		t.Fatal("sænk gulvet")
	}
	if _, out := ts.do(t, "GET", "/api/v1/beacon/count", nil); out["installs"] == nil {
		t.Errorf("tallet blev holdt tilbage uden et gulv: %v", out)
	}
}

func beaconSettingsOf(t *testing.T, ts *testServer) map[string]any {
	t.Helper()
	resp, out := ts.do(t, "GET", "/api/v1/beacon/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hent beacon-indstillinger: %d", resp.StatusCode)
	}
	return out
}
