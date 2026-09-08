package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/ai"
	"github.com/kristianwind/verdande/internal/quickadd"
	"github.com/kristianwind/verdande/internal/store"
)

// AI'ens forslag, og hvad der sker med dem.
//
// Fælles for det hele: modellen skriver ikke noget. Den foreslår en *linje* i den
// syntaks, man selv skriver i — "Ring til Anders i morgen p1 #Firma" — og
// forslaget bliver til noget, når nogen siger ja til det. Det er ikke
// forsigtighed for forsigtighedens skyld: en model, der retter i folks opgaver
// direkte, skal have ret hver gang for at være til gavn, hvor en, der foreslår,
// skal have ret tit nok til at spare et par tastetryk.
//
// Og linjen er quick-add-syntaks frem for et felt pr. egenskab, fordi parseren
// findes: der er én fortolkning af "i morgen" i dette program, og forslaget går
// gennem den samme som alt andet.

// aiSuggestionJSON er ét forslag på vej til fladen.
type aiSuggestionJSON struct {
	// TaskID er sat, når forslaget handler om en opgave, der allerede findes —
	// altså ved oprydningen i indbakken. Tomt, når forslaget er en ny opgave.
	TaskID string `json:"task_id,omitempty"`
	Line   string `json:"line"`
	Why    string `json:"why,omitempty"`
}

// aiClientFor er de tre linjer, hver eneste AI-funktion ellers ville gentage:
// hent opsætningen, og sig pænt fra, når der ikke er nogen.
func (s *Server) aiClientFor(w http.ResponseWriter, r *http.Request) (*ai.Client, bool) {
	cfg, err := s.aiConfig(r, userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "ai settings", err)
		return nil, false
	}
	if !cfg.Configured() {
		// Ikke en fejl, nogen har lavet: funktionen er slået fra, indtil man
		// lægger sin egen nøgle ind.
		writeError(w, http.StatusConflict, CodeAINotConfigured, "no AI provider is configured")
		return nil, false
	}
	return ai.New(cfg), true
}

// projectNames er de projekter, modellen må nævne.
//
// Givet med frem for udeladt, fordi alternativet er, at den finder på et: et
// `#Nyt projekt`, ingen har, ville lave et projekt ved et uheld, første gang
// nogen sagde ja til forslaget.
func (s *Server) projectNames(r *http.Request) []string {
	projects, err := s.db.ListProjects(r.Context(), userFrom(r.Context()).ID, false)
	if err != nil {
		s.log.Warn("projects for ai", "err", err)
		return nil
	}
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		if p.IsInbox {
			continue
		}
		out = append(out, p.Name)
	}
	return out
}

// handleAITidyInbox foreslår, hvordan det, der ligger løst i indbakken, kunne
// skrives færdig.
//
// Indbakken er der, hvor ting lander, når man ikke havde tid til at skrive dem
// ordentligt — og den er derfor også der, hvor de bliver liggende. At åbne hver
// enkelt og sætte projekt, dato og prioritet er ti klik pr. linje; det her er ét
// spørgsmål og et ja pr. linje.
func (s *Server) handleAITidyInbox(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	client, ok := s.aiClientFor(w, r)
	if !ok {
		return
	}

	inbox, err := s.db.InboxID(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "inbox", err)
		return
	}
	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		ProjectIDs: []string{inbox}, Limit: 50,
	})
	if err != nil {
		s.internal(w, r, "list inbox", err)
		return
	}
	if len(tasks) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"suggestions": []aiSuggestionJSON{}})
		return
	}

	// Nummereret, så svaret kan lægges tilbage på den rigtige opgave. Modellen
	// bliver bedt om at svare i rækkefølge, men en model, der springer en over,
	// må ikke kunne flytte alle de andres forslag én plads — derfor står
	// nummeret i teksten og bliver læst tilbage.
	lines := make([]string, 0, len(tasks))
	for i, t := range tasks {
		line := strconv.Itoa(i+1) + ". " + t.Content
		if t.Description != "" {
			line += " — " + firstWords(t.Description, 30)
		}
		lines = append(lines, line)
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	today := time.Now().In(userLocation(user.Timezone)).Format("2006-01-02")
	suggestions, err := client.TidyInbox(ctx, lines, s.projectNames(r), today, user.Locale)
	if err != nil {
		s.log.Warn("ai tidy inbox", "err", err)
		writeError(w, StatusUpstreamRefused, CodeInternal, err.Error())
		return
	}

	out := make([]aiSuggestionJSON, 0, len(suggestions))
	for i, sug := range suggestions {
		n, line := numberedLine(sug.Line)
		// Uden et nummer falder vi tilbage på rækkefølgen, som er det, der blev
		// bedt om. Med et nummer, der ikke findes, springes forslaget over —
		// et forslag, der peger på en opgave, der ikke er der, er ikke et forslag.
		if n == 0 {
			n = i + 1
		}
		if n < 1 || n > len(tasks) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, aiSuggestionJSON{TaskID: tasks[n-1].ID, Line: line, Why: sug.Why})
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

type applyLineRequest struct {
	TaskID string `json:"task_id"`
	Line   string `json:"line"`
}

// handleAIApplyToTask skriver ét accepteret forslag ind i den opgave, det handler
// om.
//
// Parsningen sker her og ikke i browseren, så "i morgen" betyder det samme, som
// det gør alle andre steder i programmet — og så et forslag, der er blevet rettet
// i hånden, inden det blev sagt ja til, bliver læst på nøjagtig samme måde som et,
// der ikke blev rørt.
func (s *Server) handleAIApplyToTask(w http.ResponseWriter, r *http.Request) {
	var req applyLineRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())
	if strings.TrimSpace(req.Line) == "" {
		writeFieldErrors(w, map[string]string{"line": "required"})
		return
	}

	task, err := s.db.GetTask(r.Context(), req.TaskID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}

	loc := userLocation(user.Timezone)
	parsed := quickadd.Parse(req.Line, time.Now().In(loc), user.Locale)
	if strings.TrimSpace(parsed.Content) == "" {
		writeFieldErrors(w, map[string]string{"line": "there is no task in that"})
		return
	}

	// Kun det, linjen faktisk siger. En tom dato i et forslag betyder "der stod
	// ikke noget om en dato" og ikke "fjern den, der er" — modellen er blevet bedt
	// om at lade være med at gætte datoer, og så må et manglende gæt heller ikke
	// slette noget.
	update := store.TaskUpdate{Content: &parsed.Content}
	if parsed.Priority != 0 {
		update.Priority = &parsed.Priority
	}
	if parsed.DueDate != "" {
		update.SetDue, update.DueDate = true, parsed.DueDate
	}
	if parsed.Recurrence != "" {
		update.RecurrenceRule = &parsed.Recurrence
	}
	if len(parsed.Labels) > 0 {
		update.Labels, update.SetLabels = parsed.Labels, true
	}
	if err := s.db.UpdateTask(r.Context(), task.ID, user.ID, update); err != nil {
		s.storeError(w, r, "update task", err)
		return
	}

	// Et projekt, modellen fandt på, flytter ingenting. Den fik listen over dem,
	// der findes, og et navn uden for den er et gæt — og et gæt må ikke kunne
	// lave et projekt eller flytte en opgave hen, hvor ejeren ikke leder.
	if parsed.Project != "" {
		if id, err := s.db.ProjectByName(r.Context(), user.ID, parsed.Project); err == nil && id != task.ProjectID {
			if _, err := store.RequireProjectRole(r.Context(), s.db, id, user.ID, store.RoleEditor); err == nil {
				if _, err := s.db.MoveTask(r.Context(), task.ID, id, "", "", ""); err != nil {
					s.internal(w, r, "move task", err)
					return
				}
			}
		}
	}

	updated, err := s.db.GetTask(r.Context(), task.ID, user.ID)
	if err != nil {
		s.storeError(w, r, "get task", err)
		return
	}
	s.publish(updated.ProjectID, "task.updated", toTaskJSON(*updated))
	writeJSON(w, http.StatusOK, toTaskJSON(*updated))
}

// handleAINoteActions trækker det, nogen har lovet, ud af en note.
//
// Et referat er fuldt af sætninger, der ligner opgaver, og af tre, der er det.
// Modellen bliver bedt om kun at tage dem, nogen har forpligtet sig på, og om at
// citere de ord, den tog dem fra — så et forkert punkt kan gennemskues frem for
// bare at være forkert.
func (s *Server) handleAINoteActions(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	client, ok := s.aiClientFor(w, r)
	if !ok {
		return
	}

	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleViewer) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	if strings.TrimSpace(n.Body) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"suggestions": []aiSuggestionJSON{}})
		return
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	today := time.Now().In(userLocation(user.Timezone)).Format("2006-01-02")
	suggestions, err := client.ActionsInNote(ctx, n.Body, s.projectNames(r), today, user.Locale)
	if err != nil {
		s.log.Warn("ai note actions", "err", err)
		writeError(w, StatusUpstreamRefused, CodeInternal, err.Error())
		return
	}

	out := make([]aiSuggestionJSON, 0, len(suggestions))
	for _, sug := range suggestions {
		out = append(out, aiSuggestionJSON{Line: sug.Line, Why: sug.Why})
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// handleAICreateFromNote laver én accepteret opgave fra en note.
//
// Noten bliver ikke rørt. Det er fristende at skrive linket ind i den, så de to
// peger på hinanden, men en tekst, et program skriver i uden at blive bedt om
// det, er en tekst, man holder op med at stole på. I stedet bærer opgaven, hvor
// den kom fra — og noten kan findes på sin titel.
func (s *Server) handleAICreateFromNote(w http.ResponseWriter, r *http.Request) {
	var req applyLineRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.mayTouchNote(r, n, store.RoleViewer) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	loc := userLocation(user.Timezone)
	parsed := quickadd.Parse(req.Line, time.Now().In(loc), user.Locale)
	if strings.TrimSpace(parsed.Content) == "" {
		writeFieldErrors(w, map[string]string{"line": "there is no task in that"})
		return
	}

	// Notens eget projekt som udgangspunkt: står referatet i et projekt, hører
	// det, der blev aftalt i det, som regel til samme sted. Et `#projekt` i
	// linjen vinder, fordi det er valgt frem for arvet.
	projectID := n.ProjectID
	if parsed.Project != "" {
		if id, err := s.db.ProjectByName(r.Context(), user.ID, parsed.Project); err == nil {
			projectID = id
		}
	}
	if projectID == "" {
		if projectID, err = s.db.InboxID(r.Context(), user.ID); err != nil {
			s.internal(w, r, "inbox", err)
			return
		}
	}
	if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, store.RoleEditor); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	task := &store.Task{
		ProjectID: projectID, Content: parsed.Content, Priority: parsed.Priority,
		DueDate: parsed.DueDate, RecurrenceRule: parsed.Recurrence,
		Description: "[[" + n.Title + "]]", CreatedBy: user.ID,
	}
	if err := s.db.CreateTask(r.Context(), task, parsed.Labels); err != nil {
		s.internal(w, r, "create task from note", err)
		return
	}
	s.publish(projectID, "task.created", toTaskJSON(*task))
	writeJSON(w, http.StatusCreated, toTaskJSON(*task))
}

// numberedLine skiller "3. Ring til Anders" ad i nummeret og linjen.
//
// Nummeret er, hvordan et forslag finder tilbage til den opgave, det handler om.
// Rækkefølgen alene ville også kunne bruges, og gør det som reserve — men en
// model, der springer en opgave over, ville så flytte alle de følgende forslag
// én plads, og så bliver "sæt dato på den her" sat på en anden.
func numberedLine(s string) (int, string) {
	s = strings.TrimSpace(s)
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot > 3 {
		return 0, s
	}
	n := 0
	for _, c := range s[:dot] {
		if c < '0' || c > '9' {
			return 0, s
		}
		n = n*10 + int(c-'0')
	}
	return n, strings.TrimSpace(s[dot+1:])
}

// firstWords klipper en beskrivelse ned til noget, der kan stå i en prompt uden at
// fylde den med en hel note.
func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) <= n {
		return strings.Join(fields, " ")
	}
	return strings.Join(fields[:n], " ") + " …"
}
