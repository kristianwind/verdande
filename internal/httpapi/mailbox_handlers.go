package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kristianwind/verdande/internal/imap"
	"github.com/kristianwind/verdande/internal/store"
)

// defaultMailboxSyncBudget bounds one read of one mailbox when somebody is waiting.
// The same reasoning as Gmail's: a mailbox that has stopped answering must not
// hold a request open until whatever sits in front of this server answers for it.
const defaultMailboxSyncBudget = 25 * time.Second

func (s *Server) handleListMailboxes(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	list, err := s.db.Mailboxes(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "list mailboxes", err)
		return
	}
	if list == nil {
		list = []store.Mailbox{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mailboxes": list})
}

// handleAddMailbox connects one, after proving it can actually be read.
//
// Tested before it is saved, not after: a mailbox that was accepted and then
// silently fails every ten minutes is worse than one that refused at the door,
// where the person is still holding the password they just typed.
func (s *Server) handleAddMailbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Host     string `json:"host"`
		Username string `json:"username"`
		Password string `json:"password"`
		Folder   string `json:"folder"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}

	body.Host = strings.TrimSpace(body.Host)
	body.Username = strings.TrimSpace(body.Username)
	if body.Host == "" || body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"a mailbox needs a host, a username and a password")
		return
	}
	// A host with no port is the mistake everyone makes once, and 993 is the answer
	// for every host worth naming.
	if !strings.Contains(body.Host, ":") {
		body.Host += ":993"
	}

	account := imap.Account{
		Host: body.Host, Username: body.Username,
		Password: body.Password, Folder: body.Folder,
	}
	client, err := imap.Dial(account)
	if err != nil {
		s.upstream(w, r, CodeMailboxFailed, "connect mailbox", err)
		return
	}
	client.Close()

	user := userFrom(r.Context())
	m := &store.Mailbox{
		UserID: user.ID, Kind: "imap", Name: strings.TrimSpace(body.Name),
		Host: body.Host, Username: body.Username,
		Password: body.Password, Folder: body.Folder,
	}
	if m.Name == "" {
		m.Name = body.Username
	}
	if err := s.db.SaveMailbox(r.Context(), m); err != nil {
		// The unique index. Saying which mailbox is already here beats "constraint
		// failed", which is true and useless.
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, CodeValidation,
				fmt.Sprintf("%s on %s is already connected", body.Username, body.Host))
			return
		}
		s.internal(w, r, "save mailbox", err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleDeleteMailbox(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := s.db.DeleteMailbox(r.Context(), user.ID, chi.URLParam(r, "mailboxID")); err != nil {
		s.internal(w, r, "delete mailbox", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncMailbox reads one mailbox now, within a budget of its own.
func (s *Server) handleSyncMailbox(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	m, err := s.db.Mailbox(r.Context(), user.ID, chi.URLParam(r, "mailboxID"))
	if err != nil {
		s.internal(w, r, "read mailbox", err)
		return
	}
	if m == nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such mailbox")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultMailboxSyncBudget)
	defer cancel()

	created, err := s.SyncMailbox(ctx, user, m)
	if err != nil {
		// Whatever was made by the deadline is kept: each task is committed as it
		// is made, so a short budget is a shorter run rather than a lost one.
		if errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusOK, map[string]any{"created": created, "partial": true})
			return
		}
		s.upstream(w, r, CodeMailboxFailed, "sync mailbox", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created})
}

// SyncMailbox turns the flagged mail newer than the last run into tasks.
//
// Exported because the background job calls it too, on the same terms and without
// a budget: nobody is waiting there.
func (s *Server) SyncMailbox(ctx context.Context, user *store.User, m *store.Mailbox) (int, error) {
	client, err := imap.Dial(imap.Account{
		Host: m.Host, Username: m.Username, Password: m.Password, Folder: m.Folder,
	})
	if err != nil {
		return 0, err
	}
	defer client.Close()

	messages, err := client.Since(m.LastUID, 25)
	if err != nil {
		return 0, err
	}
	if len(messages) == 0 {
		return 0, nil
	}

	projectID, err := s.db.InboxID(ctx, user.ID)
	if err != nil {
		return 0, err
	}

	created := 0
	highest := m.LastUID
	for _, msg := range messages {
		if ctx.Err() != nil {
			// What was made so far is already committed and counted, and the marker
			// below has to be written for it: a run that created tasks and forgot how
			// far it got would make them all again on the next pass.
			break
		}

		subject := msg.Subject
		if subject == "" {
			subject = "(uden emne)"
		}
		content := subject
		if msg.From != "" {
			content = msg.From + ": " + subject
		}

		task := &store.Task{
			ProjectID:   projectID,
			Content:     content,
			Description: msg.Snippet,
			Priority:    4,
			CreatedBy:   user.ID,
		}
		if err := s.db.CreateTask(ctx, task, nil); err != nil {
			s.log.Warn("mailbox create task", "err", err, "mailbox", m.ID)
			continue
		}
		created++
		if msg.UID > highest {
			highest = msg.UID
		}
		s.hub.Publish(projectID, "task.created", toTaskJSON(*task))
	}

	// Written even when the loop was cut short, and outside the request's context
	// so a cancelled request still records what it did.
	if highest > m.LastUID {
		if err := s.db.MarkMailboxRead(context.WithoutCancel(ctx), m.ID, highest, time.Now()); err != nil {
			s.log.Warn("mark mailbox read", "err", err, "mailbox", m.ID)
		}
	}
	if ctx.Err() != nil {
		return created, ctx.Err()
	}
	return created, nil
}
