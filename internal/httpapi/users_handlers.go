package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

// Who is on the instance, and how somebody else gets on it.
//
// There is no open registration and there never was: an account comes into
// existence in exactly two ways — the first admin at setup, and somebody accepting
// an invite. This surface is the second one made reachable without first sharing a
// project, which is what "create a user" turns out to mean here.
//
// The whole file is administrators only.

type adminUserJSON struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AvatarColor string `json:"avatar_color"`
	IsAdmin     bool   `json:"is_admin"`
	// Self marks the caller, so the interface can grey out the two things nobody
	// should do to their own account from here.
	Self       bool   `json:"self"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
	// ProjectCount and TaskCount are what a delete would destroy. Sent with the
	// list rather than fetched when the button is pressed, so the confirmation can
	// name real numbers without a round trip in the middle of a decision.
	ProjectCount int `json:"project_count"`
	TaskCount    int `json:"task_count"`
}

type pendingInviteJSON struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	// ProjectName is empty for an invite to the instance itself, which is what
	// this page issues. The two read very differently to whoever is looking.
	ProjectName string `json:"project_name,omitempty"`
	Role        string `json:"role"`
	InvitedBy   string `json:"invited_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())

	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		s.internal(w, "list users", err)
		return
	}
	invites, err := s.db.ListPendingInvites(r.Context())
	if err != nil {
		s.internal(w, "list pending invites", err)
		return
	}

	out := make([]adminUserJSON, 0, len(users))
	for _, u := range users {
		j := adminUserJSON{
			ID: u.ID, Email: u.Email, Name: u.Name, AvatarColor: u.AvatarColor,
			IsAdmin: u.IsAdmin, Self: u.ID == me.ID,
			CreatedAt:    u.CreatedAt.Format(time.RFC3339),
			ProjectCount: u.ProjectCount, TaskCount: u.TaskCount,
		}
		// Zero means never signed in, which is not a time and must not be sent as
		// one — 1970 in an interface reads as a bug rather than as "never".
		if !u.LastSeenAt.IsZero() {
			j.LastSeenAt = u.LastSeenAt.Format(time.RFC3339)
		}
		out = append(out, j)
	}

	pending := make([]pendingInviteJSON, 0, len(invites))
	for _, i := range invites {
		pending = append(pending, pendingInviteJSON{
			ID: i.ID, Email: i.Email, ProjectName: i.ProjectName, Role: string(i.Role),
			InvitedBy: i.InvitedBy,
			CreatedAt: i.CreatedAt.Format(time.RFC3339),
			ExpiresAt: i.ExpiresAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"users": out, "invites": pending})
}

type createUserRequest struct {
	Email string `json:"email"`
}

// handleCreateUser invites somebody to the instance.
//
// An invite rather than an account with a password the administrator chooses. A
// password typed here would be known to two people before it was ever used, and
// would have to travel to the second one by some channel nobody has thought about.
// The link does the same job and ends with a password only its owner has seen.
//
// No name is asked for, because the signup form asks the person themselves — a
// name an administrator guesses is a name somebody has to correct.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	inviter := userFrom(r.Context())

	email := store.NormalizeEmail(req.Email)
	if email == "" || !strings.Contains(email, "@") {
		writeFieldErrors(w, map[string]string{"email": "must be an email address"})
		return
	}
	if _, err := s.db.UserByEmail(r.Context(), email); err == nil {
		writeError(w, http.StatusConflict, CodeConflict, "that email address already has an account")
		return
	}

	// RoleEditor is carried on the row because the column requires a role, and it
	// is never read: AcceptInvite returns early when there is no project to grant
	// anything on.
	token, _, err := s.db.CreateInvite(r.Context(), email, "", store.RoleEditor,
		inviter.ID, s.cfg.InviteTTL)
	if err != nil {
		s.internal(w, "create instance invite", err)
		return
	}
	link := s.cfg.BaseURL + "/invite?token=" + token

	emailed := false
	if s.mail.Configured() {
		if err := s.mail.SendInvite(r.Context(), email, inviter.Name, "", link, s.cfg.InviteTTL); err != nil {
			s.log.Error("send instance invite", "err", err, "to", email)
		} else {
			emailed = true
		}
	}
	writeJSON(w, http.StatusCreated, inviteResponse{Link: link, Emailed: emailed})
}

type updateUserRequest struct {
	IsAdmin *bool `json:"is_admin"`
}

// handleUpdateUser promotes or demotes an administrator.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req updateUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	userID := chi.URLParam(r, "userID")
	me := userFrom(r.Context())

	if req.IsAdmin == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Standing down as the last administrator locks everybody out of this page,
	// and there is no console and no second setup run to undo it with.
	if !*req.IsAdmin {
		if err := s.refuseIfLastAdmin(w, r, userID, "the last administrator cannot be demoted"); err != nil {
			return
		}
	}

	if err := s.db.SetUserAdmin(r.Context(), userID, *req.IsAdmin); err != nil {
		s.storeError(w, "set user admin", err)
		return
	}
	s.log.Info("admin changed", "by", me.ID, "user", userID, "is_admin", *req.IsAdmin)
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteUser removes an account and everything the foreign keys take with
// it. See store.DeleteUser for what that is; it is more than it looks.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	me := userFrom(r.Context())

	// Not yourself. Deleting the account you are signed in as would take your own
	// projects while you were looking at them, and end the session mid-request.
	// Somebody who really means it can be demoted first and removed by another
	// administrator.
	if userID == me.ID {
		writeError(w, http.StatusConflict, CodeConflict,
			"you cannot delete the account you are signed in as")
		return
	}
	if err := s.refuseIfLastAdmin(w, r, userID, "the last administrator cannot be deleted"); err != nil {
		return
	}

	if err := s.db.DeleteUser(r.Context(), userID); err != nil {
		s.storeError(w, "delete user", err)
		return
	}
	s.log.Info("user deleted", "by", me.ID, "user", userID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	if err := s.db.DeleteInvite(r.Context(), chi.URLParam(r, "inviteID")); err != nil {
		s.storeError(w, "delete invite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refuseIfLastAdmin writes a 409 and returns an error when the named user is the
// only administrator left. The two callers both need it, and getting it wrong in
// one of them is an instance nobody can administer again.
func (s *Server) refuseIfLastAdmin(w http.ResponseWriter, r *http.Request, userID, message string) error {
	target, err := s.db.UserByID(r.Context(), userID)
	if err != nil {
		s.storeError(w, "get user", err)
		return err
	}
	if !target.IsAdmin {
		return nil
	}
	admins, err := s.db.CountAdmins(r.Context())
	if err != nil {
		s.internal(w, "count admins", err)
		return err
	}
	if admins <= 1 {
		writeError(w, http.StatusConflict, CodeLastAdmin, message)
		return errLastAdmin
	}
	return nil
}
