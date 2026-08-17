package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/ai"
	"github.com/kristianwind/verdande/internal/auth"
	"github.com/kristianwind/verdande/internal/quickadd"
	"github.com/kristianwind/verdande/internal/store"
)

// --- mail to task -------------------------------------------------------------------

type mailAddressResponse struct {
	Address string `json:"address"`
}

// handleGetMailAddress returns the personal address that turns an email into a
// task, minting the token on first ask.
//
// The token goes in the local part rather than the domain — todo+<token>@domain —
// because that is what a single Mailcow alias can route without a wildcard domain
// or a DNS change per user.
func (s *Server) handleGetMailAddress(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	token, err := s.db.EnsureMailToken(r.Context(), user.ID)
	if err != nil {
		s.internal(w, "mail token", err)
		return
	}
	writeJSON(w, http.StatusOK, mailAddressResponse{Address: s.mailAddress(token)})
}

func (s *Server) handleRotateMailAddress(w http.ResponseWriter, r *http.Request) {
	token, err := auth.NewToken()
	if err != nil {
		s.internal(w, "generate mail token", err)
		return
	}
	if err := s.db.SetMailToken(r.Context(), userFrom(r.Context()).ID, token); err != nil {
		s.internal(w, "set mail token", err)
		return
	}
	writeJSON(w, http.StatusOK, mailAddressResponse{Address: s.mailAddress(token)})
}

func (s *Server) mailAddress(token string) string {
	domain := feedDomain(s.cfg.BaseURL)
	if from := s.cfg.SMTP.From; strings.Contains(from, "@") {
		// The mail server's own domain, which is the one that can actually route
		// this — the web address may well be a tunnel hostname that receives no mail.
		domain = from[strings.Index(from, "@")+1:]
	}
	return "todo+" + token + "@" + domain
}

type inboundMailRequest struct {
	// To is the address the mail was sent to, from which the token is read.
	To      string `json:"to"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// handleInboundMail turns a delivered email into a task.
//
// Mailcow delivers to this endpoint over LMTP or a small forwarding script; either
// way what arrives here is already-parsed mail. Authentication is the token in the
// recipient address, which is the only credential an inbound mail can carry.
//
// The subject is parsed with the quick-add parser, so "Fakturer Anders p1 #Firma"
// in a subject line does what it does everywhere else in the app.
func (s *Server) handleInboundMail(w http.ResponseWriter, r *http.Request) {
	var req inboundMailRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	token := mailToken(req.To)
	if token == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such address")
		return
	}
	user, err := s.db.UserByMailToken(r.Context(), token)
	if err != nil {
		// Deliberately identical to a malformed address: an inbound endpoint that
		// distinguishes them is an oracle for which tokens are live.
		writeError(w, http.StatusNotFound, CodeNotFound, "no such address")
		return
	}

	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		subject = "(uden emne)"
	}

	parsed := quickadd.Parse(subject, time.Now().In(userLocation(user.Timezone)), user.Locale)
	content := parsed.Content
	if strings.TrimSpace(content) == "" {
		content = subject
	}

	projectID := ""
	if parsed.Project != "" {
		if id, err := s.db.ProjectByName(r.Context(), user.ID, parsed.Project); err == nil {
			projectID = id
		}
	}
	if projectID == "" {
		if projectID, err = s.db.InboxID(r.Context(), user.ID); err != nil {
			s.internal(w, "inbox", err)
			return
		}
	}

	task := &store.Task{
		ProjectID: projectID, Content: content, Priority: parsed.Priority,
		Description: strings.TrimSpace(req.Body), DueDate: parsed.DueDate,
		RecurrenceRule: parsed.Recurrence, CreatedBy: user.ID,
	}
	if err := s.db.CreateTask(r.Context(), task, parsed.Labels); err != nil {
		s.internal(w, "create task from mail", err)
		return
	}

	s.publish(projectID, "task.created", toTaskJSON(*task))
	s.log.Info("task created from mail", "user", user.ID, "from", req.From)
	writeJSON(w, http.StatusCreated, map[string]string{"task_id": task.ID})
}

// mailToken reads the token out of "Name <todo+TOKEN@domain>".
func mailToken(address string) string {
	if i := strings.LastIndex(address, "<"); i >= 0 {
		address = address[i+1:]
	}
	address = strings.TrimSuffix(strings.TrimSpace(address), ">")

	local, _, ok := strings.Cut(address, "@")
	if !ok {
		return ""
	}
	_, token, ok := strings.Cut(local, "+")
	if !ok {
		return ""
	}
	return token
}

// --- AI ---------------------------------------------------------------------------------

type aiSettings struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	// APIKey is written but never read back — see handleGetAISettings.
	APIKey string `json:"api_key,omitempty"`
	// HasKey tells the settings page whether a key is stored, without showing it.
	HasKey bool `json:"has_key"`
}

func (s *Server) handleGetAISettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.aiConfig(r, userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "ai settings", err)
		return
	}
	// The key is never sent back. A settings page that repopulates a password
	// field is a settings page that will eventually leak one into a screenshot.
	writeJSON(w, http.StatusOK, aiSettings{
		Provider: string(cfg.Provider), BaseURL: cfg.BaseURL,
		Model: cfg.Model, HasKey: cfg.APIKey != "",
	})
}

func (s *Server) handleSetAISettings(w http.ResponseWriter, r *http.Request) {
	var req aiSettings
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	switch ai.Provider(req.Provider) {
	case ai.ProviderAnthropic, ai.ProviderOpenAI, ai.ProviderGoogle, ai.ProviderCompatible, "":
	default:
		writeFieldErrors(w, map[string]string{
			"provider": "must be anthropic, openai, google or compatible",
		})
		return
	}

	existing, err := s.aiConfig(r, user.ID)
	if err != nil {
		s.internal(w, "ai settings", err)
		return
	}
	// An empty key means "leave it alone", not "delete it" — otherwise saving a
	// changed model would silently clear the key.
	key := existing.APIKey
	if req.APIKey != "" {
		key = req.APIKey
	}

	settings := map[string]any{
		"provider": req.Provider, "base_url": req.BaseURL,
		"model": req.Model, "api_key": key,
	}
	if err := s.db.SetUserSettings(r.Context(), user.ID, "ai", settings); err != nil {
		s.internal(w, "save ai settings", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) aiConfig(r *http.Request, userID string) (ai.Config, error) {
	values, err := s.db.UserSettings(r.Context(), userID, "ai")
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

// handleAISplit breaks a task into sub-tasks and creates them.
func (s *Server) handleAISplit(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	taskID := chi.URLParam(r, "taskID")

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	cfg, err := s.aiConfig(r, user.ID)
	if err != nil {
		s.internal(w, "ai settings", err)
		return
	}
	if !cfg.Configured() {
		// Not an error the user did anything to cause: the feature is simply off.
		writeError(w, http.StatusConflict, CodeConflict,
			"der er ikke sat en AI-udbyder op under indstillinger")
		return
	}

	task, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err != nil {
		s.storeError(w, "get task", err)
		return
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	subtasks, err := ai.New(cfg).SplitIntoSubtasks(ctx, task.Content, task.Description, user.Locale)
	if err != nil {
		s.log.Warn("ai split", "err", err)
		writeError(w, http.StatusBadGateway, CodeInternal, err.Error())
		return
	}

	created := make([]taskJSON, 0, len(subtasks))
	for _, content := range subtasks {
		child := &store.Task{
			ProjectID: task.ProjectID, ParentID: task.ID, Content: content,
			Priority: task.Priority, CreatedBy: user.ID,
		}
		if err := s.db.CreateTask(r.Context(), child, nil); err != nil {
			s.internal(w, "create subtask", err)
			return
		}
		created = append(created, toTaskJSON(*child))
	}

	s.activity(r, task.ProjectID, task.ID, "task.split", map[string]any{"count": len(created)})
	writeJSON(w, http.StatusCreated, map[string]any{"subtasks": created})
}

// handleAISummary writes a short prioritisation note over what is outstanding.
func (s *Server) handleAISummary(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	cfg, err := s.aiConfig(r, user.ID)
	if err != nil {
		s.internal(w, "ai settings", err)
		return
	}
	if !cfg.Configured() {
		writeError(w, http.StatusConflict, CodeConflict,
			"der er ikke sat en AI-udbyder op under indstillinger")
		return
	}

	tasks, err := s.db.ListTasks(r.Context(), user.ID, store.TaskFilter{Limit: 100})
	if err != nil {
		s.internal(w, "list tasks", err)
		return
	}
	if len(tasks) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"summary": "Der er ingen åbne opgaver."})
		return
	}

	lines := make([]string, 0, len(tasks))
	for _, t := range tasks {
		line := "- " + t.Content
		if t.DueDate != "" {
			line += " (forfalder " + t.DueDate + ")"
		}
		if t.Priority < 4 {
			line += " [P" + string(rune('0'+t.Priority)) + "]"
		}
		lines = append(lines, line)
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	summary, err := ai.New(cfg).WeeklySummary(ctx, lines, user.Locale)
	if err != nil {
		s.log.Warn("ai summary", "err", err)
		writeError(w, http.StatusBadGateway, CodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"summary": summary})
}

// --- Gmail ----------------------------------------------------------------------------

type gmailSettings struct {
	Connected bool   `json:"connected"`
	Email     string `json:"email,omitempty"`
	// Trigger is what causes a task to be created: a starred message, a label, or
	// both. One-way by design — unstarring does nothing to the task.
	Trigger string `json:"trigger"`
	Label   string `json:"label,omitempty"`
}

func (s *Server) handleGetGmail(w http.ResponseWriter, r *http.Request) {
	values, err := s.db.UserSettings(r.Context(), userFrom(r.Context()).ID, "gmail")
	if err != nil {
		s.internal(w, "gmail settings", err)
		return
	}
	str := func(key string) string {
		v, _ := values[key].(string)
		return v
	}
	writeJSON(w, http.StatusOK, gmailSettings{
		Connected: str("refresh_token") != "",
		Email:     str("email"),
		Trigger:   str("trigger"),
		Label:     str("label"),
	})
}

func (s *Server) handleSetGmail(w http.ResponseWriter, r *http.Request) {
	var req gmailSettings
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	values, err := s.db.UserSettings(r.Context(), user.ID, "gmail")
	if err != nil {
		s.internal(w, "gmail settings", err)
		return
	}
	values["trigger"] = req.Trigger
	values["label"] = req.Label

	if err := s.db.SetUserSettings(r.Context(), user.ID, "gmail", values); err != nil {
		s.internal(w, "save gmail settings", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDisconnectGmail forgets the connection, tokens and all.
func (s *Server) handleDisconnectGmail(w http.ResponseWriter, r *http.Request) {
	if err := s.db.SetUserSettings(r.Context(), userFrom(r.Context()).ID, "gmail", map[string]any{}); err != nil {
		s.internal(w, "disconnect gmail", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var _ = json.Marshal
