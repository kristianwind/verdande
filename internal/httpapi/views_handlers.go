package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

// The date views are computed against the *user's* timezone, not the server's.
// "Today" is a question about where the person is standing, and a container running
// in UTC would otherwise call it tomorrow for a Dane from 22:00 onwards — quietly
// emptying the Today view every evening.

type todayResponse struct {
	Date    string     `json:"date"`
	Overdue []taskJSON `json:"overdue"`
	Today   []taskJSON `json:"today"`
}

func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	today := time.Now().In(userLocation(user.Timezone)).Format("2006-01-02")

	// Everything due on or before today, in one query, split in Go. Two queries
	// would be two round trips to answer one screen.
	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		DueBefore: today,
		Limit:     500,
	})
	if err != nil {
		s.internal(w, "today", err)
		return
	}

	resp := todayResponse{Date: today, Overdue: []taskJSON{}, Today: []taskJSON{}}
	for _, t := range tasks {
		if t.DueDate < today {
			resp.Overdue = append(resp.Overdue, toTaskJSON(t))
		} else {
			resp.Today = append(resp.Today, toTaskJSON(t))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type upcomingDay struct {
	Date  string     `json:"date"`
	Tasks []taskJSON `json:"tasks"`
}

// handleUpcoming returns the next seven days, one entry per day.
//
// Empty days are included on purpose: the view is a calendar strip, and a Thursday
// missing from the response because nothing is due would collapse the layout and
// make an empty day indistinguishable from one that was not returned.
func (s *Server) handleUpcoming(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	loc := userLocation(user.Timezone)
	start := time.Now().In(loc)

	days := parseLimit(r.URL.Query().Get("days"), 7, 31)
	from := start.Format("2006-01-02")
	to := start.AddDate(0, 0, days-1).Format("2006-01-02")

	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		DueFrom:   from,
		DueBefore: to,
		Limit:     1000,
	})
	if err != nil {
		s.internal(w, "upcoming", err)
		return
	}

	byDate := map[string][]taskJSON{}
	for _, t := range tasks {
		byDate[t.DueDate] = append(byDate[t.DueDate], toTaskJSON(t))
	}

	out := make([]upcomingDay, 0, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		day := upcomingDay{Date: date, Tasks: byDate[date]}
		if day.Tasks == nil {
			day.Tasks = []taskJSON{}
		}
		out = append(out, day)
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
}

// handleSearch is what Cmd+K calls. It searches across every project the user can
// see, which is the point — the thing you are looking for is usually somewhere you
// were not looking.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"tasks": []taskJSON{}, "projects": []projectJSON{}})
		return
	}

	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		Search: query,
		// Completed tasks are included: "what was that thing I did last week" is
		// a search, and excluding them makes the box feel broken.
		IncludeCompleted: true,
		Limit:            parseLimit(r.URL.Query().Get("limit"), 40, 100),
	})
	if err != nil {
		s.internal(w, "search tasks", err)
		return
	}

	// Projects are matched in Go: there are tens of them, not thousands, and a
	// second FTS index for that would be more machinery than the problem needs.
	projects, err := s.db.ListProjects(r.Context(), user.ID, false)
	if err != nil {
		s.internal(w, "search projects", err)
		return
	}
	matched := []projectJSON{}
	folded := store.FoldDanish(lower(query))
	for _, p := range projects {
		name := lower(p.Name)
		if contains(name, lower(query)) || contains(store.FoldDanish(name), folded) {
			matched = append(matched, toProjectJSON(p))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":    toTaskList(tasks),
		"projects": matched,
	})
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	entries, err := s.db.ListActivity(r.Context(), chi.URLParam(r, "projectID"),
		parseLimit(r.URL.Query().Get("limit"), 50, 200))
	if err != nil {
		s.internal(w, "list activity", err)
		return
	}

	type activityJSON struct {
		ID        string         `json:"id"`
		TaskID    string         `json:"task_id,omitempty"`
		UserID    string         `json:"user_id"`
		UserName  string         `json:"user_name"`
		Event     string         `json:"event"`
		Payload   map[string]any `json:"payload,omitempty"`
		CreatedAt string         `json:"created_at"`
	}
	out := make([]activityJSON, 0, len(entries))
	for _, a := range entries {
		out = append(out, activityJSON{
			ID: a.ID, TaskID: a.TaskID, UserID: a.UserID, UserName: a.UserName,
			Event: a.Event, Payload: a.Payload,
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": out})
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		} else if r == 'Æ' {
			out[i] = 'æ'
		} else if r == 'Ø' {
			out[i] = 'ø'
		} else if r == 'Å' {
			out[i] = 'å'
		}
	}
	return string(out)
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
