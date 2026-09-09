package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/ai"
	"github.com/kristianwind/verdande/internal/store"
)

// Dagens plan: én besked om morgenen om det, der venter i dag.
//
// Klokken siger, hvad andre har gjort. Det her siger, hvad der ligger foran en —
// og det er en anden slags besked: den kommer, fordi det er blevet morgen, ikke
// fordi nogen har rørt noget. Uden den skal man selv åbne listen for at opdage,
// at der lå tre ting og forfaldt i går.
//
// Sendt som en besked frem for en mail, fordi røret allerede findes: klokken,
// push til telefonen og mærket på ikonet er ét kald, og en mail mere i indbakken
// er præcis det, dette program findes for at slippe for.

const planScope = "dailyplan"

// planSettings er, hvad én person har bedt om.
//
// Slået fra som udgangspunkt. En besked, man ikke har bedt om, klokken syv om
// morgenen, er ikke en hjælp — og modsat beaconet er der ingen, der har brug for,
// at det her er tændt for at kunne gøre sit arbejde.
type planSettings struct {
	Enabled bool `json:"enabled"`
	// Hour er lokal tid hos personen selv. Et tal og ikke et klokkeslæt: planen
	// sendes på timen, og et minuttal ville love en præcision, en time-tikkende
	// baggrundsjob ikke har.
	Hour int `json:"hour"`
	// LastSent er datoen, den sidst blev sendt, som "2006-01-02" i personens egen
	// tidszone. En dato og ikke et tidsstempel, fordi spørgsmålet er "har de fået
	// dagens plan i dag" — og det er et spørgsmål om dagen dér, hvor de er.
	LastSent string `json:"last_sent,omitempty"`
	// Plan er teksten, som den blev sendt, og PlanDate den dag den gælder.
	//
	// Gemt frem for regnet forfra, hver gang nogen kigger. To grunde, og den anden
	// er den vigtige: en model pr. sidevisning er dyr, og en plan, der siger noget
	// andet end den besked, man fik i morges, er værre end begge dele hver for
	// sig. Det er den samme tekst — ellers er det ikke *dagens plan*, men et nyt
	// gæt hver gang man kigger.
	//
	// PlanAt er klokkeslættet, den blev lavet, så kortet kan sige det: en plan fra
	// klokken syv er en anden slags oplysning klokken fire end klokken otte.
	Plan     string `json:"plan,omitempty"`
	PlanDate string `json:"plan_date,omitempty"`
	PlanAt   int64  `json:"plan_at,omitempty"`
}

func (s *Server) planSettings(ctx context.Context, userID string) (planSettings, error) {
	out := planSettings{Hour: 7}
	stored, err := s.db.UserSettings(ctx, userID, planScope)
	if err != nil {
		return out, err
	}
	if v, ok := stored["enabled"].(bool); ok {
		out.Enabled = v
	}
	if v, ok := stored["hour"].(float64); ok && v >= 0 && v <= 23 {
		out.Hour = int(v)
	}
	if v, ok := stored["last_sent"].(string); ok {
		out.LastSent = v
	}
	if v, ok := stored["plan"].(string); ok {
		out.Plan = v
	}
	if v, ok := stored["plan_date"].(string); ok {
		out.PlanDate = v
	}
	if v, ok := stored["plan_at"].(float64); ok {
		out.PlanAt = int64(v)
	}
	return out, nil
}

func (s *Server) savePlanSettings(ctx context.Context, userID string, p planSettings) error {
	return s.db.SetUserSettings(ctx, userID, planScope, map[string]any{
		"enabled": p.Enabled, "hour": p.Hour, "last_sent": p.LastSent,
		"plan": p.Plan, "plan_date": p.PlanDate, "plan_at": p.PlanAt,
	})
}

// handleGetPlanSettings er både indstillingen og dagens plan.
//
// Ét endepunkt, fordi det er én ting: planen *er* ressourcen, og indstillingen er,
// hvornår den bliver lavet. To ville betyde to kald fra siden, der viser den, og
// to steder at huske at rydde en plan fra i går.
//
// En plan fra i går sendes ikke med. Den er ikke forkert, den er bare ikke dagens
// — og et kort, der viser gårsdagens plan under overskriften "i dag", er værre end
// et tomt kort.
func (s *Server) handleGetPlanSettings(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	p, err := s.planSettings(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "plan settings", err)
		return
	}
	if p.PlanDate != time.Now().In(userLocation(user.Timezone)).Format("2006-01-02") {
		p.Plan, p.PlanDate, p.PlanAt = "", "", 0
	}
	writeJSON(w, http.StatusOK, p)
}

type planRequest struct {
	Enabled *bool `json:"enabled"`
	Hour    *int  `json:"hour"`
}

func (s *Server) handleSetPlanSettings(w http.ResponseWriter, r *http.Request) {
	var req planRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())
	p, err := s.planSettings(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "plan settings", err)
		return
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if req.Hour != nil {
		if *req.Hour < 0 || *req.Hour > 23 {
			writeFieldErrors(w, map[string]string{"hour": "must be between 0 and 23"})
			return
		}
		p.Hour = *req.Hour
		// Timen er flyttet, så dagens plan er ikke sendt på den nye tid endnu.
		// Uden det her ville en, der flytter planen fra 07 til 16, først få den i
		// morgen — hvilket ligner, at indstillingen ikke virkede.
		p.LastSent = ""
	}
	if err := s.savePlanSettings(r.Context(), user.ID, p); err != nil {
		s.internal(w, r, "save plan settings", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type planNowRequest struct {
	// Silent er kortet på I dag, der beder om en plan, mens man kigger på den.
	//
	// En besked om noget, man står og ser på, er en besked, der lærer folk at
	// ignorere klokken. Indstillingssidens knap sender derimod rigtigt — det er
	// hele dens formål: at vise, at push virker på det her apparat.
	Silent bool `json:"silent"`
}

// handlePlanNow laver dagens plan med det samme, til den der beder om det.
//
// Findes, fordi en indstilling, man skal vente til i morgen for at se virkningen
// af, er en indstilling, ingen tør slå til — og fordi kortet på I dag skal kunne
// lave en plan for den, der har beskeden slået fra.
func (s *Server) handlePlanNow(w http.ResponseWriter, r *http.Request) {
	var req planNowRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			return
		}
	}
	user := userFrom(r.Context())

	plan, err := s.buildPlan(r.Context(), user)
	if err != nil {
		s.internal(w, r, "build plan", err)
		return
	}
	if plan == "" {
		writeJSON(w, http.StatusOK, map[string]any{"plan": "", "sent": false})
		return
	}
	if err := s.rememberPlan(r.Context(), user, plan); err != nil {
		s.internal(w, r, "save plan", err)
		return
	}

	sent := false
	if !req.Silent {
		s.notify(r, &store.Notification{
			UserID: user.ID, Kind: "daily.plan",
			Title: planTitle(user.Locale), Body: plan,
		})
		sent = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan": plan, "sent": sent,
		"plan_at": time.Now().Unix(),
	})
}

// rememberPlan gemmer dagens plan, så den kan ses igen uden at blive lavet igen.
func (s *Server) rememberPlan(ctx context.Context, user *store.User, plan string) error {
	p, err := s.planSettings(ctx, user.ID)
	if err != nil {
		return err
	}
	now := time.Now()
	p.Plan = plan
	p.PlanDate = now.In(userLocation(user.Timezone)).Format("2006-01-02")
	p.PlanAt = now.Unix()
	return s.savePlanSettings(ctx, user.ID, p)
}

// DailyPlans er timeslaget: hvem er der morgen hos, og hvem har ikke fået planen
// endnu i dag.
//
// Kaldt hver time frem for lagt på et klokkeslæt — samme form som backup og
// beacon, og af samme grund: en instans, der sov klokken syv, ville ellers
// springe dagen over. Og fordi klokken syv er syv forskellige steder: personens
// egen tidszone afgør, hvornår det er morgen hos dem.
func (s *Server) DailyPlans(ctx context.Context) error {
	users, err := s.db.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		user, err := s.db.UserByID(ctx, u.ID)
		if err != nil {
			continue
		}
		p, err := s.planSettings(ctx, user.ID)
		if err != nil || !p.Enabled {
			continue
		}

		now := time.Now().In(userLocation(user.Timezone))
		today := now.Format("2006-01-02")
		if p.LastSent == today || now.Hour() < p.Hour {
			continue
		}
		// Kun samme dag. En instans, der har været slukket i tre dage, skal ikke
		// sende tre planer, når den kommer op — de to af dem er om dage, der er
		// gået, og en plan for i forgårs er ikke en plan.
		if now.Hour() > p.Hour+3 {
			p.LastSent = today
			_ = s.savePlanSettings(ctx, user.ID, p)
			continue
		}

		plan, err := s.buildPlan(ctx, user)
		if err != nil {
			s.log.Warn("build daily plan", "err", err, "user", user.ID)
			continue
		}
		// Ingen plan er ikke en besked. En morgen uden noget at gøre er en god
		// morgen, og en besked, der siger "der er ingenting", lærer folk at lade
		// være med at åbne beskeder.
		if plan != "" {
			s.notifyDirect(ctx, &store.Notification{
				UserID: user.ID, Kind: "daily.plan",
				Title: planTitle(user.Locale), Body: plan,
			})
			// Gemt, og gemt her frem for i rememberPlan: rækken er allerede
			// læst ind i `p`, og to gemninger oven i hinanden ville lade den
			// ene overskrive den andens `last_sent`.
			p.Plan, p.PlanDate, p.PlanAt = plan, today, time.Now().Unix()
		}
		p.LastSent = today
		if err := s.savePlanSettings(ctx, user.ID, p); err != nil {
			s.log.Warn("save plan settings", "err", err, "user", user.ID)
		}
	}
	return nil
}

// buildPlan skriver dagens plan.
//
// Tallene kommer fra basen og sætningen fra modellen — i den rækkefølge. En model,
// der selv skulle tælle, ville af og til tælle forkert, og en plan, der siger fire
// opgaver, hvor der er tre, er værre end ingen plan. Er der ingen model sat op,
// står tallene alene, og det er stadig det, man havde brug for at vide.
func (s *Server) buildPlan(ctx context.Context, user *store.User) (string, error) {
	loc := userLocation(user.Timezone)
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	// I går, ikke i dag: grænserne i filteret er inklusive, så "før i dag" skrevet
	// som DueBefore: today ville tælle dagens egne opgaver med som sprunget over —
	// og en plan, der klokken syv om morgenen siger, at dagens opgaver allerede er
	// overskredet, er en plan, man holder op med at læse.
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	overdue, err := s.db.ListTasks(ctx, user.ID, store.TaskFilter{DueBefore: yesterday, Limit: 50})
	if err != nil {
		return "", err
	}
	due, err := s.db.ListTasks(ctx, user.ID, store.TaskFilter{
		DueFrom: today, DueBefore: today, Limit: 50,
	})
	if err != nil {
		return "", err
	}
	if len(overdue) == 0 && len(due) == 0 {
		return "", nil
	}

	plain := plainPlan(len(due), len(overdue), user.Locale)

	cfg, err := s.aiConfigFor(ctx, user)
	if err != nil || !cfg.Configured() {
		return plain, nil
	}

	lines := make([]string, 0, len(due)+len(overdue))
	for _, t := range overdue {
		lines = append(lines, "- "+t.Content+" ("+overdueWord(user.Locale)+" "+t.DueDate+")")
	}
	for _, t := range due {
		lines = append(lines, "- "+t.Content)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	written, err := ai.New(cfg).DailyPlan(ctx, lines, len(due), len(overdue), user.Locale)
	if err != nil {
		// Modellen er ikke det, planen står og falder med. Tallene er rigtige uden
		// den, og en morgen uden besked, fordi en API-nøgle er udløbet, er en
		// dårligere handel end en besked uden en velskrevet sætning.
		s.log.Info("daily plan without the model", "err", err, "user", user.ID)
		return plain, nil
	}
	return strings.TrimSpace(written), nil
}

// aiConfigFor er aiConfig uden en request bag sig — baggrundsjobbet har ingen.
//
// AllowPrivate er falsk her, og det er med vilje: retten til at nå det private
// net blev givet til en administrator, der sad og skrev adressen ind, og et job,
// der kører klokken syv om morgenen, er ikke nogen, der sidder og skriver. En
// model på maskinen selv skal derfor bruges gennem fladen, ikke af planen.
func (s *Server) aiConfigFor(ctx context.Context, user *store.User) (ai.Config, error) {
	values, err := s.db.UserSettings(ctx, user.ID, "ai")
	if err != nil {
		return ai.Config{}, err
	}
	str := func(key string) string {
		v, _ := values[key].(string)
		return v
	}
	return ai.Config{
		Provider: ai.Provider(str("provider")),
		APIKey:   str("api_key"),
		BaseURL:  str("base_url"),
		Model:    str("model"),
	}, nil
}

// notifyDirect er notify uden en request. Baggrundsjobbet har ingen bruger i
// konteksten og ingen actor — planen er ikke noget, nogen har gjort ved en.
func (s *Server) notifyDirect(ctx context.Context, n *store.Notification) {
	if err := s.db.CreateNotification(ctx, n); err != nil {
		s.log.Warn("create notification", "err", err)
		return
	}
	s.hub.PublishToUser(n.UserID, "notification", notificationJSON{
		ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body,
		CreatedAt: n.CreatedAt.Format(time.RFC3339),
	})
	go s.pushToUser(n.UserID, n.Title, n.Body, "")
}

func planTitle(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		return "Today"
	}
	return "I dag"
}

func overdueWord(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		return "was due"
	}
	return "forfaldt"
}

// plainPlan er planen uden en model: to tal, og ikke et ord mere.
//
// Det er ikke en nødløsning, det er bunden. Det, man har brug for at vide klokken
// syv, er om der er noget, der er sprunget over — og det tal er rigtigt, uanset
// om nogen har lagt en API-nøgle ind.
func plainPlan(due, overdue int, locale string) string {
	english := strings.HasPrefix(strings.ToLower(locale), "en")
	var parts []string
	if due > 0 {
		if english {
			parts = append(parts, strconv.Itoa(due)+" due today")
		} else {
			parts = append(parts, strconv.Itoa(due)+" forfalder i dag")
		}
	}
	if overdue > 0 {
		if english {
			parts = append(parts, strconv.Itoa(overdue)+" overdue")
		} else {
			parts = append(parts, strconv.Itoa(overdue)+" er sprunget over")
		}
	}
	return strings.Join(parts, ", ")
}
