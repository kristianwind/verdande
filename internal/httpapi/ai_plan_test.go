package httpapi

import (
	"net/http"
	"testing"
	"time"
)

// Planen er tal fra basen, ikke fra modellen. En plan, der siger fire opgaver,
// hvor der er tre, er værre end ingen plan — så tallene tælles her, og modellen
// får dem udleveret som kendsgerninger.
func TestTheDailyPlanCountsForItself(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.quickTask(t, "ring til anders i dag")
	ts.quickTask(t, "køb mælk i dag")

	resp, out := ts.do(t, "POST", "/api/v1/ai/plan/now", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send planen: %d %v", resp.StatusCode, out)
	}
	if out["sent"] != true {
		t.Fatalf("planen blev ikke sendt: %v", out)
	}
	// Uden en model står tallene alene, og det er stadig det, man havde brug for
	// at vide klokken syv.
	if plan, _ := out["plan"].(string); plan != "2 forfalder i dag" {
		t.Errorf("plan = %q", plan)
	}

	// Og den lander i klokken som en besked.
	list, unread := notifications(t, ts)
	if len(list) != 1 || unread != 1 {
		t.Fatalf("beskeder = %d (%v ulæste), want 1", len(list), unread)
	}
	if n := firstOf(t, list); n["kind"] != "daily.plan" {
		t.Errorf("besked = %v", n)
	}
}

// Ingen plan er ikke en besked. En morgen uden noget at gøre er en god morgen, og
// en besked, der siger "der er ingenting", lærer folk at lade være med at åbne
// beskeder.
func TestAnEmptyDaySendsNoPlan(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, out := ts.do(t, "POST", "/api/v1/ai/plan/now", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send planen: %d", resp.StatusCode)
	}
	if out["sent"] != false {
		t.Errorf("der blev sendt en plan om ingenting: %v", out)
	}
	if list, _ := notifications(t, ts); len(list) != 0 {
		t.Errorf("beskeder = %d, want 0", len(list))
	}
}

// Timen er flyttet, så dagens plan er ikke sendt på den nye tid endnu. Uden det
// ville en, der flytter planen fra 07 til 16, først få den i morgen — hvilket
// ligner, at indstillingen ikke virkede.
func TestMovingThePlanHourClearsTodaysMark(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.quickTask(t, "noget i dag")

	if resp, _ := ts.do(t, "PUT", "/api/v1/ai/plan", map[string]any{
		"enabled": true, "hour": 7,
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("slå planen til")
	}
	if _, out := ts.do(t, "POST", "/api/v1/ai/plan/now", nil); out["sent"] != true {
		t.Fatalf("planen blev ikke sendt: %v", out)
	}

	resp, out := ts.do(t, "PUT", "/api/v1/ai/plan", map[string]any{"hour": 16})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flyt timen: %d", resp.StatusCode)
	}
	if out["hour"] != float64(16) {
		t.Errorf("time = %v", out["hour"])
	}
	if out["last_sent"] != nil && out["last_sent"] != "" {
		t.Errorf("mærket for i dag blev stående: %v", out["last_sent"])
	}
}

// En time, der ikke findes på et ur, er en fejl i feltet og ikke en 500.
func TestAnImpossiblePlanHourIsRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	if resp, _ := ts.do(t, "PUT", "/api/v1/ai/plan", map[string]any{"hour": 25}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("time 25 gav %d, want 422", resp.StatusCode)
	}
}

// Planen gemmes for dagen, så kortet på I dag kan vise nøjagtig den tekst,
// beskeden bar — og ikke et nyt gæt, hver gang nogen kigger.
func TestTodaysPlanIsKeptForTheDay(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.quickTask(t, "noget i dag")

	// Ingen plan endnu: kortet viser en knap frem for gårsdagens tekst.
	if _, before := ts.do(t, "GET", "/api/v1/ai/plan", nil); before["plan"] != nil && before["plan"] != "" {
		t.Fatalf("der lå en plan, før nogen havde lavet en: %v", before["plan"])
	}

	resp, made := ts.do(t, "POST", "/api/v1/ai/plan/now", map[string]any{"silent": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lav planen: %d", resp.StatusCode)
	}
	if made["sent"] != false {
		t.Errorf("en stille plan sendte en besked: %v", made["sent"])
	}
	if list, _ := notifications(t, ts); len(list) != 0 {
		t.Errorf("kortets egen plan gav %d beskeder, want 0", len(list))
	}

	_, after := ts.do(t, "GET", "/api/v1/ai/plan", nil)
	if after["plan"] != made["plan"] {
		t.Errorf("den gemte plan = %v, den lavede = %v", after["plan"], made["plan"])
	}
	if after["plan_at"] == nil {
		t.Error("planen bærer ikke, hvornår den blev lavet")
	}
}

// En plan fra i går er ikke dagens. Et kort, der viser gårsdagens plan under
// overskriften "i dag", er værre end et tomt kort.
func TestYesterdaysPlanIsNotTodays(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.quickTask(t, "noget i dag")

	if resp, _ := ts.do(t, "POST", "/api/v1/ai/plan/now", map[string]any{"silent": true}); resp.StatusCode != http.StatusOK {
		t.Fatal("lav planen")
	}

	// Sat en dag tilbage, som om den var lavet i går.
	user := userID(t, ts, "kristian@example.dk")
	stored, err := ts.db.UserSettings(t.Context(), user, "dailyplan")
	if err != nil {
		t.Fatal(err)
	}
	stored["plan_date"] = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if err := ts.db.SetUserSettings(t.Context(), user, "dailyplan", stored); err != nil {
		t.Fatal(err)
	}

	_, out := ts.do(t, "GET", "/api/v1/ai/plan", nil)
	if plan, _ := out["plan"].(string); plan != "" {
		t.Errorf("gårsdagens plan blev vist som dagens: %q", plan)
	}
}
