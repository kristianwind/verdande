package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/filterquery"
	"github.com/kristianwind/verdande/internal/store"
)

type filterJSON struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Query     string  `json:"query"`
	Color     string  `json:"color"`
	SortOrder float64 `json:"sort_order"`
}

func toFilterJSON(f store.Filter) filterJSON {
	return filterJSON{ID: f.ID, Name: f.Name, Query: f.Query, Color: f.Color, SortOrder: f.SortOrder}
}

func (s *Server) handleListFilters(w http.ResponseWriter, r *http.Request) {
	filters, err := s.db.ListFilters(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list filters", err)
		return
	}
	out := make([]filterJSON, 0, len(filters))
	for _, f := range filters {
		out = append(out, toFilterJSON(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"filters": out})
}

type filterRequest struct {
	Name      *string  `json:"name"`
	Query     *string  `json:"query"`
	Color     *string  `json:"color"`
	SortOrder *float64 `json:"sort_order"`
}

func (s *Server) handleCreateFilter(w http.ResponseWriter, r *http.Request) {
	var req filterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	name := strings.TrimSpace(valueOr(req.Name, ""))
	query := strings.TrimSpace(valueOr(req.Query, ""))

	fields := map[string]string{}
	if name == "" {
		fields["name"] = "required"
	}
	// The expression is compiled before it is saved. A filter that cannot run is
	// worse than no filter: it is discovered later, from a list that is empty for
	// a reason nobody can see.
	if msg := checkFilterQuery(query, user); msg != "" {
		fields["query"] = msg
	}
	if len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	f := &store.Filter{UserID: user.ID, Name: name, Query: query, Color: valueOr(req.Color, "")}
	if req.SortOrder != nil {
		f.SortOrder = *req.SortOrder
	}
	if err := s.db.CreateFilter(r.Context(), f); err != nil {
		s.internal(w, "create filter", err)
		return
	}
	writeJSON(w, http.StatusCreated, toFilterJSON(*f))
}

func (s *Server) handleUpdateFilter(w http.ResponseWriter, r *http.Request) {
	var req filterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	if req.Query != nil {
		query := strings.TrimSpace(*req.Query)
		if msg := checkFilterQuery(query, user); msg != "" {
			writeFieldErrors(w, map[string]string{"query": msg})
			return
		}
		req.Query = &query
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeFieldErrors(w, map[string]string{"name": "required"})
			return
		}
		req.Name = &name
	}

	err := s.db.UpdateFilter(r.Context(), user.ID, chi.URLParam(r, "filterID"), store.FilterUpdate{
		Name: req.Name, Query: req.Query, Color: req.Color, SortOrder: req.SortOrder,
	})
	if err != nil {
		s.storeError(w, "update filter", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFilter(w http.ResponseWriter, r *http.Request) {
	err := s.db.DeleteFilter(r.Context(), userFrom(r.Context()).ID, chi.URLParam(r, "filterID"))
	if err != nil {
		s.storeError(w, "delete filter", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunFilter executes a saved filter and returns the tasks it selects.
func (s *Server) handleRunFilter(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	f, err := s.db.GetFilter(r.Context(), user.ID, chi.URLParam(r, "filterID"))
	if err != nil {
		s.storeError(w, "get filter", err)
		return
	}
	s.runQuery(w, r, f.Query)
}

// handlePreviewFilter runs an expression without saving it, so the filter editor
// can show what it selects while it is being written.
func (s *Server) handlePreviewFilter(w http.ResponseWriter, r *http.Request) {
	s.runQuery(w, r, r.URL.Query().Get("query"))
}

func (s *Server) runQuery(w http.ResponseWriter, r *http.Request, query string) {
	user := userFrom(r.Context())

	compiled, err := filterquery.Parse(query, filterContext(user))
	if err != nil {
		// The message is the parser's own, in Danish, naming what it could not
		// read — this is a language somebody is writing by hand in a small box.
		writeJSON(w, http.StatusUnprocessableEntity, APIError{
			Code:    CodeValidation,
			Message: err.Error(),
			Fields:  map[string]string{"query": err.Error()},
		})
		return
	}

	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{
		FilterSQL:  compiled.SQL,
		FilterArgs: compiled.Args,
		// A filter that names "completed" has to be able to find completed tasks,
		// so the query decides rather than the default.
		IncludeCompleted: strings.Contains(strings.ToLower(query), "completed") ||
			strings.Contains(strings.ToLower(query), "færdig") ||
			strings.Contains(strings.ToLower(query), "afsluttet"),
		Limit: parseLimit(r.URL.Query().Get("limit"), 200, 500),
	})
	if err != nil {
		s.internal(w, "run filter", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": toTaskList(tasks)})
}

func filterContext(user *store.User) filterquery.Context {
	return filterquery.Context{
		UserID:   user.ID,
		Now:      time.Now(),
		Location: userLocation(user.Timezone),
	}
}

// parseFilter compiles an expression for a user. Shared with the MCP tools, so a
// filter written in Claude means exactly what the same filter means in the app.
func parseFilter(query string, user *store.User) (filterquery.Compiled, error) {
	return filterquery.Parse(query, filterContext(user))
}

func checkFilterQuery(query string, user *store.User) string {
	if strings.TrimSpace(query) == "" {
		return "required"
	}
	if _, err := filterquery.Parse(query, filterContext(user)); err != nil {
		return err.Error()
	}
	return ""
}

// --- labels ---------------------------------------------------------------------

type labelJSON struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	SortOrder float64 `json:"sort_order"`
	TaskCount int     `json:"task_count"`
}

func (s *Server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := s.db.ListLabels(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list labels", err)
		return
	}
	out := make([]labelJSON, 0, len(labels))
	for _, l := range labels {
		out = append(out, labelJSON{l.ID, l.Name, l.Color, l.SortOrder, l.TaskCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": out})
}

type labelRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

func (s *Server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	var req labelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	name := strings.TrimSpace(valueOr(req.Name, ""))
	if name == "" {
		writeFieldErrors(w, map[string]string{"name": "required"})
		return
	}

	l := &store.Label{UserID: userFrom(r.Context()).ID, Name: name, Color: valueOr(req.Color, "")}
	if err := s.db.CreateLabel(r.Context(), l); err != nil {
		if errors.Is(err, store.ErrLabelExists) {
			writeError(w, http.StatusConflict, CodeConflict, "you already have a label with that name")
			return
		}
		s.internal(w, "create label", err)
		return
	}
	s.publishToOwner(r, "label.changed", labelJSON{l.ID, l.Name, l.Color, l.SortOrder, 0})
	writeJSON(w, http.StatusCreated, labelJSON{l.ID, l.Name, l.Color, l.SortOrder, 0})
}

func (s *Server) handleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	var req labelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	err := s.db.UpdateLabel(r.Context(), userFrom(r.Context()).ID, chi.URLParam(r, "labelID"),
		strings.TrimSpace(valueOr(req.Name, "")), valueOr(req.Color, ""))
	if err != nil {
		if errors.Is(err, store.ErrLabelExists) {
			writeError(w, http.StatusConflict, CodeConflict, "you already have a label with that name")
			return
		}
		s.storeError(w, "update label", err)
		return
	}
	s.publishToOwner(r, "label.changed", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	err := s.db.DeleteLabel(r.Context(), userFrom(r.Context()).ID, chi.URLParam(r, "labelID"))
	if err != nil {
		s.storeError(w, "delete label", err)
		return
	}
	s.publishToOwner(r, "label.changed", nil)
	w.WriteHeader(http.StatusNoContent)
}
