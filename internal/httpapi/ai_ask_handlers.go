package httpapi

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/store"
)

// Spørg om sine egne noter og opgaver i almindeligt sprog.
//
// Søgningen finder det, der indeholder ordene. Det her svarer på spørgsmålet —
// "hvad lovede jeg Anders i august?" — og det er ikke det samme: svaret står
// spredt over tre noter og en opgave, og ingen af dem indeholder ordet "lovede".
//
// To halvdele, og rækkefølgen er hele sikkerheden i det. Basen finder kandidaterne
// med den søgning, der allerede findes, og som allerede kun kan se det, personen
// selv må se. Modellen får kun dem — den slår ikke selv noget op og kan ikke bede
// om mere. Et svar, der ikke står i det udleverede, kan derfor ikke handle om
// nogen andens noter.
//
// Og svaret bærer, hvad det er bygget på. En model, der svarer "det gjorde du den
// 14.", er kun brugbar, hvis man kan slå op i den note, den læste det i.

type askRequest struct {
	Question string `json:"question"`
}

type askSourceJSON struct {
	Kind  string `json:"kind"` // "note" eller "task"
	ID    string `json:"id"`
	Title string `json:"title"`
}

// handleAIAsk svarer på et spørgsmål ud fra personens egne noter og opgaver.
func (s *Server) handleAIAsk(w http.ResponseWriter, r *http.Request) {
	var req askRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	question := strings.TrimSpace(req.Question)
	if question == "" {
		writeFieldErrors(w, map[string]string{"question": "required"})
		return
	}
	user := userFrom(r.Context())
	client, ok := s.aiClientFor(w, r)
	if !ok {
		return
	}

	// Ord for ord, ikke hele spørgsmålet på én gang.
	//
	// Søgningen kræver, at *alle* ord optræder, og det er den rigtige regel for et
	// søgefelt: skriver man to ord, mener man begge. Men et spørgsmål er ikke en
	// søgning — "hvad lovede jeg Anders i august" indeholder ét ord, der står i
	// noten, og fem, der ikke gør, så et krav om dem alle finder ingenting hver
	// eneste gang.
	//
	// Så: et groft net her, og modellen læser fangsten. Alternativet — at lade
	// modellen skrive søgningen — lægger et led mere ind, der kan tage fejl,
	// mellem spørgsmålet og de noter, der faktisk findes.
	notes, tasks, err := s.candidatesFor(r, user.ID, question)
	if err != nil {
		s.internal(w, r, "search for the question", err)
		return
	}
	if len(notes) == 0 && len(tasks) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"answer": "", "sources": []askSourceJSON{},
		})
		return
	}

	// Nummereret, fordi svaret skal kunne pege tilbage. En henvisning til "noten
	// om leveringen" er ikke noget, fladen kan lave et link ud af; et nummer er.
	sources := make([]askSourceJSON, 0, len(notes)+len(tasks))
	passages := make([]string, 0, len(notes)+len(tasks))
	for _, n := range notes {
		sources = append(sources, askSourceJSON{Kind: "note", ID: n.ID, Title: n.Title})
		passages = append(passages, "["+itoaSource(len(sources))+"] Note: "+n.Title+"\n"+
			firstWords(n.Body, 300))
	}
	for _, t := range tasks {
		sources = append(sources, askSourceJSON{Kind: "task", ID: t.ID, Title: t.Content})
		line := "[" + itoaSource(len(sources)) + "] Task: " + t.Content
		if t.DueDate != "" {
			line += " (" + t.DueDate + ")"
		}
		if t.Description != "" {
			line += "\n" + firstWords(t.Description, 60)
		}
		passages = append(passages, line)
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	answer, used, err := client.Ask(ctx, question, passages, user.Locale)
	if err != nil {
		s.log.Warn("ai ask", "err", err)
		writeError(w, StatusUpstreamRefused, CodeInternal, err.Error())
		return
	}

	// Kun de kilder, svaret faktisk brugte. Alle tolv ville gøre henvisningen
	// værdiløs: en liste over alt, der blev kigget i, er ikke det samme som det,
	// svaret står på.
	cited := make([]askSourceJSON, 0, len(used))
	for _, n := range used {
		if n >= 1 && n <= len(sources) {
			cited = append(cited, sources[n-1])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"answer": answer, "sources": cited})
}

func itoaSource(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// candidatesFor henter det, spørgsmålet kunne handle om.
//
// Ét opslag pr. ord, lagt sammen og med det, der rammes af flere ord, øverst: et
// spørgsmål har ét eller to bærende ord og en håndfuld, der bare er dansk, og den
// note, der indeholder begge de bærende, er den, det handler om.
//
// Korte ord springes over. "i", "på" og "the" står i alt, og en note, der blev
// hentet, fordi den indeholder "i", fortrænger en, der faktisk svarer.
func (s *Server) candidatesFor(r *http.Request, userID, question string) ([]store.Note, []store.Task, error) {
	terms := store.SearchTerms(question)

	type scored struct {
		note store.Note
		task store.Task
		hits int
	}
	noteHits := map[string]*scored{}
	taskHits := map[string]*scored{}

	for _, term := range terms {
		if len([]rune(term)) < 3 {
			continue
		}
		found, err := s.db.SearchNotes(r.Context(), term, 10)
		if err != nil {
			return nil, nil, err
		}
		for _, n := range found {
			if at, ok := noteHits[n.ID]; ok {
				at.hits++
				continue
			}
			noteHits[n.ID] = &scored{note: n, hits: 1}
		}

		tasks, err := s.db.ListTasks(r.Context(), userID, store.TaskFilter{
			Search: term, Limit: 10, IncludeCompleted: true,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, t := range tasks {
			if at, ok := taskHits[t.ID]; ok {
				at.hits++
				continue
			}
			taskHits[t.ID] = &scored{task: t, hits: 1}
		}
	}

	notes := make([]store.Note, 0, len(noteHits))
	for _, at := range noteHits {
		notes = append(notes, at.note)
	}
	sort.SliceStable(notes, func(i, j int) bool {
		return noteHits[notes[i].ID].hits > noteHits[notes[j].ID].hits
	})

	tasks := make([]store.Task, 0, len(taskHits))
	for _, at := range taskHits {
		tasks = append(tasks, at.task)
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		return taskHits[tasks[i].ID].hits > taskHits[tasks[j].ID].hits
	})

	// Loftet er, hvad der kan stå i én prompt uden at koste mere, end svaret er
	// værd — og hvad et menneske ville have gidet læse igennem selv.
	return capped(notes, 12), capped(tasks, 20), nil
}

func capped[T any](in []T, n int) []T {
	if len(in) > n {
		return in[:n]
	}
	return in
}
