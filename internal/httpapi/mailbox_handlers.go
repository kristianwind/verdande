package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	// One reader of this mailbox at a time. Held for the whole run, marker and all:
	// releasing before the marker is written would leave exactly the gap this is
	// here to close.
	unlock := s.lockSync(m.ID)
	defer unlock()

	// Re-read under the lock. The row that came in may have been fetched before the
	// run that just finished moved the marker.
	if fresh, err := s.db.Mailbox(ctx, m.UserID, m.ID); err == nil && fresh != nil {
		m = fresh
	}

	client, err := imap.Dial(imap.Account{
		Host: m.Host, Username: m.Username, Password: m.Password, Folder: m.Folder,
	})
	if err != nil {
		return 0, err
	}
	defer client.Close()

	messages, searched, err := client.Since(m.LastUID, 25)
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
	// The uids of the mails that actually became tasks. Only those get unflagged,
	// and only after the task is committed: a flag removed from a mail whose task
	// was never written is a mail nobody will ever see again.
	done := make([]uint32, 0, len(messages))
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
			// Host and uid together: a uid is only unique within one mailbox on
			// one server, and two accounts will hand out the same small numbers.
			SourceKey: fmt.Sprintf("imap:%s:%s:%d", m.Host, m.Folder, msg.UID),
		}
		if err := s.db.CreateTask(ctx, task, nil); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				// Already a task. Still count it as read, or last_uid never moves
				// past it and every sweep from here on stops at the same message.
				done = append(done, msg.UID)
				if msg.UID > highest {
					highest = msg.UID
				}
				continue
			}
			s.log.Warn("mailbox create task", "err", err, "mailbox", m.ID)
			continue
		}
		created++
		done = append(done, msg.UID)
		if msg.UID > highest {
			highest = msg.UID
		}
		s.hub.Publish(projectID, "task.created", toTaskJSON(*task))
	}

	// The flag comes off what became a task. This is what actually stops a mail
	// arriving twice; the marker below is now a second line rather than the only
	// one. See imap.Unflag for why the record belongs in the mailbox and not here.
	//
	// Outside the request's context, like the marker: a cancelled request that
	// already created tasks must still take the flags off them, or the next run
	// makes them all again.
	if err := client.Unflag(done); err != nil {
		// Not fatal, and not the caller's problem. A folder somebody opened
		// read-only, or a server that refuses the STORE, still gets the uid marker
		// underneath — worse, but not broken.
		s.log.Warn("could not unflag imported mail", "err", err, "mailbox", m.ID, "count", len(done))
	}

	// A run that got all the way through moves the marker to the whole of what the
	// search considered, not merely to the last message it happened to read. That
	// is what stops the same mail becoming a task again on the next run when the
	// server did not put a uid in the fetch reply.
	if ctx.Err() == nil && searched > highest {
		highest = searched
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

// lockSync serialises one thing that polls against itself and returns the release.
//
// A mailbox to begin with; a calendar connection now takes the same lock under a
// key of its own. Two runs at once — somebody pressing "fetch now" while the sweep
// is in the middle of the same thing — both start from the state as it was, and
// the loser writes last. For a mailbox that made forty copies of one mail in a
// second; for a calendar it would be a window deleted after it was refilled.
func (s *Server) lockSync(id string) func() {
	value, _ := s.syncing.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
